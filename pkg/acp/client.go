package acp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/mcp"
	"github.com/stpinkie/rhizome/pkg/media"
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
	media      media.MediaStore // optional; media:// refs degrade without it

	mu       sync.Mutex
	procs    map[string]*agentProcess
	sessions map[string]acpsdk.SessionId // remembered persistent session ids

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
		sessions:   make(map[string]acpsdk.SessionId),
	}
	m.dial = m.spawn
	return m
}

// SetMediaStore injects the media store used to materialize media://
// attachment refs into ACP prompt content blocks.
func (m *ClientManager) SetMediaStore(s media.MediaStore) {
	m.media = s
}

func normalizeClientPolicy(p string) string {
	if v, ok := normalizeClientPolicyStrict(p); ok {
		return v
	}
	return ClientPolicyDeny
}

func normalizeClientPolicyStrict(p string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case ClientPolicyAllow:
		return ClientPolicyAllow, true
	case ClientPolicyAllowReadOnly, "allow_read_only", "readonly", "read-only":
		return ClientPolicyAllowReadOnly, true
	case ClientPolicyDeny:
		return ClientPolicyDeny, true
	default:
		return "", false
	}
}

// policyFor resolves the effective permission policy for a binding:
// agents.list[].acp.permission_policy wins over the acp.client global.
func (m *ClientManager) policyFor(inst *agent.AgentInstance) string {
	if inst != nil && inst.ACP != nil {
		if v, ok := normalizeClientPolicyStrict(inst.ACP.PermissionPolicy); ok {
			return v
		}
		if strings.TrimSpace(inst.ACP.PermissionPolicy) != "" {
			logger.WarnCF("acp", "unknown acp.permission_policy on agent binding; using acp.client default",
				map[string]any{"agent_id": inst.ID, "value": inst.ACP.PermissionPolicy})
		}
	}
	return m.policy
}

// termPolicyFor resolves the effective terminal policy for a binding:
// agents.list[].acp.terminal_policy wins over the acp.client global.
func (m *ClientManager) termPolicyFor(inst *agent.AgentInstance) string {
	if inst != nil && inst.ACP != nil {
		raw := strings.TrimSpace(inst.ACP.TerminalPolicy)
		if raw != "" {
			switch strings.ToLower(raw) {
			case TerminalPolicyAllow:
				return TerminalPolicyAllow
			case TerminalPolicyDeny:
				return TerminalPolicyDeny
			default:
				logger.WarnCF("acp", "unknown acp.terminal_policy on agent binding; using acp.client default",
					map[string]any{"agent_id": inst.ID, "value": inst.ACP.TerminalPolicy})
			}
		}
	}
	return m.termPolicy
}

// sessionModeFor returns "persistent" or "oneshot" (default) for a binding.
func sessionModeFor(inst *agent.AgentInstance) string {
	if inst == nil || inst.ACP == nil {
		return sessionModeOneshot
	}
	switch strings.ToLower(strings.TrimSpace(inst.ACP.SessionMode)) {
	case "", sessionModeOneshot:
		return sessionModeOneshot
	case sessionModePersistent:
		return sessionModePersistent
	default:
		return ""
	}
}

const (
	sessionModeOneshot    = "oneshot"
	sessionModePersistent = "persistent"
)

// agentProcess is one running external ACP agent with its connection.
type agentProcess struct {
	conn      *acpsdk.ClientSideConnection
	handler   *clientHandler
	caps      acpsdk.AgentCapabilities // advertised at initialize
	sessionID acpsdk.SessionId         // live session on this process (persistent mode)
	kill      func()                   // terminates the child process / test double
	mu        sync.Mutex
	closed    bool
}

// RunAgent sends a prompt to the ACP-bound agent and returns the
// accumulated text output. It implements tools.ACPInvoker. Session
// lifetime follows agents.list[].acp.session_mode: "oneshot" (default)
// creates and closes a session per call; "persistent" reuses one session
// per agent id.
func (m *ClientManager) RunAgent(ctx context.Context, agentID, prompt string) (string, error) {
	return m.runAgent(ctx, agentID, prompt, nil)
}

func (m *ClientManager) runAgent(
	ctx context.Context,
	agentID, prompt string,
	mediaRefs []string,
) (string, error) {
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

	persistent := sessionModeFor(inst) == sessionModePersistent
	var sessionID acpsdk.SessionId
	if persistent {
		sessionID, err = m.ensureSessionLocked(ctx, proc, inst)
	} else {
		sessionID, err = m.newSessionLocked(ctx, proc, inst)
	}
	if err != nil {
		m.dropProcess(agentID, proc)
		return "", err
	}
	if !persistent {
		defer func() {
			// Best-effort cleanup; the agent may not implement session/close.
			ctx2, cancel := context.WithTimeout(context.Background(), closeSessionTimeout)
			defer cancel()
			_, _ = proc.conn.CloseSession(ctx2, acpsdk.CloseSessionRequest{SessionId: sessionID})
			proc.handler.dropSession(sessionID)
			proc.sessionID = ""
		}()
	}

	proc.handler.beginSession(sessionID)

	resp, err := proc.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sessionID,
		Prompt:    m.promptBlocks(prompt, mediaRefs, proc),
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

// newSessionLocked creates a fresh session on the process: forwards the
// operator's MCP servers (filtered by advertised capabilities) and then
// acp.mode via session/set_mode when advertised. Caller holds proc.mu.
func (m *ClientManager) newSessionLocked(
	ctx context.Context,
	proc *agentProcess,
	inst *agent.AgentInstance,
) (acpsdk.SessionId, error) {
	session, err := proc.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        m.sessionCwd(inst),
		McpServers: m.mcpServers(proc, inst),
	})
	if err != nil {
		return "", fmt.Errorf("acp session/new failed for agent %q: %w", inst.ID, err)
	}
	proc.sessionID = session.SessionId
	m.forwardModeLocked(ctx, proc, inst, session.SessionId, session.Modes)
	return session.SessionId, nil
}

// ensureSessionLocked returns the process's live persistent session,
// recreating it when needed: session/load with the remembered id when the
// agent advertised loadSession, else a fresh session/new. Caller holds
// proc.mu.
func (m *ClientManager) ensureSessionLocked(
	ctx context.Context,
	proc *agentProcess,
	inst *agent.AgentInstance,
) (acpsdk.SessionId, error) {
	if proc.sessionID != "" {
		return proc.sessionID, nil
	}
	m.mu.Lock()
	remembered := m.sessions[inst.ID]
	m.mu.Unlock()
	if remembered != "" && proc.caps.LoadSession {
		loadCtx, cancel := context.WithTimeout(ctx, acpInitTimeout)
		resp, err := proc.conn.LoadSession(loadCtx, acpsdk.LoadSessionRequest{
			SessionId:  remembered,
			Cwd:        m.sessionCwd(inst),
			McpServers: m.mcpServers(proc, inst),
		})
		cancel()
		if err == nil {
			proc.sessionID = remembered
			m.forwardModeLocked(ctx, proc, inst, remembered, resp.Modes)
			return remembered, nil
		}
		logger.WarnCF("acp", "session/load failed; falling back to session/new",
			map[string]any{"agent_id": inst.ID, "session_id": string(remembered), "error": err.Error()})
	}
	sessionID, err := m.newSessionLocked(ctx, proc, inst)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	m.sessions[inst.ID] = sessionID
	m.mu.Unlock()
	return sessionID, nil
}

// mcpServers maps the operator's enabled tools.mcp.servers entries onto
// ACP session-declared MCP servers, honoring the transports the external
// agent advertised in mcpCapabilities: stdio is implicit, http/sse entries
// are refused with a per-entry error rather than silently dropped.
func (m *ClientManager) mcpServers(proc *agentProcess, inst *agent.AgentInstance) []acpsdk.McpServer {
	configured := m.cfg.Tools.MCP.Servers
	if len(configured) == 0 {
		return []acpsdk.McpServer{}
	}
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)

	caps := proc.caps.McpCapabilities
	out := make([]acpsdk.McpServer, 0, len(configured))
	for _, name := range names {
		sc := configured[name]
		if !sc.Enabled {
			continue
		}
		srv, err := mcpServerFor(name, sc, caps, m.sessionCwd(inst))
		if err != nil {
			logger.ErrorCF("acp", "mcp server not forwarded to external agent",
				map[string]any{"agent_id": inst.ID, "server": name, "error": err.Error()})
			continue
		}
		out = append(out, srv)
	}
	if out == nil {
		return []acpsdk.McpServer{}
	}
	return out
}

// mcpServerFor converts one tools.mcp.servers entry into its ACP
// McpServer variant. http/sse transports require the matching advertised
// capability; env files are expanded locally so the external agent sees
// the same effective environment the local MCP client would have used.
func mcpServerFor(
	name string,
	sc config.MCPServerConfig,
	caps acpsdk.McpCapabilities,
	workspace string,
) (acpsdk.McpServer, error) {
	typ := strings.ToLower(strings.TrimSpace(sc.Type))
	if typ == "" {
		if strings.TrimSpace(sc.Command) != "" {
			typ = "stdio"
		} else {
			typ = "sse"
		}
	}
	switch typ {
	case "stdio":
		env := make(map[string]string, len(sc.Env))
		for k, v := range sc.Env {
			env[k] = v
		}
		if sc.EnvFile != "" {
			envFile := sc.EnvFile
			if !filepath.IsAbs(envFile) && workspace != "" {
				envFile = filepath.Join(workspace, envFile)
			}
			vars, err := mcp.LoadEnvFile(envFile)
			if err != nil {
				return acpsdk.McpServer{}, fmt.Errorf("load env file: %w", err)
			}
			for k, v := range vars {
				if _, ok := env[k]; !ok {
					env[k] = v
				}
			}
		}
		vars := make([]acpsdk.EnvVariable, 0, len(env))
		for k, v := range env {
			vars = append(vars, acpsdk.EnvVariable{Name: k, Value: v})
		}
		sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
		return acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{
			Name:    name,
			Command: sc.Command,
			Args:    sc.Args,
			Env:     vars,
		}}, nil
	case "sse":
		if !caps.Sse {
			return acpsdk.McpServer{}, fmt.Errorf("sse transport not supported by agent mcpCapabilities")
		}
		return acpsdk.McpServer{Sse: &acpsdk.McpServerSseInline{
			Name:    name,
			Url:     sc.URL,
			Headers: httpHeaders(sc.Headers),
		}}, nil
	case "http", "streamable-http":
		if !caps.Http {
			return acpsdk.McpServer{}, fmt.Errorf("http transport not supported by agent mcpCapabilities")
		}
		return acpsdk.McpServer{Http: &acpsdk.McpServerHttpInline{
			Name:    name,
			Url:     sc.URL,
			Headers: httpHeaders(sc.Headers),
		}}, nil
	default:
		return acpsdk.McpServer{}, fmt.Errorf("unknown mcp server type %q", sc.Type)
	}
}

func httpHeaders(h map[string]string) []acpsdk.HttpHeader {
	if len(h) == 0 {
		return nil
	}
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]acpsdk.HttpHeader, 0, len(h))
	for _, k := range names {
		out = append(out, acpsdk.HttpHeader{Name: k, Value: h[k]})
	}
	return out
}

// forwardModeLocked applies agents.list[].acp.mode via session/set_mode
// when the agent advertised that mode in the session response; anything
// else warns and continues — agents own their mode vocabularies.
func (m *ClientManager) forwardModeLocked(
	ctx context.Context,
	proc *agentProcess,
	inst *agent.AgentInstance,
	sessionID acpsdk.SessionId,
	modes *acpsdk.SessionModeState,
) {
	want := ""
	if inst != nil && inst.ACP != nil {
		want = strings.TrimSpace(inst.ACP.Mode)
	}
	if want == "" {
		return
	}
	if modes == nil || len(modes.AvailableModes) == 0 {
		logger.WarnCF("acp", "acp.mode configured but agent advertises no session modes",
			map[string]any{"agent_id": inst.ID, "mode": want})
		return
	}
	available := make([]string, 0, len(modes.AvailableModes))
	advertised := false
	for _, am := range modes.AvailableModes {
		available = append(available, string(am.Id))
		if string(am.Id) == want {
			advertised = true
		}
	}
	if !advertised {
		logger.WarnCF("acp", "acp.mode not advertised by agent; leaving agent default",
			map[string]any{"agent_id": inst.ID, "mode": want, "available": strings.Join(available, ",")})
		return
	}
	modeCtx, cancel := context.WithTimeout(ctx, acpInitTimeout)
	defer cancel()
	if _, err := proc.conn.SetSessionMode(modeCtx, acpsdk.SetSessionModeRequest{
		SessionId: sessionID,
		ModeId:    acpsdk.SessionModeId(want),
	}); err != nil {
		logger.WarnCF("acp", "session/set_mode failed; continuing with agent default",
			map[string]any{"agent_id": inst.ID, "mode": want, "error": err.Error()})
	}
}

// promptBlocks builds the prompt content blocks: the text prompt plus one
// image block per media:// ref when the agent advertised
// promptCapabilities.image; anything unresolvable or unsupported degrades
// to a text reference appended to the prompt.
func (m *ClientManager) promptBlocks(prompt string, refs []string, proc *agentProcess) []acpsdk.ContentBlock {
	var degraded strings.Builder
	blocks := make([]acpsdk.ContentBlock, 0, len(refs)+1)
	for _, ref := range refs {
		block, name, ok := m.mediaBlock(ref, proc)
		if ok {
			blocks = append(blocks, block)
			continue
		}
		if name == "" {
			name = ref
		}
		fmt.Fprintf(&degraded, "\n[media attachment: %s](%s)", name, ref)
	}
	return append([]acpsdk.ContentBlock{acpsdk.TextBlock(prompt + degraded.String())}, blocks...)
}

// mediaBlock resolves one media:// ref into a ContentBlockImage. Returns
// the resolved display name so callers can degrade to a text reference.
func (m *ClientManager) mediaBlock(ref string, proc *agentProcess) (acpsdk.ContentBlock, string, bool) {
	if m.media == nil {
		return acpsdk.ContentBlock{}, "", false
	}
	path, meta, err := m.media.ResolveWithMeta(ref)
	if err != nil {
		logger.WarnCF("acp", "cannot resolve media ref for external agent",
			map[string]any{"agent_id": proc.handler.agentID, "ref": ref, "error": err.Error()})
		return acpsdk.ContentBlock{}, "", false
	}
	if !proc.caps.PromptCapabilities.Image || !strings.HasPrefix(meta.ContentType, "image/") {
		return acpsdk.ContentBlock{}, meta.Filename, false
	}
	maxSize := int64(m.cfg.Agents.Defaults.GetMaxMediaSize())
	if info, statErr := os.Stat(path); statErr != nil || info.Size() > maxSize {
		return acpsdk.ContentBlock{}, meta.Filename, false
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the media store
	if err != nil {
		return acpsdk.ContentBlock{}, meta.Filename, false
	}
	return acpsdk.ImageBlock(base64.StdEncoding.EncodeToString(data), meta.ContentType), "", true
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
	return m.runAgent(ctx, target.ID, req.Prompt, req.Media)
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
	if sessionModeFor(inst) == "" {
		logger.WarnCF("acp", "unknown acp.session_mode on agent binding; using oneshot",
			map[string]any{"agent_id": agentID, "value": inst.ACP.SessionMode})
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

// Close terminates all managed processes, politely closing any live
// persistent sessions first.
func (m *ClientManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.procs {
		if !p.closed {
			p.closed = true
			if p.sessionID != "" {
				ctx, cancel := context.WithTimeout(context.Background(), closeSessionTimeout)
				_, _ = p.conn.CloseSession(ctx, acpsdk.CloseSessionRequest{SessionId: p.sessionID})
				cancel()
				if p.handler != nil {
					p.handler.dropSession(p.sessionID)
				}
			}
			if p.handler != nil && p.handler.term != nil {
				p.handler.term.close()
			}
			if p.kill != nil {
				p.kill()
			}
		}
		delete(m.procs, id)
	}
	clear(m.sessions)
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

	policy := m.policyFor(inst)
	handler := &clientHandler{
		agentID:   agentID,
		policy:    policy,
		workspace: m.sessionCwd(inst),
		restrict:  m.cfg.Agents.Defaults.RestrictToWorkspace,
		allowRead: toolfs.CompilePatterns(m.cfg.Tools.AllowReadPaths),
		allowWr:   toolfs.CompilePatterns(m.cfg.Tools.AllowWritePaths),
		sessions:  make(map[acpsdk.SessionId]*sessionBuffer),
	}
	if m.termPolicyFor(inst) == TerminalPolicyAllow {
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
				WriteTextFile: policy != ClientPolicyAllowReadOnly,
			},
			Terminal: handler.term != nil,
		},
	})
	if err != nil {
		proc.kill()
		return nil, fmt.Errorf("acp initialize failed for agent %q: %w", agentID, err)
	}
	proc.caps = initResp.AgentCapabilities
	if err := m.authenticate(initCtx, agentID, inst, conn, initResp.AuthMethods); err != nil {
		proc.kill()
		return nil, err
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
