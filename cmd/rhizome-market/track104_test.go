// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 104: wallet signing + chain pinning + escrow event watches —
// exercised over the real RPC rail against FakeChain (raw signed txs
// land on-chain, getLogs drives purchase transitions).

package main

import (
	"context"
	"encoding/json"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// fixture key — tests only; never holds funds anywhere real.
const t104Key = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"

var t104RailCfg = settlement.RailConfig{
	ChainID:           11155111,
	Factory:           "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	Token:             "0x1111111111111111111111111111111111111111",
	Arbiter:           "0x4444444444444444444444444444444444444444",
	DisputeWindowSecs: 3600,
	WrappedNative:     "0xfFf9976782d46CC05630D1f6eBAb18b2324d6B14",
}

// walletAt creates a scrypt-backed wallet store under home (the exact
// location the module opens) and imports the fixture key.
func walletAt(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "t104-test")
	ws := web3.OpenWalletStore(web3.WalletDir(home))
	e, err := ws.Import(t104Key, "buyer")
	if err != nil {
		t.Fatalf("wallet import: %v", err)
	}
	return e.Address
}

func rpcRailFor(
	t *testing.T, snd settlement.Sender,
) *settlement.RPCRail {
	t.Helper()
	r, err := settlement.NewRPCRail(t104RailCfg, snd)
	if err != nil {
		t.Fatalf("rail: %v", err)
	}
	return r
}

// --- wallet signer ---------------------------------------------------------

// The flagship Track-104 test: a buy whose escrow open + release are
// locally signed raw transactions, landing on FakeChain end to end.
func TestBuy_WalletSignerEndToEnd(t *testing.T) {
	fc := settlement.NewFakeChain(t, t104RailCfg)
	ctx := context.Background()
	audit := newAuditLogger("")

	// Seller side — sessionMgr gates on the same FakeChain rail.
	sellerDir := t.TempDir()
	sellerID := testutil.NewIdentity(t)
	smgr := newSessionMgr(sellerDir, audit)
	smgr.ident.Store(sellerID)
	stub := &stubAgent{replyText: "wallet-signed work product", promptCh: make(chan string, 4)}
	smgr.spawnFn = func(
		_ context.Context, _ *config.Config, _ acp.BoundSource, opts acp.BoundSpawn,
	) (*acp.BoundAgent, error) {
		return boundPipeAgent(t, stub, opts)
	}
	smc := &marketConfig{
		serveEnabled: true, runtime: "exec", payoutAddress: testSeller,
		maxSessions: 8, sessionTTL: 2 * time.Minute,
		offers: []offer{{
			ID: "offer-1", AgentBinding: "agent-1",
			PriceSheet: priceSheet{PerTask: "0.05", Asset: "USDC", ChainID: 11155111},
		}},
	}
	sellerClient := web3.NewClient(fc.Endpoint(), "", nil)
	smgr.setConfig(smc, &config.Config{},
		map[string]acp.BoundSource{
			"agent-1": {ID: "agent-1", ACP: &config.ACPAgentConfig{Command: "stub"}},
		},
		rpcRailFor(t, settlement.NewDirectSender(sellerClient, testSeller)))
	bs := &bridgeServer{mgr: smgr, audit: audit}

	// Buyer side — wallet-signing purchase manager.
	home, buyerDir := t.TempDir(), t.TempDir()
	buyer := walletAt(t, home)
	pm := newPurchaseMgr(buyerDir, home, audit)
	bmc := &marketConfig{
		buyerAddress:        buyer,
		signerMode:          "wallet",
		exportRequireReview: "never",
		exportRedact:        true,
		buyAutoRelease:      true,
		sessionTTL:          2 * time.Minute,
		rail:                &t104RailCfg,
	}
	ep := &web3.Endpoint{URL: fc.Endpoint(), Source: web3.SourceOverride}
	pm.setConfig(bmc,
		rpcRailFor(t, settlement.NewDirectSender(sellerClient, buyer)), ep)
	pm.dial = func(peer, protocol string) (net.Conn, error) {
		if protocol != acpMarketProtocol {
			return nil, net.ErrClosed
		}
		c1, c2 := net.Pipe()
		a := newConnAgent("buyer-peer", smgr.nextConnID(), smgr, audit)
		go bs.serveACP(c2, a)
		return c1, nil
	}

	// Fund the buyer — FakeChain tokens price at 18 decimals.
	fc.DeployToken(t104RailCfg.Token)
	fc.Mint(t104RailCfg.Token, buyer,
		new(big.Int).Mul(big.NewInt(100), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))

	// Journal the seller's advert (configured posture → factory must match).
	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: sellerID.PeerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Payout:    &advertPayout{Address: testSeller, Asset: "USDC", ChainID: 11155111},
		Offers: []offer{{
			ID: "offer-1", AgentBinding: "agent-1",
			PriceSheet: priceSheet{PerTask: "0.05", Asset: "USDC", ChainID: 11155111},
		}},
		Escrow: &advertEscrow{Posture: "configured", Contract: t104RailCfg.Factory},
	})
	if err := peeradverts.Record(home, sellerID.PeerID, true,
		map[string]json.RawMessage{moduleID: advJSON}); err != nil {
		t.Fatalf("journal: %v", err)
	}

	p, _, err := pm.begin(ctx, buyRequest{
		Provider: sellerID.PeerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	snap := waitState(t, pm, p.PurchaseID, purchaseCompleted)
	if snap.ReceiptOK == nil || !*snap.ReceiptOK {
		t.Fatalf("receipt unverified: %+v", snap)
	}
	// Open and release each record one tx hash — both wallet-signed.
	if len(snap.TxHashes) < 2 {
		t.Fatalf("expected open + release tx hashes, got %v", snap.TxHashes)
	}
	// Release moves escrow balance to the provider (testSeller).
	if fc.Balance(t104RailCfg.Token, testSeller).Sign() == 0 {
		t.Fatalf("escrow not released on-chain - provider balance is 0")
	}
}

// --- chain pinning at rail assembly ----------------------------------------

func assembleFixture(
	t *testing.T, endpoint string, rail *settlement.RailConfig,
	allowMainnet bool, signer string,
) (settlement.Rail, *web3.Endpoint) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Tools.Web3.Endpoint = endpoint
	cfg.Tools.Web3.AllowPrivateEndpoints = true // loopback FakeChain
	mc := &marketConfig{
		rail: rail, payoutAddress: testSeller,
		signerMode: signer, allowMainnet: allowMainnet,
	}
	return assembleRail(context.Background(), cfg, mc, t.TempDir())
}

func TestAssembleRail_ChainPinMismatch(t *testing.T) {
	fc := settlement.NewFakeChain(t, t104RailCfg) // serves chain 11155111
	bad := t104RailCfg
	bad.ChainID = 8453 // pinned wrong — the rail must refuse
	if rail, _ := assembleFixture(t, fc.Endpoint(), &bad, false, "approval"); rail != nil {
		t.Fatal("mismatched chain pin should refuse the rail")
	}
}

func TestAssembleRail_MainnetRefused(t *testing.T) {
	fc := settlement.NewFakeChain(t, settlement.RailConfig{
		ChainID: 1, Factory: t104RailCfg.Factory, WrappedNative: t104RailCfg.WrappedNative,
	})
	mn := t104RailCfg
	mn.ChainID = 1
	if rail, _ := assembleFixture(t, fc.Endpoint(), &mn, false, "approval"); rail != nil {
		t.Fatal("mainnet must refuse without escrow_allow_mainnet")
	}
	if rail, _ := assembleFixture(t, fc.Endpoint(), &mn, true, "approval"); rail == nil {
		t.Fatal("mainnet with explicit opt-in should assemble")
	}
}

func TestAssembleRail_WalletSignerAssembles(t *testing.T) {
	fc := settlement.NewFakeChain(t, t104RailCfg)
	walletAt(t, t.TempDir()) // env only — sender store opens lazily
	rail, ep := assembleFixture(t, fc.Endpoint(), &t104RailCfg, false, "wallet")
	if rail == nil || ep == nil {
		t.Fatal("wallet signer rail should assemble")
	}
}

// --- escrow event watcher --------------------------------------------------

// watcherFixture: a configured-rail purchase mid-flight plus a writer
// rail to drive on-chain events the watcher must observe.
func watcherFixture(
	t *testing.T, ev func(ctx context.Context, r settlement.Rail, escrow string),
	want string,
) *purchase {
	fc := settlement.NewFakeChain(t, t104RailCfg)
	ctx := context.Background()
	client := web3.NewClient(fc.Endpoint(), "", nil)
	audit := newAuditLogger("")

	home, dir := t.TempDir(), t.TempDir()
	pm := newPurchaseMgr(dir, home, audit)
	bmc := &marketConfig{
		rail: &t104RailCfg, watchInterval: time.Minute,
		signerMode: "direct", buyerAddress: testBuyer,
	}
	writer := rpcRailFor(t, settlement.NewDirectSender(client, testBuyer))
	pm.setConfig(bmc, writer, &web3.Endpoint{URL: fc.Endpoint()})

	// Open a real escrow so events have somewhere to live.
	fc.DeployToken(t104RailCfg.Token)
	fc.Mint(t104RailCfg.Token, testBuyer,
		new(big.Int).Mul(big.NewInt(100), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
	var th [32]byte
	escrow, err := writer.PredictEscrowAddr(ctx, "watch-corr")
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if _, err := writer.Open(ctx, "watch-corr", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: t104RailCfg.Token,
		Amount: big.NewInt(5), TaskHash: th,
		TerminationTime: time.Now().Unix() + 3600,
	}); err != nil {
		t.Fatalf("open: %v", err)
	}

	p := &purchase{
		V: 1, PurchaseID: "pw-" + escrow[:10], SessionID: escrow,
		State: purchaseSession, Settlement: "configured",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	pm.register(p)
	_ = pm.save(p)

	ev(ctx, writer, escrow)
	pm.scanEscrowEvents(ctx)
	got := pm.snapshot(p)
	if got.State != want {
		t.Fatalf("watcher left purchase in %s, want %s", got.State, want)
	}
	if got.WatchBlock == 0 {
		t.Fatal("watch_block not advanced")
	}
	return &got
}

func TestWatcher_LockDisputes(t *testing.T) {
	watcherFixture(t, func(ctx context.Context, r settlement.Rail, escrow string) {
		var details [32]byte
		copy(details[:], []byte("bad result"))
		if _, err := r.Dispute(ctx, escrow, details); err != nil {
			t.Fatalf("lock: %v", err)
		}
	}, purchaseDisputed)
}

func TestWatcher_ReleaseCompletes(t *testing.T) {
	watcherFixture(t, func(ctx context.Context, r settlement.Rail, escrow string) {
		if _, err := r.Release(ctx, escrow); err != nil {
			t.Fatalf("release: %v", err)
		}
	}, purchaseCompleted)
}

func TestWatcher_ResolveResolves(t *testing.T) {
	fc := settlement.NewFakeChain(t, t104RailCfg)
	ctx := context.Background()
	client := web3.NewClient(fc.Endpoint(), "", nil)
	audit := newAuditLogger("")
	home, dir := t.TempDir(), t.TempDir()
	pm := newPurchaseMgr(dir, home, audit)
	bmc := &marketConfig{
		rail: &t104RailCfg, watchInterval: time.Minute,
		signerMode: "direct", buyerAddress: testBuyer,
	}
	writer := rpcRailFor(t, settlement.NewDirectSender(client, testBuyer))
	pm.setConfig(bmc, writer, &web3.Endpoint{URL: fc.Endpoint()})
	fc.DeployToken(t104RailCfg.Token)
	fc.Mint(t104RailCfg.Token, testBuyer,
		new(big.Int).Mul(big.NewInt(100), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)))
	var th, details [32]byte
	escrow, _ := writer.PredictEscrowAddr(ctx, "watch-res")
	if _, err := writer.Open(ctx, "watch-res", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: t104RailCfg.Token,
		Amount: big.NewInt(5), TaskHash: th,
		TerminationTime: time.Now().Unix() + 3600,
	}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := writer.Dispute(ctx, escrow, details); err != nil {
		t.Fatalf("lock: %v", err)
	}
	p := &purchase{
		V: 1, PurchaseID: "pw-res", SessionID: escrow,
		State: purchaseDisputed, Settlement: "configured",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	pm.register(p)
	// Arbiter resolves — a sender neither side controls.
	arb := rpcRailFor(t,
		settlement.NewDirectSender(client, t104RailCfg.Arbiter))
	if _, err := arb.Resolve(ctx, escrow, big.NewInt(2), big.NewInt(3), details); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	pm.scanEscrowEvents(ctx)
	if got := pm.snapshot(p); got.State != purchaseResolved {
		t.Fatalf("state %s, want resolved", got.State)
	}
}

// --- config ----------------------------------------------------------------

func TestConfig_WalletSignerFields(t *testing.T) {
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"settlement_signer":     "wallet",
		"escrow_allow_mainnet":  "true",
		"escrow_watch_interval": "45s",
		"escrow_chain_id":       "11155111",
		"escrow_contract":       t104RailCfg.Factory,
		"escrow_token":          t104RailCfg.Token,
		"escrow_arbiter":        t104RailCfg.Arbiter,
		"escrow_dispute_window": "3600",
	}, nil), t.TempDir())
	if mc.signerMode != "wallet" || !mc.allowMainnet ||
		mc.watchInterval != 45*time.Second {
		t.Fatalf("fields not parsed: %+v", mc)
	}
	for _, e := range mc.errs {
		if strings.Contains(e, "settlement_signer") {
			t.Fatalf("wallet mode errs: %s", e)
		}
	}
}

func TestConfig_SignerOnFixtureRailWarns(t *testing.T) {
	// wallet/direct signing on the fixture rail is a no-op — the config
	// says so rather than silently ignoring the mode.
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"settlement_signer": "wallet",
	}, nil), t.TempDir())
	found := false
	for _, e := range mc.errs {
		if strings.Contains(e, "no effect without escrow_contract") {
			found = true
		}
	}
	if !found {
		t.Fatalf("wallet-on-fixture should warn, errs: %v", mc.errs)
	}
}

func TestConfig_WatchIntervalBounds(t *testing.T) {
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"escrow_watch_interval": "5s",
	}, nil), t.TempDir())
	found := false
	for _, e := range mc.errs {
		if strings.Contains(e, "escrow_watch_interval") {
			found = true
		}
	}
	if !found {
		t.Fatal("sub-15s watch interval must err")
	}
}
