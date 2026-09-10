package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
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

// Supported reports whether the peer supports the swarm protocol by attempting
// to open a stream. This is more reliable than waiting for the peerstore to be
// updated by an identify push, which can race with stream handler registration.
func (t *Transport) Supported(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, err := p2putil.OpenProtocolStream(ctx, t.host, pid, ProtocolID, timeout)
	if err != nil {
		return false
	}
	_ = s.Close()
	return true
}

// Call sends an envelope to a peer and waits for its response envelope.
// OpenProtocolStream performs the protocol negotiation and retries while the
// remote handler is registering, so no separate Supported pre-check is needed.
func (t *Transport) Call(ctx context.Context, pid peer.ID, env Envelope) (Envelope, error) {
	s, err := p2putil.OpenProtocolStream(ctx, t.host, pid, ProtocolID, 15*time.Second)
	if err != nil {
		return Envelope{}, fmt.Errorf("open swarm stream: %w", err)
	}

	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(callTimeout),
		stream.WithWriteTimeout(pushTimeout))
	defer func() { _ = rc.Close() }()

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
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}

	var lastErr error
	backoffs := []time.Duration{0, 100 * time.Millisecond, 300 * time.Millisecond}
	for attempt := 0; attempt < len(backoffs); attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
		s, err := p2putil.OpenProtocolStream(ctx, t.host, pid, ProtocolID, 15*time.Second)
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
	defer func() { _ = rc.Close() }()

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
		// The ReliableConn now performs an ACK-aware graceful close, so
		// we no longer need to pause before the deferred rc.Close().
	}
}
