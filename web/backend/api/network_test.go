package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	ppid "github.com/stpinkie/rhizome/pkg/pid"
)

func setupNetworkTest(t *testing.T) {
	t.Helper()
	originalFind := findRhizomeBinaryForNetwork
	originalRun := runNetworkStatus
	t.Cleanup(func() {
		findRhizomeBinaryForNetwork = originalFind
		runNetworkStatus = originalRun
	})
	findRhizomeBinaryForNetwork = func() string { return "rhizome" }
}

func TestNetworkPeers(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return []byte(`{"peer_id":"12D3","peers":[]}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["peer_id"] != "12D3" {
		t.Fatalf("peer_id = %v, want 12D3", got["peer_id"])
	}
	if _, ok := got["peers"]; !ok {
		t.Fatalf("expected peers in response")
	}
}

func TestNetworkDHT(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return []byte(`{"peer_id":"12D3","dht":{"routing_table":0}}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/dht", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["peer_id"] != "12D3" {
		t.Fatalf("peer_id = %v, want 12D3", got["peer_id"])
	}
	dht := got["dht"].(map[string]any)
	if dht["routing_table"] != float64(0) {
		t.Fatalf("routing_table = %v, want 0", dht["routing_table"])
	}
}

func TestNetworkPeersInvalidTimeout(t *testing.T) {
	setupNetworkTest(t)

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers?timeout=not-a-duration", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestNetworkPeersTimeout(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return nil, nil, errors.New("deadline exceeded")
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil).WithContext(ctx)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusGatewayTimeout, rec.Body.String())
	}
}

func TestNetworkPeersCLIError(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return nil, []byte("no identity found"), errors.New("exit status 1")
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}

	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "no identity found") {
		t.Fatalf("error body does not contain stderr: %s", string(body))
	}
}

func TestNetworkPeersInvalidJSON(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return []byte("not json"), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

func TestNetworkPeersCache(t *testing.T) {
	setupNetworkTest(t)

	calls := 0
	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		calls++
		return []byte(`{"peer_id":"cached","peers":[]}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/network/peers", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want %d", i, rec.Code, http.StatusOK)
		}
		time.Sleep(time.Millisecond)
	}

	if calls != 1 {
		t.Fatalf("runNetworkStatus called %d times, want 1", calls)
	}
}

func TestNetworkStatusEndpoint(t *testing.T) {
	setupNetworkTest(t)

	runNetworkStatus = func(_ context.Context, _ string, _ []string, _ []string) ([]byte, []byte, error) {
		return []byte(`{"peer_id":"12D3","peers":[],"dht":{}}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["peer_id"] != "12D3" {
		t.Fatalf("peer_id = %v, want 12D3", got["peer_id"])
	}
}

func TestNetworkStatusInvalidBootstrap(t *testing.T) {
	setupNetworkTest(t)

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/status?bootstrap=", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestNetworkStatusTrustAppendsCLIArg(t *testing.T) {
	setupNetworkTest(t)

	var gotArgs []string
	runNetworkStatus = func(_ context.Context, _ string, args []string, _ []string) ([]byte, []byte, error) {
		gotArgs = args
		return []byte(`{"peer_id":"12D3","peers":[]}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/status?bootstrap=/ip4/127.0.0.1/tcp/4001/p2p/12D3&trust=true", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	hasTrust := false
	for _, a := range gotArgs {
		if a == "--trust" {
			hasTrust = true
			break
		}
	}
	if !hasTrust {
		t.Fatalf("runNetworkStatus args did not include --trust: %v", gotArgs)
	}
}

func TestNetworkStatusListenForcesCLI(t *testing.T) {
	setupNetworkTest(t)

	var gotArgs []string
	runNetworkStatus = func(_ context.Context, _ string, args []string, _ []string) ([]byte, []byte, error) {
		gotArgs = args
		return []byte(`{"peer_id":"12D3","peers":[]}`), nil, nil
	}

	h := NewHandler("")
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/status?listen=/ip4/127.0.0.1/tcp/0", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	hasListen := false
	for i, a := range gotArgs {
		if a == "--listen" && i+1 < len(gotArgs) && gotArgs[i+1] == "/ip4/127.0.0.1/tcp/0" {
			hasListen = true
			break
		}
	}
	if !hasListen {
		t.Fatalf("runNetworkStatus args did not include --listen: %v", gotArgs)
	}
}

func TestNetworkSavedPeers(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network/saved-peers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got savedPeersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(got.SavedPeers) != 1 {
		t.Fatalf("saved peers count = %d, want 1", len(got.SavedPeers))
	}
	if got.SavedPeers[0].PeerID != peerID {
		t.Fatalf("peer id = %q, want %q", got.SavedPeers[0].PeerID, peerID)
	}
	if !got.SavedPeers[0].Trusted {
		t.Fatalf("expected saved peer to be trusted")
	}
	if len(got.SavedPeers[0].BootstrapAddrs) != 1 || got.SavedPeers[0].BootstrapAddrs[0] != bootstrap {
		t.Fatalf("unexpected bootstrap addrs: %v", got.SavedPeers[0].BootstrapAddrs)
	}
}

func TestNetworkSavedPeersUntrust(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/network/saved-peers?action=untrust&peer="+peerID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(saved.Mesh.TrustedPeers) != 0 {
		t.Fatalf("trusted peers = %v, want empty", saved.Mesh.TrustedPeers)
	}
	if len(saved.Mesh.BootstrapPeers) != 1 || saved.Mesh.BootstrapPeers[0] != bootstrap {
		t.Fatalf("bootstrap peers = %v, want [%s]", saved.Mesh.BootstrapPeers, bootstrap)
	}
}

func TestNetworkSavedPeersRemove(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/network/saved-peers?peer="+peerID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(saved.Mesh.TrustedPeers) != 0 {
		t.Fatalf("trusted peers = %v, want empty", saved.Mesh.TrustedPeers)
	}
	if len(saved.Mesh.BootstrapPeers) != 0 {
		t.Fatalf("bootstrap peers = %v, want empty", saved.Mesh.BootstrapPeers)
	}
}

func TestNetworkSavedPeersUntrustMissingPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/network/saved-peers?action=untrust&peer="+peerID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestNetworkSavedPeersRemoveMissingPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/network/saved-peers?peer="+peerID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestNetworkSavedPeersRemoveMissingPeerViaGateway(t *testing.T) {
	// Override the gateway process matcher so the launcher treats the current
	// process as a live gateway without requiring a real rhizome binary.
	originalMatcher := gatewayProcessMatcher
	t.Cleanup(func() { gatewayProcessMatcher = originalMatcher })
	gatewayProcessMatcher = func(int) (bool, bool) { return true, true }

	// Snapshot and isolate package-level gateway state.
	originalPIDData := gateway.pidData
	originalCmd := gateway.cmd
	originalPicoToken := gateway.picoToken
	originalRuntimeStatus := gateway.runtimeStatus
	t.Cleanup(func() {
		gateway.pidData = originalPIDData
		gateway.cmd = originalCmd
		gateway.picoToken = originalPicoToken
		gateway.runtimeStatus = originalRuntimeStatus
	})

	peerID := "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv"
	token := "test-token"

	// Start a mock gateway that returns 404 for saved-peers.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/network/saved-peers" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "peer not found in saved peers"})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("parse server address: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse server port: %v", err)
	}

	// Write a PID file so gatewayAvailableForProxy sees a valid gateway.
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	pidData := ppid.PidFileData{
		PID:     os.Getpid(),
		Token:   token,
		Version: "test",
		Port:    1,
		Host:    "127.0.0.1",
	}
	raw, err := json.Marshal(pidData)
	if err != nil {
		t.Fatalf("marshal pid data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".rhizome.pid"), raw, 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	// Configure the launcher with the mock gateway's host/port.
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.Gateway.Host = host
	cfg.Gateway.Port = port
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/network/saved-peers?peer="+peerID, nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
