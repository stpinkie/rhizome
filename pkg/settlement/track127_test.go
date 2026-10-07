package settlement

// Track 127 — escrow graduation: the session-keyed RhizomeEscrow rail
// end-to-end over the FakeChain's graduated mode. Covers the survey's
// documented Smart Invoice gaps: amount-based partial releases, the
// native seller claim, and per-session arbiter resolution.

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

var _ Rail = (*GraduatedRail)(nil)

func testGraduatedConfig() RailConfig {
	cfg := testRailConfig()
	cfg.Kind = RailKindRhizome
	cfg.WrappedNative = "" // ERC-20-only — no wrapped-native requirement
	return cfg
}

type gradFixture struct {
	fc   *FakeChain
	rail *GraduatedRail
	snd  *DirectSender
}

func newGradFixture(t *testing.T) *gradFixture {
	t.Helper()
	cfg := testGraduatedConfig()
	fc := NewFakeGraduatedChain(t, cfg)
	fc.DeployToken(testToken)
	snd := NewDirectSender(web3.NewClient(fc.Endpoint(), "", nil), testBuyer)
	rail, err := NewGraduatedRail(cfg, snd)
	if err != nil {
		t.Fatalf("NewGraduatedRail: %v", err)
	}
	return &gradFixture{fc: fc, rail: rail, snd: snd}
}

func TestGraduatedConfigFromFields(t *testing.T) {
	t.Run("rhizome kind parses without wrapped native", func(t *testing.T) {
		cfg, err := ConfigFromFields(map[string]string{
			"escrow_rail":           "rhizome",
			"escrow_chain_id":       "11155111",
			"escrow_contract":       testFactory,
			"escrow_token":          testToken,
			"escrow_arbiter":        testArbiter,
			"escrow_dispute_window": "7200",
		})
		if err != nil {
			t.Fatalf("ConfigFromFields: %v", err)
		}
		if cfg.Kind != RailKindRhizome {
			t.Fatalf("kind = %q, want %q", cfg.Kind, RailKindRhizome)
		}
		if cfg.WrappedNative != "" {
			t.Fatalf("wrapped = %q, want empty for graduated rail", cfg.WrappedNative)
		}
	})
	t.Run("bad kind rejected", func(t *testing.T) {
		_, err := ConfigFromFields(map[string]string{
			"escrow_rail":           "paper_checks",
			"escrow_chain_id":       "11155111",
			"escrow_contract":       testFactory,
			"escrow_token":          testToken,
			"escrow_arbiter":        testArbiter,
			"escrow_dispute_window": "7200",
		})
		if err == nil || !strings.Contains(err.Error(), "escrow_rail") {
			t.Fatalf("err = %v; want escrow_rail rejection", err)
		}
	})
	t.Run("default kind stays smart_invoice", func(t *testing.T) {
		cfg, err := ConfigFromFields(map[string]string{
			"escrow_chain_id":       "11155111",
			"escrow_contract":       testFactory,
			"escrow_token":          testToken,
			"escrow_arbiter":        testArbiter,
			"escrow_dispute_window": "7200",
		})
		if err != nil {
			t.Fatalf("ConfigFromFields: %v", err)
		}
		if cfg.Kind != RailKindSmartInvoice {
			t.Fatalf("kind = %q, want %q", cfg.Kind, RailKindSmartInvoice)
		}
	})
}

func TestGraduatedSessionIDAndFilter(t *testing.T) {
	fx := newGradFixture(t)
	sid := fx.rail.SessionID("corr-g")
	want := bytes32Hex(GraduatedSessionID("corr-g"))
	if sid != want {
		t.Fatalf("SessionID = %s, want %s", sid, want)
	}
	// Deterministic + distinct per correlation.
	if fx.rail.SessionID("corr-h") == sid {
		t.Fatal("distinct correlations must derive distinct session ids")
	}
	addr, topic, err := fx.rail.SessionLogFilter(sid)
	if err != nil {
		t.Fatalf("SessionLogFilter: %v", err)
	}
	if !addrEq(addr, testFactory) {
		t.Fatalf("filter addr = %s, want contract %s", addr, testFactory)
	}
	if topic != sid {
		t.Fatalf("filter topic = %s, want session %s", topic, sid)
	}
	// Clone-address-shaped ids (20 bytes) must be refused on the
	// graduated rail — the wire session is a bytes32.
	if _, _, err := fx.rail.SessionLogFilter(testFactory); err == nil {
		t.Fatal("address-shaped session id must fail the filter parse")
	}
}

func TestGraduatedOpenAndVerify(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	sid := fx.rail.SessionID("corr-open")

	// Unfunded buyer: the approve lands but transferFrom reverts.
	if _, err := fx.rail.Open(ctx, "corr-open", terms); err == nil {
		t.Fatal("open without funds must fail")
	}
	if ok, err := fx.rail.VerifyLock(ctx, sid, terms); err != nil || ok {
		t.Fatalf("verify unopened = %v, %v; want false, nil", ok, err)
	}

	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	tx, err := fx.rail.Open(ctx, "corr-open", terms)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !strings.HasPrefix(tx, "0x") {
		t.Fatalf("open tx = %q", tx)
	}
	if got := fx.fc.Balance(testToken, testFactory); got.Cmp(terms.Amount) != 0 {
		t.Fatalf("contract balance = %s, want %s", got, terms.Amount)
	}
	if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(900)) != 0 {
		t.Fatalf("buyer balance = %s, want 900", got)
	}

	ok, err := fx.rail.VerifyLock(ctx, sid, terms)
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v; want true, nil", ok, err)
	}
	// On-chain terms round-trip.
	st, exists := fx.fc.GraduatedSnapshot(sid)
	if !exists {
		t.Fatal("no session recorded")
	}
	if !addrEq(st.buyer, testBuyer) || !addrEq(st.seller, testSeller) ||
		!addrEq(st.arbiter, testArbiter) || !addrEq(st.token, testToken) {
		t.Fatalf("session parties mismatch: %+v", st)
	}
	if st.amount.Cmp(terms.Amount) != 0 || st.taskHash != terms.TaskHash {
		t.Fatalf("session terms mismatch: %+v", st)
	}
	if st.status != graduatedStatusOpen {
		t.Fatalf("status = %d, want Open", st.status)
	}
	// Double-open on the same session id reverts.
	if _, err := fx.rail.Open(ctx, "corr-open", terms); err == nil {
		t.Fatal("duplicate session id must revert")
	}
}

func TestGraduatedVerifyMismatches(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-vm", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-vm")

	cases := []struct {
		name string
		mut  func(*Terms)
	}{
		{"amount", func(x *Terms) { x.Amount = big.NewInt(99) }},
		{"seller", func(x *Terms) { x.Seller = testBuyer }},
		{"buyer", func(x *Terms) { x.Buyer = testSeller }},
		{"token", func(x *Terms) { x.Token = "0x9000000000000000000000000000000000000009" }},
		{"taskhash", func(x *Terms) { x.TaskHash = [32]byte{0x11} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := terms
			tc.mut(&bad)
			if ok, err := fx.rail.VerifyLock(ctx, sid, bad); err != nil || ok {
				t.Fatalf("verify = %v, %v; want false, nil", ok, err)
			}
		})
	}
}

func TestGraduatedPartialRelease(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-p", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-p")

	// Amount-based drawdown: two partials settle the budget.
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(30)); err != nil {
		t.Fatalf("partial 30: %v", err)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(big.NewInt(30)) != 0 {
		t.Fatalf("seller balance = %s, want 30", got)
	}
	// Non-buyer release reverts.
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(10)); err == nil {
		t.Fatal("seller release must revert")
	}
	fx.snd.SetFrom(testBuyer)
	// Over-release beyond the remaining balance reverts.
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(71)); err == nil {
		t.Fatal("release beyond balance must revert")
	}
	// Zero/nil releases refused client-side.
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(0)); err == nil {
		t.Fatal("zero release must fail")
	}
	// Full Release() drains the remainder — the settle form.
	if _, err := fx.rail.Release(ctx, sid); err != nil {
		t.Fatalf("release remainder: %v", err)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(terms.Amount) != 0 {
		t.Fatalf("seller balance = %s, want %s", got, terms.Amount)
	}
	// Fully-released session still verifies released>0 — verify fails
	// since released must be zero at lock-check time.
	if ok, _ := fx.rail.VerifyLock(ctx, sid, terms); ok {
		t.Fatal("released session must fail verify")
	}
}

func TestGraduatedClaimAndWithdraw(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-cl", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-cl")

	// Seller claim before the deadline reverts.
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Claim(ctx, sid); err == nil {
		t.Fatal("claim inside the window must revert")
	}
	// Buyer claim reverts — seller-only verb.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Claim(ctx, sid); err == nil {
		t.Fatal("buyer claim must revert")
	}
	// Partial release first, then deadline passes: seller claims the
	// remainder — the claim gap the survey documented, now closed.
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(25)); err != nil {
		t.Fatalf("partial: %v", err)
	}
	fx.fc.Advance(3601)
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Claim(ctx, sid); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(terms.Amount) != 0 {
		t.Fatalf("seller balance = %s, want %s", got, terms.Amount)
	}
	if got := fx.fc.Balance(testToken, testFactory); got.Sign() != 0 {
		t.Fatalf("contract holds %s after claim, want 0", got)
	}

	// Second session: buyer withdraw after deadline + claim grace —
	// the abandoned-session clawback.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Open(ctx, "corr-wd", terms); err != nil {
		t.Fatal(err)
	}
	sid2 := fx.rail.SessionID("corr-wd")
	if _, err := fx.rail.Withdraw(ctx, sid2); err == nil {
		t.Fatal("withdraw inside window must revert")
	}
	fx.fc.Advance(3601) // past deadline, inside claim grace
	if _, err := fx.rail.Withdraw(ctx, sid2); err == nil {
		t.Fatal("withdraw inside claim grace must revert")
	}
	// Seller can still claim during the grace.
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Claim(ctx, sid2); err != nil {
		t.Fatalf("claim during grace: %v", err)
	}
	// Third session: grace lapses too — buyer's withdraw lands.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Open(ctx, "corr-wd2", terms); err != nil {
		t.Fatal(err)
	}
	sid3 := fx.rail.SessionID("corr-wd2")
	// Past deadline AND past claim grace from this session's open time.
	fx.fc.Advance(3600 + graduatedClaimGrace + 1)
	if _, err := fx.rail.Withdraw(ctx, sid3); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(800)) != 0 {
		t.Fatalf("buyer balance = %s, want 800", got)
	}
}

func TestGraduatedDisputeResolve(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-dr", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-dr")

	// Partial release first — the dispute splits the REMAINING 70.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(30)); err != nil {
		t.Fatalf("partial: %v", err)
	}
	// Non-party dispute reverts.
	fx.snd.SetFrom(testArbiter)
	if _, err := fx.rail.Dispute(ctx, sid, [32]byte{0xaa}); err == nil {
		t.Fatal("arbiter dispute must revert — party only")
	}
	// Seller-initiated dispute locks the session.
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Dispute(ctx, sid, [32]byte{0xdd}); err != nil {
		t.Fatalf("seller dispute: %v", err)
	}
	// Locked: releases and claims revert now.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(1)); err == nil {
		t.Fatal("release on locked session must revert")
	}
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Claim(ctx, sid); err == nil {
		t.Fatal("claim on locked session must revert")
	}
	// Non-arbiter resolve reverts.
	if _, err := fx.rail.Resolve(ctx, sid, big.NewInt(20), big.NewInt(50), [32]byte{}); err == nil {
		t.Fatal("non-arbiter resolve must revert")
	}
	fx.snd.SetFrom(testArbiter)
	// Awards not covering the remaining 70 revert.
	if _, err := fx.rail.Resolve(ctx, sid, big.NewInt(20), big.NewInt(40), [32]byte{}); err == nil {
		t.Fatal("under-allocated awards must revert")
	}
	if _, err := fx.rail.Resolve(ctx, sid, big.NewInt(20), big.NewInt(50), [32]byte{0xee}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(big.NewInt(80)) != 0 {
		t.Fatalf("seller balance = %s, want 80 (30 released + 50 award)", got)
	}
	if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(920)) != 0 {
		t.Fatalf("buyer balance = %s, want 920", got)
	}
	st, _ := fx.fc.GraduatedSnapshot(sid)
	if st.status != graduatedStatusResolved {
		t.Fatalf("status = %d, want Resolved", st.status)
	}
}

func TestGraduatedEventsQueryable(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-ev", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-ev")
	addr, topic, err := fx.rail.SessionLogFilter(sid)
	if err != nil {
		t.Fatal(err)
	}
	// The watcher filters contract + sessionId topic — eth_getLogs must
	// return this session's Opened under that filter.
	raw, err := fx.snd.client.Call(ctx, "eth_getLogs", []any{map[string]any{
		"address": addr,
		"topics":  []any{nil, topic},
	}})
	if err != nil {
		t.Fatalf("getLogs: %v", err)
	}
	logs, trunc, err := web3.DecodeLogs(raw, 10)
	if err != nil || trunc {
		t.Fatalf("decode: %v trunc=%v", err, trunc)
	}
	if len(logs) == 0 {
		t.Fatal("sessionId-topic filter returned no logs")
	}
	found := false
	for _, lg := range logs {
		if lg.Topics[0] == topicOf("Opened(bytes32,address,address,address,uint128,uint64,bytes32)") {
			found = true
		}
	}
	if !found {
		t.Fatal("Opened event not under the session filter")
	}
}

func TestGraduatedPastTerminationRefused(t *testing.T) {
	fx := newGradFixture(t)
	ctx := context.Background()
	terms := testTerms()
	terms.TerminationTime = time.Now().Unix() - 10
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-past", terms); err == nil ||
		!strings.Contains(err.Error(), "in the past") {
		t.Fatalf("open err = %v; want in-the-past refusal", err)
	}
}

func TestGraduatedRailValidation(t *testing.T) {
	snd := NewDirectSender(web3.NewClient("http://127.0.0.1:1", "", nil), testBuyer)
	if _, err := NewGraduatedRail(RailConfig{}, snd); err == nil {
		t.Fatal("zero config must fail")
	}
	bad := testGraduatedConfig()
	bad.Arbiter = "not-an-address"
	if _, err := NewGraduatedRail(bad, snd); err == nil {
		t.Fatal("bad arbiter must fail")
	}
	bad = testGraduatedConfig()
	bad.DisputeWindowSecs = 0
	if _, err := NewGraduatedRail(bad, snd); err == nil {
		t.Fatal("zero dispute window must fail")
	}
}
