// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

// Track 128 — session-drawdown settlement: one funded escrow session
// serves many task draws. Covers config parsing + rail-kind rejection,
// ledger acquire/reuse/exhaustion/rollback, the gate's bytes32 +
// composite-key handling, the end-to-end two-buys-one-escrow flow,
// dispute locking, and the deadline sweep's remainder refund.

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// --- config --------------------------------------------------------------

func TestTrack128_DrawdownConfig(t *testing.T) {
	t.Run("drawdown parses budget + interval", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"escrow_settlement": "drawdown",
			"session_budget":    "25.5",
			"draw_interval":     "30",
		}, nil), t.TempDir())
		if len(mc.errs) != 0 {
			t.Fatalf("errs: %v", mc.errs)
		}
		if !mc.drawdownMode() {
			t.Fatal("drawdownMode should be on")
		}
		if mc.sessionBudget != "25.5" || mc.drawInterval != 30*time.Second {
			t.Fatalf("budget=%q interval=%v", mc.sessionBudget, mc.drawInterval)
		}
	})

	t.Run("per_task default", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, nil, nil), t.TempDir())
		if mc.drawdownMode() {
			t.Fatal("default settlement must be per_task")
		}
	})

	t.Run("bad enum rejected", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"escrow_settlement": "tab",
		}, nil), t.TempDir())
		if !hasErr(mc.errs, "escrow_settlement") {
			t.Fatalf("errs %v — want escrow_settlement rejection", mc.errs)
		}
	})

	t.Run("drawdown requires session_budget", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"escrow_settlement": "drawdown",
		}, nil), t.TempDir())
		if !hasErr(mc.errs, "session_budget") {
			t.Fatalf("errs %v — want session_budget rejection", mc.errs)
		}
	})

	t.Run("drawdown refuses smart invoice", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"escrow_settlement":     "drawdown",
			"session_budget":        "50",
			"escrow_rail":           "smart_invoice",
			"escrow_chain_id":       "11155111",
			"escrow_contract":       "0x5000000000000000000000000000000000000005",
			"escrow_token":          "0x4000000000000000000000000000000000000004",
			"escrow_arbiter":        "0x3000000000000000000000000000000000000003",
			"escrow_dispute_window": "7200",
			"escrow_wrapped_native": "0x4000000000000000000000000000000000000004",
		}, nil), t.TempDir())
		if !hasErr(mc.errs, "drawdown") {
			t.Fatalf("errs %v — want drawdown/smart_invoice rejection", mc.errs)
		}
	})

	t.Run("drawdown + rhizome rail passes", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"escrow_settlement":     "drawdown",
			"session_budget":        "50",
			"escrow_rail":           "rhizome",
			"escrow_chain_id":       "11155111",
			"escrow_contract":       "0x5000000000000000000000000000000000000005",
			"escrow_token":          "0x4000000000000000000000000000000000000004",
			"escrow_arbiter":        "0x3000000000000000000000000000000000000003",
			"escrow_dispute_window": "7200",
		}, nil), t.TempDir())
		if len(mc.errs) != 0 {
			t.Fatalf("errs: %v", mc.errs)
		}
	})
}

func hasErr(errs []string, frag string) bool {
	for _, e := range errs {
		if strings.Contains(e, frag) {
			return true
		}
	}
	return false
}

// --- ledger --------------------------------------------------------------

func TestTrack128_Ledger(t *testing.T) {
	now := time.Now()
	ledger := openDrawdownLedger(t.TempDir())
	mint := func() (string, string) { return "0xsess" + randomHex(4), "corr" }

	t.Run("reuse accumulates draws on one session", func(t *testing.T) {
		s1, fresh, err := ledger.acquire(
			"peer-A", testSeller, testBuyer, fixtureToken, "p1",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		if !fresh || s1.SessionID == "" {
			t.Fatalf("first acquire should mint (fresh=%v sid=%q)", fresh, s1.SessionID)
		}
		_ = ledger.markOpened(s1.SessionID, "0xopen")
		s2, fresh, err := ledger.acquire(
			"peer-A", testSeller, testBuyer, fixtureToken, "p2",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if err != nil {
			t.Fatalf("acquire2: %v", err)
		}
		if fresh || s2.SessionID != s1.SessionID {
			t.Fatalf("second acquire should reuse %s (got %s fresh=%v)",
				s1.SessionID, s2.SessionID, fresh)
		}
		if s2.Drawn != "10" {
			t.Fatalf("drawn = %s, want 10", s2.Drawn)
		}
	})

	t.Run("budget exhaustion mints a new session", func(t *testing.T) {
		s, _, _ := ledger.acquire(
			"peer-A", testSeller, testBuyer, fixtureToken, "p3",
			big.NewInt(11), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if s.Drawn != "11" || len(ledger.sessions) != 2 {
			t.Fatalf("11 > headroom 10 should mint a second session: %+v", s)
		}
	})

	t.Run("rollback frees headroom", func(t *testing.T) {
		l2 := openDrawdownLedger(t.TempDir())
		s, _, _ := l2.acquire(
			"peer-B", testSeller, testBuyer, fixtureToken, "px",
			big.NewInt(15), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		_ = l2.markOpened(s.SessionID, "0xopen")
		l2.rollbackDraw(s.SessionID, "px")
		got := l2.sessions[s.SessionID]
		if got.Drawn != "0" || len(got.Draws) != 0 {
			t.Fatalf("rollback should free the draw: %+v", got.Draws)
		}
	})

	t.Run("unopened sessions never serve draws", func(t *testing.T) {
		l3 := openDrawdownLedger(t.TempDir())
		s1, _, _ := l3.acquire(
			"peer-C", testSeller, testBuyer, fixtureToken, "pa",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		// Never markOpened — simulates a crashed open.
		s2, fresh, _ := l3.acquire(
			"peer-C", testSeller, testBuyer, fixtureToken, "pb",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if !fresh || s2.SessionID == s1.SessionID {
			t.Fatal("un-opened session must not serve the next draw")
		}
	})

	t.Run("disputed sessions stop serving", func(t *testing.T) {
		l4 := openDrawdownLedger(t.TempDir())
		s1, _, _ := l4.acquire(
			"peer-D", testSeller, testBuyer, fixtureToken, "pa",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		_ = l4.markOpened(s1.SessionID, "0xopen")
		l4.markClosed(s1.SessionID, "disputed")
		s2, fresh, _ := l4.acquire(
			"peer-D", testSeller, testBuyer, fixtureToken, "pb",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if !fresh || s2.SessionID == s1.SessionID {
			t.Fatal("disputed session must not serve new draws")
		}
	})

	t.Run("persisted sessions reload", func(t *testing.T) {
		dir := t.TempDir()
		l5 := openDrawdownLedger(dir)
		s1, _, _ := l5.acquire(
			"peer-E", testSeller, testBuyer, fixtureToken, "pa",
			big.NewInt(7), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		_ = l5.markOpened(s1.SessionID, "0xopen")
		l6 := openDrawdownLedger(dir)
		s2, fresh, err := l6.acquire(
			"peer-E", testSeller, testBuyer, fixtureToken, "pb",
			big.NewInt(5), big.NewInt(20), now.Unix()+3600, mint,
			func() time.Time { return now })
		if err != nil || fresh || s2.SessionID != s1.SessionID {
			t.Fatalf("reloaded ledger should reuse %s: fresh=%v err=%v",
				s1.SessionID, fresh, err)
		}
	})
}

// --- gate: bytes32 + composite keys ---------------------------------------

// gradGateFixture wires a sessionMgr to a GraduatedRail over FakeChain —
// the seller-side posture under escrow_rail=rhizome.
type gradGateFixture struct {
	mgr       *sessionMgr
	rail      *settlement.GraduatedRail
	mc        *marketConfig
	sessionID string
}

func newGradGateFixture(t *testing.T, drawdown bool) *gradGateFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := settlement.RailConfig{
		Kind:              settlement.RailKindRhizome,
		ChainID:           11155111,
		Factory:           "0x5000000000000000000000000000000000000005",
		Token:             "0x4000000000000000000000000000000000000004",
		Arbiter:           "0x3000000000000000000000000000000000000003",
		DisputeWindowSecs: 7200,
	}
	fc := settlement.NewFakeGraduatedChain(t, cfg)
	fc.DeployToken(cfg.Token)
	mint := new(big.Int).Exp(big.NewInt(10), big.NewInt(27), nil) // 1e9 tokens @18dec
	fc.Mint(cfg.Token, testBuyer, mint)
	snd := settlement.NewDirectSender(
		web3.NewClient(fc.Endpoint(), "", nil), testBuyer)
	rail, err := settlement.NewGraduatedRail(cfg, snd)
	if err != nil {
		t.Fatalf("NewGraduatedRail: %v", err)
	}
	mc := &marketConfig{
		serveEnabled:  true,
		runtime:       "exec",
		payoutAddress: testSeller,
		rail:          &cfg,
		maxSessions:   8,
		sessionTTL:    time.Minute,
		offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet:   priceSheet{PerTask: "5", Asset: "USDC", ChainID: 11155111},
		}},
	}
	mgr := newSessionMgr(dir, newAuditLogger(""))
	stub := &stubAgent{promptCh: make(chan string, 4)}
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

	// The fake token reports 18 decimals — per_task "5" prices to 5e18
	// base units, the drawdown session budget to 20e18.
	amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	amount.Mul(amount, big.NewInt(5))
	if drawdown {
		amount.Mul(amount, big.NewInt(4))
	}
	corr := "corr-g1"
	sessionID := rail.SessionID(corr)
	var taskHash [32]byte
	copy(taskHash[:], testTaskHash[:])
	if _, err := rail.Open(context.Background(), corr, settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: cfg.Token,
		Amount: amount, TaskHash: taskHash,
		TerminationTime: time.Now().Unix() + 7200, Drawdown: drawdown,
	}); err != nil {
		t.Fatalf("graduated open: %v", err)
	}
	return &gradGateFixture{mgr: mgr, rail: rail, mc: mc, sessionID: sessionID}
}

func (f *gradGateFixture) presentation(
	taskNonce string, drawdown bool,
) json.RawMessage {
	p := map[string]any{
		"session_id": f.sessionID,
		"task_hash":  taskHashHex(),
		"task_nonce": taskNonce,
		"offer_id":   "offer-1",
		"buyer":      testBuyer,
		"terms": map[string]any{
			"amount":   "5000000000000000000", // 5e18 — the fake token's 18-dec base units
			"token":    f.mc.rail.Token,
			"chain_id": int64(f.mc.rail.ChainID),
			"drawdown": drawdown,
		},
	}
	data, _ := json.Marshal(p)
	return data
}

func TestTrack128_GateGraduatedSessionID(t *testing.T) {
	f := newGradGateFixture(t, false)
	ctx := context.Background()

	t.Run("bytes32 session id gates on the graduated rail", func(t *testing.T) {
		s, err := gateSessionOpen(ctx, f.mgr, "peer", 1, f.presentation("", false))
		if err != nil {
			t.Fatalf("gate refused bytes32 session: %v", err)
		}
		if s.EscrowID != strings.ToLower(f.sessionID) {
			t.Fatalf("EscrowID = %q, want %q", s.EscrowID, f.sessionID)
		}
	})

	t.Run("address-shaped session id rejected on the graduated rail", func(t *testing.T) {
		p := map[string]any{}
		_ = json.Unmarshal(f.presentation("", false), &p)
		p["session_id"] = "0x1111111111111111111111111111111111111111"
		raw, _ := json.Marshal(p)
		_, err := gateSessionOpen(ctx, f.mgr, "peer", 2, raw)
		ge, ok := err.(*gateError)
		if !ok || ge.code != "bad_session_id" {
			t.Fatalf("want bad_session_id, got %v", err)
		}
	})
}

func TestTrack128_GateDrawdownComposite(t *testing.T) {
	f := newGradGateFixture(t, true)
	ctx := context.Background()

	s1, err := gateSessionOpen(ctx, f.mgr, "peer", 1, f.presentation("p-a", true))
	if err != nil {
		t.Fatalf("first drawdown open: %v", err)
	}
	s2, err := gateSessionOpen(ctx, f.mgr, "peer", 2, f.presentation("p-b", true))
	if err != nil {
		t.Fatalf("second drawdown open on shared escrow: %v", err)
	}
	if s1.ID == s2.ID {
		t.Fatal("drawdown draws must register under distinct local keys")
	}
	if s1.EscrowID != s2.EscrowID {
		t.Fatal("drawdown draws must share the on-chain escrow id")
	}
	if !s1.Terms.Drawdown || !s2.Terms.Drawdown {
		t.Fatal("gate must propagate the drawdown flag into terms")
	}

	t.Run("nonce required under drawdown", func(t *testing.T) {
		_, err := gateSessionOpen(ctx, f.mgr, "peer", 3, f.presentation("", true))
		ge, ok := err.(*gateError)
		if !ok || ge.code != "bad_request" {
			t.Fatalf("want bad_request for missing nonce, got %v", err)
		}
	})

	t.Run("budget exhaustion refuses the next draw", func(t *testing.T) {
		// VerifyLock reads released on-chain: draw the full 20e18 budget,
		// then the next draw fails the headroom check.
		budget := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
		budget.Mul(budget, big.NewInt(20))
		if _, err := f.rail.ReleasePartial(ctx, f.sessionID, budget); err != nil {
			t.Fatalf("release: %v", err)
		}
		_, err := gateSessionOpen(ctx, f.mgr, "peer", 7, f.presentation("p-e", true))
		ge, ok := err.(*gateError)
		if !ok || ge.code != "terms_mismatch" {
			t.Fatalf("exhausted budget should fail terms_mismatch, got %v", err)
		}
	})
}

// --- end to end: two buys, one escrow -------------------------------------

func TestTrack128_DrawdownEndToEnd(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.settlement = "drawdown"
		mc.sessionBudget = "20"
	})
	f.journalSeller(t)
	ctx := context.Background()

	p1, _, err := f.pm.begin(ctx, buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: "first task",
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	p1 = waitState(t, f.pm, p1.PurchaseID, purchaseCompleted)
	if !p1.Drawdown || p1.SessionID == "" {
		t.Fatalf("drawdown purchase missing session: %+v", p1)
	}
	sid := p1.SessionID

	p2, _, err := f.pm.begin(ctx, buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: "second task",
	})
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	p2 = waitState(t, f.pm, p2.PurchaseID, purchaseCompleted)
	if p2.SessionID != sid {
		t.Fatalf("second buy should reuse session %s, got %s", sid, p2.SessionID)
	}

	// One session, two recorded draws totalling 10M of the 20M budget.
	f.pm.drawdown.mu.Lock()
	sess := f.pm.drawdown.sessions[sid]
	f.pm.drawdown.mu.Unlock()
	if sess == nil || len(sess.Draws) != 2 || sess.Drawn != "10000000" {
		t.Fatalf("ledger = %+v, want 2 draws / 10M drawn", sess)
	}
	bal, err := f.rail.EscrowBalance(sid)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal.String() != "10000000" {
		t.Fatalf("escrow balance = %s, want 10000000 (20M budget − 2×5M)", bal)
	}
}

// --- sweep + dispute --------------------------------------------------------

func TestTrack128_DrawdownSweepWithdrawsRemainder(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.settlement = "drawdown"
		mc.sessionBudget = "20"
	})
	f.journalSeller(t)
	ctx := context.Background()

	p1, _, err := f.pm.begin(ctx, buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: "a task",
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	p1 = waitState(t, f.pm, p1.PurchaseID, purchaseCompleted)
	sid := p1.SessionID

	// Advance the mock past the session deadline, then sweep.
	f.rail.SetNowFunc(func() time.Time {
		return time.Now().Add(2 * time.Hour)
	})
	f.pm.nowFn = func() time.Time { return time.Now().Add(2 * time.Hour) }
	f.pm.sweepDrawdowns(ctx)

	f.pm.drawdown.mu.Lock()
	sess := f.pm.drawdown.sessions[sid]
	f.pm.drawdown.mu.Unlock()
	if sess == nil || sess.State != "closed" {
		t.Fatalf("session should be closed post-sweep: %+v", sess)
	}
	if escrowFunded(t, f.rail, sid) {
		t.Fatal("remainder should be withdrawn — balance must be zero")
	}
}

func TestTrack128_DrawdownDisputeLocksSession(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.settlement = "drawdown"
		mc.sessionBudget = "20"
		mc.buyAutoRelease = false // park at awaiting_release — disputable
	})
	f.journalSeller(t)
	ctx := context.Background()

	p1, _, err := f.pm.begin(ctx, buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: "disputed task",
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	p1 = waitState(t, f.pm, p1.PurchaseID, purchaseAwaitRelease)

	// A dispute on a parked draw locks the whole shared session on-chain
	// and the ledger — subsequent buys must mint a fresh session.
	if _, err := f.pm.dispute(ctx, p1.PurchaseID, "quality issue"); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	f.pm.drawdown.mu.Lock()
	sess := f.pm.drawdown.sessions[p1.SessionID]
	f.pm.drawdown.mu.Unlock()
	if sess == nil || sess.State != "disputed" {
		t.Fatalf("session should be marked disputed: %+v", sess)
	}

	p2, _, err := f.pm.begin(ctx, buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: "another task",
	})
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	p2 = waitState(t, f.pm, p2.PurchaseID, purchaseAwaitRelease)
	if p2.SessionID == p1.SessionID {
		t.Fatal("post-dispute buy must not reuse the locked session")
	}
}
