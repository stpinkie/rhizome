// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 110: ACP-over-HTTPS — TLS listener, websocket upgrade, ndjson
// adaptation, advert endpoints/fingerprint, and buyer-side wss TOFU.

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
)

// startTestHTTPS brings up a real TLS listener on a loopback port with
// the self-signed fallback — the full serve path minus main()'s wiring.
func startTestHTTPS(
	t *testing.T, mc *marketConfig, moduleDir string, mgr *sessionMgr,
) *httpsServer {
	t.Helper()
	audit := newAuditLogger("")
	h, err := startHTTPS(mc, moduleDir, mgr, mgr.nextConnID, audit)
	if err != nil {
		t.Fatalf("startHTTPS: %v", err)
	}
	t.Cleanup(h.Close)
	return h
}

// --- certificates ----------------------------------------------------------

func TestHTTPS_SelfSignedCertPersisted(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := loadOrGenHTTPSCert(dir, "", "")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, httpsCertFile)); err != nil {
		t.Fatalf("cert not persisted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, httpsKeyFile)); err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
	// Second resolution loads the persisted pair — the fingerprint must
	// be stable or TOFU breaks across restarts.
	_, fp2, err := loadOrGenHTTPSCert(dir, "", "")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if fp1 == "" || fp1 != fp2 {
		t.Fatalf("fingerprint not stable across restart: %q vs %q", fp1, fp2)
	}
	if len(fp1) != 64 {
		t.Fatalf("fingerprint %q is not a sha256 hex", fp1)
	}
}

func TestHTTPS_ConfiguredCertLoaded(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "mine.pem")
	keyPath := filepath.Join(dir, "mine.key")
	if err := genSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	_, fpConfigured, err := loadOrGenHTTPSCert(dir, certPath, keyPath)
	if err != nil {
		t.Fatalf("configured load: %v", err)
	}
	_, fpAuto, err := loadHTTPSCert(
		filepath.Join(dir, httpsCertFile), filepath.Join(dir, httpsKeyFile))
	// The configured pair wins — no https-cert.pem must have been minted.
	if err == nil {
		t.Fatalf("auto pair minted alongside configured cert (fp %s)", fpAuto)
	}
	if fpConfigured == "" {
		t.Fatal("no fingerprint from configured cert")
	}
}

func TestHTTPS_ConfigValidation(t *testing.T) {
	t.Run("cert without key", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"serve_https_listen": "127.0.0.1:9443",
			"serve_https_cert":   "/tmp/cert.pem",
		}, nil), t.TempDir())
		if len(mc.errs) == 0 {
			t.Fatal("half cert pair not reported")
		}
		if mc.httpsCert != "" || mc.httpsKey != "" {
			t.Fatal("half pair must be neutralized, not reach startHTTPS")
		}
	})
	t.Run("bad listen addr", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"serve_https_listen": "not-an-addr",
		}, nil), t.TempDir())
		if len(mc.errs) == 0 {
			t.Fatal("bad listen not reported")
		}
		if mc.httpsListen != "" {
			t.Fatal("invalid listen must be neutralized")
		}
	})
	t.Run("bad max conns", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"serve_https_max_conns": "0",
		}, nil), t.TempDir())
		if len(mc.errs) == 0 {
			t.Fatal("max_conns=0 not reported")
		}
	})
	t.Run("advertise normalization", func(t *testing.T) {
		for _, v := range []string{
			"wss://market.example.com", "market.example.com:9443",
			"https://market.example.com",
		} {
			mc := loadMarketConfig(cfgWith(t, map[string]string{
				"serve_https_advertise": v,
			}, nil), t.TempDir())
			if len(mc.errs) != 0 {
				t.Fatalf("advertise %q flagged: %v", v, mc.errs)
			}
		}
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"serve_https_advertise": "ftp://nope",
		}, nil), t.TempDir())
		if len(mc.errs) == 0 {
			t.Fatal("ftp advertise not reported")
		}
	})
}

// --- listener + ws transport ------------------------------------------------

// TestHTTPS_WSUpgradeServesACP is the transport proof: a real wss dial
// against the listener drives initialize → session_open → session/new →
// prompt → receipt through the identical serveAgentConn path the bridge
// uses — nothing about the gate changes because the transport did.
func TestHTTPS_WSUpgradeServesACP(t *testing.T) {
	f := newGateFixture(t, 4)
	f.stub.replyText = "wss reply"
	mc := &marketConfig{httpsListen: "127.0.0.1:0"}
	h := startTestHTTPS(t, mc, f.dir, f.mgr)

	conn, err := dialWSS(h.advertiseEndpoint(), h.fingerprint)
	if err != nil {
		t.Fatalf("wss dial: %v", err)
	}
	p := &purchase{
		SessionID: f.sessionID,
		TaskHash:  taskHashHex(),
		OfferID:   "offer-1",
		Buyer:     testBuyer,
		Task:      testPrompt,
		Terms: purchaseTerms{
			Amount: "5000000", Token: fixtureToken, ChainID: 11155111,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := runBuyerSession(ctx, conn, p)
	_ = conn.Close()
	if err != nil {
		t.Fatalf("wss session: %v", err)
	}
	if res.ResultText != "wss reply" {
		t.Fatalf("result %q", res.ResultText)
	}
	// A completed prompt finishes the session — the minted receipt is the
	// durable proof the gate→spawn→prompt path ran over wss. finish()
	// persists after the client sees the prompt response, so poll.
	deadline := time.Now().Add(5 * time.Second)
	var rc *receipt
	for time.Now().Before(deadline) {
		if r, err := loadReceipt(f.dir, f.sessionID); err == nil {
			rc = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rc == nil {
		t.Fatal("receipt never persisted after wss session")
	}
	if rc.Interrupted {
		t.Fatal("clean wss session marked interrupted")
	}
}

func TestHTTPS_ConnCapEnforced(t *testing.T) {
	f := newGateFixture(t, 4)
	mc := &marketConfig{httpsListen: "127.0.0.1:0", httpsMaxConns: 1}
	h := startTestHTTPS(t, mc, f.dir, f.mgr)
	ep := h.advertiseEndpoint()

	c1, err := dialWSS(ep, h.fingerprint)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer c1.Close()
	// The cap refuses at the HTTP layer — the upgrade never happens.
	_, err = dialWSS(ep, h.fingerprint)
	if err == nil {
		t.Fatal("second dial exceeded the conn cap")
	}
	if !strings.Contains(err.Error(), "503") &&
		!strings.Contains(err.Error(), "bad status") {
		t.Fatalf("cap refusal should surface the 503, got: %v", err)
	}
}

func TestHTTPS_ConnDropFinalizesSession(t *testing.T) {
	f := newGateFixture(t, 4)
	f.mc.sessionTTL = time.Minute
	mc := &marketConfig{httpsListen: "127.0.0.1:0"}
	h := startTestHTTPS(t, mc, f.dir, f.mgr)

	conn, err := dialWSS(h.advertiseEndpoint(), h.fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	cli := acpsdk.NewClientSideConnection(
		&buyerClientHandler{}, conn, conn)
	ictx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cli.Initialize(ictx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var open map[string]any
	if err := json.Unmarshal(f.presentation(nil), &open); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.CallExtension(ictx, "_rhizome.session_open", open); err != nil {
		t.Fatalf("session_open: %v", err)
	}
	if got := f.mgr.sessionCount(); got != 1 {
		t.Fatalf("open: %d sessions", got)
	}
	// The wss drop must finalize the conn's session — same teardown the
	// bridge exercises on libp2p stream close. finish() flips the state
	// before mint+persist, so poll the receipt file — it's the real
	// completion signal.
	_ = conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	var rc *receipt
	for time.Now().Before(deadline) {
		if r, err := loadReceipt(f.dir, f.sessionID); err == nil {
			rc = r
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rc == nil {
		t.Fatal("interrupted receipt never persisted after wss drop")
	}
	if !rc.Interrupted {
		t.Fatal("dropped wss should mint an interrupted receipt")
	}
	if got := f.mgr.sessionCount(); got != 0 {
		t.Fatalf("wss drop left %d live sessions", got)
	}
}

// --- advert -----------------------------------------------------------------

// fakeHTTPS builds the advert's view of a running listener without
// serving — render only reads ln.Addr/fingerprint/advertise.
func fakeHTTPS(t *testing.T, listen, advertise, fp string) *httpsServer {
	t.Helper()
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &httpsServer{ln: ln, advertise: advertise, fingerprint: fp}
}

func TestAdvert_EndpointsAndFingerprint(t *testing.T) {
	h := fakeHTTPS(t, "127.0.0.1:0", "", "deadbeef")
	w := newAdvertWriter(t.TempDir(), "v", "", newAuditLogger(""), h)
	data, reason := w.render(servingCfg(t))
	if data == nil {
		t.Fatalf("advert omitted: %s", reason)
	}
	var a advert
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Endpoints) != 1 || !strings.HasPrefix(a.Endpoints[0], "wss://127.0.0.1:") {
		t.Fatalf("endpoints %+v", a.Endpoints)
	}
	if a.TLSFingerprint != "deadbeef" {
		t.Fatalf("fingerprint %q", a.TLSFingerprint)
	}
}

func TestAdvert_EndpointOverrides(t *testing.T) {
	// serve_https_advertise wins over the bound addr (public DNS/NAT).
	h := fakeHTTPS(t, "127.0.0.1:0", "wss://market.example.com", "fp")
	if got := h.advertiseEndpoint(); got != "wss://market.example.com/rhizome/acp" {
		t.Fatalf("advertise endpoint %q", got)
	}
	// A wildcard listen without advertise emits nothing — there is no
	// honest guess for 0.0.0.0.
	h = fakeHTTPS(t, "0.0.0.0:0", "", "fp")
	if got := h.advertiseEndpoint(); got != "" {
		t.Fatalf("wildcard emitted endpoint %q", got)
	}
	// https:// advertises normalize to wss.
	h = fakeHTTPS(t, "127.0.0.1:0", "https://m.example.com:9443", "fp")
	if got := h.advertiseEndpoint(); got != "wss://m.example.com:9443/rhizome/acp" {
		t.Fatalf("https normalize %q", got)
	}
}

func TestAdvert_NoEndpointsWithoutServing(t *testing.T) {
	// A listener that is up but not serving must not claim a dialable
	// endpoint — the gate would refuse every session_open anyway.
	h := fakeHTTPS(t, "127.0.0.1:0", "", "fp")
	w := newAdvertWriter(t.TempDir(), "v", "", newAuditLogger(""), h)
	data, _ := w.render(loadMarketConfig(cfgWith(t, nil, nil), t.TempDir()))
	var a advert
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if len(a.Endpoints) != 0 || a.TLSFingerprint != "" {
		t.Fatalf("non-serving advert claims wss: %+v", a)
	}
}

// --- buyer dial -------------------------------------------------------------

func TestHTTPS_DialRefusesFingerprintMismatch(t *testing.T) {
	f := newGateFixture(t, 4)
	mc := &marketConfig{httpsListen: "127.0.0.1:0"}
	h := startTestHTTPS(t, mc, f.dir, f.mgr)

	// A substituted cert / wrong pin must refuse — TOFU means the advert
	// fingerprint is the entire trust anchor.
	wrong := strings.Repeat("0", 64)
	_, err := dialWSS(h.advertiseEndpoint(), wrong)
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("mismatch not refused: %v", err)
	}
}

func TestHTTPS_DialRefusesUnpinned(t *testing.T) {
	if _, err := dialWSS("wss://127.0.0.1:1/rhizome/acp", ""); err == nil {
		t.Fatal("unpinned wss dial allowed")
	}
	if _, err := dialWSS("ws://127.0.0.1:1/rhizome/acp", "ab"); err == nil {
		t.Fatal("plaintext ws dial allowed")
	}
}

// --- the flagship: a whole purchase over real wss ----------------------------

func TestBuy_WSSEndToEnd(t *testing.T) {
	f := newBuyFixture(t, nil)

	// Seller serves wss on loopback; the advert carries the bound
	// endpoint + cert fingerprint so resolveProvider picks the direct
	// transport over the mesh dial.
	smc := &marketConfig{httpsListen: "127.0.0.1:0"}
	h := startTestHTTPS(t, smc, t.TempDir(), f.smgr)
	ep := h.advertiseEndpoint()
	if ep == "" {
		t.Fatal("no advertised endpoint")
	}

	advJSON, err := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: f.sellerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt:      time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Endpoints:      []string{ep},
		TLSFingerprint: h.fingerprint,
		Payout:         &advertPayout{Address: testSeller, Asset: "USDC", ChainID: 11155111},
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

	bp, _, err := f.pm.begin(context.Background(), buyRequest{
		Provider: f.sellerID, Offer: "offer-1", Task: buyTask,
	})
	if err != nil {
		t.Fatalf("buy: %v", err)
	}
	p := waitState(t, f.pm, bp.PurchaseID, purchaseCompleted)
	if p.DialTLSFP != h.fingerprint {
		t.Fatalf("purchase pinned fp %q, want %q", p.DialTLSFP, h.fingerprint)
	}
	if !strings.HasPrefix(p.DialAddr, "wss://") {
		t.Fatalf("purchase did not dial wss: %q", p.DialAddr)
	}
	if p.Receipt == nil || p.ReceiptOK == nil || !*p.ReceiptOK {
		t.Fatal("receipt not verified over wss")
	}
}
