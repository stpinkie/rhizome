// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 103: buy-side purchasing — find → buy → escrow → ACP session →
// receipt → release, all over net.Pipe against a shared MockRail.

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/settlement"
)

const buyTask = "summarise the incident timeline"

// buyFixture wires a buyer purchaseMgr to a full seller sessionMgr over
// net.Pipe — every /v1/buy exercise drives the real gate→spawn→prompt→
// receipt path end to end, with one MockRail both sides settle on.
type buyFixture struct {
	pm       *purchaseMgr
	smgr     *sessionMgr
	rail     *settlement.MockRail
	stub     *stubAgent
	sellerID string // advert/peer identity the buyer discovers
	buyerDir string // purchase records
	home     string // journal location
	mc       *marketConfig
}

func newBuyFixture(t *testing.T, tune func(*marketConfig)) *buyFixture {
	t.Helper()
	audit := newAuditLogger("")
	rail := settlement.NewMockRail(settlement.RailConfig{})

	// Seller: session manager + stub agent behind spawnFn, signed receipts.
	sellerDir := t.TempDir()
	sellerID := testutil.NewIdentity(t)
	smgr := newSessionMgr(sellerDir, audit)
	smgr.ident.Store(sellerID)
	stub := &stubAgent{
		replyText: "the timeline has five entries",
		promptCh:  make(chan string, 4),
	}
	smgr.spawnFn = func(
		_ context.Context, _ *config.Config, _ acp.BoundSource, opts acp.BoundSpawn,
	) (*acp.BoundAgent, error) {
		return boundPipeAgent(t, stub, opts)
	}
	smc := &marketConfig{
		serveEnabled:  true,
		runtime:       "exec",
		payoutAddress: testSeller,
		maxSessions:   8,
		sessionTTL:    2 * time.Minute,
		offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet: priceSheet{
				PerTask: "5", Asset: "USDC", ChainID: 11155111,
			},
		}},
	}
	var railAny settlement.Rail = rail
	smgr.setConfig(smc, &config.Config{},
		map[string]acp.BoundSource{
			"agent-1": {ID: "agent-1", ACP: &config.ACPAgentConfig{Command: "stub"}},
		}, railAny)
	bs := &bridgeServer{mgr: smgr, audit: audit}

	// Buyer.
	home, buyerDir := t.TempDir(), t.TempDir()
	pm := newPurchaseMgr(buyerDir, home, audit)
	bmc := &marketConfig{
		buyerAddress:        testBuyer,
		exportRequireReview: "never",
		exportRedact:        true,
		buyAutoRelease:      true,
		sessionTTL:          2 * time.Minute,
	}
	if tune != nil {
		tune(bmc)
	}
	pm.setConfig(bmc, railAny, nil)
	pm.dial = func(peer, protocol string) (net.Conn, error) {
		if protocol != acpMarketProtocol {
			return nil, fmt.Errorf("protocol %s", protocol)
		}
		c1, c2 := net.Pipe()
		a := newConnAgent("buyer-peer", smgr.nextConnID(), smgr, audit)
		go bs.serveACP(c2, a)
		return c1, nil
	}
	f := &buyFixture{
		pm: pm, smgr: smgr, rail: rail, stub: stub,
		sellerID: sellerID.PeerID, buyerDir: buyerDir, home: home, mc: bmc,
	}
	return f
}

// journalSeller writes the seller's advert into the buyer's peer journal.
func (f *buyFixture) journalSeller(t *testing.T) {
	t.Helper()
	advJSON, err := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: f.sellerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Payout:    &advertPayout{Address: testSeller, Asset: "USDC", ChainID: 11155111},
		Offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet: priceSheet{
				PerTask: "5", Asset: "USDC", ChainID: 11155111,
			},
		}},
		Escrow: &advertEscrow{Posture: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := peeradverts.Record(f.home, f.sellerID, true,
		map[string]json.RawMessage{moduleID: advJSON}); err != nil {
		t.Fatalf("journal: %v", err)
	}
}

// waitState polls until the purchase reaches one of the wanted states.
func waitState(t *testing.T, pm *purchaseMgr, id string, want ...string) *purchase {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		p := pm.lookup(id)
		if p == nil {
			t.Fatalf("purchase %s gone", id)
		}
		snap := pm.snapshot(p)
		for _, w := range want {
			if snap.State == w {
				return &snap
			}
		}
		if snap.State == purchaseFailed {
			wantFailed := false
			for _, w := range want {
				wantFailed = wantFailed || w == purchaseFailed
			}
			if !wantFailed {
				t.Fatalf("purchase failed: %s — %s", snap.ErrCode, snap.Error)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap := pm.snapshot(pm.lookup(id))
	t.Fatalf("purchase never reached %v (stuck at %s — %s)",
		want, snap.State, snap.Error)
	return nil
}

// escrowOpened reports whether the rail has an escrow at the address —
// "no record" is how a never-opened session_id reads on MockRail.
func escrowOpened(rail *settlement.MockRail, sessionID string) bool {
	_, err := rail.EscrowBalance(sessionID)
	return err == nil
}

// escrowFunded reports whether the escrow still holds its balance (not
// released and not withdrawn).
func escrowFunded(t *testing.T, rail *settlement.MockRail, sessionID string) bool {
	t.Helper()
	bal, err := rail.EscrowBalance(sessionID)
	if err != nil {
		t.Fatalf("escrow balance: %v", err)
	}
	return bal.Sign() > 0
}

// --- the flagship: full purchase lifecycle ---------------------------------

func TestBuy_EndToEndCompletes(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)

	p, reviewID, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if reviewID != "" {
		t.Fatalf("review=never should not mint a review id, got %q", reviewID)
	}
	if p.SessionID == "" || p.TaskHash == "" {
		t.Fatalf("purchase missing escrow/task binding: %+v", p)
	}

	snap := waitState(t, f.pm, p.PurchaseID, purchaseCompleted)
	if len(snap.TxHashes) != 2 {
		t.Fatalf("want open+release tx hashes, got %v", snap.TxHashes)
	}
	if snap.ReceiptOK == nil || !*snap.ReceiptOK {
		t.Fatalf("receipt not verified: %+v", snap.ReceiptOK)
	}
	if snap.ResultSHA256 == "" {
		t.Fatal("result hash not recorded")
	}
	want := sha256.Sum256([]byte(f.stub.replyText))
	if snap.ResultSHA256 != "0x"+hex.EncodeToString(want[:]) {
		t.Fatalf("result hash %s ≠ output", snap.ResultSHA256)
	}

	// The seller's agent got the exact task text; the escrow settled.
	select {
	case got := <-f.stub.promptCh:
		if got != buyTask {
			t.Fatalf("seller prompt %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("seller never saw the prompt")
	}
	if escrowFunded(t, f.rail, snap.SessionID) {
		t.Fatal("completed purchase left the escrow funded — release didn't land")
	}
	rc, err := loadReceipt(f.smgr.moduleDir, snap.SessionID)
	if err != nil {
		t.Fatalf("seller receipt: %v", err)
	}
	if rc.Signature == "" {
		t.Fatal("seller receipt unsigned")
	}
	// The purchase persisted with the receipt embedded.
	disk := filepath.Join(f.buyerDir, purchaseDirName,
		sanitizeSessionID(snap.PurchaseID)+".json")
	if _, err := readBounded(disk, 256<<10); err != nil {
		t.Fatalf("purchase record not persisted: %v", err)
	}
}

func TestBuy_UnknownProviderRefused(t *testing.T) {
	f := newBuyFixture(t, nil)
	_, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: "12D3peer-nobody", Offer: "offer-1", Task: buyTask,
	})
	be, ok := err.(*buyError)
	if !ok || be.code != "provider_unknown" {
		t.Fatalf("want provider_unknown, got %v", err)
	}
}

func TestBuy_UnknownOfferRefused(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	_, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "nope", Task: buyTask,
	})
	if be, ok := err.(*buyError); !ok || be.code != "unknown_offer" {
		t.Fatalf("want unknown_offer, got %v", err)
	}
}

func TestBuy_ExportRedactionApplies(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) { mc.exportRedact = true })
	f.journalSeller(t)
	// A token-shaped secret in the task text must reach the seller masked.
	task := "debug this — my key is sk-proj1234567890abcdef ok"
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: task,
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseCompleted)
	select {
	case got := <-f.stub.promptCh:
		if got == task || strings.Contains(got, "1234567890abcdef") {
			t.Fatalf("unredacted secret reached the seller: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no prompt")
	}
}

// --- caps -------------------------------------------------------------------

func TestBuy_Caps(t *testing.T) {
	// Per-task cap.
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.buyMaxCostPerTask = "4.99"
	})
	f.journalSeller(t)
	_, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if be, ok := err.(*buyError); !ok || be.code != "cap_exceeded" {
		t.Fatalf("per-task cap: %v", err)
	}

	// --max-cost ceiling.
	f = newBuyFixture(t, nil)
	f.journalSeller(t)
	_, _, err = f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask, MaxCost: "2",
	})
	if be, ok := err.(*buyError); !ok || be.code != "cap_exceeded" {
		t.Fatalf("--max-cost: %v", err)
	}
	// Bad --max-cost input is a bad request, not a silent ignore.
	_, _, err = f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask, MaxCost: "abc",
	})
	if be, ok := err.(*buyError); !ok || be.code != "bad_max_cost" {
		t.Fatalf("bad max-cost: %v", err)
	}

	// Daily cap: a same-asset completed purchase counts against the day.
	f = newBuyFixture(t, func(mc *marketConfig) {
		mc.buyMaxCostPerDay = "6"
	})
	f.journalSeller(t)
	f.pm.register(&purchase{
		PurchaseID: "prior", Price: "5", Asset: "USDC",
		State: purchaseCompleted, CreatedAt: time.Now(),
	})
	_, _, err = f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if be, ok := err.(*buyError); !ok || be.code != "cap_exceeded" {
		t.Fatalf("daily cap: %v", err)
	}
}

// --- attachments ------------------------------------------------------------

func TestBuy_Attachments(t *testing.T) {
	// Refused when the export flag is off (the default).
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	_, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
		Attachments: []string{"https://example.com/log.txt"},
	})
	if be, ok := err.(*buyError); !ok || be.code != "attachments_denied" {
		t.Fatalf("attachments should deny by default: %v", err)
	}

	// Enabled: http(s) links pass; file:// is refused even when allowed.
	f = newBuyFixture(t, func(mc *marketConfig) {
		mc.exportAllowAttachments = true
	})
	f.journalSeller(t)
	_, _, err = f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
		Attachments: []string{"file:///etc/passwd"},
	})
	if be, ok := err.(*buyError); !ok || be.code != "attachments_denied" {
		t.Fatalf("file:// should refuse even when enabled: %v", err)
	}
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
		Attachments: []string{"https://example.com/log.txt"},
	})
	if err != nil {
		t.Fatalf("enabled attachments: %v", err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseCompleted)
	if len(p.Attachments) != 1 {
		t.Fatalf("attachments not recorded: %v", p.Attachments)
	}
}

// --- export review ------------------------------------------------------------

func TestBuy_ReviewFlow(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.exportRequireReview = "prompt"
	})
	f.journalSeller(t)

	p, reviewID, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil || reviewID == "" {
		t.Fatalf("begin: %v review=%q", err, reviewID)
	}
	if pm := f.pm.snapshot(p); pm.State != purchasePendingReview {
		t.Fatalf("state %s", pm.State)
	}
	// No escrow opened while the review pends.
	if escrowOpened(f.rail, p.SessionID) {
		t.Fatal("escrow opened before review confirmation")
	}

	// Unknown/expired tokens refuse; a wrong purchase arg can't substitute.
	if _, _, err := f.pm.begin(context.Background(), buyRequest{
		ConfirmReviewID: "bogus",
	}); err == nil {
		t.Fatal("bogus review id accepted")
	}

	p2, _, err := f.pm.begin(context.Background(), buyRequest{
		ConfirmReviewID: reviewID,
	})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if p2.PurchaseID != p.PurchaseID {
		t.Fatal("confirm should replay the stored purchase")
	}
	waitState(t, f.pm, p.PurchaseID, purchaseCompleted)
}

func TestBuy_ReviewExpiry(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.exportRequireReview = "always"
	})
	f.journalSeller(t)
	_, reviewID, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.pm.mu.Lock()
	f.pm.reviews[reviewID].expiresAt = time.Now().Add(-time.Minute)
	f.pm.mu.Unlock()
	if _, _, err := f.pm.begin(context.Background(), buyRequest{
		ConfirmReviewID: reviewID,
	}); err == nil {
		t.Fatal("expired review token accepted")
	}
}

// --- receipt verification -----------------------------------------------------

func TestVerifyPurchaseReceipt_Catches(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseCompleted)
	good := pmReceipt(t, f.pm, p)

	cases := []struct {
		name   string
		mutate func(*receipt)
	}{
		{"session mismatch", func(r *receipt) { r.SessionID = "0x999" }},
		{"seller mismatch", func(r *receipt) { r.SellerPeerID = "12D3other" }},
		{"result hash mismatch", func(r *receipt) { r.ResultSHA256 = "0x00" }},
		{"offer mismatch", func(r *receipt) { r.OfferID = "other" }},
		{"price mismatch", func(r *receipt) { r.Terms.Price = "1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := *good
			tc.mutate(&bad)
			ok, err := verifyPurchaseReceipt(p, &bad)
			if ok || err == nil {
				t.Fatalf("tampered receipt accepted (ok=%v err=%v)", ok, err)
			}
		})
	}
}

func pmReceipt(t *testing.T, pm *purchaseMgr, p *purchase) *receipt {
	t.Helper()
	snap := pm.snapshot(p)
	if snap.Receipt == nil {
		t.Fatal("no receipt recorded")
	}
	return snap.Receipt
}

func TestBuy_UnsignedReceiptDisputable(t *testing.T) {
	// A seller with no signing identity mints an explicitly unsigned
	// receipt — the buyer must NOT release; funds stay locked disputable.
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	f.smgr.ident.Store(nil) // seller cannot sign

	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitState(t, f.pm, p.PurchaseID, purchaseDisputable)
	if snap.ErrCode != "receipt_invalid" && snap.ErrCode != "receipt_unverified" {
		t.Fatalf("want receipt refusal, got %s", snap.ErrCode)
	}
	if len(snap.TxHashes) != 1 {
		t.Fatalf("only the open tx should exist, got %v", snap.TxHashes)
	}
	// The escrow is still funded — no release was faked.
	if !escrowFunded(t, f.rail, p.SessionID) {
		t.Fatal("escrow released on an unverified receipt")
	}
}

// --- dispute / refund / release ----------------------------------------------

func TestBuy_DisputeLocksEscrow(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	f.smgr.ident.Store(nil) // unsigned receipt → disputable
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseDisputable)

	updated, err := f.pm.dispute(context.Background(), p.PurchaseID, "bad output")
	if err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if updated.State != purchaseDisputed {
		t.Fatalf("state %s", updated.State)
	}
	// Session-id addressing works too (the CLI takes either).
	f2 := newBuyFixture(t, nil)
	f2.journalSeller(t)
	f2.smgr.ident.Store(nil)
	p2, _, err := f2.pm.begin(context.Background(), buyRequest{
		Provider: f2.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f2.pm, p2.PurchaseID, purchaseDisputable)
	if _, err := f2.pm.dispute(
		context.Background(), p2.SessionID, "by session id"); err != nil {
		t.Fatalf("dispute by session id: %v", err)
	}
}

func TestBuy_RefundRequiresTermination(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	f.smgr.ident.Store(nil)
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseDisputable)
	// The escrow's termination is +24h — withdraw refuses honestly.
	if _, err := f.pm.refund(context.Background(), p.PurchaseID); err == nil {
		t.Fatal("withdraw during live escrow should refuse")
	}
}

func TestBuy_ManualRelease(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.buyAutoRelease = false
	})
	f.journalSeller(t)
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := waitState(t, f.pm, p.PurchaseID, purchaseAwaitRelease)
	if len(snap.TxHashes) != 1 {
		t.Fatalf("only the open tx should exist pre-release, got %v", snap.TxHashes)
	}
	if _, err := f.pm.releaseByID(context.Background(), p.PurchaseID); err != nil {
		t.Fatalf("manual release: %v", err)
	}
	post := f.pm.snapshot(p)
	if post.State != purchaseCompleted || len(post.TxHashes) != 2 {
		t.Fatalf("post-release: %s %v", post.State, post.TxHashes)
	}
}

// --- persistence ---------------------------------------------------------------

func TestPurchase_PersistsAcrossRestart(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	p, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, f.pm, p.PurchaseID, purchaseCompleted)

	// A fresh manager over the same dir restores the record.
	pm2 := newPurchaseMgr(f.buyerDir, f.home, newAuditLogger(""))
	got := pm2.lookup(p.PurchaseID)
	if got == nil {
		t.Fatal("purchase lost across restart")
	}
	snap := pm2.snapshot(got)
	if snap.State != purchaseCompleted || len(snap.TxHashes) != 2 {
		t.Fatalf("restored purchase %s %v", snap.State, snap.TxHashes)
	}
	// Session-id addressing restores too.
	if got := pm2.lookupAny(p.SessionID); got == nil || got.PurchaseID != p.PurchaseID {
		t.Fatal("session-id lookup broken after restart")
	}
}

// --- find -----------------------------------------------------------------------

func TestFind_PeerJournal(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	out, err := f.pm.runFind(context.Background(), "offer-1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	m, _ := out.(map[string]any)
	if m["source"] != "peers" {
		t.Fatalf("source %v", m)
	}
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 1 || rows[0].OfferID != "offer-1" {
		t.Fatalf("rows %v", rows)
	}
	if !rows[0].Trusted || rows[0].PeerID != f.sellerID {
		t.Fatalf("row flags %+v", rows[0])
	}
	// Non-matching query returns empty, not an error.
	out, err = f.pm.runFind(context.Background(), "nomatch")
	if err != nil {
		t.Fatal(err)
	}
	m, _ = out.(map[string]any)
	if rows, _ := m["providers"].([]findRow); len(rows) != 0 {
		t.Fatalf("nomatch rows %v", rows)
	}
}

// signedIndexServer serves an operator-curated index for find tests.
func signedIndexServer(
	t *testing.T, idx *marketIndex,
) (url string, pubkeyB64 string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(priv, doc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(doc)
		case "/index.json.sig":
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, base64.StdEncoding.EncodeToString(pub)
}

func TestFind_IndexSignedFetchAndStaleServe(t *testing.T) {
	f := newBuyFixture(t, nil)
	sellerID := testutil.NewIdentity(t)
	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: sellerID.PeerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Payout:    &advertPayout{Address: testSeller},
		Offers: []offer{{
			ID:           "offer-9",
			AgentBinding: "agent-1",
			PriceSheet:   priceSheet{PerTask: "2", Asset: "USDC", ChainID: 11155111},
		}},
	})
	idx := &marketIndex{V: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Providers: []indexProvider{{
		PeerID: sellerID.PeerID,
		Advert: json.RawMessage(advJSON),
	}}}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey

	out, err := f.pm.runFind(context.Background(), "offer-9")
	if err != nil {
		t.Fatalf("index find: %v", err)
	}
	m, _ := out.(map[string]any)
	if m["source"] != "index" || m["stale"] != false {
		t.Fatalf("find response %v", m)
	}
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 1 || rows[0].OfferID != "offer-9" {
		t.Fatalf("index rows %v", rows)
	}

	// A bad signature refuses — no serving unverified data.
	f.mc.indexPubKey = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	if _, err := f.pm.fetchIndexFresh(
		context.Background(), url, f.mc.indexPubKey); err == nil {
		t.Fatal("bad curator key verified an index")
	}
	// ...but the fresh cache still serves while TTL-valid.
	f.mc.indexPubKey = pubkey
	idx2, stale, err := f.pm.fetchIndex(context.Background())
	if err != nil || stale || idx2 == nil {
		t.Fatalf("cached fetch: %v stale=%v", err, stale)
	}
}

func TestFind_IndexStaleServeOnOutage(t *testing.T) {
	f := newBuyFixture(t, nil)
	idx := &marketIndex{V: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Providers: []indexProvider{}}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey
	if _, _, err := f.pm.fetchIndex(context.Background()); err != nil {
		t.Fatalf("seed fetch: %v", err)
	}
	// Force the cache stale and break the network — stale-serve wins.
	cachePath := filepath.Join(f.buyerDir, indexCacheFile)
	c := readIndexCache(cachePath)
	c.FetchedAt = time.Now().Add(-2 * indexCacheTTL)
	data, _ := json.Marshal(c)
	_ = writeFileAtomic(cachePath, data)
	f.mc.indexURL = "http://127.0.0.1:1" // unreachable
	got, stale, err := f.pm.fetchIndex(context.Background())
	if err != nil || !stale || got == nil {
		t.Fatalf("want stale-serve, got idx=%v stale=%v err=%v", got, stale, err)
	}
}

// --- id-vs-session_id regression ------------------------------------------------

func TestAPI_SessionIDAliasRegression(t *testing.T) {
	// The Track-99 CLI sends {"id": ...}; Track 102's server parsed only
	// {"session_id": ...}. Both forms must reach the same lookup —
	// an unknown id must 404, never 400 on the id field itself.
	_, addr := startTestAPI(t, "tok")
	for _, body := range []string{
		`{"id":"0x00000000000000000000000000000000000000aa"}`,
		`{"session_id":"0x00000000000000000000000000000000000000aa"}`,
	} {
		for _, verb := range []string{"session", "receipt"} {
			code, body := apiPost(t, addr, "tok", verb, body)
			if code != http.StatusNotFound {
				t.Fatalf("%s %s → %d %v (want 404 not-found)", verb, body, code, body)
			}
		}
	}
}
