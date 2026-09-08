// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package browsertools

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/utils"
)

func stubLookup(t *testing.T, fn func(string) ([]net.IPAddr, error)) {
	t.Helper()
	orig := lookupIPAddr
	lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		return fn(host)
	}
	t.Cleanup(func() { lookupIPAddr = orig })
}

func toolWithWhitelist(t *testing.T, entries []string) *Tool {
	t.Helper()
	var wl *utils.PrivateHostWhitelist
	if len(entries) > 0 {
		var err error
		wl, err = utils.NewPrivateHostWhitelist(entries)
		if err != nil {
			t.Fatalf("whitelist: %v", err)
		}
	}
	return &Tool{name: "browser_open", whitelist: wl}
}

func TestCheckURL_BlocksPrivateLiteral(t *testing.T) {
	tool := toolWithWhitelist(t, nil)
	for _, raw := range []string{
		"http://127.0.0.1:8080/admin",
		"http://10.0.0.5/internal",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/",
		"http://localhost:3000",
		"http://foo.localhost/",
	} {
		if err := tool.checkURL(context.Background(), raw); err == nil {
			t.Errorf("checkURL(%q) = nil, want block", raw)
		}
	}
}

func TestCheckURL_BlocksNonHTTPAndMalformed(t *testing.T) {
	tool := toolWithWhitelist(t, nil)
	for _, raw := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ftp://example.com/x",
		"http://",
		"://missing",
	} {
		if err := tool.checkURL(context.Background(), raw); err == nil {
			t.Errorf("checkURL(%q) = nil, want error", raw)
		}
	}
}

func TestCheckURL_BlocksHostResolvingToPrivateIP(t *testing.T) {
	stubLookup(t, func(host string) ([]net.IPAddr, error) {
		if host == "innocent.example.com" {
			return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	tool := toolWithWhitelist(t, nil)
	if err := tool.checkURL(context.Background(), "https://innocent.example.com/"); err == nil {
		t.Fatal("expected DNS-rebinding block")
	} else if !strings.Contains(err.Error(), "private or restricted") {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := tool.checkURL(context.Background(), "https://example.com/"); err != nil {
		t.Fatalf("public host blocked: %v", err)
	}
}

func TestCheckURL_WhitelistAllowsPrivateResolution(t *testing.T) {
	stubLookup(t, func(string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
	})
	tool := toolWithWhitelist(t, []string{"10.0.0.0/8"})
	if err := tool.checkURL(context.Background(), "https://intranet.corp/"); err != nil {
		t.Fatalf("whitelisted resolution blocked: %v", err)
	}
}

func TestCheckURL_ResolutionFailureBlocked(t *testing.T) {
	stubLookup(t, func(string) ([]net.IPAddr, error) {
		return nil, errors.New("NXDOMAIN")
	})
	tool := toolWithWhitelist(t, nil)
	if err := tool.checkURL(context.Background(), "https://nonexistent.invalid/"); err == nil {
		t.Fatal("expected resolution failure to block")
	}
}

// The optional `url` argument on non-navigation tools (used by stateless REST
// backends) must pass the same SSRF policy.
func TestSnapshotURLArgIsChecked(t *testing.T) {
	tool := &Tool{name: "browser_snapshot", action: "snapshot"}
	res := tool.Execute(context.Background(), map[string]any{
		"url": "http://169.254.169.254/latest/meta-data",
	})
	if res == nil || res.ForLLM == "" {
		t.Fatalf("expected error result, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "private or local network") {
		t.Fatalf("expected SSRF block, got %q", res.ForLLM)
	}
}
