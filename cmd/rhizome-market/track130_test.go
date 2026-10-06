// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 130: portable signed reputation — buyer-signed completion
// attestations, the sig → peer → terms verify chain, the bounded
// seller store, and the advert rendering path.

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// attestFixture is a purchaseMgr with a buyer identity and a completed
// purchase — the attestable shape.
func attestFixture(t *testing.T, state string) (*purchaseMgr, *purchase) {
	t.Helper()
	pm := newPurchaseMgr(t.TempDir(), t.TempDir(), newAuditLogger(""))
	pm.ident.Store(testutil.NewIdentity(t))
	p := &purchase{
		PurchaseID:   "pur-a1",
		SessionID:    "0x" + strings.Repeat("ab", 32),
		SellerPeerID: testutil.NewIdentity(t).PeerID,
		State:        state,
		TaskHash:     "0x" + strings.Repeat("42", 32),
		Terms:        purchaseTerms{Amount: "500", Token: testToken},
	}
	pm.mu.Lock()
	pm.byID[p.PurchaseID] = p
	pm.mu.Unlock()
	return pm, p
}

func TestTrack130_MintAndVerify(t *testing.T) {
	pm, p := attestFixture(t, purchaseCompleted)

	a, err := pm.attest(p.PurchaseID)
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if a.V != 1 || a.ProviderPeerID != p.SellerPeerID ||
		a.SessionID != p.SessionID || a.Outcome != purchaseCompleted {
		t.Fatalf("attestation fields: %+v", a)
	}
	if a.BuyerPeerID == "" || a.Signature == "" {
		t.Fatal("attestation must be buyer-signed")
	}
	if !strings.HasPrefix(a.TermsHash, "0x") || len(a.TermsHash) != 66 {
		t.Fatalf("terms_hash = %q", a.TermsHash)
	}
	// Recorded on the purchase for the trail.
	if p.Attestation == nil {
		t.Fatal("issued attestation must persist on the purchase")
	}
	// Verifies signature → peer chain.
	ok, err := VerifyAttestation(a)
	if err != nil || !ok {
		t.Fatalf("VerifyAttestation = %v, %v", ok, err)
	}
	// Tampering breaks it.
	tampered := *a
	tampered.Outcome = "resolved_for_peer"
	if ok, _ := VerifyAttestation(&tampered); ok {
		t.Fatal("tampered outcome must fail verification")
	}
	tampered = *a
	tampered.Signature = strings.Repeat("00", 64)
	if ok, _ := VerifyAttestation(&tampered); ok {
		t.Fatal("bad signature must fail verification")
	}
}

func TestTrack130_AttestGuards(t *testing.T) {
	t.Run("non-terminal purchase refuses", func(t *testing.T) {
		pm, p := attestFixture(t, purchaseSession)
		if _, err := pm.attest(p.PurchaseID); err == nil {
			t.Fatal("attesting a live session must refuse")
		}
	})

	t.Run("no identity refuses unsigned mints", func(t *testing.T) {
		pm, p := attestFixture(t, purchaseCompleted)
		pm.ident.Store(nil)
		if _, err := pm.attest(p.PurchaseID); err == nil {
			t.Fatal("attestation without a buyer key must refuse")
		}
	})

	t.Run("missing seller peer refuses", func(t *testing.T) {
		pm, p := attestFixture(t, purchaseCompleted)
		p.SellerPeerID = ""
		if _, err := pm.attest(p.PurchaseID); err == nil {
			t.Fatal("unbound provider must refuse")
		}
	})

	t.Run("unknown purchase", func(t *testing.T) {
		pm, _ := attestFixture(t, purchaseCompleted)
		if _, err := pm.attest("nope"); err == nil {
			t.Fatal("unknown id must fail")
		}
	})
}

func TestTrack130_VerifyTermsChain(t *testing.T) {
	pm, p := attestFixture(t, purchaseCompleted)
	a, err := pm.attest(p.PurchaseID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)

	// Local holder of the purchase: terms cross-check runs.
	chk, err := pm.verifyAttestation(raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !chk.Verified || !chk.SignatureOK {
		t.Fatalf("check = %+v", chk)
	}
	if chk.TermsMatch == nil || !*chk.TermsMatch {
		t.Fatalf("terms_match = %v — want true against the local record", chk.TermsMatch)
	}

	// A foreign verifier (no purchase record) gets sig-only + nil terms.
	pm2 := newPurchaseMgr(t.TempDir(), t.TempDir(), newAuditLogger(""))
	chk2, err := pm2.verifyAttestation(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !chk2.Verified || chk2.TermsMatch != nil {
		t.Fatalf("foreign check = %+v — want verified with terms_match unset", chk2)
	}

	// Forged terms fail the local cross-check.
	forged := *a
	forged.TermsHash = "0x" + strings.Repeat("ff", 32)
	// Re-sign would be needed to pass sig — instead flip both sig check
	// outcome and terms: tampered terms_hash fails sig first.
	forgedRaw, _ := json.Marshal(&forged)
	chk3, err := pm.verifyAttestation(forgedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if chk3.Verified {
		t.Fatalf("forged terms must fail: %+v", chk3)
	}
}

func TestTrack130_Store(t *testing.T) {
	pm, p := attestFixture(t, purchaseCompleted)
	a, err := pm.attest(p.PurchaseID)
	if err != nil {
		t.Fatal(err)
	}
	st := openAttestationStore(t.TempDir())

	// Valid attestation registers.
	if err := st.register(*a); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Bad signature refuses.
	bad := *a
	bad.Signature = strings.Repeat("00", 64)
	if err := st.register(bad); err == nil {
		t.Fatal("unverifiable attestation must refuse registration")
	}
	// Re-registration dedups on (session, buyer).
	a2, err := pm.attest(p.PurchaseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.register(*a2); err != nil {
		t.Fatal(err)
	}
	if got := st.latest(attestationMaxLive); len(got) != 1 {
		t.Fatalf("dedup: %d entries, want 1", len(got))
	}
	// Persisted + reloaded.
	st2 := openAttestationStore(st.path[:len(st.path)-len(attestationFile)])
	if got := st2.latest(1); len(got) != 1 || got[0].SessionID != a.SessionID {
		t.Fatalf("reload: %+v", got)
	}
	// Bound: register 40 distinct sessions → newest-32 stored cap holds
	// (bound is 64; the advert carries newest-32).
	for i := 0; i < 40; i++ {
		pi := &purchase{
			PurchaseID:   "pur-bound-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			SessionID:    "0x" + strings.Repeat(string(rune('0'+i%10)), 64),
			SellerPeerID: p.SellerPeerID,
			State:        purchaseCompleted,
			TaskHash:     p.TaskHash,
			Terms:        p.Terms,
		}
		pm.mu.Lock()
		pm.byID[pi.PurchaseID] = pi
		pm.mu.Unlock()
		ai, err := pm.attest(pi.PurchaseID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.register(*ai); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.latest(attestationBoundLen + 8); len(got) > attestationBoundLen {
		t.Fatalf("store bound exceeded: %d", len(got))
	}
}

func TestTrack130_AdvertCarries(t *testing.T) {
	pm, p := attestFixture(t, purchaseCompleted)
	st := openAttestationStore(t.TempDir())
	a, err := pm.attest(p.PurchaseID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.register(*a); err != nil {
		t.Fatal(err)
	}
	aw := newAdvertWriter(t.TempDir(), "v0.16.0-test", p.SellerPeerID,
		newAuditLogger(""), nil, st)
	mc := &marketConfig{
		serveEnabled: true, runtime: "sandbox",
		payoutAddress: testSeller, payoutChainID: 11155111,
		maxSessions: 4, sessionTTL: time.Minute,
		offers: []offer{{
			ID: "o1", AgentBinding: "a1",
			PriceSheet: priceSheet{PerTask: "5", Asset: "USDC", ChainID: 11155111},
		}},
	}
	data, reason := aw.render(mc)
	if data == nil {
		t.Fatalf("render refused: %s", reason)
	}
	var got advert
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Attestations) != 1 ||
		got.Attestations[0].SessionID != a.SessionID {
		t.Fatalf("advert attestations = %+v", got.Attestations)
	}
	// serveEnabled=off drops the field entirely.
	mc.serveEnabled = false
	data, _ = aw.render(mc)
	var got2 advert
	if err := json.Unmarshal(data, &got2); err != nil {
		t.Fatal(err)
	}
	if got2.Attestations != nil {
		t.Fatal("non-serving advert must not carry attestations")
	}
}
