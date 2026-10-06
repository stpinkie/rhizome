package settlement

// Track 129 — decentralized arbitration: the RhizomeKlerosAdapter in
// the session's arbiter slot, the payable escalate path, ERC-1497
// evidence, ruling→award mapping, and the kleros:<court> config spec.

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/web3"
)

const (
	testAdapter    = "0x6000000000000000000000000000000000000006"
	testArbitrator = "0x7000000000000000000000000000000000000007"
)

// klerosCfg builds a graduated-rail config whose sessions bind the
// adapter as arbiter — the kleros:<court> resolved shape.
func klerosCfg() RailConfig {
	cfg := testGraduatedConfig()
	cfg.ArbiterKind = ArbiterKindKleros
	cfg.ArbiterCourt = 0
	cfg.ArbiterAdapter = testAdapter
	cfg.Arbiter = testAdapter // resolved: adapter occupies the slot
	return cfg
}

type klerosFixture struct {
	fc   *FakeChain
	rail *GraduatedRail
	snd  *DirectSender // buyer sender
	arb  *DirectSender // arbitrator sender (rule() delivery)
}

func newKlerosFixture(t *testing.T) *klerosFixture {
	t.Helper()
	cfg := klerosCfg()
	fc := NewFakeGraduatedChain(t, cfg)
	fc.DeployToken(testToken)
	fc.DeployKleros(testAdapter, testArbitrator, big.NewInt(7))
	client := web3.NewClient(fc.Endpoint(), "", nil)
	snd := NewDirectSender(client, testBuyer)
	rail, err := NewGraduatedRail(cfg, snd)
	if err != nil {
		t.Fatalf("NewGraduatedRail: %v", err)
	}
	return &klerosFixture{
		fc: fc, rail: rail, snd: snd,
		arb: NewDirectSender(client, testArbitrator),
	}
}

// openLocked opens a session (adapter as arbiter) and locks it via
// Dispute — the escalate-precondition the adapter enforces.
func (f *klerosFixture) openLocked(
	t *testing.T, corr string, terms Terms,
) string {
	t.Helper()
	if _, err := f.rail.Open(context.Background(), corr, terms); err != nil {
		t.Fatalf("open %s: %v", corr, err)
	}
	sid := f.rail.SessionID(corr)
	if _, err := f.rail.Dispute(
		context.Background(), sid, [32]byte{0xee}); err != nil {
		t.Fatalf("dispute %s: %v", sid, err)
	}
	return sid
}

func TestKlerosConfigSpec(t *testing.T) {
	base := map[string]string{
		"escrow_rail":           "rhizome",
		"escrow_chain_id":       "11155111",
		"escrow_contract":       testFactory,
		"escrow_token":          testToken,
		"escrow_dispute_window": "7200",
	}
	with := func(kv map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range kv {
			m[k] = v
		}
		return m
	}

	t.Run("kleros court resolves adapter as arbiter", func(t *testing.T) {
		cfg, err := ConfigFromFields(with(map[string]string{
			"escrow_arbiter":         "kleros:1",
			"escrow_arbiter_adapter": testAdapter,
		}))
		if err != nil {
			t.Fatalf("ConfigFromFields: %v", err)
		}
		if cfg.ArbiterKind != ArbiterKindKleros || cfg.ArbiterCourt != 1 {
			t.Fatalf("kind/court = %q/%d, want kleros/1", cfg.ArbiterKind, cfg.ArbiterCourt)
		}
		if !addrEq(cfg.Arbiter, testAdapter) {
			t.Fatalf("resolved arbiter = %q, want adapter %s", cfg.Arbiter, testAdapter)
		}
	})

	t.Run("adapter required under kleros", func(t *testing.T) {
		_, err := ConfigFromFields(with(map[string]string{
			"escrow_arbiter": "kleros:0",
		}))
		if err == nil || !strings.Contains(err.Error(), "escrow_arbiter_adapter") {
			t.Fatalf("err = %v; want adapter-required rejection", err)
		}
	})

	t.Run("non-numeric court rejected", func(t *testing.T) {
		_, err := ConfigFromFields(with(map[string]string{
			"escrow_arbiter":         "kleros:general-court",
			"escrow_arbiter_adapter": testAdapter,
		}))
		if err == nil || !strings.Contains(err.Error(), "kleros") {
			t.Fatalf("err = %v; want court-parse rejection", err)
		}
	})

	t.Run("kleros refused on smart_invoice", func(t *testing.T) {
		fields := with(map[string]string{
			"escrow_rail":            "smart_invoice",
			"escrow_arbiter":         "kleros:0",
			"escrow_arbiter_adapter": testAdapter,
		})
		_, err := ConfigFromFields(fields)
		if err == nil || !strings.Contains(err.Error(), RailKindRhizome) {
			t.Fatalf("err = %v; want rail-kind rejection", err)
		}
	})

	t.Run("designated address stays the default", func(t *testing.T) {
		cfg, err := ConfigFromFields(with(map[string]string{
			"escrow_arbiter": testArbiter,
		}))
		if err != nil {
			t.Fatalf("ConfigFromFields: %v", err)
		}
		if cfg.ArbiterKind != ArbiterKindAddress || cfg.ArbiterCourt != 0 ||
			cfg.ArbiterAdapter != "" {
			t.Fatalf("designated arbiter mis-parsed: %+v", cfg)
		}
		if !addrEq(cfg.Arbiter, testArbiter) {
			t.Fatalf("arbiter = %q, want %s", cfg.Arbiter, testArbiter)
		}
	})
}

func TestKlerosEscalateAutoRuling(t *testing.T) {
	fx := newKlerosFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	fx.fc.SetArbiterRuling(2) // seller wins

	// Partial first — the ruling maps the REMAINING 70.
	if _, err := fx.rail.Open(ctx, "corr-k2", terms); err != nil {
		t.Fatal(err)
	}
	sid := fx.rail.SessionID("corr-k2")
	if _, err := fx.rail.ReleasePartial(ctx, sid, big.NewInt(30)); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if _, err := fx.rail.Dispute(ctx, sid, [32]byte{0xdd}); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	// ERC-1497 evidence lands while Locked.
	if _, err := fx.rail.SubmitEvidence(ctx, sid, `{"receipt":{},"terms_hash":"0x1"}`); err != nil {
		t.Fatalf("submitEvidence: %v", err)
	}
	// Escalate — payable createDispute; the fake auto-rules seller (2).
	tx, err := fx.rail.EscalateDispute(ctx, sid)
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if !strings.HasPrefix(tx, "0x") {
		t.Fatalf("escalate tx = %q", tx)
	}
	// Ruling delivered: session Resolved, seller paid the remaining 70.
	st, ok := fx.fc.GraduatedSnapshot(sid)
	if !ok || st.status != graduatedStatusResolved {
		t.Fatalf("status = %+v, want Resolved", st)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("seller balance = %s, want 100 (30 released + 70 awarded)", got)
	}
}

func TestKlerosRulingMap(t *testing.T) {
	cases := []struct {
		ruling     int64
		wantBuyer  int64
		wantSeller int64
	}{
		{1, 100, 0}, // buyer wins → whole remaining to buyer
		{2, 0, 100}, // seller wins
		{0, 50, 50}, // refused → even split
	}
	for _, tc := range cases {
		t.Run(itoa(tc.ruling), func(t *testing.T) {
			fx := newKlerosFixture(t)
			ctx := context.Background()
			terms := testTerms()
			fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
			fx.fc.SetArbiterRuling(tc.ruling)
			sid := fx.openLocked(t, "corr-map", terms)
			if _, err := fx.rail.EscalateDispute(ctx, sid); err != nil {
				t.Fatalf("escalate: %v", err)
			}
			if got := fx.fc.Balance(testToken, testSeller); got.Cmp(big.NewInt(tc.wantSeller)) != 0 {
				t.Fatalf("seller = %s, want %d", got, tc.wantSeller)
			}
			if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(900+tc.wantBuyer)) != 0 {
				t.Fatalf("buyer = %s, want %d", got, 900+tc.wantBuyer)
			}
		})
	}
}

func TestKlerosManualRule(t *testing.T) {
	fx := newKlerosFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	sid := fx.openLocked(t, "corr-m", terms)

	// No preset ruling — the dispute stays pending until rule() lands.
	if _, err := fx.rail.EscalateDispute(ctx, sid); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	st, _ := fx.fc.GraduatedSnapshot(sid)
	if st.status != graduatedStatusLocked {
		t.Fatalf("status = %d, want still Locked pending jurors", st.status)
	}
	// Double escalation reverts — one dispute per session.
	if _, err := fx.rail.EscalateDispute(ctx, sid); err == nil {
		t.Fatal("second escalate must revert")
	}
	// Wrong caller can't rule.
	m, _ := klerosAdapterABI.Method("rule", 2)
	calldata, err := m.PackArgs([]any{
		new(big.Int).SetUint64(1), new(big.Int).SetUint64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.snd.SendTx(ctx, testAdapter, calldata, nil); err == nil {
		t.Fatal("rule from non-arbitrator must revert")
	}
	// The arbitrator's callback resolves buyer-wins.
	if _, err := fx.arb.SendTx(ctx, testAdapter, calldata, nil); err != nil {
		t.Fatalf("rule: %v", err)
	}
	st, _ = fx.fc.GraduatedSnapshot(sid)
	if st.status != graduatedStatusResolved {
		t.Fatalf("status = %d, want Resolved after rule", st.status)
	}
	if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("buyer = %s, want 1000 (full refund)", got)
	}
}

func TestKlerosEscalateGuards(t *testing.T) {
	ctx := context.Background()

	t.Run("escalate needs kleros binding", func(t *testing.T) {
		fx := newGradFixture(t) // designated-arbiter rail
		if _, err := fx.rail.EscalateDispute(ctx, "0x"+strings.Repeat("ab", 32)); err == nil {
			t.Fatal("escalate on a designated-arbiter rail must fail")
		}
	})

	t.Run("escalate on unlocked session reverts", func(t *testing.T) {
		fx := newKlerosFixture(t)
		terms := testTerms()
		fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
		if _, err := fx.rail.Open(ctx, "corr-g", terms); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.rail.EscalateDispute(
			ctx, fx.rail.SessionID("corr-g")); err == nil {
			t.Fatal("escalate must require a Locked session")
		}
	})

	t.Run("escalate another adapter's session reverts", func(t *testing.T) {
		fx := newKlerosFixture(t)
		terms := testTerms()
		fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
		// Session names the designated arbiter, not the adapter.
		terms.Amount = big.NewInt(10)
		cfg2 := testGraduatedConfig()
		rail2, err := NewGraduatedRail(cfg2, fx.snd)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rail2.Open(ctx, "corr-o", terms); err != nil {
			t.Fatal(err)
		}
		sid := rail2.SessionID("corr-o")
		if _, err := rail2.Dispute(ctx, sid, [32]byte{}); err != nil {
			t.Fatal(err)
		}
		if _, err := fx.rail.EscalateDispute(ctx, sid); err == nil {
			t.Fatal("adapter must reject sessions it doesn't arbitrate")
		}
	})

	t.Run("malformed session id refused client-side", func(t *testing.T) {
		fx := newKlerosFixture(t)
		if _, err := fx.rail.EscalateDispute(ctx, "not-hex"); err == nil {
			t.Fatal("bad session id must fail before any chain call")
		}
	})

	t.Run("arbitration cost quotes the adapter", func(t *testing.T) {
		fx := newKlerosFixture(t)
		cost, err := fx.rail.ArbitrationCost(ctx)
		if err != nil {
			t.Fatalf("cost: %v", err)
		}
		if cost.Cmp(big.NewInt(7)) != 0 {
			t.Fatalf("cost = %s, want 7 (the DeployKleros quote)", cost)
		}
	})

	t.Run("evidence requires locked + party", func(t *testing.T) {
		fx := newKlerosFixture(t)
		terms := testTerms()
		fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
		if _, err := fx.rail.Open(ctx, "corr-ev", terms); err != nil {
			t.Fatal(err)
		}
		sid := fx.rail.SessionID("corr-ev")
		// Pre-dispute: not Locked → revert.
		if _, err := fx.rail.SubmitEvidence(ctx, sid, "{}"); err == nil {
			t.Fatal("evidence on unlocked session must revert")
		}
		if _, err := fx.rail.SubmitEvidence(ctx, sid, ""); err == nil {
			t.Fatal("empty evidence must fail client-side")
		}
	})
}
