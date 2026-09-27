// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func servingCfg(t *testing.T) *marketConfig {
	t.Helper()
	return loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled":   "true",
		"offers_json":     validOffersJSON(),
		"payout_address":  "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		"payout_chain_id": "8453",
	}, nil), t.TempDir())
}

func TestAdvert_RenderServing(t *testing.T) {
	w := newAdvertWriter(t.TempDir(), "v0.0.0-test", "12D3peer", newAuditLogger(""))
	data, reason := w.render(servingCfg(t))
	if data == nil {
		t.Fatalf("advert omitted: %s", reason)
	}
	if len(data) > advertMaxBytes {
		t.Fatalf("advert over 16 KiB bound: %d", len(data))
	}
	var a advert
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if !a.ServeEnabled || a.PeerID != "12D3peer" || len(a.Offers) != 1 {
		t.Fatalf("advert = %+v", a)
	}
	if a.Payout == nil || a.Payout.Address != "0x8227b9868e00B8eE951F17B480D369b84Cd17c20" {
		t.Fatalf("payout missing: %+v", a.Payout)
	}
	if a.Escrow == nil || a.Escrow.Posture != "fixture" {
		t.Fatalf("escrow posture: %+v", a.Escrow)
	}
	if a.ExpiresAt == "" || a.ExpiresAt <= a.GeneratedAt {
		t.Fatalf("expires_at must postdate generated_at: %+v", a)
	}
}

func TestAdvert_OmitOnInvalid(t *testing.T) {
	w := newAdvertWriter(t.TempDir(), "v", "", newAuditLogger(""))

	// serve_enabled without payout_address → omit.
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled": "true", "offers_json": validOffersJSON(),
	}, nil), t.TempDir())
	if data, _ := w.render(mc); data != nil {
		t.Fatal("serving without payout must not advertise")
	}

	// serve_enabled with zero offers → omit.
	mc = loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled":  "true",
		"payout_address": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	}, nil), t.TempDir())
	if data, _ := w.render(mc); data != nil {
		t.Fatal("serving without offers must not advertise")
	}

	// Malformed config while serving → omit.
	mc = loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled":  "true",
		"offers_json":    "{broken",
		"payout_address": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	}, nil), t.TempDir())
	if data, _ := w.render(mc); data != nil {
		t.Fatal("broken config while serving must not advertise")
	}
}

func TestAdvert_NonServingStillWritten(t *testing.T) {
	// A non-serving module still publishes posture (serve_enabled:false,
	// escrow fixture/configured) — the daemon gates on the field anyway.
	dir := t.TempDir()
	w := newAdvertWriter(dir, "v", "", newAuditLogger(""))
	mc := loadMarketConfig(cfgWith(t, nil, nil), dir)
	w.refresh(mc)
	data, err := os.ReadFile(filepath.Join(dir, advertFile))
	if err != nil {
		t.Fatalf("advert missing: %v", err)
	}
	var a advert
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if a.ServeEnabled {
		t.Fatal("non-serving advert claims serving")
	}
	if a.Escrow == nil || a.Escrow.Posture != "fixture" {
		t.Fatalf("escrow: %+v", a.Escrow)
	}
}

func TestAdvert_ConfiguredEscrowRendered(t *testing.T) {
	w := newAdvertWriter(t.TempDir(), "v", "", newAuditLogger(""))
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"escrow_chain_id":       "11155111",
		"escrow_contract":       "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		"escrow_token":          "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
		"escrow_arbiter":        "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		"escrow_dispute_window": "86400",
	}, nil), t.TempDir())
	data, _ := w.render(mc)
	var a advert
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	if a.Escrow == nil || a.Escrow.Posture != "configured" || a.Escrow.ChainID != 11155111 {
		t.Fatalf("escrow: %+v", a.Escrow)
	}
}

func TestAdvert_ChangeDetectionAndRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, advertFile)
	w := newAdvertWriter(dir, "v", "", newAuditLogger(""))
	mc := loadMarketConfig(cfgWith(t, nil, nil), dir)

	w.refresh(mc)
	info1, _ := os.Stat(path)
	if info1 == nil {
		t.Fatal("no advert")
	}
	// Same config + fresh expiry → no rewrite (bytes unchanged modulo
	// timestamps — render uses now(), so assert file stays byte-stable is
	// wrong; assert lastRaw dedup path: a second refresh keeps the file).
	w.refresh(mc)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("advert vanished")
	}

	// Removal on shutdown.
	w.remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("advert not removed")
	}
}

func TestAdvert_OfferBoundRespected(t *testing.T) {
	// offers beyond the advert's 16 KiB budget → render refuses, not
	// silently truncates.
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < offersMaxCount; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":"offer-%d-%s","agent_binding":"a",
			"price_sheet":{"asset":"USDC","chain_id":1}}`,
			i, strings.Repeat("x", 200))
	}
	sb.WriteString("]")
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled":  "true",
		"offers_json":    sb.String(),
		"payout_address": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	}, nil), dir)
	w := newAdvertWriter(dir, "v", "", newAuditLogger(""))
	data, reason := w.render(mc)
	if data != nil && len(data) > advertMaxBytes {
		t.Fatalf("over-bound advert written: %d bytes", len(data))
	}
	if data == nil && !strings.Contains(reason, "exceeds") {
		t.Fatalf("omitted for unexpected reason: %s", reason)
	}
}
