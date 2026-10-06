// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/settlement"
)

// --- test doubles --------------------------------------------------------

// stubAgent is an in-process ACP agent served over one end of a net.Pipe —
// the spawnFn seam hands the module the client end as a BoundAgent, so the
// whole gate→spawn→prompt→receipt path runs without real processes.
type stubAgent struct {
	replyText  string
	usage      *acpsdk.Usage
	promptCh   chan string
	spawned    *atomic.Int32
	agentConn  *acpsdk.AgentSideConnection
	promptErr  error
	sessionErr error
}

func (s *stubAgent) agentIface() *stubAgentIface { return &stubAgentIface{s: s} }

type stubAgentIface struct{ s *stubAgent }

func (a *stubAgentIface) Initialize(
	context.Context, acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{},
	}, nil
}

func (a *stubAgentIface) Authenticate(
	context.Context, acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, nil
}

func (a *stubAgentIface) NewSession(
	_ context.Context, _ acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	if a.s.sessionErr != nil {
		return acpsdk.NewSessionResponse{}, a.s.sessionErr
	}
	return acpsdk.NewSessionResponse{SessionId: "agent-session-1"}, nil
}

func (a *stubAgentIface) Prompt(
	ctx context.Context, req acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	if a.s.promptCh != nil {
		var b strings.Builder
		for _, blk := range req.Prompt {
			if blk.Text != nil {
				b.WriteString(blk.Text.Text)
			}
		}
		select {
		case a.s.promptCh <- b.String():
		default:
		}
	}
	if a.s.promptErr != nil {
		return acpsdk.PromptResponse{}, a.s.promptErr
	}
	if a.s.replyText != "" && a.s.agentConn != nil {
		_ = a.s.agentConn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: req.SessionId,
			Update: acpsdk.SessionUpdate{
				AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
					Content: acpsdk.ContentBlock{
						Text: &acpsdk.ContentBlockText{Type: "text", Text: a.s.replyText},
					},
				},
			},
		})
	}
	return acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonEndTurn,
		Usage:      a.s.usage,
	}, nil
}

func (a *stubAgentIface) LoadSession(
	context.Context, acpsdk.LoadSessionRequest,
) (acpsdk.LoadSessionResponse, error) {
	return acpsdk.LoadSessionResponse{}, &acpsdk.RequestError{Code: -32601, Message: "nope"}
}

func (a *stubAgentIface) ListSessions(
	context.Context, acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}

func (a *stubAgentIface) ResumeSession(
	context.Context, acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (a *stubAgentIface) CloseSession(
	context.Context, acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *stubAgentIface) SetSessionMode(
	context.Context, acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, nil
}

func (a *stubAgentIface) SetSessionConfigOption(
	context.Context, acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

func (a *stubAgentIface) Logout(
	context.Context, acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (a *stubAgentIface) Cancel(context.Context, acpsdk.CancelNotification) error { return nil }

// --- fixtures ------------------------------------------------------------

const (
	testBuyer  = "0x8227b9868e00B8eE951F17B480D369b84Cd17c20"
	testSeller = "0x2222222222222222222222222222222222222222"
	testPrompt = "summarise the weekly report"
)

var testTaskHash = sha256.Sum256([]byte(testPrompt))

func taskHashHex() string { return "0x" + hex.EncodeToString(testTaskHash[:]) }

// gateFixture builds a sessionMgr wired to a MockRail with one funded
// escrow for (buyer, testOffer, taskHash) and returns the pieces.
type gateFixture struct {
	mgr       *sessionMgr
	rail      *settlement.MockRail
	mc        *marketConfig
	sessionID string
	stub      *stubAgent
	dir       string
}

func newGateFixture(t *testing.T, maxSessions int) *gateFixture {
	t.Helper()
	dir := t.TempDir()
	rail := settlement.NewMockRail(settlement.RailConfig{})
	mc := &marketConfig{
		serveEnabled:  true,
		runtime:       "sandbox",
		payoutAddress: testSeller,
		payoutAsset:   "USDC",
		payoutChainID: 11155111,
		maxSessions:   maxSessions,
		sessionTTL:    50 * time.Millisecond,
		offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet: priceSheet{
				PerTask: "5", Asset: "USDC", ChainID: 11155111,
			},
		}},
	}
	stub := &stubAgent{promptCh: make(chan string, 1)}
	mgr := newSessionMgr(dir, newAuditLogger(""))
	mgr.spawnFn = func(
		_ context.Context, _ *config.Config, _ acp.BoundSource, opts acp.BoundSpawn,
	) (*acp.BoundAgent, error) {
		return boundPipeAgent(t, stub, opts)
	}
	bindings := map[string]acp.BoundSource{
		"agent-1": {ID: "agent-1", ACP: &config.ACPAgentConfig{Command: "stub"}},
	}
	var railAny settlement.Rail = rail
	mgr.setConfig(mc, &config.Config{}, bindings, railAny)

	// Fixture decimals = 6 → per_task "5" → 5_000_000 base units.
	sessionID := rail.PredictEscrowAddr("corr-1")
	_, err := rail.Open(context.Background(), "corr-1", settlement.Terms{
		Buyer:    testBuyer,
		Seller:   testSeller,
		Token:    fixtureToken,
		Amount:   big.NewInt(5_000_000),
		TaskHash: testTaskHash,
	})
	if err != nil {
		t.Fatalf("mock open: %v", err)
	}
	return &gateFixture{
		mgr: mgr, rail: rail, mc: mc, sessionID: sessionID, stub: stub, dir: dir,
	}
}

// boundPipeAgent builds a BoundAgent over a net.Pipe whose far end serves
// the stub agent with the module's real client handler in between —
// exactly what SpawnBound produces, minus the process.
func boundPipeAgent(
	t *testing.T, stub *stubAgent, opts acp.BoundSpawn,
) (*acp.BoundAgent, error) {
	t.Helper()
	c1, c2 := net.Pipe()
	iface := stub.agentIface()
	stub.agentConn = acpsdk.NewAgentSideConnection(iface, c2, c2)
	go func() { <-stub.agentConn.Done(); _ = c2.Close() }()
	conn := acpsdk.NewClientSideConnection(opts.Handler, c1, c1)
	ictx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.Initialize(ictx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	}); err != nil {
		return nil, err
	}
	if stub.spawned != nil {
		stub.spawned.Add(1)
	}
	return &acp.BoundAgent{Conn: conn}, nil
}

// presentation builds the session_open params matching the fixture.
func (f *gateFixture) presentation(overrides map[string]string) json.RawMessage {
	p := map[string]any{
		"session_id": f.sessionID,
		"task_hash":  taskHashHex(),
		"offer_id":   "offer-1",
		"buyer":      testBuyer,
		"terms": map[string]any{
			"amount": "5000000",
			"token":  fixtureToken,
		},
	}
	for k, v := range overrides {
		p[k] = v
	}
	data, _ := json.Marshal(p)
	return data
}

func newAgent(f *gateFixture) *marketAgent {
	return newConnAgent("12D3peer-buyer", f.mgr.nextConnID(), f.mgr, newAuditLogger(""))
}

// --- gate tests -----------------------------------------------------------

func TestGate_PresentationValidation(t *testing.T) {
	f := newGateFixture(t, 4)
	a := newAgent(f)
	ctx := context.Background()

	cases := []struct {
		name   string
		raw    json.RawMessage
		mutate func(map[string]any)
		code   string
	}{
		{name: "bad json", raw: json.RawMessage(`{bad`), code: "bad_request"},
		{name: "bad session id", mutate: func(m map[string]any) {
			m["session_id"] = "not-an-addr"
		}, code: "bad_session_id"},
		{name: "bad task hash", mutate: func(m map[string]any) {
			m["task_hash"] = "0x1234"
		}, code: "bad_task_hash"},
		{name: "unknown offer", mutate: func(m map[string]any) {
			m["offer_id"] = "nope"
		}, code: "unknown_offer"},
		{name: "bad buyer", mutate: func(m map[string]any) {
			m["buyer"] = "not-an-addr"
		}, code: "bad_buyer"},
		{name: "bad amount", mutate: func(m map[string]any) {
			m["terms"] = map[string]any{"amount": "abc", "token": fixtureToken}
		}, code: "bad_amount"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.raw
			if tc.mutate != nil {
				var p map[string]any
				_ = json.Unmarshal(f.presentation(nil), &p)
				tc.mutate(p)
				raw, _ = json.Marshal(p)
			}
			_, err := gateSessionOpen(ctx, f.mgr, a.peer, a.connID, raw)
			ge, ok := err.(*gateError)
			if !ok || ge.code != tc.code {
				t.Fatalf("want gateError %s, got %v", tc.code, err)
			}
		})
	}
}

func TestGate_ServeDisabled(t *testing.T) {
	f := newGateFixture(t, 4)
	f.mc.serveEnabled = false
	_, err := gateSessionOpen(context.Background(), f.mgr, "peer", 1, f.presentation(nil))
	if ge, ok := err.(*gateError); !ok || ge.code != "serve_disabled" {
		t.Fatalf("want serve_disabled, got %v", err)
	}
}

func TestGate_TermsMismatch(t *testing.T) {
	f := newGateFixture(t, 4)
	a := newAgent(f)

	// Wrong amount vs the offer's per_task price.
	var p map[string]any
	_ = json.Unmarshal(f.presentation(nil), &p)
	p["terms"] = map[string]any{"amount": "1", "token": fixtureToken}
	raw, _ := json.Marshal(p)
	_, err := gateSessionOpen(context.Background(), f.mgr, a.peer, a.connID, raw)
	if ge, ok := err.(*gateError); !ok || ge.code != "terms_mismatch" {
		t.Fatalf("want terms_mismatch, got %v", err)
	}

	// A second buyer-side escrow over a different task hash → the
	// task-hash binding check refuses it.
	f.rail.Open(context.Background(), "corr-other", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: fixtureToken,
		Amount:   big.NewInt(5_000_000),
		TaskHash: sha256.Sum256([]byte("different task")),
	})
	other := f.rail.PredictEscrowAddr("corr-other")
	_ = json.Unmarshal(f.presentation(nil), &p)
	p["session_id"] = other
	raw, _ = json.Marshal(p)
	_, err = gateSessionOpen(context.Background(), f.mgr, a.peer, a.connID, raw)
	if ge, ok := err.(*gateError); !ok || ge.code != "terms_mismatch" {
		t.Fatalf("want terms_mismatch for task hash, got %v", err)
	}
}

func TestGate_UnfundedOrMissingEscrow(t *testing.T) {
	f := newGateFixture(t, 4)
	a := newAgent(f)

	// An escrow address that was never opened on the mock rail.
	missing := f.rail.PredictEscrowAddr("never-opened")
	p := map[string]any{}
	_ = json.Unmarshal(f.presentation(nil), &p)
	p["session_id"] = missing
	raw, _ := json.Marshal(p)
	_, err := gateSessionOpen(context.Background(), f.mgr, a.peer, a.connID, raw)
	if ge, ok := err.(*gateError); !ok || ge.code != "verify_failed" {
		t.Fatalf("want verify_failed, got %v", err)
	}
}

func TestGate_CapEnforced(t *testing.T) {
	f := newGateFixture(t, 1) // global cap = 1
	ctx := context.Background()
	a := newAgent(f)
	if _, err := gateSessionOpen(ctx, f.mgr, a.peer, a.connID, f.presentation(nil)); err != nil {
		t.Fatalf("first open: %v", err)
	}
	// A second escrow + presentation from a *different* peer hits the
	// global cap (the same peer would hit the per-peer share first).
	f.rail.Open(ctx, "corr-2", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: fixtureToken,
		Amount: big.NewInt(5_000_000), TaskHash: testTaskHash,
	})
	p := map[string]any{}
	_ = json.Unmarshal(f.presentation(nil), &p)
	p["session_id"] = f.rail.PredictEscrowAddr("corr-2")
	raw, _ := json.Marshal(p)
	a2 := newConnAgent("peer-two", f.mgr.nextConnID(), f.mgr, newAuditLogger(""))
	_, err := gateSessionOpen(ctx, f.mgr, a2.peer, a2.connID, raw)
	if ge, ok := err.(*gateError); !ok || ge.code != "cap_reached" {
		t.Fatalf("want cap_reached, got %v", err)
	}
}

func TestGate_PerPeerRateLimit(t *testing.T) {
	f := newGateFixture(t, 4)
	ctx := context.Background()
	a := newAgent(f)
	// peerOpenPerMinute opens per peer — all fail the escrow lookup
	// (bad session ids are cheap) but still consume the rate window.
	for i := 0; i < peerOpenPerMinute; i++ {
		_, _ = gateSessionOpen(ctx, f.mgr, a.peer, a.connID,
			json.RawMessage(`{"x":1}`))
	}
	_, err := gateSessionOpen(ctx, f.mgr, a.peer, a.connID, f.presentation(nil))
	if ge, ok := err.(*gateError); !ok || ge.code != "rate_limited" {
		t.Fatalf("want rate_limited, got %v", err)
	}
}

func TestGate_TTLExpiryReaps(t *testing.T) {
	f := newGateFixture(t, 4)
	f.mc.sessionTTL = 30 * time.Millisecond
	a := newAgent(f)
	if _, err := gateSessionOpen(
		context.Background(), f.mgr, a.peer, a.connID, f.presentation(nil)); err != nil {
		t.Fatalf("open: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	f.mgr.reapSessions()
	if got := f.mgr.sessionCount(); got != 0 {
		t.Fatalf("ttl expiry left %d sessions", got)
	}
	// Expired session minted an interrupted receipt to disk.
	if _, err := loadReceipt(f.dir, f.sessionID); err != nil {
		t.Fatalf("receipt not persisted: %v", err)
	}
}

// --- session flow ---------------------------------------------------------

// driveSessionOpen performs the full open→new→prompt over the in-process
// agent and returns the finished session + receipt.
func driveSessionOpen(
	t *testing.T, f *gateFixture, promptText string,
) (*marketSession, *receipt, error) {
	t.Helper()
	ctx := context.Background()
	a := newAgent(f)
	resp, err := a.HandleExtensionMethod(ctx, "_rhizome.session_open", f.presentation(nil))
	if err != nil {
		return nil, nil, fmt.Errorf("session_open: %w", err)
	}
	m, ok := resp.(map[string]any)
	if !ok || m["accepted"] != true {
		return nil, nil, fmt.Errorf("session_open response: %v", resp)
	}
	if _, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd: "/", McpServers: []acpsdk.McpServer{},
	}); err != nil {
		return nil, nil, fmt.Errorf("session/new: %w", err)
	}
	pr, err := a.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(f.sessionID),
		Prompt: []acpsdk.ContentBlock{
			{Text: &acpsdk.ContentBlockText{Type: "text", Text: promptText}},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("prompt: %w", err)
	}
	if pr.StopReason != acpsdk.StopReasonEndTurn {
		return nil, nil, fmt.Errorf("stop reason %s", pr.StopReason)
	}
	s := f.mgr.lookup(f.sessionID)
	return s, nil, nil
}

func TestSession_FullFlowMintsReceipt(t *testing.T) {
	f := newGateFixture(t, 4)
	f.stub.replyText = "the report covers three things"
	f.stub.usage = &acpsdk.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	f.mgr.ident.Store(testutil.NewIdentity(t))

	driveSessionOpen(t, f, testPrompt)

	// The agent saw the buyer's exact prompt.
	select {
	case got := <-f.stub.promptCh:
		if got != testPrompt {
			t.Fatalf("agent prompt %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent never got prompt")
	}

	rc, err := loadReceipt(f.dir, f.sessionID)
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	wantSum := sha256.Sum256([]byte(f.stub.replyText))
	if rc.ResultSHA256 != "0x"+hex.EncodeToString(wantSum[:]) {
		t.Fatalf("result hash %s", rc.ResultSHA256)
	}
	if rc.Usage == nil || rc.Usage.TotalTokens != 15 {
		t.Fatalf("usage %+v", rc.Usage)
	}
	if rc.Terms.Price != "5000000" || rc.Terms.Asset != "USDC" || rc.Terms.ChainID != 11155111 {
		t.Fatalf("terms %+v", rc.Terms)
	}
	if rc.Signature == "" || rc.SellerPeerID == "" {
		t.Fatal("receipt unsigned")
	}
	ok, err := VerifyReceipt(rc)
	if err != nil || !ok {
		t.Fatalf("verify: %v ok=%v", err, ok)
	}
}

func TestSession_ResultHashDeterministic(t *testing.T) {
	// Two sessions over the same reply text mint identical result hashes.
	for i := 0; i < 2; i++ {
		f := newGateFixture(t, 4)
		f.stub.replyText = "fixed reply"
		driveSessionOpen(t, f, testPrompt)
		rc, err := loadReceipt(f.dir, f.sessionID)
		if err != nil {
			t.Fatal(err)
		}
		want := sha256.Sum256([]byte("fixed reply"))
		if rc.ResultSHA256 != "0x"+hex.EncodeToString(want[:]) {
			t.Fatalf("run %d: hash %s", i, rc.ResultSHA256)
		}
	}
}

func TestSession_TaskMismatchRefused(t *testing.T) {
	f := newGateFixture(t, 4)
	f.stub.replyText = "x"
	a := newAgent(f)
	ctx := context.Background()
	if _, err := a.HandleExtensionMethod(
		ctx, "_rhizome.session_open", f.presentation(nil)); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd: "/", McpServers: []acpsdk.McpServer{},
	}); err != nil {
		t.Fatalf("new: %v", err)
	}
	// Prompt text does not hash to the escrow's task_hash.
	_, err := a.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId(f.sessionID),
		Prompt: []acpsdk.ContentBlock{
			{Text: &acpsdk.ContentBlockText{Type: "text", Text: "different task"}},
		},
	})
	if err == nil {
		t.Fatal("mismatched prompt accepted")
	}
}

func TestSession_NoOpenNoSessionNew(t *testing.T) {
	f := newGateFixture(t, 4)
	a := newAgent(f)
	_, err := a.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd: "/", McpServers: []acpsdk.McpServer{},
	})
	if err == nil {
		t.Fatal("session/new without session_open accepted")
	}
}

func TestSession_SpawnNeverHappensOnGateFailure(t *testing.T) {
	f := newGateFixture(t, 4)
	var spawns atomic.Int32
	f.stub.spawned = &spawns
	a := newAgent(f)
	// Bad escrow — gate refuses before spawn.
	p := map[string]any{}
	_ = json.Unmarshal(f.presentation(nil), &p)
	p["session_id"] = f.rail.PredictEscrowAddr("never")
	raw, _ := json.Marshal(p)
	_, err := a.HandleExtensionMethod(context.Background(), "_rhizome.session_open", raw)
	if err == nil {
		t.Fatal("expected gate refusal")
	}
	if got := spawns.Load(); got != 0 {
		t.Fatalf("gate failure spawned %d agents", got)
	}
}

func TestSession_ConnDropFinalizes(t *testing.T) {
	f := newGateFixture(t, 4)
	var spawns atomic.Int32
	f.stub.spawned = &spawns
	a := newAgent(f)
	ctx := context.Background()
	if _, err := a.HandleExtensionMethod(
		ctx, "_rhizome.session_open", f.presentation(nil)); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := a.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd: "/", McpServers: []acpsdk.McpServer{},
	}); err != nil {
		t.Fatalf("new: %v", err)
	}
	// The peer's transport dies mid-session.
	a.onConnClose()
	f.mgr.finalizeConn(a.connID)
	if got := f.mgr.sessionCount(); got != 0 {
		t.Fatalf("conn drop left %d live sessions", got)
	}
	// An interrupted receipt was still minted — the claim path's evidence.
	rc, err := loadReceipt(f.dir, f.sessionID)
	if err != nil {
		t.Fatalf("interrupted receipt missing: %v", err)
	}
	if !rc.Interrupted {
		t.Fatal("dropped conn should mint an interrupted receipt")
	}
}

// --- client handler --------------------------------------------------------

func TestClientHandler_DeniesEverything(t *testing.T) {
	h := &marketClientHandler{sess: &marketSession{}}
	ctx := context.Background()
	if _, err := h.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{}); err == nil {
		t.Fatal("fs read allowed")
	}
	if _, err := h.WriteTextFile(ctx, acpsdk.WriteTextFileRequest{}); err == nil {
		t.Fatal("fs write allowed")
	}
	if _, err := h.CreateTerminal(ctx, acpsdk.CreateTerminalRequest{}); err == nil {
		t.Fatal("terminal create allowed")
	}
	resp, err := h.RequestPermission(ctx, acpsdk.RequestPermissionRequest{})
	if err != nil {
		t.Fatalf("permission should deny not error: %v", err)
	}
	if resp.Outcome.Cancelled == nil {
		t.Fatal("permission outcome not cancelled")
	}
}

func TestClientHandler_RelaysAndAccumulates(t *testing.T) {
	s := &marketSession{ID: "sess-1"}
	var got []string
	h := &marketClientHandler{
		sess: s,
		upstream: func(_ context.Context, n acpsdk.SessionNotification) error {
			got = append(got, string(n.SessionId))
			return nil
		},
	}
	notif := acpsdk.SessionNotification{
		SessionId: "agent-internal",
		Update: acpsdk.SessionUpdate{
			AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
				Content: acpsdk.ContentBlock{
					Text: &acpsdk.ContentBlockText{Type: "text", Text: "chunk1"},
				},
			},
		},
	}
	if err := h.SessionUpdate(context.Background(), notif); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("relay got %v", got)
	}
	if s.result.String() != "chunk1" {
		t.Fatalf("result %q", s.result.String())
	}
}

// --- receipt ---------------------------------------------------------------

func TestReceipt_VerifyRejectsTampered(t *testing.T) {
	f := newGateFixture(t, 4)
	f.stub.replyText = "reply"
	f.mgr.ident.Store(testutil.NewIdentity(t))
	driveSessionOpen(t, f, testPrompt)
	rc, err := loadReceipt(f.dir, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyReceipt(rc)
	if err != nil || !ok {
		t.Fatalf("untampered verify: %v", err)
	}
	rc.DurationMS++
	if ok, _ := VerifyReceipt(rc); ok {
		t.Fatal("tampered receipt verified")
	}
}

func TestReceipt_UnsignedWhenNoIdentity(t *testing.T) {
	f := newGateFixture(t, 4)
	f.stub.replyText = "reply"
	driveSessionOpen(t, f, testPrompt)
	rc, err := loadReceipt(f.dir, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rc.Signature != "" {
		t.Fatal("signed without identity")
	}
	ok, err := VerifyReceipt(rc)
	if err != nil || ok {
		t.Fatalf("unsigned receipt: ok=%v err=%v", ok, err)
	}
}

func TestAudit_SessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, auditFile)
	audit := newAuditLogger(auditPath)
	f := newGateFixture(t, 4)
	f.mgr.audit = audit
	f.stub.replyText = "ok"
	driveSessionOpen(t, f, testPrompt)
	data, _ := os.ReadFile(auditPath)
	s := string(data)
	for _, ev := range []string{
		"market.gate.open", "market.session.active",
		"market.session.end", "market.receipt.mint",
	} {
		if !strings.Contains(s, ev) {
			t.Fatalf("audit missing %s:\n%s", ev, s)
		}
	}
	// Prompt bodies must never land in the audit log.
	if strings.Contains(s, testPrompt) || strings.Contains(s, "reply") {
		t.Fatal("audit leaked task body/result")
	}
}
