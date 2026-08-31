package agentrpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
type Transport struct {
	host    host.Host
	handler Handler
}

// Handler processes incoming agent requests.
type Handler interface {
	HandleRequest(from peer.ID, req Request) (Response, error)
}

// NewTransport creates an agent RPC transport.
func NewTransport(h host.Host, handler Handler) *Transport {
	return &Transport{host: h, handler: handler}
}

// Start registers the protocol handler and blocks until the context is done.
func (t *Transport) Start(ctx context.Context) error {
	t.host.SetStreamHandler(ProtocolID, t.handleStream)
	<-ctx.Done()
	t.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

// waitForPeerProtocol polls until the given peer advertises support for the
// agent RPC protocol. It returns false if the context is cancelled or the
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
	defer s.Close()

	w := bufio.NewWriter(s)
	r := bufio.NewReader(s)

	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}
	if err = stream.WriteFrame(w, frameRequest, payload); err != nil {
		return Response{}, fmt.Errorf("write request: %w", err)
	}
	if err = w.Flush(); err != nil {
		return Response{}, fmt.Errorf("flush request: %w", err)
	}

	deadline, ok := ctx.Deadline()
	if ok {
		_ = s.SetReadDeadline(deadline)
	} else {
		_ = s.SetReadDeadline(time.Now().Add(5 * time.Minute))
	}

	typ, payload, err := stream.ReadFrame(r)
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
	defer s.Close()

	r := bufio.NewReader(s)
	w := bufio.NewWriter(s)

	_ = s.SetReadDeadline(time.Now().Add(30 * time.Second))
	typ, payload, err := stream.ReadFrame(r)
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

	resp, err := t.handler.HandleRequest(s.Conn().RemotePeer(), req)
	if err != nil {
		resp = Response{
			CorrelationID: req.CorrelationID,
			Status:        "error",
			Error:         err.Error(),
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	if err := stream.WriteFrame(w, frameResponse, data); err != nil {
		return
	}
	_ = w.Flush()
}
