// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

func testCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Tools.Web3.AllowPrivateEndpoints = true // loopback httptest servers
	return cfg
}

func TestResolve_ClientSetupError(t *testing.T) {
	// A safe-HTTP-client construction failure must surface through
	// Resolve — a nil client would otherwise fall back to a default
	// http.Client in NewClient, bypassing the safe-dial SSRF guard.
	p := NewProvider(testCfg())
	p.hc = nil
	p.hcErr = errors.New("dialer setup failed")
	_, err := p.Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP client setup") {
		t.Fatalf("expected client setup error, got %v", err)
	}
}

func TestResolve_OverrideEndpoint(t *testing.T) {
	cfg := testCfg()
	cfg.Tools.Web3.Endpoint = "http://127.0.0.1:8545"
	p := NewProvider(cfg)
	ep, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ep.URL != "http://127.0.0.1:8545" || ep.Source != SourceOverride {
		t.Fatalf("endpoint = %+v", ep)
	}
}

func TestResolve_EthereumRPCModule(t *testing.T) {
	cfg := testCfg()
	var key config.SecureString
	if err := key.UnmarshalText([]byte("module-key")); err != nil {
		t.Fatal(err)
	}
	cfg.Modules = config.ModulesConfig{
		"ethereum-rpc": {
			Fields:  map[string]string{"endpoint_url": "https://rpc.example/v3/x"},
			Secrets: map[string]config.SecureString{"api_key": key},
		},
	}
	p := NewProvider(cfg)
	ep, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ep.Source != SourceEthereumRPC || ep.APIKey != "module-key" {
		t.Fatalf("endpoint = %+v", ep)
	}
}

func TestResolve_NimbusProbe(t *testing.T) {
	cfg := testCfg()
	var probed atomic.Int32
	p := NewProvider(cfg)
	p.probe = func(_ context.Context, u string) error {
		probed.Add(1)
		if u != "http://127.0.0.1:8545" {
			t.Fatalf("probed %q, want catalog default listen_url", u)
		}
		return nil
	}
	ep, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ep.Source != SourceNimbus || ep.URL != "http://127.0.0.1:8545" {
		t.Fatalf("endpoint = %+v", ep)
	}
	// Second resolve within the TTL must not re-probe.
	if _, err := p.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve(2): %v", err)
	}
	if probed.Load() != 1 {
		t.Fatalf("probe count = %d, want 1 (cached)", probed.Load())
	}
}

func TestResolve_NimbusFieldOverride(t *testing.T) {
	cfg := testCfg()
	cfg.Modules = config.ModulesConfig{
		"nimbus-verified-proxy": {
			Fields: map[string]string{"listen_url": "http://127.0.0.1:9999"},
		},
	}
	p := NewProvider(cfg)
	var got string
	p.probe = func(_ context.Context, u string) error { got = u; return nil }
	ep, err := p.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "http://127.0.0.1:9999" || ep.URL != got {
		t.Fatalf("endpoint = %+v probed %q", ep, got)
	}
}

func TestResolve_NimbusProbeFails(t *testing.T) {
	cfg := testCfg()
	p := NewProvider(cfg)
	p.probe = func(context.Context, string) error { return context.DeadlineExceeded }
	_, err := p.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected descriptive error when no endpoint is reachable")
	}
	for _, want := range []string{"tools.web3.endpoint", "ethereum-rpc", "nimbus-verified-proxy"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q should mention %q", err, want)
		}
	}
}

func TestResolve_Precedence(t *testing.T) {
	// Override beats a configured ethereum-rpc module; ethereum-rpc beats nimbus.
	cfg := testCfg()
	cfg.Tools.Web3.Endpoint = "http://127.0.0.1:7777"
	cfg.Modules = config.ModulesConfig{
		"ethereum-rpc": {Fields: map[string]string{"endpoint_url": "https://rpc.example"}},
	}
	p := NewProvider(cfg)
	p.probe = func(context.Context, string) error { t.Fatal("nimbus probe should not run"); return nil }
	ep, err := p.Resolve(context.Background())
	if err != nil || ep.Source != SourceOverride {
		t.Fatalf("endpoint = %+v err=%v", ep, err)
	}

	cfg2 := testCfg()
	cfg2.Modules = config.ModulesConfig{
		"ethereum-rpc": {Fields: map[string]string{"endpoint_url": "https://rpc.example"}},
	}
	p2 := NewProvider(cfg2)
	p2.probe = func(context.Context, string) error { t.Fatal("nimbus probe should not run"); return nil }
	ep2, err := p2.Resolve(context.Background())
	if err != nil || ep2.Source != SourceEthereumRPC {
		t.Fatalf("endpoint = %+v err=%v", ep2, err)
	}
}

func TestResolve_SchemeValidation(t *testing.T) {
	for _, bad := range []string{
		"file:///etc/passwd", "gopher://x", "ftp://x", "http://", "://nohost",
	} {
		cfg := testCfg()
		cfg.Tools.Web3.Endpoint = bad
		_, err := NewProvider(cfg).Resolve(context.Background())
		if err == nil {
			t.Fatalf("endpoint %q should be rejected", bad)
		}
	}
}

func TestResolve_PrivateBlockedWhenDisallowed(t *testing.T) {
	cfg := testCfg()
	cfg.Tools.Web3.AllowPrivateEndpoints = false
	cfg.Tools.Web3.Endpoint = "http://127.0.0.1:8545"
	_, err := NewProvider(cfg).Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected private-host rejection, got %v", err)
	}
}

func TestResolve_PublicHostnameResolutionGuard(t *testing.T) {
	cfg := testCfg()
	cfg.Tools.Web3.AllowPrivateEndpoints = false
	cfg.Tools.Web3.Endpoint = "https://public-looking.example.com"
	p := NewProvider(cfg)
	orig := lookupIPAddr
	defer func() { lookupIPAddr = orig }()

	// A public-looking name resolving to a private address must be rejected
	// (DNS-rebinding defense at resolve time).
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil
	}
	_, err := p.Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected DNS-rebinding rejection, got %v", err)
	}

	// A public answer passes the resolve-time check.
	lookupIPAddr = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	if _, err := p.Resolve(context.Background()); err != nil {
		t.Fatalf("public IP should resolve: %v", err)
	}
}

func TestResolve_ChainIDEnforcement(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		return "0x1", nil // mainnet
	})
	defer srv.Close()

	cfg := testCfg()
	cfg.Tools.Web3.Endpoint = srv.URL
	cfg.Tools.Web3.ChainIDs = []uint64{1}
	if _, err := NewProvider(cfg).Resolve(context.Background()); err != nil {
		t.Fatalf("chain 1 allowed: %v", err)
	}

	cfg2 := testCfg()
	cfg2.Tools.Web3.Endpoint = srv.URL
	cfg2.Tools.Web3.ChainIDs = []uint64{11155111}
	_, err := NewProvider(cfg2).Resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "chain 1") {
		t.Fatalf("expected chain mismatch error, got %v", err)
	}

	// Empty list = any chain.
	cfg3 := testCfg()
	cfg3.Tools.Web3.Endpoint = srv.URL
	if _, err := NewProvider(cfg3).Resolve(context.Background()); err != nil {
		t.Fatalf("empty chain_ids: %v", err)
	}
}

func TestResolve_FailureNotCached(t *testing.T) {
	cfg := testCfg()
	var calls atomic.Int32
	p := NewProvider(cfg)
	p.probe = func(context.Context, string) error {
		calls.Add(1)
		return context.DeadlineExceeded
	}
	for i := 0; i < 2; i++ {
		if _, err := p.Resolve(context.Background()); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("probe calls = %d, want 2 (failures not cached)", calls.Load())
	}
}

func TestStaticProvider(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		return "0x5", nil
	})
	defer srv.Close()
	p := NewStaticProvider(srv.URL, "")
	raw, err := p.Call(context.Background(), "eth_chainId", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s != "0x5" {
		t.Fatalf("result = %s", raw)
	}
	if p.Source() != string(SourceOverride) {
		t.Fatalf("Source = %q", p.Source())
	}
}
