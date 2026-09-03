package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
