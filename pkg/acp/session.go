package acp

import (
	"context"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/tools"
)

// PermissionPolicy controls how tool-approval requests are bridged to the
// ACP client.
type PermissionPolicy string

const (
	// PermissionPrompt forwards every tool approval to the client as a
	// session/request_permission prompt. allow_always / reject_always
	// answers are cached per tool for the session lifetime.
	PermissionPrompt PermissionPolicy = "prompt"
	// PermissionAllow approves every tool call without asking the client.
	PermissionAllow PermissionPolicy = "allow"
	// PermissionDeny rejects every tool call, turning the agent read-only.
	PermissionDeny PermissionPolicy = "deny"
)

// acpSession tracks one ACP session: its Rhizome session key, the per-prompt
// streamer, cached always-decisions, and lifecycle state.
type acpSession struct {
	id        acpsdk.SessionId
	key       string
	cwd       string
	createdAt time.Time

	mu       sync.Mutex
	stream   *sessionStreamer
	allow    map[string]bool
	deny     map[string]bool
	closed   bool
	promptAt time.Time

	// mcp is the per-session MCP manager (nil when the session declared no
	// servers). toolNames are the session-scoped names registered on
	// toolRegistry; both are torn down on close.
	mcp          sessionMCPConn
	toolRegistry *tools.ToolRegistry
	toolNames    []string

	// onDecisions persists cached always-decisions (set by the server when a
	// session store is configured).
	onDecisions func(allow, deny []string)
}

// sessionMCPConn is the seam between a session and its MCP manager — the
// real implementation is *mcp.Manager; tests substitute a fake.
type sessionMCPConn interface {
	Close() error
}

func newACPSession(id acpsdk.SessionId, sessionKey string) *acpSession {
	return &acpSession{
		id:        id,
		key:       sessionKey,
		createdAt: time.Now(),
		allow:     make(map[string]bool),
		deny:      make(map[string]bool),
	}
}

func (s *acpSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *acpSession) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

// teardown releases session resources: the per-session MCP manager and the
// session-scoped tool registrations. Idempotent.
func (s *acpSession) teardown() {
	s.mu.Lock()
	mgr := s.mcp
	s.mcp = nil
	reg := s.toolRegistry
	names := s.toolNames
	s.toolRegistry = nil
	s.toolNames = nil
	s.mu.Unlock()

	if mgr != nil {
		_ = mgr.Close()
	}
	if reg != nil {
		for _, name := range names {
			reg.Unregister(name)
		}
	}
}

func (s *acpSession) markPromptStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptAt = time.Now()
}

// resetStreamer installs a fresh streamer for a new prompt turn and returns
// it. Each turn gets its own context and counters.
func (s *acpSession) resetStreamer(srv *Server, ctx context.Context) *sessionStreamer {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stream = &sessionStreamer{srv: srv, sess: s, ctx: ctx}
	return s.stream
}

// streamer returns the active turn's streamer, creating a detached one if a
// turn is somehow mid-flight without reset (defensive).
func (s *acpSession) streamer(srv *Server) *sessionStreamer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream == nil {
		s.stream = &sessionStreamer{srv: srv, sess: s}
	}
	return s.stream
}

// cachedDecision returns the cached allow_always/reject_always verdict for a
// tool, if one exists.
func (s *acpSession) cachedDecision(tool string) (approved bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deny[tool] {
		return false, true
	}
	if s.allow[tool] {
		return true, true
	}
	return false, false
}

func (s *acpSession) cacheDecision(tool string, approved bool) {
	s.mu.Lock()
	cb := s.onDecisions
	if approved {
		s.allow[tool] = true
	} else {
		s.deny[tool] = true
	}
	var allow, deny []string
	if cb != nil {
		allow = make([]string, 0, len(s.allow))
		for k := range s.allow {
			allow = append(allow, k)
		}
		deny = make([]string, 0, len(s.deny))
		for k := range s.deny {
			deny = append(deny, k)
		}
	}
	s.mu.Unlock()
	if cb != nil {
		cb(allow, deny)
	}
}

// restoreDecisions seeds the always-decision caches from a persisted record.
func (s *acpSession) restoreDecisions(allow, deny []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range allow {
		s.allow[t] = true
	}
	for _, t := range deny {
		s.deny[t] = true
	}
}

// setMCP wires the session's MCP manager + tool registrations for teardown.
func (s *acpSession) setMCP(mgr sessionMCPConn, reg *tools.ToolRegistry, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcp = mgr
	s.toolRegistry = reg
	s.toolNames = names
}
