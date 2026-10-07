package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

// Track 135 — /api/market/sessions proxies the rhizome-market module's
// loopback API the same way `rhizome market` does: api.addr +
// bridge-token under the module dir, bearer auth, passthrough JSON.

func marketModuleDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	return filepath.Join(home, "modules", "rhizome-market")
}

func TestMarketSessions_NotInstalled(t *testing.T) {
	marketModuleDir(t) // env home set; no module dir

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/market/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["installed"] != false {
		t.Fatalf("installed = %v, want false", got["installed"])
	}
	if got["sessions"] == nil || got["sell_sessions"] == nil || got["disputes"] == nil {
		t.Fatalf("empty posture must still carry the arrays: %v", got)
	}
}

func TestMarketSessions_NotUp(t *testing.T) {
	dir := marketModuleDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Module dir exists but no api.addr — installed but not serving.

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/market/sessions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

func TestMarketSessions_Passthrough(t *testing.T) {
	payload := `{"sessions":[{"purchase_id":"p1","state":"session"}],` +
		`"sell_sessions":[{"session_id":"0xabc","state":"active"}],` +
		`"disputes":[{"purchase_id":"p2","state":"disputed"}],` +
		`"spend":{"assets":{"USDC":{"spent_24h":"3.5"}}}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sessions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	dir := marketModuleDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// api.addr stores host:port — strip the scheme.
	addr := upstream.Listener.Addr().String()
	if err := os.WriteFile(filepath.Join(dir, "api.addr"), []byte(addr), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "bridge-token"), []byte("tok-123"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/market/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["installed"] != true {
		t.Fatalf("installed = %v, want true", got["installed"])
	}
	sessions, _ := got["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v", got["sessions"])
	}
	sell, _ := got["sell_sessions"].([]any)
	if len(sell) != 1 {
		t.Fatalf("sell_sessions = %v", got["sell_sessions"])
	}
	disputes, _ := got["disputes"].([]any)
	if len(disputes) != 1 {
		t.Fatalf("disputes = %v", got["disputes"])
	}
}

func TestMarketSessions_NonLoopbackRefused(t *testing.T) {
	dir := marketModuleDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A tampered api.addr pointing off-host must not carry the bearer.
	if err := os.WriteFile(
		filepath.Join(dir, "api.addr"), []byte("203.0.113.9:443"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "bridge-token"), []byte("tok-123"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/market/sessions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}
