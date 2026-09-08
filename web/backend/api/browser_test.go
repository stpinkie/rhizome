package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

// TestHandlePutBrowserConfig_PreservesUnsentFields verifies that a PUT body
// carrying only the fields the web console manages does not wipe
// session_timeout, private_host_whitelist, or stored backend secrets.
func TestHandlePutBrowserConfig_PreservesUnsentFields(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Tools.Browser.SessionTimeout = "30m"
	cfg.Tools.Browser.PrivateHostWhitelist = config.FlexibleStringSlice{"10.0.0.0/8"}
	cfg.Tools.Browser.Backends = config.BrowserBackendsConfig{
		"steel": {
			APIKey:  *config.NewSecureString("steel_secret"),
			BaseURL: "https://self.example.com",
		},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// The console sends only enabled/default_backend (plus whatever backend
	// map it last loaded — here we omit backends entirely).
	req := httptest.NewRequest(http.MethodPut, "/api/browser",
		bytes.NewBufferString(`{"enabled":true,"default_backend":"steel"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(updated) error = %v", err)
	}
	b := updated.Tools.Browser
	if !b.Enabled {
		t.Fatal("expected enabled=true")
	}
	if b.DefaultBackend != "steel" {
		t.Fatalf("default_backend = %q, want steel", b.DefaultBackend)
	}
	if b.SessionTimeout != "30m" {
		t.Fatalf("session_timeout = %q, want preserved 30m", b.SessionTimeout)
	}
	if len(b.PrivateHostWhitelist) != 1 || b.PrivateHostWhitelist[0] != "10.0.0.0/8" {
		t.Fatalf("private_host_whitelist = %v, want preserved", b.PrivateHostWhitelist)
	}
	steel, ok := b.Backends["steel"]
	if !ok {
		t.Fatal("steel backend dropped")
	}
	if steel.APIKey.String() != "steel_secret" {
		t.Fatalf("api_key = %q, want preserved secret", steel.APIKey.String())
	}
	if steel.BaseURL != "https://self.example.com" {
		t.Fatalf("base_url = %q, want preserved", steel.BaseURL)
	}
}

// TestHandlePutBrowserConfig_RejectsBadSessionTimeout checks input validation.
func TestHandlePutBrowserConfig_RejectsBadSessionTimeout(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for _, body := range []string{
		`{"session_timeout":"not-a-duration"}`,
		`{"session_timeout":"-5m"}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/browser",
			bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want %d", body, rec.Code, http.StatusBadRequest)
		}
	}
}

// TestHandlePutBrowserConfig_RejectsUnknownBackend checks catalog validation.
func TestHandlePutBrowserConfig_RejectsUnknownBackend(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPut, "/api/browser",
		bytes.NewBufferString(`{"backends":{"not-a-backend":{"api_key":"x"}}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
