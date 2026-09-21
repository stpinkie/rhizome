// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rpcServer builds an httptest server answering JSON-RPC per handler —
// handler receives the method and returns (result, rpcErr); exactly one is
// non-nil.
func rpcServer(
	t *testing.T,
	handler func(method string, params json.RawMessage, id uint64) (any, *RPCError),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			ID      uint64          `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, rpcErr := handler(req.Method, req.Params, req.ID)
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			resp["error"] = rpcErr
		} else {
			resp["result"] = result
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestClient_CallResult(t *testing.T) {
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		if method != "eth_chainId" {
			t.Fatalf("unexpected method %q", method)
		}
		return "0x1", nil
	})
	defer srv.Close()

	raw, err := NewClient(srv.URL, "", srv.Client()).Call(context.Background(), "eth_chainId", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s != "0x1" {
		t.Fatalf("result = %s, want \"0x1\"", raw)
	}
}

func TestClient_RPCError(t *testing.T) {
	srv := rpcServer(t, func(_ string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		return nil, &RPCError{Code: -32602, Message: "invalid params"}
	})
	defer srv.Close()

	_, err := NewClient(srv.URL, "", srv.Client()).Call(context.Background(), "eth_getBalance", nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected *RPCError, got %v", err)
	}
	if rpcErr.Code != -32602 || rpcErr.Message != "invalid params" {
		t.Fatalf("RPCError = %+v", rpcErr)
	}
}

func TestClient_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "", srv.Client()).Call(context.Background(), "eth_chainId", nil)
	if err == nil {
		t.Fatal("expected error for HTTP 502")
	}
}

func TestClient_BearerHeader(t *testing.T) {
	var gotAuth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 1, "result": "0x1",
		})
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, "secret-key", srv.Client()).Call(
		context.Background(), "eth_chainId", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := gotAuth.Load(); got != "Bearer secret-key" {
		t.Fatalf("Authorization = %v, want Bearer secret-key", got)
	}

	if _, err := NewClient(srv.URL, "", srv.Client()).Call(
		context.Background(), "eth_chainId", nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got := gotAuth.Load(); got != "" {
		t.Fatalf("Authorization = %v, want empty when apiKey unset", got)
	}
}

func TestClient_IDMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": 999, "result": "0x1",
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "", srv.Client()).Call(context.Background(), "eth_chainId", nil)
	if err == nil {
		t.Fatal("expected id-mismatch error")
	}
}

func TestClient_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(500 * time.Millisecond):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := NewClient(srv.URL, "", srv.Client()).Call(ctx, "eth_chainId", nil)
	if err == nil {
		t.Fatal("expected context timeout error")
	}
}

func TestClient_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, "", srv.Client()).Call(context.Background(), "eth_chainId", nil)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

// Transport errors must not leak the endpoint URL — it can embed an API
// key in its path (e.g. …/v3/<key>).
func TestClient_TransportErrorHidesURL(t *testing.T) {
	// RFC 5737 documentation address with nothing listening on :1.
	endpoint := "http://192.0.2.1:1/v3/secretkey123"
	hc := &http.Client{Timeout: 500 * time.Millisecond}
	_, err := NewClient(endpoint, "", hc).Call(context.Background(), "eth_chainId", nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), "192.0.2.1") || strings.Contains(err.Error(), "secretkey123") {
		t.Fatalf("transport error leaked endpoint URL: %v", err)
	}
}
