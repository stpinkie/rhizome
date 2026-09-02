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
	Signature     []byte        `json:"signature,omitempty"`
}

// ToolRef is a lightweight reference to a tool capability advertised by a peer.
type ToolRef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Response carries the result of a remote agent task.
type Response struct {
	CorrelationID string                 `json:"correlation_id"`
	Status        string                 `json:"status"`
	Result        *toolshared.ToolResult `json:"result,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Signature     []byte                 `json:"signature,omitempty"`
}

// Transport provides libp2p stream handling for the agent RPC protocol.
// It keeps a small in-memory idempotency cache so duplicate requests with the
// same CorrelationID receive the same response without re-executing the task.
type Transport struct {
	host    host.Host
	handler Handler

	results   map[string]cachedResult
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

func (t *Transport) handleRequestWithCache(from peer.ID, req Request) Response {
	t.resultsMu.RLock()
	cached, ok := t.results[req.CorrelationID]
	t.resultsMu.RUnlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.resp
	}

	resp, err := t.handler.HandleRequest(from, req)
	if err != nil {
		resp = Response{
			CorrelationID: req.CorrelationID,
			Status:        "error",
			Error:         err.Error(),
		}
	}

	t.resultsMu.Lock()
	t.results[req.CorrelationID] = cachedResult{resp: resp, expiresAt: time.Now().Add(t.resultTTL)}
	t.resultsMu.Unlock()

	// Clean up old results opportunistically.
	t.pruneResults()

	return resp
}

func (t *Transport) pruneResults() {
	t.resultsMu.Lock()
	defer t.resultsMu.Unlock()
	now := time.Now()
	for id, cr := range t.results {
		if now.After(cr.expiresAt) {
			delete(t.results, id)
		}
	}
}
