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
	// Models lists the model_list names offered in the session's
	// category:model config option. Empty disables the selector.
	Models []string
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
	models  []string
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
		models:   append([]string(nil), opts.Models...),
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
			// MCP transports we can host for session-declared servers:
			// stdio is implicit in ACP; http/sse/acp stay refused.
			McpCapabilities: acpsdk.McpCapabilities{
				Http: false,
				Sse:  false,
			},
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
	sess.agentID = agentID
	sess.onDecisions = s.decisionPersister(sid)

	s.mu.Lock()
	s.sessions[sid] = sess
	s.mu.Unlock()

	s.sessionMCPTools(ctx, sess, agentID, params.McpServers)
	s.persistSession(sess, agentID)

	s.log.Info("acp: session created", "session_id", string(sid), "agent", agentID)
	return acpsdk.NewSessionResponse{
		SessionId:     sid,
		Modes:         s.modeState(sess),
		ConfigOptions: s.configOptions(sess),
	}, nil
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
	sess.agentID = rec.AgentID
	sess.restoreDecisions(rec.AllowAlways, rec.DenyAlways)
	// Restore the persisted mode only when the current server policy still
	// offers it — a record written under prompt must not widen a deny server.
	if s.modeOffered(acpsdk.SessionModeId(rec.Mode)) {
		sess.setMode(acpsdk.SessionModeId(rec.Mode))
	}
	// Same for the persisted model override: an entry removed from
	// model_list since the record was written falls back to inherit.
	if s.modelOffered(rec.Model) {
		sess.setModel(rec.Model)
	}
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
	return acpsdk.LoadSessionResponse{
		Modes:         s.modeState(sess),
		ConfigOptions: s.configOptions(sess),
	}, nil
}

// availableModes is the session-mode set offered to clients. A deny server
// only advertises read-only — clients may narrow but never widen the
// operator's permission policy.
func (s *Server) availableModes() []acpsdk.SessionMode {
	readOnly := acpsdk.SessionMode{
		Id:          modeReadOnly,
		Name:        "Read-only",
		Description: acpsdk.Ptr("Reject every tool call — the agent can read and answer but not act"),
	}
	if s.policy == PermissionDeny {
		return []acpsdk.SessionMode{readOnly}
	}
	return []acpsdk.SessionMode{
		{
			Id:          modeAsk,
			Name:        "Ask",
			Description: acpsdk.Ptr("Prompt the client before each tool call"),
		},
		{
			Id:          modeAuto,
			Name:        "Auto",
			Description: acpsdk.Ptr("Approve every tool call without prompting"),
		},
		readOnly,
	}
}

// modeOffered reports whether id is in the advertised set.
func (s *Server) modeOffered(id acpsdk.SessionModeId) bool {
	for _, m := range s.availableModes() {
		if m.Id == id {
			return true
		}
	}
	return false
}

// modeState builds the SessionModeState reported on session responses.
func (s *Server) modeState(sess *acpSession) *acpsdk.SessionModeState {
	return &acpsdk.SessionModeState{
		AvailableModes: s.availableModes(),
		CurrentModeId:  sess.effectiveMode(s.policy),
	}
}

// Session config option ids and reserved select values.
const (
	modelConfigID     acpsdk.SessionConfigId      = "model"
	modelInheritValue acpsdk.SessionConfigValueId = "inherit"
)

// modelOffered reports whether name is a selectable model for this server.
func (s *Server) modelOffered(name string) bool {
	for _, m := range s.models {
		if m == name {
			return true
		}
	}
	return false
}

// agentModel returns the configured model of the session's bound agent.
func (s *Server) agentModel(sess *acpSession) string {
	reg := s.runner.GetRegistry()
	if reg == nil {
		return ""
	}
	inst, ok := reg.GetAgent(sess.agentID)
	if !ok || inst == nil {
		return ""
	}
	return inst.Model
}

// configOptions builds the session's config-option list. The only option
// today is the category:model select over Options.Models plus an "inherit"
// entry that clears the per-session override.
func (s *Server) configOptions(sess *acpSession) []acpsdk.SessionConfigOption {
	if len(s.models) == 0 {
		return nil
	}
	inheritName := "Agent default"
	if active := s.agentModel(sess); active != "" {
		inheritName = fmt.Sprintf("Agent default (%s)", active)
	}
	values := make([]acpsdk.SessionConfigSelectOption, 0, len(s.models)+1)
	values = append(values, acpsdk.SessionConfigSelectOption{
		Name:  inheritName,
		Value: modelInheritValue,
	})
	for _, name := range s.models {
		values = append(values, acpsdk.SessionConfigSelectOption{
			Name:  name,
			Value: acpsdk.SessionConfigValueId(name),
		})
	}
	current := sess.modelOverride()
	if current == "" {
		current = s.agentModel(sess)
	}
	if current == "" {
		current = string(modelInheritValue)
	}
	ungrouped := acpsdk.SessionConfigSelectOptionsUngrouped(values)
	return []acpsdk.SessionConfigOption{{
		Select: &acpsdk.SessionConfigOptionSelect{
			Id:           modelConfigID,
			Name:         "Model",
			Category:     acpsdk.Ptr(acpsdk.SessionConfigOptionCategoryModel),
			CurrentValue: acpsdk.SessionConfigValueId(current),
			Options:      acpsdk.SessionConfigSelectOptions{Ungrouped: &ungrouped},
		},
	}}
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

// persistMode records the session's mode override (no-op when unconfigured).
func (s *Server) persistMode(sess *acpSession) {
	if s.store == nil {
		return
	}
	if err := s.store.UpdateMode(string(sess.id), string(sess.sessionMode())); err != nil {
		s.log.Warn("acp: failed to persist session mode", "session_id", string(sess.id), "error", err)
	}
}

// persistModel records the session's model override (no-op when
// unconfigured).
func (s *Server) persistModel(sess *acpSession) {
	if s.store == nil {
		return
	}
	if err := s.store.UpdateModel(string(sess.id), sess.modelOverride()); err != nil {
		s.log.Warn("acp: failed to persist session model", "session_id", string(sess.id), "error", err)
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
		// Per-session model override (session/set_config_option).
		ModelOverride: sess.modelOverride(),
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

// SetSessionMode switches the session's permission mode. The change clears
// cached *_always decisions — a mode switch must not inherit approvals
// granted under a previous mode — persists the mode, and announces it via a
// current_mode_update notification.
func (s *Server) SetSessionMode(
	ctx context.Context,
	params acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	sess, ok := s.sessionByID(params.SessionId)
	if !ok || sess.isClosed() {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unknown or closed session",
		})
	}
	if !s.modeOffered(params.ModeId) {
		return acpsdk.SetSessionModeResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error":     fmt.Sprintf("unknown mode %q", params.ModeId),
			"available": s.availableModes(),
		})
	}

	sess.setMode(params.ModeId)
	sess.clearDecisions()
	s.persistMode(sess)

	if err := s.notify(ctx, sess.id, acpsdk.SessionUpdate{
		CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{CurrentModeId: params.ModeId},
	}); err != nil {
		s.log.Warn("acp: mode update notification failed",
			"session_id", string(sess.id), "error", err)
	}
	return acpsdk.SetSessionModeResponse{}, nil
}

// SetSessionConfigOption applies a client-chosen config option. The only
// declared option is the category:model select: a value id names a
// model_list entry and becomes the session's model override; "inherit"
// clears it. The response carries the full option set per the ACP spec.
func (s *Server) SetSessionConfigOption(
	_ context.Context,
	params acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	// Only the select variant is meaningful — no boolean options exist.
	if params.ValueId == nil {
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unsupported config option payload",
		})
	}
	req := params.ValueId
	sess, ok := s.sessionByID(req.SessionId)
	if !ok || sess.isClosed() {
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error": "unknown or closed session",
		})
	}
	if req.ConfigId != modelConfigID {
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error":      fmt.Sprintf("unknown configId %q", req.ConfigId),
			"candidates": []string{string(modelConfigID)},
		})
	}
	val := string(req.Value)
	switch {
	case val == string(modelInheritValue):
		sess.setModel("")
	case s.modelOffered(val):
		sess.setModel(val)
	default:
		return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewInvalidParams(map[string]any{
			"error":     fmt.Sprintf("unknown model %q", val),
			"available": s.models,
		})
	}
	s.persistModel(sess)
	return acpsdk.SetSessionConfigOptionResponse{ConfigOptions: s.configOptions(sess)}, nil
}
