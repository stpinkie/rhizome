package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func newTestNetworkStatusMesh(t *testing.T) (*mesh.Mesh, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	derived, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	if err != nil {
		t.Fatalf("derive identity: %v", err)
	}

	node, err := rnet.NewNode(ctx, derived.Libp2pPrivKey, rnet.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	m := mesh.NewMesh(node, nil, derived, config.MeshConfig{}, nil)
	return m, func() {
		_ = node.Close()
		cancel()
	}
}

func TestNetworkStatusHandlerRequiresAuth(t *testing.T) {
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestNetworkStatusHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/network/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestNetworkStatusHandlerNoMesh(t *testing.T) {
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestNetworkStatusHandlerInvalidBootstrap(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status?bootstrap=not-a-multiaddr", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid bootstrap") {
		t.Fatalf("body missing 'invalid bootstrap': %s", rec.Body.String())
	}
}

func TestNetworkStatusHandlerInvalidTimeout(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status?timeout=not-a-duration", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestNetworkStatusHandlerSaveTrustedPeer(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"mesh":{"enabled":true}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	derived, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	if err != nil {
		t.Fatalf("derive identity: %v", err)
	}

	pid, err := peer.Decode(derived.PeerID)
	if err != nil {
		t.Fatalf("decode peer id: %v", err)
	}

	addr := "/ip4/127.0.0.1/tcp/4001/p2p/" + derived.PeerID
	if err := saveTrustedPeer(configPath, &sync.Mutex{}, addr, pid); err != nil {
		t.Fatalf("saveTrustedPeer: %v", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}

	foundBootstrap := false
	for _, b := range cfg.Mesh.BootstrapPeers {
		if b == addr {
			foundBootstrap = true
			break
		}
	}
	if !foundBootstrap {
		t.Fatalf("bootstrap peer not saved: %v", cfg.Mesh.BootstrapPeers)
	}

	foundTrusted := false
	for _, p := range cfg.Mesh.TrustedPeers {
		if p == derived.PeerID {
			foundTrusted = true
			break
		}
	}
	if !foundTrusted {
		t.Fatalf("trusted peer not saved: %v", cfg.Mesh.TrustedPeers)
	}
}

func TestNetworkStatusHandlerTrustFalseDoesNotSave(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	status := m.NetworkStatus(dir)
	if status.PeerID == "" {
		t.Fatal("mesh peer id is empty")
	}

	// Use a syntactically valid but unreachable bootstrap address so the connect
	// branch runs without actually dialling a peer.
	bootstrap := fmt.Sprintf("/ip4/127.0.0.1/tcp/1/p2p/%s", status.PeerID)

	h := newNetworkStatusHandler(m, "secret-token", dir, configPath)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status?trust=false&bootstrap="+bootstrap+"&timeout=100ms", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config.json was created when trust=false: %v", err)
	}
}

func newStartedPeerMesh(t *testing.T, nodeIndex uint32) (*mesh.Mesh, *rnet.Node, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	derived, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		nodeIndex,
	)
	if err != nil {
		t.Fatalf("derive identity: %v", err)
	}

	node, err := rnet.NewNode(ctx, derived.Libp2pPrivKey, rnet.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}

	m := mesh.NewMesh(node, nil, derived, config.MeshConfig{Enabled: true}, nil)
	if err := m.Start(ctx); err != nil {
		_ = node.Close()
		cancel()
		t.Fatalf("start mesh: %v", err)
	}

	return m, node, func() {
		_ = m.Stop()
		_ = node.Close()
		cancel()
	}
}

func TestNetworkStatusHandlerTrustTrueSaves(t *testing.T) {
	_, peerNode, peerCleanup := newStartedPeerMesh(t, 1)
	defer peerCleanup()

	// Give the peer a moment to start listening.
	time.Sleep(100 * time.Millisecond)

	peerAddrs := peerNode.BootstrapAddrs()
	if len(peerAddrs) == 0 {
		t.Fatal("peer has no bootstrap addresses")
	}

	daemonMesh, daemonNode, daemonCleanup := newStartedPeerMesh(t, 2)
	defer daemonCleanup()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"mesh":{"enabled":true}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	h := newNetworkStatusHandler(daemonMesh, "secret-token", dir, configPath)

	rec := httptest.NewRecorder()
	url := "/network/status?trust=true&bootstrap=" + peerAddrs[0]
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var status mesh.NetworkStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// The peer should be reported as trusted in the status snapshot.
	var found bool
	for _, p := range status.Peers {
		if p.PeerID == peerNode.PeerID() {
			found = true
			if !p.Trusted {
				t.Fatalf("peer %s is not trusted in status", p.PeerID)
			}
		}
	}
	if !found {
		// The peer may not appear if it disconnected after the save. The config
		// write is the more important assertion below, so only log here.
		t.Logf("peer %s not in connected peers: %v", peerNode.PeerID(), status.Peers)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load saved config: %v", err)
	}

	var savedBootstrap bool
	for _, b := range cfg.Mesh.BootstrapPeers {
		if b == peerAddrs[0] {
			savedBootstrap = true
			break
		}
	}
	if !savedBootstrap {
		t.Fatalf("bootstrap peer not saved: %v", cfg.Mesh.BootstrapPeers)
	}

	var savedTrusted bool
	for _, p := range cfg.Mesh.TrustedPeers {
		if p == peerNode.PeerID() {
			savedTrusted = true
			break
		}
	}
	if !savedTrusted {
		t.Fatalf("trusted peer not saved: %v", cfg.Mesh.TrustedPeers)
	}

	// Avoid compiler warnings for unused daemonNode.
	_ = daemonNode
}

func TestNetworkStatusHandlerReturnsStatus(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir(), "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), "peer_id") {
		t.Fatalf("body missing peer_id: %s", rec.Body.String())
	}
}
