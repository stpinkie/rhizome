package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

func newTestPeerID(t *testing.T) string {
	t.Helper()
	derived, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	if err != nil {
		t.Fatalf("derive identity: %v", err)
	}
	return derived.PeerID
}

func TestNetworkSavedPeersHandlerRequiresAuth(t *testing.T) {
	h := newNetworkSavedPeersHandler(nil, "secret-token", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/saved-peers", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestNetworkSavedPeersHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkSavedPeersHandler(nil, "secret-token", "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/network/saved-peers", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestNetworkSavedPeersHandlerListFromConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	peerID := newTestPeerID(t)
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/saved-peers?include_status=false", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp mesh.SavedPeersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", rec.Body.String())
	}

	if len(resp.SavedPeers) != 1 {
		t.Fatalf("saved peers count = %d, want 1", len(resp.SavedPeers))
	}
	if resp.SavedPeers[0].PeerID != peerID {
		t.Fatalf("peer id = %q, want %q", resp.SavedPeers[0].PeerID, peerID)
	}
	if !resp.SavedPeers[0].Trusted {
		t.Fatalf("expected saved peer to be trusted")
	}
	if len(resp.SavedPeers[0].BootstrapAddrs) != 1 || resp.SavedPeers[0].BootstrapAddrs[0] != bootstrap {
		t.Fatalf("unexpected bootstrap addrs: %v", resp.SavedPeers[0].BootstrapAddrs)
	}
}

func TestNetworkSavedPeersHandlerUntrust(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	peerID := newTestPeerID(t)
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/network/saved-peers?action=untrust&peer="+peerID, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(saved.Mesh.TrustedPeers) != 0 {
		t.Fatalf("trusted peers = %v, want empty", saved.Mesh.TrustedPeers)
	}
	if len(saved.Mesh.BootstrapPeers) != 1 || saved.Mesh.BootstrapPeers[0] != bootstrap {
		t.Fatalf("bootstrap peers = %v, want [%s]", saved.Mesh.BootstrapPeers, bootstrap)
	}
}

func TestNetworkSavedPeersHandlerRemove(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	peerID := newTestPeerID(t)
	bootstrap := "/ip4/127.0.0.1/tcp/4001/p2p/" + peerID

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{bootstrap}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/network/saved-peers?peer="+peerID, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}

	saved, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(saved.Mesh.TrustedPeers) != 0 {
		t.Fatalf("trusted peers = %v, want empty", saved.Mesh.TrustedPeers)
	}
	if len(saved.Mesh.BootstrapPeers) != 0 {
		t.Fatalf("bootstrap peers = %v, want empty", saved.Mesh.BootstrapPeers)
	}
}

func TestNetworkSavedPeersHandlerInvalidPeerID(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"mesh":{"enabled":true}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/network/saved-peers?peer=not-a-peer-id", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid peer id") {
		t.Fatalf("body missing 'invalid peer id': %s", rec.Body.String())
	}
}

func TestNetworkSavedPeersHandlerUntrustMissingPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/network/saved-peers?action=untrust&peer=12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestNetworkSavedPeersHandlerRemoveMissingPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(nil, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/network/saved-peers?peer=12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestNetworkSavedPeersHandlerListWithMesh(t *testing.T) {
	_, peerNode, peerCleanup := newStartedPeerMesh(t, 1)
	defer peerCleanup()

	// Give the peer a moment to start listening.
	time.Sleep(100 * time.Millisecond)

	peerAddrs := peerNode.BootstrapAddrs()
	if len(peerAddrs) == 0 {
		t.Fatal("peer has no bootstrap addresses")
	}

	daemonMesh, _, daemonCleanup := newStartedPeerMesh(t, 2)
	defer daemonCleanup()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := daemonMesh.Connect(ctx, peerAddrs[0]); err != nil {
		t.Fatalf("connect to peer: %v", err)
	}

	peerID := peerNode.PeerID()
	pid, err := peer.Decode(peerID)
	if err != nil {
		t.Fatalf("decode peer id: %v", err)
	}

	daemonMesh.TrustPeer(pid)

	cfg := config.DefaultConfig()
	cfg.Mesh.TrustedPeers = []string{peerID}
	cfg.Mesh.BootstrapPeers = []string{peerAddrs[0]}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	h := newNetworkSavedPeersHandler(daemonMesh, "secret-token", configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/saved-peers", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp mesh.SavedPeersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", rec.Body.String())
	}

	if len(resp.SavedPeers) != 1 {
		t.Fatalf("saved peers count = %d, want 1", len(resp.SavedPeers))
	}
	if resp.SavedPeers[0].PeerID != peerID {
		t.Fatalf("peer id = %q, want %q", resp.SavedPeers[0].PeerID, peerID)
	}
	if !resp.SavedPeers[0].Connected {
		t.Fatalf("expected saved peer to be reported as connected")
	}
}
