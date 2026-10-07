package api

// Market dashboard depth (v0.16.0 Track 135): the web Network page's
// market panel reads the rhizome-market module's loopback API the same
// way `rhizome market` does — api.addr + bridge-token under the module
// dir. The module is the only writer of purchase/session state; when
// it's absent the endpoint reports {installed:false} rather than
// synthesizing empty state from nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	marketModuleID     = "rhizome-market"
	marketAPITimeout   = 10 * time.Second
	marketFileMaxBytes = 4 << 10
	marketBodyMaxBytes = 1 << 20
)

// registerMarketRoutes binds the market dashboard endpoints.
func (h *Handler) registerMarketRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/market/sessions", h.handleMarketSessions)
}

// marketAPIClient is a resolved handle on the module's loopback API —
// the same api.addr + bridge-token pair `rhizome market` uses, so the
// posture (loopback-only, bearer) is identical.
type marketAPIClient struct {
	addr  string
	token string
}

// resolveMarketAPI locates the module dir and reads api.addr +
// bridge-token. Returns (nil, err) — the handler maps the kinds:
// module dir missing → installed:false posture; addr/token missing →
// module installed but API not up.
func (h *Handler) resolveMarketAPI() (*marketAPIClient, bool, error) {
	mgr, err := h.moduleManager()
	if err != nil {
		return nil, false, err
	}
	dir := mgr.Dir(marketModuleID)
	info, err := os.Stat(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	if info == nil || !info.IsDir() {
		return nil, false, nil // not installed — posture, not error
	}
	addr, err := readMarketFile(filepath.Join(dir, "api.addr"))
	if err != nil || addr == "" {
		return nil, true, fmt.Errorf(
			"rhizome-market module installed but its API isn't up")
	}
	if err := requireMarketLoopback(addr); err != nil {
		return nil, true, err
	}
	token, err := readMarketFile(filepath.Join(dir, "bridge-token"))
	if err != nil || token == "" {
		return nil, true, fmt.Errorf("rhizome-market bridge token missing")
	}
	return &marketAPIClient{addr: addr, token: token}, true, nil
}

// requireMarketLoopback mirrors cmd/rhizome/internal/market — the
// api.addr file is localhost-only; a tampered value must not leak the
// bearer off-host.
func requireMarketLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("api.addr %q is malformed: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("api.addr %q is not a loopback address — refusing", addr)
}

// call POSTs the JSON body to /v1/<verb> and returns the response
// verbatim — the module owns the schema.
func (c *marketAPIClient) call(
	ctx context.Context, verb string, body string,
) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		"http://"+c.addr+"/v1/"+verb, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: marketAPITimeout}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, marketBodyMaxBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return data, resp.StatusCode, nil
}

// handleMarketSessions serves GET /api/market/sessions — one call that
// carries buy sessions, live sell sessions, dispute rows, and spend
// reporting for the market panel.
func (h *Handler) handleMarketSessions(w http.ResponseWriter, r *http.Request) {
	client, installed, err := h.resolveMarketAPI()
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if !installed {
		writeModuleJSON(w, http.StatusOK, map[string]any{
			"installed":     false,
			"sessions":      []any{},
			"sell_sessions": []any{},
			"disputes":      []any{},
		})
		return
	}
	out, code, err := client.call(
		r.Context(), "sessions", `{"all":true}`)
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable,
			"market module API unreachable: "+err.Error())
		return
	}
	if code != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(out)
		return
	}
	// Pass the module's payload through verbatim with installed:true —
	// the schema is the module's (sessions/sell_sessions/disputes/spend).
	var body map[string]json.RawMessage
	if err := json.Unmarshal(out, &body); err != nil {
		respondNetworkError(w, http.StatusBadGateway,
			"malformed sessions response: "+err.Error())
		return
	}
	body["installed"] = json.RawMessage("true")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func readMarketFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is under the module dir.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, marketFileMaxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > marketFileMaxBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), marketFileMaxBytes)
	}
	return strings.TrimSpace(string(data)), nil
}
