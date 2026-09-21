// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
)

// maxResponseBytes bounds a single JSON-RPC response body. Generous enough
// for full blocks and large log sets, small enough to keep a misbehaving or
// hostile endpoint from ballooning memory.
const maxResponseBytes = 8 << 20 // 8 MiB

// RPCError is a JSON-RPC error object returned by the endpoint.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Client is a minimal JSON-RPC 2.0 client over HTTP POST.
type Client struct {
	endpoint string
	apiKey   string
	hc       *http.Client
	id       atomic.Uint64
}

// NewClient builds a client for endpoint; apiKey (optional) is sent as a
// Bearer token. hc may be nil for a default client.
func NewClient(endpoint, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{endpoint: endpoint, apiKey: apiKey, hc: hc}
}

// Endpoint returns the configured endpoint URL.
func (c *Client) Endpoint() string { return c.endpoint }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      uint64 `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
	ID      uint64          `json:"id"`
}

// Call performs one JSON-RPC call and returns the raw result. Params may be
// nil (marshaled as []). RPC error objects are returned as *RPCError.
func (c *Client) Call(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	id := c.id.Add(1)
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0", Method: method, Params: params, ID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal %s params: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// url.Error embeds the endpoint URL — which may carry a
		// path-embedded API key. Unwrap it so transport failures never
		// leak the URL into tool output or logs.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("%s: %w", method, ue.Err)
		}
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("%s response exceeds %d byte limit", method, maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}

	var rr rpcResponse
	if err := json.Unmarshal(data, &rr); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	if rr.JSONRPC != "2.0" {
		return nil, fmt.Errorf("%s: not a JSON-RPC 2.0 response", method)
	}
	if rr.ID != id {
		return nil, fmt.Errorf("%s: response id %d does not match request id %d", method, rr.ID, id)
	}
	if rr.Error != nil {
		return nil, rr.Error
	}
	return rr.Result, nil
}
