package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestNetworkStatusHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/network/status", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestNetworkStatusHandlerNoMesh(t *testing.T) {
	h := newNetworkStatusHandler(nil, "secret-token", t.TempDir())

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

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir())

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

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/status?timeout=not-a-duration", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestNetworkStatusHandlerReturnsStatus(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()

	h := newNetworkStatusHandler(m, "secret-token", t.TempDir())

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
