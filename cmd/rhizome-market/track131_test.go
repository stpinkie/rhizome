// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 131: TEE/attestation posture — the emit-when-set advert claim,
// the platform probe seam, config validation, and the buy-side
// snapshot that surfaces the claim in `market session`.

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTrack131_TEEClaim(t *testing.T) {
	orig := teeProbe
	t.Cleanup(func() { teeProbe = orig })

	t.Run("probe result emits kind", func(t *testing.T) {
		teeProbe = func() string { return "sev-snp" }
		got := (&marketConfig{}).teeClaim()
		if got == nil || got.Kind != "sev-snp" {
			t.Fatalf("claim = %+v", got)
		}
	})

	t.Run("probe empty → no claim", func(t *testing.T) {
		teeProbe = func() string { return "" }
		if got := (&marketConfig{}).teeClaim(); got != nil {
			t.Fatalf("claim should be nil, got %+v", got)
		}
	})

	t.Run("tee_kind overrides probe", func(t *testing.T) {
		teeProbe = func() string { return "" }
		mc := &marketConfig{teeKind: "sgx"}
		if got := mc.teeClaim(); got == nil || got.Kind != "sgx" {
			t.Fatalf("claim = %+v", got)
		}
	})

	t.Run("tee_kind=none suppresses probe", func(t *testing.T) {
		teeProbe = func() string { return "tdx" }
		if got := (&marketConfig{teeKind: "none"}).teeClaim(); got != nil {
			t.Fatalf("none must suppress, got %+v", got)
		}
	})

	t.Run("evidence pointers ride along", func(t *testing.T) {
		teeProbe = func() string { return "tdx" }
		mc := &marketConfig{
			teeReportURL:    "https://seller.example/tee/report",
			teeEvidenceHash: "0x" + strings.Repeat("ab", 32),
		}
		got := mc.teeClaim()
		if got == nil || got.ReportURL != mc.teeReportURL ||
			got.EvidenceHash != mc.teeEvidenceHash {
			t.Fatalf("claim = %+v", got)
		}
	})
}

func TestTrack131_AdvertCarriesTEE(t *testing.T) {
	orig := teeProbe
	t.Cleanup(func() { teeProbe = orig })
	teeProbe = func() string { return "tdx" }

	aw := newAdvertWriter(t.TempDir(), "v", "12D3peer", newAuditLogger(""), nil, nil)
	mc := &marketConfig{
		serveEnabled: true, runtime: "exec",
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
	if got.Attestation == nil || got.Attestation.Kind != "tdx" {
		t.Fatalf("advert attestation = %+v", got.Attestation)
	}

	// Non-serving drops the claim — posture must not outlive the offer.
	mc.serveEnabled = false
	data, _ = aw.render(mc)
	var got2 advert
	if err := json.Unmarshal(data, &got2); err != nil {
		t.Fatal(err)
	}
	if got2.Attestation != nil {
		t.Fatal("non-serving advert must not carry a TEE claim")
	}

	// No probe, no override → the field stays out entirely (emit-when-set).
	teeProbe = func() string { return "" }
	mc.serveEnabled = true
	data, _ = aw.render(mc)
	if strings.Contains(string(data), `"attestation"`) {
		t.Fatal("emit-when-set: no claim means no key")
	}
}

func TestTrack131_ConfigValidation(t *testing.T) {
	dir := t.TempDir()
	load := func(fields map[string]string) *marketConfig {
		return loadMarketConfig(cfgWith(t, fields, nil), dir)
	}

	mc := load(map[string]string{"tee_kind": "tpdx"})
	if len(mc.errs) == 0 || !strings.Contains(mc.errs[0], "tee_kind") {
		t.Fatalf("bad tee_kind: errs=%v", mc.errs)
	}
	mc = load(map[string]string{"tee_kind": "none"})
	if len(mc.errs) != 0 {
		t.Fatalf("none is valid: errs=%v", mc.errs)
	}
	mc = load(map[string]string{"tee_report_url": "http://insecure/report"})
	if len(mc.errs) == 0 || !strings.Contains(mc.errs[0], "tee_report_url") {
		t.Fatalf("http report url: errs=%v", mc.errs)
	}
	mc = load(map[string]string{"tee_evidence_hash": "zzz"})
	if len(mc.errs) == 0 || !strings.Contains(mc.errs[0], "tee_evidence_hash") {
		t.Fatalf("bad evidence hash: errs=%v", mc.errs)
	}
	mc = load(map[string]string{"tee_evidence_hash": "0x" + strings.Repeat("ab", 32)})
	if len(mc.errs) != 0 {
		t.Fatalf("valid evidence hash: errs=%v", mc.errs)
	}
}

func TestTrack131_PurchaseSnapshotsClaim(t *testing.T) {
	// The buyer parses the seller's advert into the same struct — a
	// claim in the JSON lands on `adv.Attestation`, and buy() copies it
	// to purchase.TEEClaim for `market session` to display.
	raw := `{"v":1,"peer_id":"12D3seller","attestation":{"kind":"sev-snp",` +
		`"evidence_hash":"0x` + strings.Repeat("ab", 32) + `"}}`
	var adv advert
	if err := json.Unmarshal([]byte(raw), &adv); err != nil {
		t.Fatal(err)
	}
	if adv.Attestation == nil || adv.Attestation.Kind != "sev-snp" {
		t.Fatalf("parsed claim = %+v", adv.Attestation)
	}
	p := &purchase{PurchaseID: "pur-tee", State: purchaseCompleted}
	p.TEEClaim = adv.Attestation
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tee_attestation"`) ||
		!strings.Contains(string(data), `"sev-snp"`) {
		t.Fatalf("snapshot must carry the claim: %s", data)
	}
}
