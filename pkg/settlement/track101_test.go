package settlement

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/web3"
)

// Compile-time conformance: both implementations satisfy SettlementRail.
var (
	_ Rail = (*MockRail)(nil)
	_ Rail = (*RPCRail)(nil)
)

const (
	testBuyer   = "0x1000000000000000000000000000000000000001"
	testSeller  = "0x2000000000000000000000000000000000000002"
	testArbiter = "0x3000000000000000000000000000000000000003"
	testToken   = "0x4000000000000000000000000000000000000004"
	testFactory = "0x5000000000000000000000000000000000000005"
)

func testTerms() Terms {
	return Terms{
		Buyer:    testBuyer,
		Seller:   testSeller,
		Token:    testToken,
		Amount:   big.NewInt(100),
		TaskHash: [32]byte{0xde, 0xad},
	}
}

func testRailConfig() RailConfig {
	return RailConfig{
		ChainID:           SepoliaChainID,
		Factory:           testFactory,
		Token:             testToken,
		Arbiter:           testArbiter,
		DisputeWindowSecs: 3600,
		WrappedNative:     "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14",
	}
}

func TestConfigFromFields(t *testing.T) {
	t.Run("unset returns nil (fixture default)", func(t *testing.T) {
		cfg, err := ConfigFromFields(map[string]string{})
		if err != nil || cfg != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", cfg, err)
		}
	})
	t.Run("full config resolves", func(t *testing.T) {
		cfg, err := ConfigFromFields(map[string]string{
			"escrow_chain_id":       "11155111",
			"escrow_contract":       SepoliaFactory,
			"escrow_token":          testToken,
			"escrow_arbiter":        testArbiter,
			"escrow_dispute_window": "7200",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ChainID != 11155111 || cfg.Factory != SepoliaFactory ||
			cfg.Token != testToken || cfg.Arbiter != testArbiter ||
			cfg.DisputeWindowSecs != 7200 {
			t.Fatalf("resolved cfg mismatch: %+v", cfg)
		}
		if cfg.WrappedNative == "" {
			t.Fatal("wrapped native should resolve for sepolia")
		}
	})
	bad := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"no chain", map[string]string{
			"escrow_contract": SepoliaFactory, "escrow_token": testToken,
			"escrow_arbiter": testArbiter, "escrow_dispute_window": "60",
		}, "escrow_chain_id"},
		{"no token", map[string]string{
			"escrow_chain_id": "11155111", "escrow_contract": SepoliaFactory,
			"escrow_arbiter": testArbiter, "escrow_dispute_window": "60",
		}, "escrow_token"},
		{"no arbiter", map[string]string{
			"escrow_chain_id": "11155111", "escrow_contract": SepoliaFactory,
			"escrow_token": testToken, "escrow_dispute_window": "60",
		}, "escrow_arbiter"},
		{"bad window", map[string]string{
			"escrow_chain_id": "11155111", "escrow_contract": SepoliaFactory,
			"escrow_token": testToken, "escrow_arbiter": testArbiter,
			"escrow_dispute_window": "0",
		}, "escrow_dispute_window"},
		{"bad contract addr", map[string]string{
			"escrow_chain_id": "11155111", "escrow_contract": "notanaddr",
			"escrow_token": testToken, "escrow_arbiter": testArbiter,
			"escrow_dispute_window": "60",
		}, "escrow_contract"},
		{"unmapped chain", map[string]string{
			"escrow_chain_id": "999999999", "escrow_contract": SepoliaFactory,
			"escrow_token": testToken, "escrow_arbiter": testArbiter,
			"escrow_dispute_window": "60",
		}, "wrapped"},
	}
	for _, tc := range bad {
		t.Run("invalid/"+tc.name, func(t *testing.T) {
			cfg, err := ConfigFromFields(tc.fields)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got cfg=%v err=%v", tc.want, cfg, err)
			}
		})
	}
}

func TestBundledABIs(t *testing.T) {
	type sig struct {
		name  string
		arity int
	}
	for _, tc := range []struct {
		abi  *web3.ABI
		sigs []sig
	}{
		{factoryABI, []sig{
			{"create", 4},
			{"createDeterministic", 5},
			{"predictDeterministicAddress", 2},
			{"resolutionRateOf", 1},
		}},
		{escrowABI, []sig{
			{"token", 0},
			{"client", 0},
			{"provider", 0},
			{"total", 0},
			{"released", 0},
			{"locked", 0},
			{"terminationTime", 0},
			{"release", 0},
			{"release", 1},
			{"lock", 1},
			{"resolve", 3},
			{"withdraw", 0},
			{"getAmounts", 0},
		}},
		{erc20ABI, []sig{
			{"transfer", 2}, {"balanceOf", 1}, {"allowance", 2},
		}},
	} {
		for _, n := range tc.sigs {
			m, err := tc.abi.Method(n.name, n.arity)
			if err != nil {
				t.Fatalf("method %s/%d missing: %v", n.name, n.arity, err)
			}
			if len(m.Selector()) != 4 {
				t.Fatalf("%s: bad selector", n.name)
			}
		}
	}
	// Spot-check a known selector: transfer(address,uint256) = 0xa9059cbb.
	m, err := erc20ABI.Method("transfer", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Selector(); got[0] != 0xa9 || got[1] != 0x05 || got[2] != 0x9c || got[3] != 0xbb {
		t.Fatalf("transfer selector = %x, want a9059cbb", got)
	}
}

func TestMockRailLifecycle(t *testing.T) {
	ctx := context.Background()
	rail := NewMockRail(testRailConfig())
	terms := testTerms()

	addr := rail.PredictEscrowAddr("sess-1")
	if _, err := rail.Open(ctx, "sess-1", terms); err != nil {
		t.Fatalf("open: %v", err)
	}

	ok, err := rail.VerifyLock(ctx, addr, terms)
	if err != nil || !ok {
		t.Fatalf("verify lock = %v, %v; want true, nil", ok, err)
	}

	// Mismatched terms must fail closed.
	bad := terms
	bad.Amount = big.NewInt(50)
	if ok, err = rail.VerifyLock(ctx, addr, bad); err != nil || ok {
		t.Fatalf("mismatched amount verify = %v, %v; want false, nil", ok, err)
	}
	if _, err := rail.VerifyLock(ctx, "0x9000000000000000000000000000000000000009", terms); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("unknown session verify err = %v; want ErrNotFound", err)
	}

	// Release settles.
	if _, err := rail.Release(ctx, addr); err != nil {
		t.Fatalf("release: %v", err)
	}
	if ok, _ = rail.VerifyLock(ctx, addr, terms); ok {
		t.Fatal("verify should fail after release")
	}
}

func TestMockRailDisputeResolve(t *testing.T) {
	ctx := context.Background()
	rail := NewMockRail(testRailConfig())
	terms := testTerms()
	addr := rail.PredictEscrowAddr("sess-2")
	if _, err := rail.Open(ctx, "sess-2", terms); err != nil {
		t.Fatal(err)
	}
	if _, err := rail.Dispute(ctx, addr, [32]byte{1}); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if locked, _ := rail.EscrowLocked(addr); !locked {
		t.Fatal("escrow should be locked")
	}
	if ok, _ := rail.VerifyLock(ctx, addr, terms); ok {
		t.Fatal("locked escrow must fail verify")
	}
	// balance=100, fee=100/20=5 -> awards must sum to 95.
	if _, err := rail.Resolve(ctx, addr, big.NewInt(50), big.NewInt(50), [32]byte{}); err == nil {
		t.Fatal("resolve with wrong sum should fail")
	}
	if _, err := rail.Resolve(ctx, addr, big.NewInt(45), big.NewInt(50), [32]byte{}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}

func TestMockRailClaimAndExpiry(t *testing.T) {
	ctx := context.Background()
	rail := NewMockRail(RailConfig{DisputeWindowSecs: 60})
	now := time.Now()
	rail.SetNowFunc(func() time.Time { return now })
	terms := testTerms()
	addr := rail.PredictEscrowAddr("sess-3")
	if _, err := rail.Open(ctx, "sess-3", terms); err != nil {
		t.Fatal(err)
	}
	// Provider-side claim locks the escrow.
	if _, err := rail.Claim(ctx, addr); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Drive the clock past termination: lock/dispute fail, verify fails.
	past := NewMockRail(RailConfig{DisputeWindowSecs: 60})
	past.SetNowFunc(func() time.Time { return now })
	terms4 := testTerms()
	addr4 := past.PredictEscrowAddr("sess-4")
	if _, err := past.Open(ctx, "sess-4", terms4); err != nil {
		t.Fatal(err)
	}
	past.SetNowFunc(func() time.Time { return now.Add(2 * time.Hour) })
	if _, err := past.Dispute(ctx, addr4, [32]byte{}); err == nil {
		t.Fatal("dispute past termination should fail")
	}
	if ok, _ := past.VerifyLock(ctx, addr4, terms4); ok {
		t.Fatal("expired escrow must fail verify")
	}
}

// --- RPCRail + FakeChain E2E ------------------------------------------

type chainFixture struct {
	fc   *FakeChain
	rail *RPCRail
	snd  *DirectSender
}

func newChainFixture(t *testing.T) *chainFixture {
	t.Helper()
	cfg := testRailConfig()
	fc := NewFakeChain(t, cfg)
	fc.DeployToken(testToken)
	snd := NewDirectSender(web3.NewClient(fc.Endpoint(), "", nil), testBuyer)
	rail, err := NewRPCRail(cfg, snd)
	if err != nil {
		t.Fatalf("NewRPCRail: %v", err)
	}
	return &chainFixture{fc: fc, rail: rail, snd: snd}
}

func TestRPCRailPredictAndOpen(t *testing.T) {
	fx := newChainFixture(t)
	ctx := context.Background()
	terms := testTerms()

	// Predict before deploy matches the fake's deployment address.
	pred, err := fx.rail.PredictEscrowAddr(ctx, "corr-1")
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if pred != fx.fc.EscrowAddr("corr-1") {
		t.Fatalf("predicted %s != fake %s", pred, fx.fc.EscrowAddr("corr-1"))
	}

	// Unfunded buyer: create deploys, funding transfer reverts.
	if _, err := fx.rail.Open(ctx, "corr-1", terms); err == nil ||
		!strings.Contains(err.Error(), "funding transfer") {
		t.Fatalf("open without funds err = %v; want funding-transfer revert", err)
	}
	// Escrow exists but is unfunded — verify fails (balance<total), no error.
	ok, err := fx.rail.VerifyLock(ctx, pred, terms)
	if err != nil {
		t.Fatalf("verify unfunded: %v", err)
	}
	if ok {
		t.Fatal("unfunded escrow must fail verify")
	}

	// Fund and reopen on a fresh correlation.
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-2", terms); err != nil {
		t.Fatalf("open: %v", err)
	}
	addr := fx.fc.EscrowAddr("corr-2")
	ok, err = fx.rail.VerifyLock(ctx, addr, terms)
	if err != nil || !ok {
		t.Fatalf("verify = %v, %v; want true, nil", ok, err)
	}

	// Terms round-trip: on-chain details == TaskHash, termination derived
	// from the window, client/provider/token bound correctly.
	got, exists := fx.fc.EscrowSnapshot(addr)
	if !exists {
		t.Fatal("no escrow recorded")
	} else {
		if got.details[0] != 0xde || got.details[1] != 0xad {
			t.Fatalf("details = %x, want dead…", got.details[:4])
		}
		if !addrEq(got.client, testBuyer) || !addrEq(got.provider, testSeller) {
			t.Fatalf("parties mismatch: %s / %s", got.client, got.provider)
		}
		if got.total.Cmp(terms.Amount) != 0 {
			t.Fatalf("total = %s, want %s", got.total, terms.Amount)
		}
		if got.resolutionRate != DefaultResolutionRate {
			t.Fatalf("resolutionRate = %d, want %d", got.resolutionRate, DefaultResolutionRate)
		}
	}
}

func TestRPCRailVerifyLockMismatches(t *testing.T) {
	fx := newChainFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-m", terms); err != nil {
		t.Fatal(err)
	}
	addr := fx.fc.EscrowAddr("corr-m")

	cases := []struct {
		name string
		mut  func(*Terms)
	}{
		{"amount", func(x *Terms) { x.Amount = big.NewInt(99) }},
		{"seller", func(x *Terms) { x.Seller = testBuyer }},
		{"buyer", func(x *Terms) { x.Buyer = testSeller }},
		{"token", func(x *Terms) { x.Token = "0x9000000000000000000000000000000000000009" }},
		{"termination", func(x *Terms) { x.TerminationTime = time.Now().Unix() + 9999 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := terms
			tc.mut(&bad)
			ok, err := fx.rail.VerifyLock(ctx, addr, bad)
			if err != nil {
				t.Fatalf("verify error: %v", err)
			}
			if ok {
				t.Fatal("mismatched terms verified — must fail closed")
			}
		})
	}
	// Exact termination match verifies.
	st, exists := fx.fc.EscrowSnapshot(addr)
	if !exists {
		t.Fatal("escrow missing")
	}
	exact := terms
	exact.TerminationTime = st.terminationTime
	ok, err := fx.rail.VerifyLock(ctx, addr, exact)
	if err != nil || !ok {
		t.Fatalf("exact-termination verify = %v, %v", ok, err)
	}
	// Un-deployed address → ErrNotFound surfaced as an error.
	if _, err := fx.rail.VerifyLock(ctx, "0x9000000000000000000000000000000000000009", terms); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("un-deployed verify err = %v; want ErrNotFound", err)
	}
}

func TestRPCRailReleaseAndLogs(t *testing.T) {
	fx := newChainFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-r", terms); err != nil {
		t.Fatal(err)
	}
	addr := fx.fc.EscrowAddr("corr-r")

	// Only the client can release — the seller's release reverts.
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Release(ctx, addr); err == nil ||
		!strings.Contains(err.Error(), "NotClient") {
		t.Fatalf("seller release err = %v; want NotClient revert", err)
	}
	fx.snd.SetFrom(testBuyer)
	tx, err := fx.rail.Release(ctx, addr)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if !strings.HasPrefix(tx, "0x") {
		t.Fatalf("release tx = %q", tx)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(terms.Amount) != 0 {
		t.Fatalf("seller balance = %s, want %s", got, terms.Amount)
	}
	if ok, _ := fx.rail.VerifyLock(ctx, addr, terms); ok {
		t.Fatal("released escrow must fail verify")
	}

	// eth_getLogs surface: the factory's LogNewInvoice is queryable.
	raw, err := fx.snd.client.Call(ctx, "eth_getLogs", []any{map[string]any{
		"address": testFactory,
		"topics":  []any{topicOf("LogNewInvoice(uint256,address,uint256[],bytes32,uint256)")},
	}})
	if err != nil {
		t.Fatalf("getLogs: %v", err)
	}
	logs, trunc, err := web3.DecodeLogs(raw, 10)
	if err != nil || trunc {
		t.Fatalf("decode logs: %v trunc=%v", err, trunc)
	}
	if len(logs) != 1 {
		t.Fatalf("want 1 LogNewInvoice log, got %d", len(logs))
	}
	if !strings.EqualFold(logs[0].Topics[2], topicAddr(addr)) {
		t.Fatalf("log invoice addr %s != escrow %s", logs[0].Topics[2], addr)
	}
}

func TestRPCRailDisputeClaimResolve(t *testing.T) {
	fx := newChainFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-d", terms); err != nil {
		t.Fatal(err)
	}
	addr := fx.fc.EscrowAddr("corr-d")

	// Buyer disputes (client lock).
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Dispute(ctx, addr, [32]byte{0xaa}); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if ok, _ := fx.rail.VerifyLock(ctx, addr, terms); ok {
		t.Fatal("locked escrow must fail verify")
	}

	// Arbiter resolves: balance 100, fee 100/20=5 → awards sum to 95.
	fx.snd.SetFrom(testArbiter)
	if _, err := fx.rail.Resolve(ctx, addr, big.NewInt(40), big.NewInt(55), [32]byte{0xbb}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := fx.fc.Balance(testToken, testSeller); got.Cmp(big.NewInt(55)) != 0 {
		t.Fatalf("seller got %s, want 55", got)
	}
	if got := fx.fc.Balance(testToken, testBuyer); got.Cmp(big.NewInt(1000-100+40)) != 0 {
		t.Fatalf("buyer got %s, want 940", got)
	}
	if got := fx.fc.Balance(testToken, testArbiter); got.Cmp(big.NewInt(5)) != 0 {
		t.Fatalf("arbiter fee %s, want 5", got)
	}

	// Provider-initiated claim on a second escrow (buyer funds it).
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Open(ctx, "corr-c", terms); err != nil {
		t.Fatal(err)
	}
	addr2 := fx.fc.EscrowAddr("corr-c")
	fx.snd.SetFrom(testSeller)
	if _, err := fx.rail.Claim(ctx, addr2); err != nil {
		t.Fatalf("claim: %v", err)
	}
	st, exists := fx.fc.EscrowSnapshot(addr2)
	if !exists || !st.locked {
		t.Fatal("claim should lock the escrow")
	}
	// A random party cannot lock.
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Open(ctx, "corr-x", terms); err != nil {
		t.Fatal(err)
	}
	fx.snd.SetFrom(testArbiter)
	if _, err := fx.rail.Dispute(ctx, fx.fc.EscrowAddr("corr-x"), [32]byte{}); err == nil ||
		!strings.Contains(err.Error(), "NotParty") {
		t.Fatalf("arbiter lock err = %v; want NotParty", err)
	}
}

func TestRPCRailTerminationEdge(t *testing.T) {
	fx := newChainFixture(t)
	ctx := context.Background()
	terms := testTerms()
	fx.fc.Mint(testToken, testBuyer, big.NewInt(1000))
	if _, err := fx.rail.Open(ctx, "corr-t", terms); err != nil {
		t.Fatal(err)
	}
	addr := fx.fc.EscrowAddr("corr-t")
	// Drive the clock past termination — verify fails, lock reverts.
	fx.fc.Advance(7200)
	if ok, _ := fx.rail.VerifyLock(ctx, addr, terms); ok {
		t.Fatal("terminated escrow must fail verify")
	}
	fx.snd.SetFrom(testBuyer)
	if _, err := fx.rail.Dispute(ctx, addr, [32]byte{}); err == nil ||
		!strings.Contains(err.Error(), "Terminated") {
		t.Fatalf("post-termination lock err = %v; want Terminated", err)
	}
}
