// Package acp implements an Agent Client Protocol (ACP) server that exposes
// the Rhizome agent loop to ACP-capable clients (Zed, JetBrains IDEs, and
// other editors). The transport is newline-delimited JSON-RPC over stdio,
// provided by github.com/coder/acp-go-sdk; all SDK usage is confined to this
// package so the dependency can be swapped later.
//
// Sessions map onto the normal Rhizome routing/session pipeline: an ACP
// session gets a legacy agent-scoped session key of the form
// "agent:<agent-id>:acp:<session-id>" and every prompt runs through
// AgentLoop.ProcessInbound on channel "acp", so routing rules, history,
// tools, and provider fallback behave the same as any other channel.
package acp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/mcp"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/providers"
)

// ChannelName is the internal channel label ACP turns run under. It is only
// injected into the running process — no "acp" channel type is registered in
// configuration, so existing user configs are unaffected.
const ChannelName = "acp"

// AgentRunner is the subset of *agent.AgentLoop the ACP server drives. It is
// an interface so tests can substitute a fake loop.
type AgentRunner interface {
	ProcessInbound(ctx context.Context, msg bus.InboundMessage) (string, error)
	RuntimeEvents() events.EventChannel
	MountHook(reg agent.HookRegistration) error
	GetRegistry() *agent.AgentRegistry
}

// ClientConn is the client-bound side of the ACP connection: session
// notifications and permission requests. *acpsdk.AgentSideConnection
// satisfies it; tests substitute a fake.
type ClientConn interface {
	SessionUpdate(ctx context.Context, params acpsdk.SessionNotification) error
	RequestPermission(
		ctx context.Context,
		params acpsdk.RequestPermissionRequest,
	) (acpsdk.RequestPermissionResponse, error)
}

// Options configures a Server.
type Options struct {
	// AgentID pins ACP sessions to a specific agent (the --agent flag).
	// Empty means the routing default.
	AgentID string
	// Policy controls tool-permission bridging (prompt|allow|deny).
	Policy PermissionPolicy
	// Media is the store used to materialize image prompt blocks. When nil,
	// the image prompt capability is not advertised.
	Media media.MediaStore
	// Version is reported as the agent's implementation version.
	Version string
	// Sessions is the persisted ACP session index. When non-nil the server
	// advertises loadSession and records new sessions for session/load.
	Sessions *SessionStore
	// Logger receives diagnostics; defaults to slog.Default().
	Logger *slog.Logger
}

// Server implements acpsdk.Agent on top of the Rhizome agent loop.
type Server struct {
	runner  AgentRunner
	media   media.MediaStore
	policy  PermissionPolicy
	agentID string
	version string
	log     *slog.Logger
	store   *SessionStore

	// newMCPManager builds the per-session MCP manager — overridable in
	// tests.
	newMCPManager func() sessionMCPManager

	mu       sync.Mutex
	conn     ClientConn
	sessions map[acpsdk.SessionId]*acpSession

	evCancel context.CancelFunc
	evDone   chan struct{}
}

var _ acpsdk.Agent = (*Server)(nil)

// NewServer builds an ACP server over the given agent runner.
func NewServer(runner AgentRunner, opts Options) *Server {
	policy := opts.Policy
	if policy == "" {
		policy = PermissionPrompt
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		runner:   runner,
		media:    opts.Media,
		policy:   policy,
		agentID:  opts.AgentID,
		version:  opts.Version,
		store:    opts.Sessions,
		log:      log,
		sessions: make(map[acpsdk.SessionId]*acpSession),
	}
	s.newMCPManager = func() sessionMCPManager { return mcp.NewManager() }
	return s
}

// Bind attaches the live client connection. Call after
// acpsdk.NewAgentSideConnection — the SDK hands the connection to the Agent
// only via this convention.
func (s *Server) Bind(conn ClientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = conn
}

// Start mounts the tool-approval hook and subscribes to runtime events for
// tool-call progress updates. The subscription uses a background context so
// it outlives individual prompt requests.
func (s *Server) Start() error {
	if err := s.runner.MountHook(agent.NamedHook("acp-permission", &toolApprover{srv: s})); err != nil {
		return fmt.Errorf("mounting acp permission hook: %w", err)
	}

	evCtx, cancel := context.WithCancel(context.Background())
	sub, ch, err := s.runner.RuntimeEvents().SubscribeChan(evCtx, events.SubscribeOptions{
		Name:         "acp-tool-events",
		Buffer:       256,
		Backpressure: events.DropOldest,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("subscribing to runtime events: %w", err)
	}

	s.mu.Lock()
	s.evCancel = func() {
		cancel()
		_ = sub.Close()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	s.evDone = done
	go s.pumpToolEvents(evCtx, ch, done)
	return nil
}

// Close tears down the event subscription and every live session's MCP
// resources; the caller owns the connection.
func (s *Server) Close() {
	s.mu.Lock()
	cancel := s.evCancel
	done := s.evDone
	sessions := make([]*acpSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, sess := range sessions {
		sess.teardown()
	}
	if done != nil {
		<-done
	}
}

func (s *Server) connOrErr() (ClientConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, fmt.Errorf("acp: no client connection bound")
	}
	return s.conn, nil
}

func (s *Server) notify(ctx context.Context, sid acpsdk.SessionId, upd acpsdk.SessionUpdate) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("acp: no client connection bound")
	}
	return conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: sid, Update: upd})
}

func (s *Server) sessionByID(id acpsdk.SessionId) (*acpSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *Server) sessionByChatID(chatID string) *acpSession {
	sess, ok := s.sessionByID(acpsdk.SessionId(chatID))
	if !ok || sess.isClosed() {
		return nil
	}
	return sess
}

// resolveAgentID determines which agent an ACP session belongs to: the
// pinned --agent value when set, otherwise the registry default.
func (s *Server) resolveAgentID() (string, error) {
	if s.agentID != "" {
		return s.agentID, nil
	}
	reg := s.runner.GetRegistry()
	if reg == nil {
		return "", fmt.Errorf("no agent registry")
	}
	def := reg.GetDefaultAgent()
	if def == nil {
		return "", fmt.Errorf("no default agent configured")
	}
	return def.ID, nil
}

// --- acpsdk.Agent implementation ---

// Initialize reports Rhizome's agent capabilities to the client.
func (s *Server) Initialize(
	_ context.Context,
	params acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentInfo: &acpsdk.Implementation{
			Name:    "rhizome",
			Version: s.version,
		},
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession: s.store != nil,
			PromptCapabilities: acpsdk.PromptCapabilities{
				Image:           s.media != nil,
				EmbeddedContext: false,
			},
		},
		AuthMethods: []acpsdk.AuthMethod{},
	}, nil
}

// NewSession allocates an ACP session bound to a Rhizome agent-scoped
// session key, so history persists across prompts within the session.
func (s *Server) NewSession(
	ctx context.Context,
	params acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	agentID, err := s.resolveAgentID()
	if err != nil {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInternalError(map[string]any{"error": err.Error()})
	}

	sid := acpsdk.SessionId(uuid.NewString())
	sess := newACPSession(sid, fmt.Sprintf("agent:%s:acp:%s", agentID, sid))
	sess.cwd = params.Cwd
	sess.onDecisions = s.decisionPersister(sid)

	s.mu.Lock()
	s.sessions[sid] = sess
	s.mu.Unlock()

	s.sessionMCPTools(ctx, sess, agentID, params.McpServers)
	s.persistSession(sess, agentID)

	s.log.Info("acp: session created", "session_id", string(sid), "agent", agentID)
	return acpsdk.NewSessionResponse{SessionId: sid}, nil
}

// LoadSession implements acpsdk.AgentLoader: it re-registers a persisted
// ACP session and replays its stored history as session/update chunks.
func (s *Server) LoadSession(
	ctx context.Context,
	params acpsdk.LoadSessionRequest,
) (acpsdk.LoadSessionResponse, error) {
	if s.store == nil {
		return acpsdk.LoadSessionResponse{}, acpsdk.NewMethodNotFound("session/load")
	}
	rec, ok := s.store.GetSession(string(params.SessionId))
	if !ok {
		return acpsdk.LoadSessionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unknown session",
		})
	}
	// When an agent is pinned via --agent, refuse loads bound to another.
	if s.agentID != "" && rec.AgentID != s.agentID {
		return acpsdk.LoadSessionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "session belongs to a different agent",
		})
	}

	sid := acpsdk.SessionId(rec.SessionID)
	sess := newACPSession(sid, rec.SessionKey)
	sess.cwd = rec.Cwd
	sess.restoreDecisions(rec.AllowAlways, rec.DenyAlways)
	sess.onDecisions = s.decisionPersister(sid)

	s.mu.Lock()
	displaced := s.sessions[sid]
	s.sessions[sid] = sess
	s.mu.Unlock()
	if displaced != nil {
		displaced.close()
		displaced.teardown()
	}

	s.sessionMCPTools(ctx, sess, rec.AgentID, params.McpServers)
	s.replayHistory(ctx, sess)

	s.log.Info("acp: session loaded",
		"session_id", string(sid), "agent", rec.AgentID, "session_key", rec.SessionKey)
	return acpsdk.LoadSessionResponse{}, nil
}

// replayHistory resends stored user/assistant messages as session/update
// notifications so the client renders prior turns. Bounded to the newest
// maxReplayMessages.
func (s *Server) replayHistory(ctx context.Context, sess *acpSession) {
	const maxReplayMessages = 200
	reg := s.runner.GetRegistry()
	if reg == nil {
		return
	}
	// The session key embeds the agent id — find the owning agent's store.
	var history []providers.Message
	for _, id := range reg.ListAgentIDs() {
		inst, ok := reg.GetAgent(id)
		if !ok || inst == nil || inst.Sessions == nil {
			continue
		}
		if h := inst.Sessions.GetHistory(sess.key); len(h) > 0 {
			history = h
			break
		}
	}
	if len(history) > maxReplayMessages {
		history = history[len(history)-maxReplayMessages:]
	}
	for _, m := range history {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		var upd acpsdk.SessionUpdate
		switch m.Role {
		case "user":
			upd = acpsdk.UpdateUserMessageText(m.Content)
		case "assistant":
			upd = acpsdk.UpdateAgentMessageText(m.Content)
		default:
			continue
		}
		if err := s.notify(ctx, sess.id, upd); err != nil {
			s.log.Warn("acp: history replay failed", "session_id", string(sess.id), "error", err)
			return
		}
	}
}

// persistSession records the session in the store (no-op when unconfigured).
func (s *Server) persistSession(sess *acpSession, agentID string) {
	if s.store == nil {
		return
	}
	if err := s.store.PutSession(SessionRecord{
		SessionID:  string(sess.id),
		AgentID:    agentID,
		SessionKey: sess.key,
		Cwd:        sess.cwd,
		CreatedAt:  sess.createdAt,
	}); err != nil {
		s.log.Warn("acp: failed to persist session record", "session_id", string(sess.id), "error", err)
	}
}

// decisionPersister returns the onDecisions callback bound to a session id.
func (s *Server) decisionPersister(sid acpsdk.SessionId) func(allow, deny []string) {
	if s.store == nil {
		return nil
	}
	return func(allow, deny []string) {
		if err := s.store.UpdateDecisions(string(sid), allow, deny); err != nil {
			s.log.Warn("acp: failed to persist session decisions",
				"session_id", string(sid), "error", err)
		}
	}
}

// Prompt runs one ACP turn through the normal Rhizome inbound pipeline.
func (s *Server) Prompt(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	sess, ok := s.sessionByID(params.SessionId)
	if !ok || sess.isClosed() {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unknown or closed session",
		})
	}

	content, mediaRefs, err := s.promptBlocks(ctx, sess, params.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(map[string]any{"error": err.Error()})
	}

	stream := sess.resetStreamer(s, ctx)
	sess.markPromptStart()

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  ChannelName,
			ChatID:   string(sess.id),
			ChatType: "direct",
			SenderID: ChannelName,
		},
		Content:    content,
		Media:      mediaRefs,
		MediaScope: sess.key,
		SessionKey: sess.key,
	}

	text, runErr := s.runner.ProcessInbound(ctx, msg)

	// If the provider never streamed, deliver the final response as one
	// chunk so clients still see output.
	if !stream.published() && text != "" && ctx.Err() == nil {
		if err := s.notify(ctx, sess.id, acpsdk.UpdateAgentMessageText(text)); err != nil {
			s.log.Warn("acp: failed to send final response", "error", err)
		}
	}

	if ctx.Err() != nil {
		//nolint:nilerr // ACP cancellation is a stop reason, not a JSON-RPC error.
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	if runErr != nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInternalError(map[string]any{"error": runErr.Error()})
	}
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

// promptBlocks flattens ACP prompt content into the Rhizome inbound form:
// text blocks concatenate; image blocks are decoded into the media store and
// referenced via media:// refs; resource links degrade to a text reference.
func (s *Server) promptBlocks(
	ctx context.Context,
	sess *acpSession,
	blocks []acpsdk.ContentBlock,
) (string, []string, error) {
	var text []string
	var refs []string
	for i, b := range blocks {
		switch {
		case b.Text != nil:
			text = append(text, b.Text.Text)
		case b.Image != nil:
			if s.media == nil {
				return "", nil, fmt.Errorf("image prompts are not supported by this agent")
			}
			ref, err := s.storeImage(ctx, sess, b.Image, i)
			if err != nil {
				return "", nil, err
			}
			refs = append(refs, ref)
		case b.ResourceLink != nil:
			text = append(text, fmt.Sprintf("[resource: %s](%s)", b.ResourceLink.Name, b.ResourceLink.Uri))
		case b.Resource != nil && b.Resource.Resource.TextResourceContents != nil:
			text = append(text, b.Resource.Resource.TextResourceContents.Text)
		case b.Audio != nil:
			return "", nil, fmt.Errorf("audio prompts are not supported")
		}
	}
	content := ""
	for i, t := range text {
		if i > 0 {
			content += "\n"
		}
		content += t
	}
	return content, refs, nil
}

// storeImage decodes a base64 image block into a temp file and registers it
// with the media store under the session's scope.
func (s *Server) storeImage(
	ctx context.Context,
	sess *acpSession,
	img *acpsdk.ContentBlockImage,
	idx int,
) (string, error) {
	data, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		return "", fmt.Errorf("decoding image block: %w", err)
	}

	ext := ".bin"
	switch img.MimeType {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}

	f, err := os.CreateTemp("", fmt.Sprintf("acp-img-*%s", ext))
	if err != nil {
		return "", fmt.Errorf("creating image temp file: %w", err)
	}
	path := filepath.Clean(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("writing image temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	defer func() { _ = os.Remove(path) }()

	ref, err := s.media.Store(path, media.MediaMeta{
		Filename:    fmt.Sprintf("acp-image-%d%s", idx, ext),
		ContentType: img.MimeType,
		Source:      "acp",
	}, sess.key)
	if err != nil {
		return "", fmt.Errorf("storing image: %w", err)
	}
	return ref, nil
}

// Cancel is invoked on session/cancel. The SDK already cancels the
// per-prompt context before calling this, so no extra work is required.
func (s *Server) Cancel(_ context.Context, _ acpsdk.CancelNotification) error {
	return nil
}

// CloseSession marks a session closed and drops it from the registry. The
// persisted record is kept so a later session/load can resurrect it.
func (s *Server) CloseSession(
	_ context.Context,
	params acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	s.mu.Lock()
	sess, ok := s.sessions[params.SessionId]
	delete(s.sessions, params.SessionId)
	s.mu.Unlock()
	if ok {
		sess.close()
		sess.teardown()
	}
	return acpsdk.CloseSessionResponse{}, nil
}

// ListSessions reports live ACP sessions.
func (s *Server) ListSessions(
	_ context.Context,
	_ acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := acpsdk.ListSessionsResponse{Sessions: []acpsdk.SessionInfo{}}
	for _, sess := range s.sessions {
		if sess.isClosed() {
			continue
		}
		resp.Sessions = append(resp.Sessions, acpsdk.SessionInfo{
			SessionId: sess.id,
			Cwd:       sess.cwd,
		})
	}
	return resp, nil
}

// ResumeSession reattaches a live session; prompt history lives in the
// Rhizome session store so no replay is needed.
func (s *Server) ResumeSession(
	_ context.Context,
	params acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	if _, ok := s.sessionByID(params.SessionId); !ok {
		return acpsdk.ResumeSessionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unknown session",
		})
	}
	return acpsdk.ResumeSessionResponse{}, nil
}

// Authenticate is not used: Rhizome advertises no auth methods.
func (s *Server) Authenticate(
	_ context.Context,
	_ acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, acpsdk.NewMethodNotFound("authenticate")
}

// Logout is a no-op — there is no authenticated state.
func (s *Server) Logout(
	_ context.Context,
	_ acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

// SetSessionMode accepts mode changes as a no-op; Rhizome does not expose
// ACP session modes.
func (s *Server) SetSessionMode(
	_ context.Context,
	_ acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, nil
}

// SetSessionConfigOption echoes an empty config-option list; Rhizome does
// not expose client-tunable session config options yet.
func (s *Server) SetSessionConfigOption(
	_ context.Context,
	_ acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}
