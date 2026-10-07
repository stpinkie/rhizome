// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// TestRun_EndToEnd boots the whole module against a temp RHIZOME_HOME: a
// written config.json, env token, both listeners, advert + audit files —
// then verifies clean shutdown removes the published files. This is the
// module-side counterpart of the Track-99 CLI contract test.
func TestRun_EndToEnd(t *testing.T) {
	tmp := t.TempDir()
	modDir := filepath.Join(tmp, "modules", moduleID)
	cfgPath := filepath.Join(tmp, "config.json")

	// offers_json is a *string* field carrying JSON — quote-embed it.
	offersQuoted, _ := json.Marshal(validOffersJSON())
	cfgJSON := fmt.Sprintf(`{"modules":{"rhizome-market":{"enabled":true,
		"fields":{"serve_enabled":"true",
			"payout_address":"0x8227b9868e00B8eE951F17B480D369b84Cd17c20",
			"payout_chain_id":"8453",
			"offers_json":%s}}}}`, offersQuoted)
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("RHIZOME_MODULE_DIR", modDir)
	t.Setenv("RHIZOME_HOME", "")
	t.Setenv(config.EnvConfig, cfgPath)
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "e2e-tok")
	t.Setenv("RHIZOME_BRIDGE_ADDR", "") // standalone: no daemon bridge

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	// Wait for the API listener to publish api.addr. run() starts the API
	// before the bridge accept listener, so bridge.addr needs its own wait —
	// a bare Stat races the gap between the two writes on slow runners.
	addr := waitForFile(t, filepath.Join(modDir, apiAddrFile), 10*time.Second)
	waitForFile(t, filepath.Join(modDir, bridgeAddrFile), 10*time.Second)

	// API serves with the token.
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer e2e-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || body["serve_enabled"] != true {
		t.Fatalf("health: %d %v", resp.StatusCode, body)
	}
	if body["escrow_posture"] != "fixture" {
		t.Fatalf("escrow: %v", body)
	}

	// Advert claims serving (config is complete).
	advData, err := os.ReadFile(filepath.Join(modDir, advertFile))
	if err != nil {
		t.Fatalf("advert: %v", err)
	}
	var a advert
	if err := json.Unmarshal(advData, &a); err != nil {
		t.Fatal(err)
	}
	if !a.ServeEnabled || len(a.Offers) != 1 {
		t.Fatalf("advert: %+v", a)
	}

	// Audit trail has market.start.
	auditData, err := os.ReadFile(filepath.Join(modDir, auditFile))
	if err != nil || !strings.Contains(string(auditData), "market.start") {
		t.Fatalf("audit missing start: %v %s", err, auditData)
	}

	// Shutdown removes the published files.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop")
	}
	for _, f := range []string{apiAddrFile, bridgeAddrFile, advertFile} {
		if _, err := os.Stat(filepath.Join(modDir, f)); !os.IsNotExist(err) {
			t.Fatalf("%s not removed on shutdown", f)
		}
	}
	// audit persists — it's the durable trail.
	if _, err := os.Stat(filepath.Join(modDir, auditFile)); err != nil {
		t.Fatal("audit file should survive shutdown")
	}
}

// waitForFile polls for a file and returns its trimmed contents.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}
