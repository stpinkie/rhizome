package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	toolfs "github.com/stpinkie/rhizome/pkg/tools/fs"
)

// Client permission policies for external ACP agents (acp.client.permission_policy).
const (
	ClientPolicyDeny          = "deny"            // default: reject every permission request
	ClientPolicyAllowReadOnly = "allow-read-only" // allow read/search/think/fetch tool kinds
	ClientPolicyAllow         = "allow"           // allow everything
)

// ClientManager owns the lifecycle of external ACP agent processes declared
// via agents.list[].acp. Processes are spawned lazily on first use, reused
// across delegations, and restarted when the connection dies.
//
// Trust posture: the external agent runs as a plain child process (NOT under
// pkg/isolation's sandbox) because it needs its own filesystem layout and
// environment. Rhizome's contribution to safety is (a) the fs.* capabilities
// we serve back are workspace-sandboxed and (b) permission requests are
// answered by acp.client.permission_policy. Treat acp-bound agents like
// running the agent's own CLI yourself.
type ClientManager struct {
	cfg        *config.Config
	registry   func() *agent.AgentRegistry // resolved lazily: registry swaps on config reload
	policy     string
	termPolicy string

	mu    sync.Mutex
	procs map[string]*agentProcess

	// dial is the process-spawn seam; tests inject an in-process connection.
	dial func(ctx context.Context, agentID string, inst *agent.AgentInstance) (*agentProcess, error)
}

// NewClientManager builds a manager. getRegistry is resolved lazily because
// the agent registry is swapped on config reload. Returns nil when no agent
// carries an acp binding so callers can skip wiring.
func NewClientManager(cfg *config.Config, getRegistry func() *agent.AgentRegistry) *ClientManager {
	reg := getRegistry()
	if reg == nil {
		return nil
	}
	found := false
	for _, id := range reg.ListAgentIDs() {
		if inst, ok := reg.GetAgent(id); ok && inst != nil && inst.ACP != nil {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	m := &ClientManager{
		cfg:        cfg,
		registry:   getRegistry,
		policy:     normalizeClientPolicy(cfg.ACP.Client.PermissionPolicy),
		termPolicy: normalizeTerminalPolicy(cfg.ACP.Client.TerminalPolicy),
		procs:      make(map[string]*agentProcess),
	}
	m.dial = m.spawn
	return m
}

func normalizeClientPolicy(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case ClientPolicyAllow:
		return ClientPolicyAllow
	case ClientPolicyAllowReadOnly, "allow_read_only", "readonly", "read-only":
		return ClientPolicyAllowReadOnly
	default:
		return ClientPolicyDeny
	}
}

// agentProcess is one running external ACP agent with its connection.
type agentProcess struct {
	conn    *acpsdk.ClientSideConnection
	handler *clientHandler
	kill    func() // terminates the child process / test double
	mu      sync.Mutex
	closed  bool
}

// RunAgent sends a one-shot prompt to the ACP-bound agent and returns the
// accumulated text output. It implements tools.ACPInvoker.
func (m *ClientManager) RunAgent(ctx context.Context, agentID, prompt string) (string, error) {
	reg := m.registry()
	if reg == nil {
		return "", fmt.Errorf("agent registry unavailable")
	}
	inst, ok := reg.GetAgent(agentID)
	if !ok || inst == nil {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	if inst.ACP == nil {
		return "", fmt.Errorf("agent %q is not bound to an external ACP process", agentID)
	}

	proc, err := m.processFor(ctx, agentID, inst)
	if err != nil {
		return "", err
	}

	proc.mu.Lock()
	defer proc.mu.Unlock()

	session, err := proc.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        m.sessionCwd(inst),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		m.dropProcess(agentID, proc)
		return "", fmt.Errorf("acp session/new failed for agent %q: %w", agentID, err)
	}
	sessionID := session.SessionId
	defer func() {
		// Best-effort cleanup; the agent may not implement session/close.
		ctx2, cancel := context.WithTimeout(context.Background(), closeSessionTimeout)
		defer cancel()
		_, _ = proc.conn.CloseSession(ctx2, acpsdk.CloseSessionRequest{SessionId: sessionID})
		proc.handler.dropSession(sessionID)
	}()

	proc.handler.beginSession(sessionID)

	resp, err := proc.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(prompt)},
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		m.dropProcess(agentID, proc)
		return "", fmt.Errorf("acp session/prompt failed for agent %q: %w", agentID, err)
	}

	text := proc.handler.sessionText(sessionID)
	switch resp.StopReason {
	case acpsdk.StopReasonEndTurn:
		return text, nil
	case acpsdk.StopReasonCancelled:
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return text, fmt.Errorf("acp agent %q cancelled the turn", agentID)
	case acpsdk.StopReasonRefusal:
		return text, fmt.Errorf("acp agent %q refused the prompt", agentID)
	default:
		// max_tokens / max_turn_requests / unknown: return what we have with
		// an error so the caller sees the truncation.
		return text, fmt.Errorf("acp agent %q stopped early: %s", agentID, resp.StopReason)
	}
}

// RunRemote adapts the manager to agent.ExternalAgentRunner for mesh peers
// delegating into an ACP-bound agent id.
func (m *ClientManager) RunRemote(
	ctx context.Context,
	target *agent.AgentInstance,
	req agent.RemoteDispatchRequest,
) (string, error) {
	if target == nil || target.ACP == nil {
		return "", fmt.Errorf("agent is not ACP-bound")
	}
	if len(req.Media) > 0 {
		logger.WarnCF("acp", "dropping media attachments on external ACP dispatch (unsupported)",
			map[string]any{"agent_id": target.ID, "count": len(req.Media)})
	}
	return m.RunAgent(ctx, target.ID, req.Prompt)
}

// processFor returns a live process for the agent, spawning or respawning
// as needed.
func (m *ClientManager) processFor(
	ctx context.Context,
	agentID string,
	inst *agent.AgentInstance,
) (*agentProcess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p := m.procs[agentID]; p != nil && !p.closed {
		select {
		case <-p.conn.Done():
			p.closed = true
		default:
			return p, nil
		}
	}

	p, err := m.dial(ctx, agentID, inst)
	if err != nil {
		return nil, err
	}
	m.procs[agentID] = p
	return p, nil
}

func (m *ClientManager) dropProcess(agentID string, p *agentProcess) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.procs[agentID]; ok && cur == p {
		delete(m.procs, agentID)
	}
	if !p.closed {
		p.closed = true
		if p.handler != nil && p.handler.term != nil {
			p.handler.term.close()
		}
		if p.kill != nil {
			p.kill()
		}
	}
}

// Close terminates all managed processes.
func (m *ClientManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.procs {
		if !p.closed {
			p.closed = true
			if p.handler != nil && p.handler.term != nil {
				p.handler.term.close()
			}
			if p.kill != nil {
				p.kill()
			}
		}
		delete(m.procs, id)
	}
}

// spawn starts the configured ACP subprocess and performs the ACP
// initialize handshake.
func (m *ClientManager) spawn(
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

	//nolint:gosec // G204: command is the operator-configured agent binding
	cmd := exec.CommandContext(context.Background(), resolved, binding.Args...)
	cmd.Dir = m.sessionCwd(inst)
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
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp agent %q failed to start: %w", agentID, err)
	}

	// Drain stderr into the file logger — never stdout, which is the ACP
	// transport and must stay protocol-clean.
	go drainStderr(agentID, stderr)

	handler := &clientHandler{
		agentID:   agentID,
		policy:    m.policy,
		workspace: m.sessionCwd(inst),
		restrict:  m.cfg.Agents.Defaults.RestrictToWorkspace,
		allowRead: toolfs.CompilePatterns(m.cfg.Tools.AllowReadPaths),
		allowWr:   toolfs.CompilePatterns(m.cfg.Tools.AllowWritePaths),
		sessions:  make(map[acpsdk.SessionId]*sessionBuffer),
	}
	if m.termPolicy == TerminalPolicyAllow {
		bridge, err := newTerminalBridge(
			handler.workspace,
			handler.restrict,
			m.cfg,
			handler.allowWr,
		)
		if err != nil {
			logger.WarnCF("acp", "terminal bridge unavailable for agent",
				map[string]any{"agent_id": agentID, "error": err})
		} else {
			handler.term = bridge
		}
	}

	conn := acpsdk.NewClientSideConnection(handler, stdin, stdout)
	proc := &agentProcess{
		conn:    conn,
		handler: handler,
		kill: func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		},
	}

	initCtx, cancel := context.WithTimeout(ctx, acpInitTimeout)
	defer cancel()
	initResp, err := conn.Initialize(initCtx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name:    "rhizome",
			Version: config.FormatVersion(),
		},
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs: acpsdk.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: m.policy != ClientPolicyAllowReadOnly,
			},
			Terminal: handler.term != nil,
		},
	})
	if err != nil {
		proc.kill()
		return nil, fmt.Errorf("acp initialize failed for agent %q: %w", agentID, err)
	}
	if len(initResp.AuthMethods) > 0 {
		proc.kill()
		return nil, fmt.Errorf(
			"acp agent %q requires authentication (%d methods advertised); rhizome does not support ACP auth yet",
			agentID, len(initResp.AuthMethods),
		)
	}

	logger.InfoCF("acp", "external ACP agent connected",
		map[string]any{"agent_id": agentID, "command": command, "pid": cmd.Process.Pid})
	return proc, nil
}

// sessionCwd picks the working directory for the external agent: explicit
// acp.cwd wins, then the agent workspace, then the daemon workspace.
func (m *ClientManager) sessionCwd(inst *agent.AgentInstance) string {
	if inst != nil && inst.ACP != nil && strings.TrimSpace(inst.ACP.Cwd) != "" {
		if abs, err := filepath.Abs(inst.ACP.Cwd); err == nil {
			return abs
		}
		return inst.ACP.Cwd
	}
	if inst != nil && strings.TrimSpace(inst.Workspace) != "" {
		return inst.Workspace
	}
	if m.cfg != nil {
		return m.cfg.WorkspacePath()
	}
	return ""
}
