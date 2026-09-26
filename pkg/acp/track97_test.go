// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package acp

// Track 97 — acp.client.runtime exec|sandbox|container, per-binding
// agents.list[].acp.runtime override, container engine/pull/network config,
// and the per-invocation isolation Options the sandbox runtime rides on.

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/isolation"
)

func acpBoundInst(id string, acpCfg *config.ACPAgentConfig) *agent.AgentInstance {
	if acpCfg == nil {
		acpCfg = &config.ACPAgentConfig{Command: "agent-cmd"}
	}
	return &agent.AgentInstance{ID: id, ACP: acpCfg}
}

func TestNormalizeACPRuntime(t *testing.T) {
	for in, want := range map[string]string{
		"":           "",
		"exec":       acpRuntimeExec,
		"sandbox":    acpRuntimeSandbox,
		"container":  acpRuntimeContainer,
		" SANDBOX ":  acpRuntimeSandbox,
		"bogus":      "",
		"containerd": "",
	} {
		if got := normalizeACPRuntime(in); got != want {
			t.Errorf("normalizeACPRuntime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRuntimeFor_BindingWinsThenGlobalThenExec(t *testing.T) {
	cfg := &config.Config{}
	cfg.ACP.Client.Runtime = "sandbox"
	m := &ClientManager{cfg: cfg, runtime: normalizeACPRuntime(cfg.ACP.Client.Runtime)}

	// No per-binding override → global.
	if got := m.runtimeFor(acpBoundInst("a", nil)); got != acpRuntimeSandbox {
		t.Fatalf("runtimeFor() = %q, want sandbox", got)
	}
	// Per-binding override wins.
	inst := acpBoundInst("b", &config.ACPAgentConfig{Command: "x", Runtime: "container"})
	if got := m.runtimeFor(inst); got != acpRuntimeContainer {
		t.Fatalf("runtimeFor() = %q, want container", got)
	}
	// Unknown per-binding value inherits the global.
	inst = acpBoundInst("c", &config.ACPAgentConfig{Command: "x", Runtime: "bogus"})
	if got := m.runtimeFor(inst); got != acpRuntimeSandbox {
		t.Fatalf("runtimeFor() = %q, want sandbox (unknown binding falls back)", got)
	}
	// Empty global → exec.
	m = &ClientManager{cfg: &config.Config{}, runtime: ""}
	if got := m.runtimeFor(acpBoundInst("d", nil)); got != acpRuntimeExec {
		t.Fatalf("runtimeFor() = %q, want exec default", got)
	}
}

func TestSpawnDispatchesPerRuntime(t *testing.T) {
	calls := map[string]int{}
	fake := func(name string) func(context.Context, string, *agent.AgentInstance) (*agentProcess, error) {
		return func(context.Context, string, *agent.AgentInstance) (*agentProcess, error) {
			calls[name]++
			return &agentProcess{}, nil
		}
	}
	cfg := &config.Config{}
	cfg.ACP.Client.Runtime = "sandbox"
	m := &ClientManager{
		cfg:     cfg,
		runtime: acpRuntimeSandbox,
		procs:   map[string]*agentProcess{},
		spawners: map[string]func(context.Context, string, *agent.AgentInstance) (*agentProcess, error){
			acpRuntimeExec:      fake("exec"),
			acpRuntimeSandbox:   fake("sandbox"),
			acpRuntimeContainer: fake("container"),
		},
	}
	ctx := context.Background()

	if _, err := m.spawn(ctx, "a", acpBoundInst("a", nil)); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if calls["sandbox"] != 1 || calls["exec"] != 0 || calls["container"] != 0 {
		t.Fatalf("global sandbox should dispatch once to sandbox: %v", calls)
	}

	inst := acpBoundInst("b", &config.ACPAgentConfig{Command: "x", Runtime: "container"})
	if _, err := m.spawn(ctx, "b", inst); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if calls["container"] != 1 {
		t.Fatalf("binding override should dispatch to container: %v", calls)
	}
}

func TestSpawnRemoteIgnoresRuntime(t *testing.T) {
	dialed := false
	m := &ClientManager{
		cfg:     &config.Config{},
		runtime: acpRuntimeSandbox, // global sandbox; remote must still dial
		procs:   map[string]*agentProcess{},
		remoteDial: func(
			_ context.Context, _, _ string,
		) (io.ReadWriteCloser, error) {
			dialed = true
			return nil, errors.New("stop after dial")
		},
	}
	inst := acpBoundInst("r", &config.ACPAgentConfig{Remote: "peer.example"})
	if _, err := m.spawn(context.Background(), "r", inst); err == nil || !dialed {
		t.Fatalf("remote binding should reach the remote dialer, dialed=%v err=%v", dialed, err)
	}
}

func TestSandboxOptions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")
	cfg := &config.Config{}
	cfg.Isolation.Enabled = false // runtime=sandbox is the opt-in; global off
	cfg.ACP.Client.Sandbox = &config.ACPSandboxConfig{Network: "none"}
	m := &ClientManager{cfg: cfg}

	opts := m.sandboxOptions(root)
	if !opts.Enabled {
		t.Fatal("sandbox options must force Enabled")
	}
	if opts.Root != root {
		t.Fatalf("sandbox root = %q, want %q", opts.Root, root)
	}
	if opts.NetMode != isolation.NetModeNone {
		t.Fatalf("sandbox NetMode = %q, want none", opts.NetMode)
	}

	cfg.ACP.Client.Sandbox = nil
	opts = m.sandboxOptions(root)
	if opts.NetMode != "" {
		t.Fatalf("default sandbox NetMode = %q, want empty/inherit", opts.NetMode)
	}
}

func TestACPSandboxRootUnderHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(config.EnvHome, home)
	got := acpSandboxRoot("peer")
	want := filepath.Join(home, "acp-sandbox", "peer")
	if got != want {
		t.Fatalf("acpSandboxRoot() = %q, want %q", got, want)
	}
}

func TestContainerNameFor(t *testing.T) {
	a := containerNameFor("peer")
	b := containerNameFor("peer")
	if a == b {
		t.Fatal("container names must be unique per spawn")
	}
	if !strings.HasPrefix(a, "rhizome-acp-peer-") {
		t.Fatalf("containerNameFor() = %q", a)
	}
	if got := containerNameFor("weird/id:x"); !strings.HasPrefix(got, "rhizome-acp-weird-id-x-") {
		t.Fatalf("containerNameFor() sanitize = %q", got)
	}
}

func TestResolveContainerEngine(t *testing.T) {
	orig := containerEngineLookPath
	t.Cleanup(func() { containerEngineLookPath = orig })

	// auto: docker wins, podman falls back, neither errors with names.
	containerEngineLookPath = func(name string) (string, error) {
		if name == "docker" {
			return "/usr/bin/docker", nil
		}
		return "", errors.New("missing")
	}
	if got, err := resolveContainerEngine("auto"); err != nil || got != "docker" {
		t.Fatalf("auto with docker = %q, %v", got, err)
	}
	containerEngineLookPath = func(name string) (string, error) {
		if name == "podman" {
			return "/usr/bin/podman", nil
		}
		return "", errors.New("missing")
	}
	if got, err := resolveContainerEngine(""); err != nil || got != "podman" {
		t.Fatalf("auto fallback = %q, %v", got, err)
	}
	containerEngineLookPath = func(string) (string, error) { return "", errors.New("missing") }
	if _, err := resolveContainerEngine("auto"); err == nil ||
		!strings.Contains(err.Error(), "docker") || !strings.Contains(err.Error(), "podman") {
		t.Fatalf("auto with no engines should name both: %v", err)
	}
	// Explicit engine must exist.
	if _, err := resolveContainerEngine("podman"); err == nil {
		t.Fatal("explicit podman without binary should error")
	}
	containerEngineLookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	if got, err := resolveContainerEngine("podman"); err != nil || got != "podman" {
		t.Fatalf("explicit podman = %q, %v", got, err)
	}
}

func TestEnsureContainerImage_PullPolicies(t *testing.T) {
	origPresent, origProbe := containerImagePresent, containerProbe
	t.Cleanup(func() { containerImagePresent, containerProbe = origPresent, origProbe })

	ctx := context.Background()
	cc := &config.ACPContainerConfig{Image: "img:test"}

	// Present → no pull regardless of policy.
	containerImagePresent = func(context.Context, string, string) bool { return true }
	pulled := false
	containerProbe = func(context.Context, string, ...string) error {
		pulled = true
		return nil
	}
	for _, p := range []string{"", "missing", "never"} {
		cc.Pull = p
		if err := ensureContainerImage(ctx, "docker", cc); err != nil {
			t.Fatalf("pull=%s present: %v", p, err)
		}
	}
	if pulled {
		t.Fatal("image present must not pull")
	}

	// Absent: missing pulls, never errors.
	containerImagePresent = func(context.Context, string, string) bool { return false }
	cc.Pull = "missing"
	if err := ensureContainerImage(ctx, "docker", cc); err != nil {
		t.Fatalf("pull=missing absent: %v", err)
	}
	if !pulled {
		t.Fatal("pull=missing should pull when absent")
	}
	cc.Pull = "never"
	if err := ensureContainerImage(ctx, "docker", cc); err == nil ||
		!strings.Contains(err.Error(), "pull=never") {
		t.Fatalf("pull=never absent should error: %v", err)
	}
}

func TestContainerRunArgs(t *testing.T) {
	cc := &config.ACPContainerConfig{
		Image:   "img:test",
		Network: "none",
		MemMB:   512,
		Cpus:    1.5,
	}
	binding := &config.ACPAgentConfig{
		Command: "/agent",
		Args:    []string{"--serve"},
		Env:     map[string]string{"B": "2", "A": "1"},
		Cwd:     "/work",
	}
	args, err := containerRunArgs(cc, binding, "cname")
	if err != nil {
		t.Fatalf("containerRunArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run -i --rm --name cname",
		"--network none",
		"--memory 512m",
		"--cpus 1.5",
		"-e A=1", "-e B=2",
		"-w /work",
		"img:test /agent --serve",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv missing %q: %s", want, joined)
		}
	}
	// Env sorted before image; image precedes the in-container command.
	if i, j := strings.Index(joined, "-e A=1"), strings.Index(joined, "-e B=2"); i > j {
		t.Fatalf("env flags unsorted: %s", joined)
	}
	if strings.Index(joined, "img:test") < strings.Index(joined, "--network") {
		t.Fatalf("image must come after flags: %s", joined)
	}

	// Defaults: bridge network, image entrypoint when command empty.
	cc = &config.ACPContainerConfig{Image: "img:test"}
	binding = &config.ACPAgentConfig{}
	args, err = containerRunArgs(cc, binding, "c")
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--network bridge") || !strings.HasSuffix(joined, "img:test") {
		t.Fatalf("defaults argv = %s", joined)
	}

	// allowlist is schema-accepted but must fail the spawn explicitly.
	cc.Network = "allowlist"
	if _, err := containerRunArgs(cc, binding, "c"); err == nil ||
		!strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("allowlist should produce a clear error: %v", err)
	}
}

func TestSpawnContainerRequiresImage(t *testing.T) {
	m := &ClientManager{cfg: &config.Config{}, runtime: acpRuntimeContainer}
	if _, err := m.spawnContainer(context.Background(), "a", acpBoundInst("a", nil)); err == nil ||
		!strings.Contains(err.Error(), "container.image") {
		t.Fatalf("missing image should error clearly: %v", err)
	}
}

func TestSessionCwdPerRuntime(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(config.EnvHome, home)

	m := &ClientManager{cfg: &config.Config{}, runtime: acpRuntimeExec}
	ws := filepath.Join(t.TempDir(), "ws")
	execInst := acpBoundInst("e", nil)
	execInst.Workspace = ws
	if got := m.sessionCwd(execInst); got != ws {
		t.Fatalf("exec sessionCwd = %q, want %q", got, ws)
	}

	m.runtime = acpRuntimeSandbox
	if got, want := m.sessionCwd(execInst), acpSandboxRoot("e"); got != want {
		t.Fatalf("sandbox sessionCwd = %q, want scratch %q", got, want)
	}

	m.runtime = acpRuntimeContainer
	cInst := acpBoundInst("c", &config.ACPAgentConfig{})
	if got := m.sessionCwd(cInst); got != "/" {
		t.Fatalf("container sessionCwd = %q, want /", got)
	}
	cInst.ACP.Cwd = "/work"
	if got := m.sessionCwd(cInst); got != "/work" {
		t.Fatalf("container sessionCwd with cwd = %q, want /work", got)
	}
}

// TestSandboxNetNoneWindowsDenies verifies the fail-closed path where the
// platform backend cannot express net isolation.
func TestSandboxNetNonePlatformGate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")
	cmd := helperCommand(t)
	opts := isolation.Options{Enabled: true, Root: root, NetMode: isolation.NetModeNone}
	err := isolation.PrepareCommandWith(cmd, opts)
	switch runtime.GOOS {
	case "windows":
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("windows net=none must deny: %v", err)
		}
	case "linux", "darwin":
		// Backends enforce it (bwrap / sandbox-exec). Missing binaries are
		// an environment limitation, not the failure under test — either a
		// clean pass or a named-tool error is acceptable here.
		if err != nil && !strings.Contains(err.Error(), "bwrap") &&
			!strings.Contains(err.Error(), "sandbox-exec") {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
		if err == nil {
			t.Fatal("unsupported platform must fail closed")
		}
	}
}

func helperCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "exit", "0")
	}
	return exec.Command("sh", "-c", "true")
}
