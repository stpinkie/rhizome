// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/logger"
	shared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// marketAgent is the per-connection ACP agent side of a bridged peer
// stream. Each verified conn gets its own instance bound to the
// authenticated peer id + a module-wide session manager — the buyer's
// initialize/session/prompt traffic proxies to a per-session spawned
// agent (module is the ACP client to it). No capability is advertised:
// market sessions are prompt→response only.
type marketAgent struct {
	peer   string
	connID uint64
	mgr    *sessionMgr
	audit  *auditLogger

	mu       sync.Mutex
	upstream *acpsdk.AgentSideConnection // set by the bridge post-construction
	pending  *marketSession              // gated, awaiting session/new
	active   *marketSession              // bound ACP session
	closed   bool
}

// newConnAgent builds the per-conn agent; attachUpstream is called right
// after NewAgentSideConnection so SessionUpdate relays reach the buyer.
func newConnAgent(peer string, connID uint64, mgr *sessionMgr, audit *auditLogger) *marketAgent {
	return &marketAgent{peer: peer, connID: connID, mgr: mgr, audit: audit}
}

func (a *marketAgent) attachUpstream(conn *acpsdk.AgentSideConnection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upstream = conn
}

// newAgentConn binds the agent side of an ACP connection onto a verified
// bridged conn (ndjson framing handled by the SDK — the bridge splices
// raw bytes).
func newAgentConn(agent *marketAgent, w io.Writer, r io.Reader) *acpsdk.AgentSideConnection {
	return acpsdk.NewAgentSideConnection(agent, w, r)
}

// errReqf builds a RequestError with a code + formatted message.
func errReqf(code int, format string, args ...any) *acpsdk.RequestError {
	return &acpsdk.RequestError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// errNotYet is the honest refusal: the verb exists on the wire, the market
// implementation lands in a later track.
func errNotYet(verb, track string) *acpsdk.RequestError {
	return &acpsdk.RequestError{
		Code:    -32601,
		Message: verb + " is not implemented in this build",
		Data:    map[string]any{"track": track},
	}
}

// gateErr maps gate failures onto protocol errors — the gateError code
// rides in Data so buyers can branch without string-matching.
func gateErr(err error) error {
	if ge, ok := err.(*gateError); ok {
		return &acpsdk.RequestError{
			Code:    -32602,
			Message: ge.msg,
			Data:    map[string]any{"code": ge.code},
		}
	}
	return errReqf(-32603, "session_open failed: %v", err)
}

func (a *marketAgent) Initialize(
	_ context.Context, _ acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{},
	}, nil
}

func (a *marketAgent) Authenticate(
	_ context.Context, _ acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	// Peer identity was already authenticated by the bridge hello — ACP
	// authenticate is a no-op rather than a refusal.
	return acpsdk.AuthenticateResponse{}, nil
}

// NewSession binds the conn's gated pending session: the module spawns the
// offer's agent under the configured runtime and opens the agent-side
// session itself — buyer-supplied cwd/mcpServers are refused (the market
// controls the serving environment).
func (a *marketAgent) NewSession(
	ctx context.Context, req acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	a.mu.Lock()
	s := a.pending
	a.pending = nil
	a.mu.Unlock()
	if s == nil {
		return acpsdk.NewSessionResponse{}, errReqf(
			-32602, "session/new requires a prior _rhizome.session_open on this connection")
	}
	if len(req.McpServers) > 0 || len(req.AdditionalDirectories) > 0 {
		return acpsdk.NewSessionResponse{}, errReqf(-32602,
			"market sessions do not honor mcpServers/additionalDirectories — "+
				"the seller controls the serving environment")
	}
	if err := a.mgr.activate(ctx, a, s); err != nil {
		a.mgr.finish(s, sessionFailed, "activate: "+err.Error())
		return acpsdk.NewSessionResponse{}, errReqf(-32603, "agent spawn failed: %v", err)
	}
	a.mu.Lock()
	a.active = s
	a.mu.Unlock()
	return acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId(s.ID)}, nil
}

// Prompt forwards the buyer's single task to the spawned agent after the
// task-hash gate; the response mints the session receipt.
func (a *marketAgent) Prompt(
	ctx context.Context, req acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	a.mu.Lock()
	s := a.active
	a.mu.Unlock()
	if s == nil || !strings.EqualFold(string(req.SessionId), s.ID) {
		return acpsdk.PromptResponse{}, errReqf(
			-32602, "no active market session %q on this connection", req.SessionId)
	}
	s.mu.Lock()
	if s.promptDone {
		s.mu.Unlock()
		return acpsdk.PromptResponse{}, errReqf(-32602,
			"market sessions are single-prompt — open a new escrow for another task")
	}
	s.mu.Unlock()

	text, err := promptText(req.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	sum := sha256.Sum256([]byte(text))
	if sum != s.taskHash {
		a.audit.log("market.gate.task_mismatch", map[string]any{
			"peer": a.peer, "session_id": s.ID,
		})
		return acpsdk.PromptResponse{}, errReqf(-32602,
			"prompt does not match the session's committed task_hash")
	}

	if s.agent == nil || s.agent.Conn == nil {
		return acpsdk.PromptResponse{}, errReqf(-32603, "session has no live agent")
	}
	resp, err := s.agent.Conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: s.agentSID,
		Prompt:    req.Prompt,
	})
	if err != nil {
		a.mgr.finish(s, sessionFailed, "agent prompt: "+err.Error())
		return acpsdk.PromptResponse{}, errReqf(-32603, "agent prompt failed: %v", err)
	}

	s.mu.Lock()
	s.promptDone = true
	s.usage = sessionUsage(s.agent, resp)
	s.mu.Unlock()
	a.mgr.finish(s, sessionCompleted, "prompt complete")
	return resp, nil
}

// sessionUsage prefers the agent's own prompt-response usage (ACP's
// unstable Usage field); otherwise queries _rhizome.usage — external ACP
// agents that answer neither report nil honestly.
func sessionUsage(agent *acp.BoundAgent, resp acpsdk.PromptResponse) *shared.RemoteUsage {
	if u := resp.Usage; u != nil {
		return &shared.RemoteUsage{
			PromptTokens:     u.InputTokens,
			CompletionTokens: u.OutputTokens,
			TotalTokens:      u.TotalTokens,
		}
	}
	if agent == nil || agent.Conn == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := agent.Conn.CallExtension(ctx, "_rhizome.usage", nil)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var usage shared.RemoteUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil
	}
	return &usage
}

func (a *marketAgent) LoadSession(
	_ context.Context, _ acpsdk.LoadSessionRequest,
) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, errNotYet("session/load", "104+")
}

func (a *marketAgent) ListSessions(
	_ context.Context, _ acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, errNotYet("session/list", "104+")
}

func (a *marketAgent) ResumeSession(
	_ context.Context, _ acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, errNotYet("session/resume", "104+")
}

// CloseSession ends the market session: kill the agent, mint the receipt
// (interrupted if the prompt never completed), free scratch.
func (a *marketAgent) CloseSession(
	_ context.Context, req acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.active
	a.active = nil
	a.mu.Unlock()
	if s == nil || !strings.EqualFold(string(req.SessionId), s.ID) {
		return acpsdk.CloseSessionResponse{}, errReqf(
			-32602, "no active market session %q on this connection", req.SessionId)
	}
	state := sessionClosed
	if !s.promptDone {
		state = sessionFailed
	}
	a.mgr.finish(s, state, "buyer closed session")
	return acpsdk.CloseSessionResponse{}, nil
}

// Cancel forwards session/cancel to the spawned agent — best effort.
func (a *marketAgent) Cancel(ctx context.Context, req acpsdk.CancelNotification) error {
	a.mu.Lock()
	s := a.active
	a.mu.Unlock()
	if s != nil && s.agent != nil && strings.EqualFold(string(req.SessionId), s.ID) {
		_ = s.agent.Conn.Cancel(ctx, acpsdk.CancelNotification{SessionId: s.agentSID})
	}
	return nil
}

func (a *marketAgent) Logout(
	_ context.Context, _ acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (a *marketAgent) SetSessionMode(
	_ context.Context, _ acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, errNotYet("session/set_mode", "104+")
}

func (a *marketAgent) SetSessionConfigOption(
	_ context.Context, _ acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, errNotYet("session/set_config_option", "104+")
}

// HandleExtensionMethod is the `_rhizome.*` dispatch: session_open runs
// the escrow gate, receipt serves the signed payload; unknown extension
// methods get a method-not-found.
func (a *marketAgent) HandleExtensionMethod(
	ctx context.Context, method string, params json.RawMessage,
) (any, error) {
	switch method {
	case "_rhizome.session_open":
		s, err := gateSessionOpen(ctx, a.mgr, a.peer, a.connID, params)
		if err != nil {
			return nil, gateErr(err)
		}
		a.mu.Lock()
		if a.pending != nil || a.active != nil {
			a.mu.Unlock()
			return nil, errReqf(-32602,
				"one market session per connection — open a fresh stream for the next task")
		}
		a.pending = s
		a.mu.Unlock()
		return map[string]any{
			"accepted":   true,
			"session_id": s.ID,
			"offer_id":   s.Offer.ID,
		}, nil
	case "_rhizome.receipt":
		var req struct {
			SessionID string `json:"session_id"`
			TaskNonce string `json:"task_nonce"`
		}
		if err := json.Unmarshal(params, &req); err != nil || req.SessionID == "" {
			return nil, errReqf(-32602, "receipt requires {session_id}")
		}
		// Drawdown stores sessions under escrow#nonce — the composite
		// resolves first when the buyer supplies it, the bare key covers
		// per-task sessions.
		keys := []string{req.SessionID}
		if req.TaskNonce != "" {
			keys = append([]string{
				drawdownSessionKey(strings.ToLower(req.SessionID), req.TaskNonce),
			}, keys...)
		}
		for _, key := range keys {
			s := a.mgr.lookup(key)
			if s == nil {
				continue
			}
			s.mu.Lock()
			rc := s.receipt
			st := s.State
			s.mu.Unlock()
			if rc != nil {
				return rc, nil
			}
			if st == sessionOpen || st == sessionActive {
				return nil, errReqf(
					-32602, "receipt not ready — session %s still %s", req.SessionID, st)
			}
		}
		// Fall back to the persisted receipt store (finished sessions are
		// unregistered from memory after close).
		for _, key := range keys {
			if rc, err := loadReceipt(a.mgr.moduleDir, key); err == nil {
				return rc, nil
			}
		}
		return nil, errReqf(-32602, "no receipt for session %s", req.SessionID)
	default:
		return nil, errReqf(-32601, "unknown extension method %s", method)
	}
}

// close finalize-hook for the bridge: called when the transport drops.
func (a *marketAgent) onConnClose() {
	a.mu.Lock()
	a.closed = true
	s := a.active
	a.active = nil
	p := a.pending
	a.pending = nil
	a.mu.Unlock()
	if s != nil {
		a.mgr.finish(s, sessionFailed, "connection dropped")
	}
	if p != nil {
		a.mgr.finish(p, sessionFailed, "connection dropped before session/new")
	}
	logger.DebugCF("market", "buyer connection closed",
		map[string]any{"peer": a.peer, "conn_id": a.connID})
}
