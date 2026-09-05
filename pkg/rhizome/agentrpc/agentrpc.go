package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const ProtocolID = protocol.ID("/rhizome/agent/1.0.0")

const (
	frameRequest  = byte(1)
	frameResponse = byte(2)
)

// Request is an agent task sent from one Rhizome node to another.
type Request struct {
	CorrelationID string        `json:"correlation_id"`
	TargetAgentID string        `json:"target_agent_id"`
	Model         string        `json:"model,omitempty"`
	SystemPrompt  string        `json:"system_prompt"`
	Timeout       time.Duration `json:"timeout,omitempty"`
	Tools         []ToolRef     `json:"tools,omitempty"`
	// Nonce is a caller-generated random value (16 bytes, hex) used for replay
	// protection. Timestamp is the unix time the request was created. Both are
	// covered by Signature.
	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Signature []byte `json:"signature,omitempty"`
	// Async hints that the caller will use the result in the background (spawn)
	// rather than waiting for it in-line (delegate). Defaults to false.
	Async bool `json:"async,omitempty"`
}

// ToolRef is a lightweight reference to a tool capability advertised by a peer.
type ToolRef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Response carries the result of a remote agent task.
type Response struct {
	CorrelationID string `json:"correlation_id"`
	// Nonce echoes the request nonce so the signed response is bound to the
	// exact request it answers.
	Nonce     string                 `json:"nonce,omitempty"`
	Status    string                 `json:"status"`
	Result    *toolshared.ToolResult `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Signature []byte                 `json:"signature,omitempty"`
}

// maxCachedResults bounds the idempotency cache so a peer cannot grow it
// without limit by sending fresh correlation ids.
const maxCachedResults = 1024

// Transport provides libp2p stream handling for the agent RPC protocol.
// It keeps a small in-memory idempotency cache so duplicate requests with the
// same CorrelationID receive the same response without re-executing the task.
type Transport struct {
	host    host.Host
	handler Handler

	results   map[string]cachedResult
	order     []string // insertion order for count-bounded eviction
	resultsMu sync.RWMutex
	resultTTL time.Duration
}

type cachedResult struct {
	resp      Response
	expiresAt time.Time
}

// Handler processes incoming agent requests.
type Handler interface {
	HandleRequest(from peer.ID, req Request) (Response, error)
}

// NewTransport creates an agent RPC transport.
func NewTransport(h host.Host, handler Handler) *Transport {
	return &Transport{
		host:      h,
		handler:   handler,
		results:   make(map[string]cachedResult),
		resultTTL: 5 * time.Minute,
	}
}

// Start registers the protocol handler and blocks until the context is done.
func (t *Transport) Start(ctx context.Context) error {
	t.host.SetStreamHandler(ProtocolID, t.handleStream)
	<-ctx.Done()
	t.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

// waitForPeerProtocol polls until the given peer advertises support for the
// agent RPC protocol. It returns false if the context is canceled or the
// timeout expires.
func (t *Transport) waitForPeerProtocol(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range t.host.Network().Peers() {
			if p == pid {
				protos, err := t.host.Peerstore().SupportsProtocols(pid, ProtocolID)
				if err == nil && len(protos) > 0 {
					return true
				}
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// Call opens a stream to a peer, sends a request, and returns the response.
func (t *Transport) Call(ctx context.Context, pid peer.ID, req Request) (Response, error) {
	// Wait until the peer has identified and advertised support for the agent
	// protocol before opening a stream. This avoids races during mesh startup.
	if !t.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return Response{}, fmt.Errorf("peer %s does not support %s", pid, ProtocolID)
	}

	s, err := t.host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return Response{}, fmt.Errorf("open agent stream: %w", err)
	}

	rto := req.Timeout
	if rto <= 0 {
		rto = 5 * time.Minute
	}
	wto := rto
	if wto > 30*time.Second {
		wto = 30 * time.Second
	}
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(rto), stream.WithWriteTimeout(wto))
	defer rc.Close()

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}
	if err = rc.WriteFrame(frameRequest, payload); err != nil {
		return Response{}, fmt.Errorf("write request: %w", err)
	}

	typ, payload, err := rc.ReadFrame()
	if err != nil {
		return Response{}, fmt.Errorf("read response: %w", err)
	}
	if typ != frameResponse {
		return Response{}, fmt.Errorf("unexpected agent frame type: %d", typ)
	}

	var resp Response
	if err = json.Unmarshal(payload, &resp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

func (t *Transport) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(5*time.Minute))
	defer rc.Close()

	typ, payload, err := rc.ReadFrame()
	if err != nil {
		return
	}
	if typ != frameRequest {
		return
	}

	var req Request
	if err = json.Unmarshal(payload, &req); err != nil {
		return
	}

	resp := t.handleRequestWithCache(s.Conn().RemotePeer(), req)

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(frameResponse, data)
}

// resultCacheKey scopes a cached result to the authenticated peer. The cache
// lookup runs before the handler's trust/signature/ACL checks, so a bare
// correlation id must never leak one peer's response to another peer.
func resultCacheKey(from peer.ID, correlationID string) string {
	return string(from) + "\x00" + correlationID
}

func (t *Transport) handleRequestWithCache(from peer.ID, req Request) Response {
	key := resultCacheKey(from, req.CorrelationID)
	t.resultsMu.RLock()
	cached, ok := t.results[key]
	t.resultsMu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.resp
	}

	resp, err := t.handler.HandleRequest(from, req)
	if err != nil {
		resp = Response{
			CorrelationID: req.CorrelationID,
			Nonce:         req.Nonce,
			Status:        "error",
			Error:         err.Error(),
		}
	}
	resp.Nonce = req.Nonce

	t.resultsMu.Lock()
	if _, exists := t.results[key]; !exists {
		t.order = append(t.order, key)
	}
	t.results[key] = cachedResult{resp: resp, expiresAt: time.Now().Add(t.resultTTL)}
	t.pruneResultsLocked()
	t.resultsMu.Unlock()

	return resp
}

// pruneResultsLocked evicts expired entries and, when the cache exceeds
// maxCachedResults, the oldest entries by insertion order. Caller holds the
// write lock.
func (t *Transport) pruneResultsLocked() {
	now := time.Now()
	for id, cr := range t.results {
		if now.After(cr.expiresAt) {
			delete(t.results, id)
		}
	}
	if len(t.results) <= maxCachedResults {
		return
	}
	kept := t.order[:0]
	for _, id := range t.order {
		if _, ok := t.results[id]; ok {
			kept = append(kept, id)
		}
	}
	t.order = kept
	for len(t.results) > maxCachedResults && len(t.order) > 0 {
		delete(t.results, t.order[0])
		t.order = t.order[1:]
	}
}
