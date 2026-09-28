// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/settlement"
)

// cfgWith builds a *config.Config carrying the given fields/secrets for
// modules.rhizome-market — the shape LoadConfig produces from config.json
// + .security.yml.
func cfgWith(t *testing.T, fields, secrets map[string]string) *config.Config {
	t.Helper()
	mc := config.ModuleConfig{Fields: fields}
	if len(secrets) > 0 {
		mc.Secrets = map[string]config.SecureString{}
		for k, v := range secrets {
			var s config.SecureString
			if err := s.UnmarshalText([]byte(v)); err != nil {
				t.Fatalf("secret %s: %v", k, err)
			}
			mc.Secrets[k] = s
		}
	}
	return &config.Config{
		Modules: config.ModulesConfig{moduleID: mc},
	}
}

func validOffersJSON() string {
	return `[{"id":"research-v1","agent_binding":"main",
		"price_sheet":{"per_task":"0.50","per_1k_prompt_tokens":"0.001",
		"per_1k_completion_tokens":"0.003","min_charge":"0.10",
		"asset":"USDC","chain_id":8453}}]`
}

func TestLoadMarketConfig_Defaults(t *testing.T) {
	mc := loadMarketConfig(cfgWith(t, nil, nil), t.TempDir())
	if len(mc.errs) != 0 {
		t.Fatalf("unexpected errs: %v", mc.errs)
	}
	if mc.serveEnabled {
		t.Fatal("serve_enabled default should be false")
	}
	if mc.runtime != "sandbox" {
		t.Fatalf("runtime = %q", mc.runtime)
	}
	if mc.maxSessions != 4 {
		t.Fatalf("maxSessions = %d", mc.maxSessions)
	}
	if mc.sessionTTL.Minutes() != 30 {
		t.Fatalf("sessionTTL = %v", mc.sessionTTL)
	}
	if !mc.exportRedact || mc.exportAllowAttachments {
		t.Fatal("export defaults wrong")
	}
	if mc.exportRequireReview != "prompt" {
		t.Fatalf("require_review = %q", mc.exportRequireReview)
	}
	if mc.rail != nil {
		t.Fatal("unset escrow_contract should leave rail nil (fixture posture)")
	}
}

func TestLoadMarketConfig_Precedence(t *testing.T) {
	dir := t.TempDir()
	fields := map[string]string{"payout_asset": "DAI", "runtime": "container"}
	secrets := map[string]string{"market_index_url": "https://idx.example.com"}
	mc := loadMarketConfig(cfgWith(t, fields, secrets), dir)
	if mc.payoutAsset != "DAI" || mc.runtime != "container" {
		t.Fatalf("fields not applied: %+v", mc)
	}
	if mc.indexURL != "https://idx.example.com" {
		t.Fatalf("secret not merged: %q", mc.indexURL)
	}
	// Catalog defaults fill unset fields.
	if mc.payoutAsset == "" || mc.sessionTTL <= 0 {
		t.Fatal("defaults not applied")
	}
}

func TestLoadMarketConfig_Offers(t *testing.T) {
	dir := t.TempDir()

	t.Run("offers_json parses", func(t *testing.T) {
		mc := loadMarketConfig(
			cfgWith(t, map[string]string{"offers_json": validOffersJSON()}, nil), dir)
		if len(mc.errs) != 0 {
			t.Fatalf("errs: %v", mc.errs)
		}
		if len(mc.offers) != 1 || mc.offers[0].ID != "research-v1" {
			t.Fatalf("offers = %+v", mc.offers)
		}
	})

	t.Run("offers.json file overrides field", func(t *testing.T) {
		fileOffers := `[{"id":"file-offer","agent_binding":"b",
			"price_sheet":{"asset":"ETH","chain_id":1}}]`
		if err := os.WriteFile(
			filepath.Join(dir, offersFile), []byte(fileOffers), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		mc := loadMarketConfig(
			cfgWith(t, map[string]string{"offers_json": validOffersJSON()}, nil), dir)
		if len(mc.offers) != 1 || mc.offers[0].ID != "file-offer" {
			t.Fatalf("file should win: %+v", mc.offers)
		}
		_ = os.Remove(filepath.Join(dir, offersFile))
	})

	t.Run("malformed offers_json is an error", func(t *testing.T) {
		mc := loadMarketConfig(
			cfgWith(t, map[string]string{"offers_json": "{nope"}, nil), dir)
		if len(mc.errs) == 0 {
			t.Fatal("expected offers parse error")
		}
	})

	t.Run("missing agent_binding flagged", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"offers_json": `[{"id":"x","price_sheet":{"asset":"USDC","chain_id":1}}]`,
		}, nil), dir)
		if len(mc.errs) == 0 {
			t.Fatal("expected agent_binding error")
		}
	})

	t.Run("bad price not a decimal", func(t *testing.T) {
		mc := loadMarketConfig(cfgWith(t, map[string]string{
			"offers_json": `[{"id":"x","agent_binding":"a",
				"price_sheet":{"per_task":"free","asset":"USDC","chain_id":1}}]`,
		}, nil), dir)
		if len(mc.errs) == 0 {
			t.Fatal("expected per_task decimal error")
		}
	})
}

func TestLoadMarketConfig_Validation(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		fields map[string]string
		want   string // substring expected in errs; "" means no errors
	}{
		{"bad runtime", map[string]string{"runtime": "bare"}, "runtime"},
		{"bad payout addr", map[string]string{"payout_address": "notanaddr"}, "payout_address"},
		{"good payout addr", map[string]string{
			"payout_address": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		}, ""},
		{"bad sessions", map[string]string{"max_concurrent_sessions": "0"}, "max_concurrent"},
		{"bad ttl", map[string]string{"session_ttl": "never"}, "session_ttl"},
		{"bad buy cap", map[string]string{"buy_max_cost_per_task": "lots"}, "buy_max_cost_per_task"},
		{"bad review enum", map[string]string{"export_require_review": "sometimes"}, "export_require_review"},
		{"bad index url", map[string]string{"market_index_url": "http://remote.example.com"}, "market_index_url"},
		{"loopback index ok", map[string]string{"market_index_url": "http://127.0.0.1:9"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mc := loadMarketConfig(cfgWith(t, tc.fields, nil), dir)
			if tc.want == "" {
				if len(mc.errs) != 0 {
					t.Fatalf("unexpected errs: %v", mc.errs)
				}
				return
			}
			found := false
			for _, e := range mc.errs {
				if strings.Contains(e, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected err containing %q; got %v", tc.want, mc.errs)
			}
		})
	}
}

func TestLoadMarketConfig_Escrow(t *testing.T) {
	dir := t.TempDir()
	fields := map[string]string{
		"escrow_chain_id":       "11155111",
		"escrow_contract":       "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		"escrow_token":          "0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238",
		"escrow_arbiter":        "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
		"escrow_dispute_window": "86400",
	}
	mc := loadMarketConfig(cfgWith(t, fields, nil), dir)
	if len(mc.errs) != 0 {
		t.Fatalf("errs: %v", mc.errs)
	}
	if mc.rail == nil {
		t.Fatal("configured escrow should produce a rail")
	}
	if mc.rail.ChainID != 11155111 || mc.rail.DisputeWindowSecs != 86400 {
		t.Fatalf("rail = %+v", mc.rail)
	}

	// Partial escrow config (contract set, token missing) is an error.
	bad := loadMarketConfig(cfgWith(t, map[string]string{
		"escrow_contract": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	}, nil), dir)
	if len(bad.errs) == 0 {
		t.Fatal("expected escrow config error")
	}
}

func TestModuleSpecIsRegistered(t *testing.T) {
	spec, ok := moduleSpec()
	if !ok {
		t.Fatal("rhizome-market missing from catalog")
	}
	if spec.Kind != "daemon" {
		t.Fatalf("kind = %q", spec.Kind)
	}
	if len(spec.Protocols) != 1 || spec.Protocols[0] != "/rhizome/acp/1.0.0" {
		t.Fatalf("protocols = %v", spec.Protocols)
	}
	if len(spec.Install.Releases) != 0 {
		t.Fatal("releases must be empty until the first pin")
	}
	if _, ok := spec.Release("latest"); ok {
		t.Fatal("no releases → Release(latest) must report none")
	}
	// All schema fields the module reads must exist in the entry.
	for _, k := range []string{
		"serve_enabled", "offers_json", "runtime", "payout_chain_id",
		"payout_address", "payout_asset", "max_concurrent_sessions",
		"session_ttl", "buy_max_cost_per_task", "buy_max_cost_per_day",
		"export_allow_attachments", "export_redact", "export_require_review",
		"market_index_enabled", "market_index_url",
		settlement.FieldChainID, settlement.FieldContract, settlement.FieldToken,
		settlement.FieldArbiter, settlement.FieldDisputeWindow,
	} {
		if _, ok := spec.Field(k); !ok {
			t.Fatalf("catalog entry missing field %q", k)
		}
	}
	// market_index_url must be a secret; payout_address must not.
	if f, _ := spec.Field("market_index_url"); !f.Secret {
		t.Fatal("market_index_url should be Secret")
	}
	if f, _ := spec.Field("payout_address"); f.Secret {
		t.Fatal("payout_address is advertised — must not be Secret")
	}
}
