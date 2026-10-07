package acp

import (
	"context"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// BoundSource is the agent subset the bound-spawn path needs — decoupled
// from the daemon's agent.AgentInstance so module processes (which share
// RHIZOME_HOME config but run no agent registry) can bind offer
// agent_bindings directly.
type BoundSource struct {
	ID        string
	Workspace string
	ACP       *config.ACPAgentConfig
}

// BoundSpawn controls one bound agent's spawn posture.
type BoundSpawn struct {
	// Runtime forces the process runtime ("exec"|"sandbox"|"container");
	// empty resolves binding.Runtime then acp.client.runtime like the
	// daemon path.
	Runtime string
	// ScratchDir is the working dir for exec spawns and the isolation root
	// for sandbox spawns. Container sessions live on image fs; the session
	// cwd is the caller's NewSession concern.
	ScratchDir string
	// Handler is the caller's client-side ACP handler — the bound path
	// advertises no client capabilities at initialize, so fs/terminal
	// requests should be refused by the handler regardless.
	Handler acpsdk.Client
	// ClientName is the initialize ClientInfo.name (default "rhizome-bound").
	ClientName string
	// NoEgress forces the tightest network posture the runtime supports:
	// sandbox gets isolation.NetModeNone, container gets --network none
	// (the market sell-side relies on it — a paid session must not get
	// silent unrestricted egress just because the operator configured
	// bridge mode for interactive agents).
	NoEgress bool
}

// BoundAgent is a spawned external ACP agent owned by the caller — one
// process per session, never pooled in ClientManager.procs. Close kills
// the child/container.
type BoundAgent struct {
	Conn *acpsdk.ClientSideConnection
	Caps acpsdk.AgentCapabilities // advertised at initialize
	kill func()
}

// Close terminates the spawned process/container. Idempotent.
func (b *BoundAgent) Close() {
	if b != nil && b.kill != nil {
		b.kill()
	}
}

// SpawnBound spawns one external ACP agent for a single consumer session
// and connects with the caller's handler — unlike the daemon's
// ClientManager path it pools nothing (process-per-session) and advertises
// zero ClientCapabilities (no fs/terminal), so the only agent→client
// traffic is session/update + permission requests the handler answers.
// Remote bindings are refused: bound sessions need a local command.
func SpawnBound(
	ctx context.Context,
	cfg *config.Config,
	src BoundSource,
	opts BoundSpawn,
) (*BoundAgent, error) {
	binding := src.ACP
	if binding == nil {
		return nil, fmt.Errorf("agent %q has no acp binding", src.ID)
	}
	if strings.TrimSpace(binding.Remote) != "" {
		return nil, fmt.Errorf(
			"agent %q is a remote acp binding — bound sessions spawn local agents only", src.ID)
	}
	if opts.Handler == nil {
		return nil, fmt.Errorf("bound spawn for agent %q requires a client handler", src.ID)
	}

	rt := normalizeACPRuntime(opts.Runtime)
	if rt == "" {
		rt = normalizeACPRuntime(binding.Runtime)
	}
	if rt == "" && cfg != nil {
		rt = normalizeACPRuntime(cfg.ACP.Client.Runtime)
	}
	if rt == "" {
		rt = acpRuntimeExec
	}

	dir := strings.TrimSpace(opts.ScratchDir)
	if dir == "" {
		dir = src.Workspace
	}
	if dir == "" && cfg != nil {
		dir = cfg.WorkspacePath()
	}

	// NoEgress is runtime-shaped: exec spawns an unwrapped child, so a
	// caller asking for enforced no-egress on exec would get a silent
	// unrestricted process — refuse instead of dropping the flag (the
	// market sell-side relies on this failing closed).
	if opts.NoEgress && rt == acpRuntimeExec {
		return nil, fmt.Errorf(
			"agent %q: no-egress requires runtime sandbox|container, not exec", src.ID)
	}

	var pio *procIO
	var err error
	switch rt {
	case acpRuntimeSandbox:
		pio, err = spawnSandboxIO(cfg, src.ID, binding, dir, opts.NoEgress)
	case acpRuntimeContainer:
		pio, err = spawnContainerIO(ctx, cfg, src.ID, binding, opts.NoEgress)
	default:
		pio, err = spawnExecIO(src.ID, binding, dir)
	}
	if err != nil {
		return nil, err
	}

	conn := acpsdk.NewClientSideConnection(opts.Handler, pio.stdin, pio.stdout)
	initCtx, cancel := context.WithTimeout(ctx, acpInitTimeout)
	defer cancel()
	name := strings.TrimSpace(opts.ClientName)
	if name == "" {
		name = "rhizome-bound"
	}
	initResp, err := conn.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name:    name,
			Version: config.FormatVersion(),
		},
		// Deliberately empty: bound sessions advertise no fs/terminal
		// capabilities — the handler refuses any that arrive anyway.
		ClientCapabilities: acpsdk.ClientCapabilities{},
	})
	if err != nil {
		pio.kill()
		return nil, fmt.Errorf("bound acp initialize failed for agent %q: %w", src.ID, err)
	}
	// terminalAllowed=false: bound sessions never bridge terminals, so only
	// env_var auth methods can be satisfied.
	if err := authenticateBound(
		initCtx, src.ID, binding, conn, initResp.AuthMethods, false); err != nil {
		pio.kill()
		return nil, err
	}

	fields := map[string]any{"agent_id": src.ID, "bound": true}
	for k, v := range pio.fields {
		fields[k] = v
	}
	logger.InfoCF("acp", "bound ACP agent connected", fields)
	return &BoundAgent{Conn: conn, Caps: initResp.AgentCapabilities, kill: pio.kill}, nil
}
