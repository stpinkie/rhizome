package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/settlement"
)

// Track 134 — sell-side hardening:
//  - paid sessions run only under runtimes that enforce no-egress
//    (sandbox|container); anything else fails closed at activate, and
//    SpawnBound refuses NoEgress+exec at the library boundary
//  - per-peer rate-limit + concurrent-session refusals land in the audit
//    trail (market.gate.reject), and every lifecycle event carries the
//    terms hash + session id
//  - the advert publishes the enforced serving posture next to the
//    Track 131 attestation field.

// auditEvents reads a JSONL audit file back into decoded entries.
func auditEvents(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("audit line: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func auditFind(events []map[string]any, name string) map[string]any {
	for _, e := range events {
		if e["event"] == name {
			return e
		}
	}
	return nil
}

func TestTrack134_ActivateRefusesNonEnforcingRuntime(t *testing.T) {
	for _, rt := range []string{"exec", "", "bogus"} {
		f := newGateFixture(t, 4)
		f.mc.runtime = rt
		s, err := gateSessionOpen(
			context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
		if err != nil {
			t.Fatalf("rt %q gate: %v", rt, err)
		}
		a := newAgent(f)
		err = f.mgr.activate(context.Background(), a, s)
		if err == nil || !strings.Contains(err.Error(), "no-egress") {
			t.Fatalf("rt %q activate = %v, want no-egress refusal", rt, err)
		}
		f.mgr.finish(s, sessionFailed, "test teardown")
	}
}

func TestTrack134_ActivatePassesNoEgressAndRuntime(t *testing.T) {
	f := newGateFixture(t, 4)
	var got acp.BoundSpawn
	f.mgr.spawnFn = func(
		_ context.Context, _ *config.Config, _ acp.BoundSource, opts acp.BoundSpawn,
	) (*acp.BoundAgent, error) {
		got = opts
		return boundPipeAgent(t, f.stub, opts)
	}
	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := f.mgr.activate(context.Background(), newAgent(f), s); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if !got.NoEgress {
		t.Fatal("BoundSpawn.NoEgress = false — paid session would egress")
	}
	if got.Runtime != "sandbox" {
		t.Fatalf("BoundSpawn.Runtime = %q, want sandbox", got.Runtime)
	}
	if got.ScratchDir == "" {
		t.Fatal("BoundSpawn.ScratchDir empty")
	}
	f.mgr.finish(s, sessionCompleted, "done")
}

func TestTrack134_GateRejectAudit(t *testing.T) {
	f := newGateFixture(t, 4)
	auditPath := filepath.Join(f.dir, "market-audit.jsonl")
	f.mgr.audit = newAuditLogger(auditPath)

	// Rate-limit: peerOpenPerMinute opens per peer — the escrow verify
	// fails for all but the funded one, but every refusal must audit.
	for i := 0; i < peerOpenPerMinute; i++ {
		raw := f.presentation(map[string]string{"session_id": "0xdead"})
		_, _ = gateSessionOpen(context.Background(), f.mgr, "12D3peer-spam", 1, raw)
	}
	// The next open trips the per-peer rate limiter before escrow verify.
	_, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-spam", 1, f.presentation(nil))
	if err == nil || !strings.Contains(err.Error(), "rate exceeded") {
		t.Fatalf("rate-limited open = %v", err)
	}

	events := auditEvents(t, auditPath)
	var codes []string
	last := map[string]any{}
	for _, e := range events {
		if e["event"] == "market.gate.reject" {
			codes = append(codes, e["code"].(string))
			last = e
		}
	}
	if len(codes) != peerOpenPerMinute+1 {
		t.Fatalf("gate.reject events = %d, want %d: %v",
			len(codes), peerOpenPerMinute+1, codes)
	}
	if last["code"] != "rate_limited" {
		t.Fatalf("last reject code = %v, want rate_limited", last["code"])
	}
	if last["peer"] != "12D3peer-spam" {
		t.Fatalf("reject peer = %v", last["peer"])
	}
	if last["session_id"] == "" {
		t.Fatal("reject event missing session_id")
	}
}

func TestTrack134_ConcurrentCapAudit(t *testing.T) {
	f := newGateFixture(t, 1)
	auditPath := filepath.Join(f.dir, "market-audit.jsonl")
	f.mgr.audit = newAuditLogger(auditPath)

	// First funded open fills maxSessions=1; a second (funded, valid)
	// open trips the global concurrent-session cap — and the refusal
	// must land in the audit trail.
	if _, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-a", 1, f.presentation(nil)); err != nil {
		t.Fatalf("first open: %v", err)
	}
	sid2 := f.rail.PredictEscrowAddr("corr-2")
	th2 := testTaskHash
	th2[0] = 0x77
	if _, err := f.rail.Open(context.Background(), "corr-2", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller,
		Token: fixtureToken, Amount: big.NewInt(5_000_000), TaskHash: th2,
	}); err != nil {
		t.Fatalf("second escrow open: %v", err)
	}
	raw := f.presentation(map[string]string{
		"session_id": sid2, "task_hash": "0x" + hex.EncodeToString(th2[:]),
	})
	_, err := gateSessionOpen(context.Background(), f.mgr, "12D3peer-b", 2, raw)
	if err == nil || !strings.Contains(err.Error(), "max_concurrent_sessions") {
		t.Fatalf("over-cap open = %v, want cap refusal", err)
	}

	ev := auditFind(auditEvents(t, auditPath), "market.gate.reject")
	if ev == nil {
		t.Fatal("cap rejection produced no market.gate.reject event")
	}
	if ev["code"] != "cap_reached" {
		t.Fatalf("reject code = %v, want cap_reached", ev["code"])
	}
	if ev["session_id"] != sid2 {
		t.Fatalf("reject session_id = %v, want %s", ev["session_id"], sid2)
	}
	if ev["peer"] != "12D3peer-b" {
		t.Fatalf("reject peer = %v", ev["peer"])
	}
}

func TestTrack134_OpenAuditCarriesTermsHash(t *testing.T) {
	f := newGateFixture(t, 4)
	auditPath := filepath.Join(f.dir, "market-audit.jsonl")
	f.mgr.audit = newAuditLogger(auditPath)

	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	want := termsHash(
		s.EscrowID, fixtureToken, "5000000",
		hex.EncodeToString(testTaskHash[:]))

	ev := auditFind(auditEvents(t, auditPath), "market.gate.open")
	if ev == nil {
		t.Fatal("market.gate.open event missing")
	}
	if ev["session_id"] != s.ID {
		t.Fatalf("open session_id = %v, want %s", ev["session_id"], s.ID)
	}
	if ev["escrow_id"] != s.EscrowID {
		t.Fatalf("open escrow_id = %v, want %s", ev["escrow_id"], s.EscrowID)
	}
	if ev["terms_hash"] != want {
		t.Fatalf("open terms_hash = %v, want %s", ev["terms_hash"], want)
	}
	// Peer blank → reportOutcome skips its async bridge write, which
	// would race TempDir cleanup against the audit file.
	s.Peer = ""
	f.mgr.finish(s, sessionFailed, "test teardown")
}

func TestTrack134_SessionLifecycleAuditTermsHash(t *testing.T) {
	f := newGateFixture(t, 4)
	auditPath := filepath.Join(f.dir, "market-audit.jsonl")
	f.mgr.audit = newAuditLogger(auditPath)

	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := f.mgr.activate(context.Background(), newAgent(f), s); err != nil {
		t.Fatalf("activate: %v", err)
	}
	// Blank peer skips reportOutcome's async bridge write — it would
	// otherwise race TempDir cleanup against the audit file.
	s.Peer = ""
	f.mgr.finish(s, sessionCompleted, "prompt complete")

	want := termsHash(
		s.EscrowID, fixtureToken, "5000000",
		hex.EncodeToString(testTaskHash[:]))
	events := auditEvents(t, auditPath)
	for _, name := range []string{"market.session.active", "market.session.end"} {
		ev := auditFind(events, name)
		if ev == nil {
			t.Fatalf("%s event missing", name)
		}
		if ev["session_id"] != s.ID {
			t.Fatalf("%s session_id = %v", name, ev["session_id"])
		}
		if ev["escrow_id"] != s.EscrowID {
			t.Fatalf("%s escrow_id = %v", name, ev["escrow_id"])
		}
		if ev["terms_hash"] != want {
			t.Fatalf("%s terms_hash = %v, want %s", name, ev["terms_hash"], want)
		}
	}
}

func TestTrack134_SessionActiveAuditCarriesTEEClaim(t *testing.T) {
	f := newGateFixture(t, 4)
	f.mc.teeKind = "tdx"
	auditPath := filepath.Join(f.dir, "market-audit.jsonl")
	f.mgr.audit = newAuditLogger(auditPath)

	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if err := f.mgr.activate(context.Background(), newAgent(f), s); err != nil {
		t.Fatalf("activate: %v", err)
	}
	ev := auditFind(auditEvents(t, auditPath), "market.session.active")
	if ev == nil {
		t.Fatal("market.session.active missing")
	}
	if ev["tee_kind"] != "tdx" {
		t.Fatalf("session.active tee_kind = %v, want tdx", ev["tee_kind"])
	}
	s.Peer = ""
	f.mgr.finish(s, sessionFailed, "test teardown")
}

func TestTrack134_AdvertServingPosture(t *testing.T) {
	dir := t.TempDir()
	w := newAdvertWriter(dir, "v-test", "12D3peer", newAuditLogger(""), nil, nil)
	mc := &marketConfig{
		serveEnabled:  true,
		runtime:       "sandbox",
		payoutAddress: testSeller,
		payoutAsset:   "USDC",
		payoutChainID: 11155111,
		maxSessions:   8,
		offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet: priceSheet{
				PerTask: "5", Asset: "USDC", ChainID: 11155111,
			},
		}},
	}
	raw, reason := w.render(mc)
	if raw == nil {
		t.Fatalf("render refused: %s", reason)
	}
	var a advert
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("advert unmarshal: %v", err)
	}
	if a.Serving == nil {
		t.Fatal("serving posture block missing under serve_enabled")
	}
	if !a.Serving.NoEgress {
		t.Fatal("serving.no_egress = false on a sandbox advert")
	}
	if a.Serving.MaxSessions != 8 {
		t.Fatalf("serving.max_sessions = %d, want 8", a.Serving.MaxSessions)
	}
	if a.Serving.PerPeerCap != 4 {
		t.Fatalf("serving.per_peer_cap = %d, want 4", a.Serving.PerPeerCap)
	}
	if a.Serving.OpenRatePerMinute != peerOpenPerMinute {
		t.Fatalf("serving.open_rate_per_minute = %d", a.Serving.OpenRatePerMinute)
	}

	// Non-serving adverts carry no posture claims at all.
	mc.serveEnabled = false
	raw, _ = w.render(mc)
	a = advert{}
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("non-serve unmarshal: %v", err)
	}
	if a.Serving != nil {
		t.Fatal("serving block present on non-serving advert")
	}
	if a.Attestation != nil {
		t.Fatal("attestation present on non-serving advert")
	}
}

func TestTrack134_PerPeerSessionCapEnforced(t *testing.T) {
	// maxSessions=4 → per-peer share = 2. Two funded escrows for one
	// buyer peer fill the share; a third from the same peer rejects
	// even though global capacity remains.
	f := newGateFixture(t, 4)
	peer := "12D3peer-buyer"
	openShare := func(corr string, nonce byte) (string, error) {
		sid := f.rail.PredictEscrowAddr(corr)
		th := testTaskHash
		th[0] = nonce
		if _, err := f.rail.Open(context.Background(), corr, settlement.Terms{
			Buyer: testBuyer, Seller: testSeller,
			Token: fixtureToken, Amount: big.NewInt(5_000_000), TaskHash: th,
		}); err != nil {
			return "", err
		}
		raw := f.presentation(map[string]string{
			"session_id": sid, "task_hash": "0x" + hex.EncodeToString(th[:]),
		})
		_, err := gateSessionOpen(context.Background(), f.mgr, peer, 7, raw)
		return sid, err
	}
	if _, err := openShare("corr-share-a", 0x10); err != nil {
		t.Fatalf("share open a: %v", err)
	}
	if _, err := openShare("corr-share-b", 0x11); err != nil {
		t.Fatalf("share open b: %v", err)
	}
	_, err := openShare("corr-share-c", 0x12)
	if err == nil || !strings.Contains(err.Error(), "per-peer session cap") {
		t.Fatalf("third same-peer open = %v, want per-peer cap refusal", err)
	}
}
