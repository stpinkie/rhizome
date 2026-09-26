package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/isolation"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Process runtimes for acp.client.runtime / agents.list[].acp.runtime.
const (
	acpRuntimeExec      = "exec"      // plain child process (default)
	acpRuntimeSandbox   = "sandbox"   // pkg/isolation wrapper, per-agent scratch root
	acpRuntimeContainer = "container" // docker|podman run -i --rm
)

// runtimeFor resolves the effective process runtime for a binding:
// agents.list[].acp.runtime wins over the acp.client global; "exec" is the
// default. Unknown per-binding values warn and inherit, matching policyFor.
func (m *ClientManager) runtimeFor(inst *agent.AgentInstance) string {
	if inst != nil && inst.ACP != nil {
		if rt := normalizeACPRuntime(inst.ACP.Runtime); rt != "" {
			return rt
		}
		if strings.TrimSpace(inst.ACP.Runtime) != "" {
			logger.WarnCF("acp", "unknown acp.runtime on agent binding; using acp.client default",
				map[string]any{"agent_id": inst.ID, "value": inst.ACP.Runtime})
		}
	}
	if m.runtime == "" {
		return acpRuntimeExec
	}
	return m.runtime
}

func normalizeACPRuntime(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case acpRuntimeExec:
		return acpRuntimeExec
	case acpRuntimeSandbox:
		return acpRuntimeSandbox
	case acpRuntimeContainer:
		return acpRuntimeContainer
	default:
		return ""
	}
}

// acpSandboxRoot is the per-agent scratch instance root a "sandbox" binding
// runs inside — not RHIZOME_HOME, so the isolated view exposes only this
// scratch area plus the configured/system mounts. The directory persists
// across respawns so a restarted agent keeps its scratch state.
func acpSandboxRoot(agentID string) string {
	return filepath.Join(config.GetHome(), "acp-sandbox", agentID)
}

// sandboxOptions builds the per-invocation isolation view for a sandbox
// binding: enabled regardless of the global isolation.enabled (runtime is
// the opt-in), rooted at the agent scratch dir, with the global
// expose_paths/backend preserved as the operator's escape hatch.
func (m *ClientManager) sandboxOptions(root string) isolation.Options {
	opts := isolation.OptionsFromConfig(m.cfg.Isolation)
	opts.Enabled = true
	opts.Root = root
	if sb := m.cfg.ACP.Client.Sandbox; sb != nil &&
		strings.EqualFold(strings.TrimSpace(sb.Network), isolation.NetModeNone) {
		opts.NetMode = isolation.NetModeNone
	}
	return opts
}

// spawnSandbox wraps the agent command in pkg/isolation rooted at a
// per-agent scratch dir: the process sees only the scratch root plus
// configured/system mounts, and acp.client.sandbox.network=none applies the
// platform net isolation (bwrap --unshare-net / sandbox profile without
// network-outbound; unsupported platforms fail the spawn, never silently
// run unsandboxed).
func (m *ClientManager) spawnSandbox(
	ctx context.Context,
	agentID string,
	inst *agent.AgentInstance,
) (*agentProcess, error) {
	binding := inst.ACP
	command := strings.TrimSpace(binding.Command)
	if command == "" {
		return nil, fmt.Errorf("agent %q acp.command is empty", agentID)
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return nil, fmt.Errorf("acp command %q for agent %q not found on PATH: %w", command, agentID, err)
	}

	root := acpSandboxRoot(agentID)
	if err := isolation.PrepareInstanceRoot(root); err != nil {
		return nil, fmt.Errorf("acp agent %q sandbox root: %w", agentID, err)
	}
	opts := m.sandboxOptions(root)

	//nolint:gosec // G204: command is the operator-configured agent binding
	cmd := exec.CommandContext(context.Background(), resolved, binding.Args...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	for k, v := range binding.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stdin: %w", agentID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stdout: %w", agentID, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stderr: %w", agentID, err)
	}
	if err := isolation.StartWith(cmd, opts); err != nil {
		return nil, fmt.Errorf("acp agent %q failed to start under sandbox: %w", agentID, err)
	}

	// Drain stderr into the file logger — never stdout, which is the ACP
	// transport and must stay protocol-clean.
	go drainStderr(agentID, stderr)

	return m.connect(ctx, agentID, inst, stdin, stdout, func() {
		// Killing the wrapper (bwrap/sandbox-exec) takes the child down via
		// --die-with-parent / process-group teardown.
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}, map[string]any{
		"command": command, "pid": cmd.Process.Pid, "runtime": acpRuntimeSandbox,
		"sandbox_root": root,
	})
}

// spawnContainer runs the agent inside a docker|podman container with
// `run -i --rm` so ACP still travels over stdin/stdout unchanged. The
// container is named so kill can target it — terminating the engine CLI
// alone would orphan the daemon-managed container.
func (m *ClientManager) spawnContainer(
	ctx context.Context,
	agentID string,
	inst *agent.AgentInstance,
) (*agentProcess, error) {
	binding := inst.ACP
	cc := m.cfg.ACP.Client.Container
	if cc == nil || strings.TrimSpace(cc.Image) == "" {
		return nil, fmt.Errorf(
			"agent %q runtime=container requires acp.client.container.image", agentID)
	}
	engine, err := resolveContainerEngine(cc.Engine)
	if err != nil {
		return nil, fmt.Errorf("agent %q container runtime: %w", agentID, err)
	}
	if err := ensureContainerImage(ctx, engine, cc); err != nil {
		return nil, fmt.Errorf("agent %q container runtime: %w", agentID, err)
	}

	cname := containerNameFor(agentID)
	args, err := containerRunArgs(cc, binding, cname)
	if err != nil {
		return nil, fmt.Errorf("agent %q container runtime: %w", agentID, err)
	}

	//nolint:gosec // G204: engine/image/command are the operator-configured binding
	cmd := exec.CommandContext(context.Background(), engine, args...)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stdin: %w", agentID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stdout: %w", agentID, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("acp agent %q stderr: %w", agentID, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp agent %q failed to start container: %w", agentID, err)
	}

	go drainStderr(agentID, stderr)

	return m.connect(ctx, agentID, inst, stdin, stdout, func() {
		// The engine CLI only proxies stdio/signals — kill the container by
		// name first so it cannot outlive the session, then reap the CLI.
		ctx2, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = containerProbe(ctx2, engine, "kill", cname)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}, map[string]any{
		"runtime": acpRuntimeContainer, "engine": engine, "image": cc.Image,
		"container": cname, "pid": cmd.Process.Pid,
	})
}

// containerNameCounter keeps container names unique even on platforms with
// coarse time resolution (two spawns can share one UnixNano tick).
var containerNameCounter atomic.Uint64

// containerNameFor builds a unique, engine-legal container name per spawn so
// kill can target it and --rm cleans it up.
func containerNameFor(agentID string) string {
	var b strings.Builder
	for _, r := range agentID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := b.String()
	if name == "" {
		name = "agent"
	}
	return fmt.Sprintf("rhizome-acp-%s-%d-%d",
		name, time.Now().UnixNano(), containerNameCounter.Add(1))
}

// containerEngineLookPath is the engine-resolution seam for tests.
var containerEngineLookPath = exec.LookPath

// resolveContainerEngine picks the engine binary: "docker"/"podman" must be
// on PATH; "" or "auto" tries docker, then podman, then a named error.
func resolveContainerEngine(engine string) (string, error) {
	switch name := strings.ToLower(strings.TrimSpace(engine)); name {
	case "docker", "podman":
		if _, err := containerEngineLookPath(name); err != nil {
			return "", fmt.Errorf("container engine %q not found on PATH: %w", name, err)
		}
		return name, nil
	default:
		for _, name := range []string{"docker", "podman"} {
			if _, err := containerEngineLookPath(name); err == nil {
				return name, nil
			}
		}
		return "", fmt.Errorf(
			"no container engine found on PATH (tried: docker, podman); " +
				"set acp.client.container.engine to an installed engine")
	}
}

// containerProbe runs an engine subcommand with output discarded; a test seam.
var containerProbe = func(ctx context.Context, engine string, args ...string) error {
	//nolint:gosec // G204: engine binary resolved/validated by the operator config
	cmd := exec.CommandContext(ctx, engine, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// containerImagePresent reports whether the engine already has the image
// locally; a test seam.
var containerImagePresent = func(ctx context.Context, engine, image string) bool {
	return containerProbe(ctx, engine, "image", "inspect", image) == nil
}

// ensureContainerImage applies acp.client.container.pull: "missing" (default)
// pulls when the image is absent locally; "never" errors instead.
func ensureContainerImage(ctx context.Context, engine string, cc *config.ACPContainerConfig) error {
	pull := strings.ToLower(strings.TrimSpace(cc.Pull))
	if pull == "" {
		pull = "missing"
	}
	if containerImagePresent(ctx, engine, cc.Image) {
		return nil
	}
	switch pull {
	case "never":
		return fmt.Errorf("container image %q is not present locally and pull=never", cc.Image)
	case "missing":
		logger.InfoCF("acp", "pulling ACP container image",
			map[string]any{"image": cc.Image, "engine": engine})
		if err := containerProbe(ctx, engine, "pull", cc.Image); err != nil {
			return fmt.Errorf("pull container image %q: %w", cc.Image, err)
		}
		return nil
	default:
		return fmt.Errorf("unknown container pull policy %q", cc.Pull)
	}
}

// containerRunArgs builds the `run` argv: detached removal, stdio-attached,
// per the operator's network/memory/cpu/env/workdir config, then the image
// and the in-container command (empty command → image entrypoint).
func containerRunArgs(
	cc *config.ACPContainerConfig,
	binding *config.ACPAgentConfig,
	cname string,
) ([]string, error) {
	args := []string{"run", "-i", "--rm", "--name", cname}
	switch net := strings.ToLower(strings.TrimSpace(cc.Network)); net {
	case "", "bridge":
		args = append(args, "--network", "bridge")
	case "none":
		args = append(args, "--network", "none")
	case "allowlist":
		return nil, fmt.Errorf(
			"acp.client.container.network=allowlist is not implemented; " +
				"egress allowlisting needs a proxy network and is not part of this track")
	default:
		return nil, fmt.Errorf("invalid acp.client.container.network %q", cc.Network)
	}
	if cc.MemMB > 0 {
		args = append(args, "--memory", strconv.Itoa(cc.MemMB)+"m")
	}
	if cc.Cpus > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(cc.Cpus, 'f', -1, 64))
	}
	// Deterministic argv order for the env and args surfaces.
	envKeys := make([]string, 0, len(binding.Env))
	for k := range binding.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+binding.Env[k])
	}
	if cwd := strings.TrimSpace(binding.Cwd); cwd != "" {
		args = append(args, "-w", cwd)
	}
	args = append(args, cc.Image)
	if command := strings.TrimSpace(binding.Command); command != "" {
		args = append(args, command)
	}
	args = append(args, binding.Args...)
	return args, nil
}
