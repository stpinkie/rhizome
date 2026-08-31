package sync

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	host  host.Host
	h     Handler
	ready chan struct{}
}

// NewTransport creates a sync transport that calls h for incoming requests.
func NewTransport(h host.Host, handler Handler) *Transport {
	return &Transport{
		host:  h,
		h:     handler,
		ready: make(chan struct{}),
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
	defer s.Close()

	r := bufio.NewReader(s)
	w := bufio.NewWriter(s)

	for {
		_ = s.SetReadDeadline(time.Now().Add(30 * time.Second))
		typ, payload, err := stream.ReadFrame(r)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		switch typ {
		case frameRequest:
			req, err := parseRequest(payload)
			if err != nil {
				_ = t.writeError(w, "invalid request")
				return
			}
			pack, head, err := t.h.ProvidePackfile(s.Conn().RemotePeer(), req.Haves, req.Wants)
			if err != nil {
				_ = t.writeError(w, err.Error())
				return
			}
			resp := append(head[:], pack...)
			if err := stream.WriteFrame(w, framePackfile, resp); err != nil {
				return
			}
			_ = w.Flush()

		case frameAnnounce:
			if len(payload) != len(plumbing.ZeroHash) {
				_ = t.writeError(w, "invalid announce")
				return
			}
			var head plumbing.Hash
			copy(head[:], payload)
			t.h.HandleAnnounce(s.Conn().RemotePeer(), head)

		default:
			_ = t.writeError(w, fmt.Sprintf("unknown frame type: %d", typ))
			return
		}
	}
}

func (t *Transport) writeError(w *bufio.Writer, msg string) error {
	if err := stream.WriteFrame(w, frameError, []byte(msg)); err != nil {
		return err
	}
	return w.Flush()
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
	defer s.Close()

	w := bufio.NewWriter(s)
	r := bufio.NewReader(s)

	req, err := encodeRequest(&requestFrame{Haves: haves, Wants: wants})
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("encode request: %w", err)
	}
	if err := stream.WriteFrame(w, frameRequest, req); err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("write request: %w", err)
	}
	if err := w.Flush(); err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("flush request: %w", err)
	}

	_ = s.SetReadDeadline(time.Now().Add(60 * time.Second))
	typ, payload, err := stream.ReadFrame(r)
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

// AnnounceHead sends a head announcement to a peer.
func (t *Transport) AnnounceHead(ctx context.Context, pid peer.ID, head plumbing.Hash) error {
	s, err := t.host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	defer s.Close()

	w := bufio.NewWriter(s)
	if err := stream.WriteFrame(w, frameAnnounce, head[:]); err != nil {
		return fmt.Errorf("write announce: %w", err)
	}
	return w.Flush()
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
