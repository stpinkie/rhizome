// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package config

import (
	"strings"
	"testing"
)

func TestWeb3_IsToolEnabled_DefaultOff(t *testing.T) {
	cfg := DefaultConfig()
	for _, name := range []string{
		"web3", "web3_chain", "web3_balance", "web3_call",
		"web3_block", "web3_transaction", "web3_logs", "web3_rpc",
	} {
		if cfg.Tools.IsToolEnabled(name) {
			t.Fatalf("IsToolEnabled(%q) = true on DefaultConfig, want false", name)
		}
	}
}

func TestWeb3_IsToolEnabled_Enabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tools.Web3.Enabled = true
	for _, name := range []string{"web3", "web3_chain", "web3_rpc"} {
		if !cfg.Tools.IsToolEnabled(name) {
			t.Fatalf("IsToolEnabled(%q) = false with Web3.Enabled", name)
		}
	}
}

func TestWeb3_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tools.Web3.GetMaxLogRange() != 10000 {
		t.Fatalf("MaxLogRange = %d", cfg.Tools.Web3.MaxLogRange)
	}
	if cfg.Tools.Web3.GetTimeout().Seconds() != 30 {
		t.Fatalf("Timeout = %v", cfg.Tools.Web3.GetTimeout())
	}
	if cfg.Tools.Web3.AllowPrivateEndpoints {
		t.Fatal("AllowPrivateEndpoints should default false")
	}
	if !cfg.Tools.Web3.IsZero() {
		t.Fatal("Web3.IsZero should be true with no api_key")
	}
	// Zero-value config still gets documented defaults from getters.
	var bare Web3ToolsConfig
	if bare.GetMaxLogRange() != 10000 || bare.GetTimeout().Seconds() != 30 {
		t.Fatal("getters should apply defaults on zero value")
	}
}

func TestWeb3_APIKeyJoinsSensitiveReplacer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tools.FilterSensitiveData = true
	var key SecureString
	if err := key.UnmarshalText([]byte("web3-secret-token-9")); err != nil {
		t.Fatal(err)
	}
	cfg.Tools.Web3.APIKey = key
	if cfg.Tools.Web3.IsZero() {
		t.Fatal("IsZero should be false once api_key is set")
	}
	out := cfg.FilterSensitiveData("calling web3-secret-token-9 endpoint")
	if strings.Contains(out, "web3-secret-token-9") {
		t.Fatalf("api_key leaked through FilterSensitiveData: %q", out)
	}
}
