// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startTestAPI boots the real server on a temp module dir with a known
// token and returns a client handle the Track-99 CLI shape exercises.
func startTestAPI(t *testing.T, token string) (*apiServer, string) {
	t.Helper()
	dir := t.TempDir()
	tp := newTokenProvider(dir)
	if token != "" {
		t.Setenv("RHIZOME_BRIDGE_TOKEN", token)
		tp = newTokenProvider(dir)
	}
	s, err := startAPI(dir, tp, newSessionMgr(dir, newAuditLogger("")),
		newPurchaseMgr(dir, dir, newAuditLogger("")), openAttestationStore(dir), newAuditLogger(""), "test")
	if err != nil {
		t.Fatalf("startAPI: %v", err)
	}
	t.Cleanup(s.Close)
	// api.addr must exist, be loopback, mode 0600.
	data, err := os.ReadFile(filepath.Join(dir, apiAddrFile))
	if err != nil {
		t.Fatalf("api.addr: %v", err)
	}
	addr := strings.TrimSpace(string(data))
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("api.addr malformed: %v", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("api.addr not loopback: %q", addr)
	}
	return s, addr
}

func apiPost(t *testing.T, addr, token, verb, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		"http://"+addr+"/v1/"+verb, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func apiGet(t *testing.T, addr, token, path string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func TestAPI_AddrPublishedAndLoopback(t *testing.T) {
	_, addr := startTestAPI(t, "tok")
	if addr == "" {
		t.Fatal("no addr")
	}
}

func TestAPI_Auth(t *testing.T) {
	_, addr := startTestAPI(t, "sekrit")

	if code, _ := apiGet(t, addr, "", "/v1/health"); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := apiGet(t, addr, "wrong", "/v1/health"); code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", code)
	}
	code, body := apiGet(t, addr, "sekrit", "/v1/health")
	if code != http.StatusOK {
		t.Fatalf("good token: %d", code)
	}
	if body["status"] != "ok" {
		t.Fatalf("health body: %v", body)
	}
}

func TestAPI_TokenMissingGives503(t *testing.T) {
	_, addr := startTestAPI(t, "")
	if code, body := apiGet(t, addr, "x", "/v1/health"); code != http.StatusServiceUnavailable {
		t.Fatalf("no-token posture: %d", code)
	} else if e, _ := body["error"].(map[string]any); e["code"] != "token_missing" {
		t.Fatalf("error shape: %v", body)
	}
}

func TestAPI_VerbsNotImplemented(t *testing.T) {
	_, addr := startTestAPI(t, "tok")
	// All verbs are live since Track 103 — the module config is unset
	// here, so find answers not_ready rather than faking results.
	code, body := apiPost(t, addr, "tok", "find", `{"query":"x"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("find unconfigured: %d %v", code, body)
	}
	if e, _ := body["error"].(map[string]any); e["code"] != "not_ready" {
		t.Fatalf("find error shape: %v", body)
	}
	// buy validates before touching rails — missing provider is a 400.
	code, body = apiPost(t, addr, "tok", "buy", `{"x":1}`)
	if code != http.StatusBadRequest {
		t.Fatalf("buy bad request: %d %v", code, body)
	}
	// session/receipt answer honestly — unknown ids 404, never fake.
	for _, verb := range []string{"session", "receipt"} {
		code, body := apiPost(t, addr, "tok", verb,
			`{"session_id":"0x00000000000000000000000000000000000000aa"}`)
		if code != http.StatusNotFound {
			t.Fatalf("%s unknown session: %d %v", verb, code, body)
		}
	}
	// Malformed body → 400, not a panic.
	if code, _ := apiPost(t, addr, "tok", "buy", "{bad json"); code != http.StatusBadRequest {
		t.Fatalf("malformed: %d", code)
	}
	// Unknown route → JSON 404.
	code, body = apiGet(t, addr, "tok", "/v1/nope")
	if code != http.StatusNotFound {
		t.Fatalf("unknown: %d %v", code, body)
	}
}

func TestAPI_HealthReflectsConfig(t *testing.T) {
	s, addr := startTestAPI(t, "tok")
	mc := loadMarketConfig(cfgWith(t, map[string]string{
		"serve_enabled":  "true",
		"offers_json":    validOffersJSON(),
		"payout_address": "0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
	}, nil), t.TempDir())
	s.setConfig(mc)
	_, body := apiGet(t, addr, "tok", "/v1/health")
	if body["serve_enabled"] != true {
		t.Fatalf("serve_enabled: %v", body)
	}
	if body["offers"].(float64) != 1 {
		t.Fatalf("offers: %v", body)
	}
}

func TestAPI_CloseRemovesAddrFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "tok")
	s, err := startAPI(dir, newTokenProvider(dir),
		newSessionMgr(dir, newAuditLogger("")),
		newPurchaseMgr(dir, dir, newAuditLogger("")), openAttestationStore(dir), newAuditLogger(""), "test")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, apiAddrFile)); !os.IsNotExist(err) {
		t.Fatal("api.addr should be removed on close")
	}
}

func TestAPI_ConflictOnSecondBind(t *testing.T) {
	// Two modules can't share a module dir's api.addr — second startAPI
	// writes the same file; this test pins the last-writer-wins file
	// behavior (supervisor prevents the real double-run).
	dir := t.TempDir()
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "tok")
	s1, err := startAPI(dir, newTokenProvider(dir),
		newSessionMgr(dir, newAuditLogger("")),
		newPurchaseMgr(dir, dir, newAuditLogger("")), openAttestationStore(dir), newAuditLogger(""), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := startAPI(dir, newTokenProvider(dir),
		newSessionMgr(dir, newAuditLogger("")),
		newPurchaseMgr(dir, dir, newAuditLogger("")), openAttestationStore(dir), newAuditLogger(""), "b")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// api.addr now points at s2 — verify by hitting it.
	data, _ := os.ReadFile(filepath.Join(dir, apiAddrFile))
	addr := strings.TrimSpace(string(data))
	code, _ := apiGet(t, addr, "tok", "/v1/health")
	if code != http.StatusOK {
		t.Fatalf("post-overwrite health: %d", code)
	}
}

// Compile-time pin: the API surface matches the Track-99 client's contract
// — POST /v1/<verb> with a bearer. Drift between the two is a bug.
func TestAPI_ClientContractParity(t *testing.T) {
	_, addr := startTestAPI(t, "parity-tok")
	// The market client sends Content-Type: application/json + Bearer.
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://%s/v1/find", addr), strings.NewReader(`{"query":"x"}`))
	req.Header.Set("Authorization", "Bearer parity-tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Track 103 made /v1/find live — an unset module config answers
	// not_ready (400); the parity pin is that the route exists and
	// returns the structured error shape, not a dead 501/404.
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400 not_ready, got %d", resp.StatusCode)
	}
}
