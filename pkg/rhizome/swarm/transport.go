package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

const (
	frameRequest  = byte(1)
	framePush     = byte(2)
	frameResponse = byte(3)

	// serverReadTimeout bounds how long the server-side reliable reader waits
	// for the next frame. Swarm handlers are quick, so a modest bound is fine.
	serverReadTimeout = 30 * time.Second
	// callTimeout bounds a request/response call.
	callTimeout = 30 * time.Second
	// pushTimeout bounds a one-way message write.
	pushTimeout = 15 * time.Second
)

// Handler processes inbound swarm envelopes. Request frames produce a
// response envelope (or nil for a bare ack); push frames get no reply.
type Handler interface {
	// HandleRequest handles an envelope that expects a response.
	HandleRequest(from peer.ID, env Envelope) Envelope
	// HandlePush handles a one-way envelope.
	HandlePush(from peer.ID, env Envelope)
}

// Transport provides libp2p stream handling for the swarm protocol.
type Transport struct {
	host     host.Host
	handler  Handler
	maxBytes int
}

// NewTransport creates a swarm protocol transport. maxBytes bounds a single
// inbound frame payload (0 applies a conservative default).
func NewTransport(h host.Host, handler Handler, maxBytes int) *Transport {
	if maxBytes <= 0 {
		maxBytes = 256 * 1024
	}
	return &Transport{host: h, handler: handler, maxBytes: maxBytes}
}

// Start registers the protocol handler and blocks until the context is done.
func (t *Transport) Start(ctx context.Context) error {
	t.host.SetStreamHandler(ProtocolID, t.handleStream)
	<-ctx.Done()
	t.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

// Stop deregisters the protocol handler without waiting on a context.
func (t *Transport) Stop() {
	t.host.RemoveStreamHandler(ProtocolID)
}

// Supported reports whether the peer advertises the swarm protocol.
func (t *Transport) Supported(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	return t.waitForPeerProtocol(ctx, pid, timeout)
}

// waitForPeerProtocol polls until the given peer advertises support for the
// swarm protocol. It returns false if the context is canceled or the timeout
// expires.
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

// Call sends an envelope to a peer and waits for its response envelope.
func (t *Transport) Call(ctx context.Context, pid peer.ID, env Envelope) (Envelope, error) {
	if !t.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return Envelope{}, fmt.Errorf("peer %s does not support %s", pid, ProtocolID)
	}

	s, err := t.host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return Envelope{}, fmt.Errorf("open swarm stream: %w", err)
	}

	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(callTimeout),
		stream.WithWriteTimeout(pushTimeout))
	defer rc.Close()

	payload, err := json.Marshal(env)
	if err != nil {
		return Envelope{}, fmt.Errorf("encode envelope: %w", err)
	}
	if err = rc.WriteFrame(frameRequest, payload); err != nil {
		return Envelope{}, fmt.Errorf("write request: %w", err)
	}

	typ, payload, err := rc.ReadFrame()
	if err != nil {
		return Envelope{}, fmt.Errorf("read response: %w", err)
	}
	if typ != frameResponse {
		return Envelope{}, fmt.Errorf("unexpected swarm frame type: %d", typ)
	}

	var resp Envelope
	if err = json.Unmarshal(payload, &resp); err != nil {
		return Envelope{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

// Push sends a one-way envelope to a peer. No response is expected. A single
// retry covers transient stream resets that occur when the remote handler
// closes its ReliableConn immediately after reading the frame — the close
// can race the ACK delivery and surface as "reliable conn closed" on the
// writer side.
func (t *Transport) Push(ctx context.Context, pid peer.ID, env Envelope) error {
	if !t.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return fmt.Errorf("peer %s does not support %s", pid, ProtocolID)
	}

	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		s, err := t.host.NewStream(ctx, pid, ProtocolID)
		if err != nil {
			lastErr = fmt.Errorf("open swarm stream: %w", err)
			continue
		}
		rc := stream.NewReliableConn(s,
			stream.WithReadTimeout(pushTimeout),
			stream.WithWriteTimeout(pushTimeout))
		if err = rc.WriteFrame(framePush, payload); err != nil {
			_ = rc.Close()
			lastErr = fmt.Errorf("write push: %w", err)
			continue
		}
		_ = rc.Close()
		return nil
	}
	return lastErr
}

// handleStream serves one inbound swarm stream: a request frame produces a
// signed response, a push frame is dispatched without a reply. Frames larger
// than maxBytes are dropped before decoding.
func (t *Transport) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(serverReadTimeout),
		stream.WithWriteTimeout(pushTimeout))
	defer rc.Close()

	typ, payload, err := rc.ReadFrame()
	if err != nil || len(payload) > t.maxBytes {
		return
	}

	var env Envelope
	if err = json.Unmarshal(payload, &env); err != nil {
		return
	}

	switch typ {
	case frameRequest:
		resp := t.handler.HandleRequest(s.Conn().RemotePeer(), env)
		data, err := json.Marshal(resp)
		if err != nil {
			return
		}
		_ = rc.WriteFrame(frameResponse, data)
	case framePush:
		t.handler.HandlePush(s.Conn().RemotePeer(), env)
		// Wait briefly for the sender to acknowledge receipt of our ACK
		// before closing. Without this, closing the ReliableConn can send
		// a stream reset that arrives at the sender before its WriteFrame
		// has processed the ACK, surfacing as "reliable conn closed".
		rc.SetReadTimeout(time.Second)
		_, _, _ = rc.ReadFrame()
	}
}
