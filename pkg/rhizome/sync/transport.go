package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

// ProtocolID is the libp2p protocol used for git packfile sync.
const ProtocolID = protocol.ID("/rhizome/git-sync/1.0.0")

// Handler is the callback interface for incoming sync requests.
type Handler interface {
	// ProvidePackfile is invoked when a peer asks for objects.
	ProvidePackfile(from peer.ID, haves, wants []plumbing.Hash) (pack []byte, head plumbing.Hash, err error)
	// HandleAnnounce is invoked when a peer announces a new head.
	HandleAnnounce(from peer.ID, head plumbing.Hash)
}

// Transport wraps libp2p stream handling for git sync frames.
type Transport struct {
	host            host.Host
	h               Handler
	ready           chan struct{}
	requestTimeout  time.Duration
	packfileTimeout time.Duration
	announceTimeout time.Duration
}

// NewTransport creates a sync transport that calls h for incoming requests.
func NewTransport(h host.Host, handler Handler) *Transport {
	return &Transport{
		host:            h,
		h:               handler,
		ready:           make(chan struct{}),
		requestTimeout:  30 * time.Second,
		packfileTimeout: 60 * time.Second,
		announceTimeout: 30 * time.Second,
	}
}

// Start registers the sync protocol handler and blocks until the context is
// canceled. The returned channel is closed once the stream handler is active.
func (t *Transport) Start(ctx context.Context) error {
	t.host.SetStreamHandler(ProtocolID, t.handleStream)
	close(t.ready)

	<-ctx.Done()
	t.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

// Ready returns a channel that is closed once the sync handler is registered.
func (t *Transport) Ready() <-chan struct{} {
	return t.ready
}

func (t *Transport) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(
		s,
		stream.WithReadTimeout(t.requestTimeout),
		stream.WithWriteTimeout(t.packfileTimeout),
	)
	defer rc.Close()

	for {
		typ, payload, err := rc.ReadFrame()
		if err != nil {
			return
		}

		switch typ {
		case frameRequest:
			req, err := parseRequest(payload)
			if err != nil {
				_ = t.writeError(rc, "invalid request")
				return
			}
			pack, head, err := t.h.ProvidePackfile(s.Conn().RemotePeer(), req.Haves, req.Wants)
			if err != nil {
				_ = t.writeError(rc, err.Error())
				return
			}
			resp := append(head[:], pack...)
			if err := rc.WriteFrame(framePackfile, resp); err != nil {
				return
			}

		case frameAnnounce:
			if len(payload) != len(plumbing.ZeroHash) {
				_ = t.writeError(rc, "invalid announce")
				return
			}
			var head plumbing.Hash
			copy(head[:], payload)
			t.h.HandleAnnounce(s.Conn().RemotePeer(), head)

		default:
			_ = t.writeError(rc, fmt.Sprintf("unknown frame type: %d", typ))
			return
		}
	}
}

func (t *Transport) writeError(rc *stream.ReliableConn, msg string) error {
	return rc.WriteFrame(frameError, []byte(msg))
}

// Fetch requests a packfile from a peer.
func (t *Transport) Fetch(
	ctx context.Context,
	pid peer.ID,
	haves, wants []plumbing.Hash,
) ([]byte, plumbing.Hash, error) {
	s, err := t.host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("open stream: %w", err)
	}

	rc := stream.NewReliableConn(
		s,
		stream.WithReadTimeout(t.packfileTimeout),
		stream.WithWriteTimeout(t.packfileTimeout),
	)
	defer rc.Close()

	req, err := encodeRequest(&requestFrame{Haves: haves, Wants: wants})
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("encode request: %w", err)
	}
	if err = rc.WriteFrame(frameRequest, req); err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("write request: %w", err)
	}

	typ, payload, err := rc.ReadFrame()
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("read response: %w", err)
	}
	if typ == frameError {
		return nil, plumbing.ZeroHash, fmt.Errorf("remote error: %s", string(payload))
	}
	if typ != framePackfile {
		return nil, plumbing.ZeroHash, fmt.Errorf("unexpected frame type: %d", typ)
	}
	if len(payload) < len(plumbing.ZeroHash) {
		return nil, plumbing.ZeroHash, fmt.Errorf("short packfile response")
	}

	var head plumbing.Hash
	copy(head[:], payload)
	pack := payload[len(plumbing.ZeroHash):]
	return pack, head, nil
}

// AnnounceHead sends a head announcement to a peer with retry.
func (t *Transport) AnnounceHead(ctx context.Context, pid peer.ID, head plumbing.Hash) error {
	backoffs := []time.Duration{0, 500 * time.Millisecond, time.Second, 2 * time.Second}
	var lastErr error
	for i, backoff := range backoffs {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		s, err := t.host.NewStream(ctx, pid, ProtocolID)
		if err != nil {
			lastErr = err
			continue
		}

		rc := stream.NewReliableConn(
			s,
			stream.WithReadTimeout(t.announceTimeout),
			stream.WithWriteTimeout(t.announceTimeout),
		)
		if err := rc.WriteFrame(frameAnnounce, head[:]); err != nil {
			_ = rc.Close()
			lastErr = err
			continue
		}
		_ = rc.Close()
		return nil
	}
	return fmt.Errorf("announce head to %s: %w", pid, lastErr)
}

const (
	frameRequest  = byte(1)
	framePackfile = byte(2)
	frameAnnounce = byte(3)
	frameError    = byte(4)
)

type requestFrame struct {
	Haves []plumbing.Hash `json:"haves"`
	Wants []plumbing.Hash `json:"wants"`
}

func encodeRequest(req *requestFrame) ([]byte, error) {
	return json.Marshal(req)
}

func parseRequest(data []byte) (*requestFrame, error) {
	var req requestFrame
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}
