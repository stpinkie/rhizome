// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

// HealthCheck probes a module per its HealthSpec. Returns (ok, supported):
// supported=false means the module defines no health check — callers should
// treat that as "unknown", not "unhealthy".
func (m *Manager) HealthCheck(ctx context.Context, id string) (bool, bool) {
	spec, _, ok := m.lookupSpec(id)
	if !ok || spec.Health.Type == "" {
		return false, false
	}
	values := m.resolvedFields(spec, true)
	target := expand(spec.Health.Target, values)
	if target == "" || strings.Contains(target, "{") {
		return false, true // check defined but not resolvable yet
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	switch spec.Health.Type {
	case "tcp":
		if !strings.Contains(target, "://") {
			target = "tcp://" + target
		}
		addr := strings.TrimPrefix(target, "tcp://")
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err != nil {
			return false, true
		}
		_ = conn.Close()
		return true, true

	case "http":
		path := spec.Health.Method
		url := strings.TrimSuffix(target, "/") + "/" + strings.TrimPrefix(path, "/")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, true
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return false, true
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode >= 200 && resp.StatusCode < 300, true

	case "jsonrpc":
		method := spec.Health.Method
		if method == "" {
			method = "web3_clientVersion"
		}
		body, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "method": method, "params": []any{}, "id": 1,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return false, true
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := m.client.Do(req)
		if err != nil {
			return false, true
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, true
		}
		var rpcResp struct {
			JSONRPC string          `json:"jsonrpc"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			return false, true
		}
		// Any well-formed JSON-RPC response — result or error — proves the
		// endpoint is alive and serving.
		return rpcResp.JSONRPC == "2.0", true

	default:
		return false, false
	}
}

// HealthString renders the health check for status output.
func (m *Manager) HealthString(ctx context.Context, id string) string {
	ok, supported := m.HealthCheck(ctx, id)
	if !supported {
		return "n/a"
	}
	if ok {
		return "healthy"
	}
	return "unhealthy"
}
