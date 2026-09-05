package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// packfileRequest records a single ProvidePackfile invocation.
type packfileRequest struct {
	from  peer.ID
	haves []plumbing.Hash
	wants []plumbing.Hash
}

// announceRecord records a single HandleAnnounce invocation.
type announceRecord struct {
	from peer.ID
	head plumbing.Hash
}

// stubSyncHandler implements the transport Handler interface with scripted
// responses and channel-delivered observations (race-safe).
type stubSyncHandler struct {
	provideFn  func(from peer.ID, haves, wants []plumbing.Hash) ([]byte, plumbing.Hash, error)
	announceCh chan announceRecord
}

func (s *stubSyncHandler) ProvidePackfile(from peer.ID, haves, wants []plumbing.Hash) ([]byte, plumbing.Hash, error) {
	return s.provideFn(from, haves, wants)
}

func (s *stubSyncHandler) HandleAnnounce(from peer.ID, head plumbing.Hash) {
	select {
	case s.announceCh <- announceRecord{from: from, head: head}:
	default:
	}
}

func testHash(b byte) plumbing.Hash {
	var h plumbing.Hash
	for i := range h {
		h[i] = b
	}
	return h
}

// TestTransportFetchRoundTrip exercises a packfile request/response between
// two in-process libp2p nodes on 127.0.0.1.
func TestTransportFetchRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 30)
	nodeB := newTestNode(t, ctx, 31)
	connectNodes(t, ctx, nodeA, nodeB)

	head := testHash(0xAB)
	reqCh := make(chan packfileRequest, 1)
	handlerB := &stubSyncHandler{
		provideFn: func(from peer.ID, haves, wants []plumbing.Hash) ([]byte, plumbing.Hash, error) {
			reqCh <- packfileRequest{from: from, haves: haves, wants: wants}
			return []byte("PACK-BYTES"), head, nil
		},
	}
	trB := NewTransport(nodeB.Host(), handlerB)
	go func() { _ = trB.Start(ctx) }()
	<-trB.Ready()
	// Identify may lag the handler registration — wait until A knows B
	// speaks the sync protocol before dialing it.
	waitSyncProtocol(t, nodeA, nodeB.ID())

	trA := NewTransport(nodeA.Host(), &stubSyncHandler{
		provideFn: func(peer.ID, []plumbing.Hash, []plumbing.Hash) ([]byte, plumbing.Hash, error) {
			return nil, plumbing.ZeroHash, errors.New("unexpected call on A")
		},
	})

	have := testHash(0x11)
	want := testHash(0x22)
	pack, gotHead, err := trA.Fetch(ctx, nodeB.ID(), []plumbing.Hash{have}, []plumbing.Hash{want})
	require.NoError(t, err)
	assert.Equal(t, []byte("PACK-BYTES"), pack)
	assert.Equal(t, head, gotHead)

	select {
	case req := <-reqCh:
		assert.Equal(t, nodeA.ID(), req.from)
		assert.Equal(t, []plumbing.Hash{have}, req.haves)
		assert.Equal(t, []plumbing.Hash{want}, req.wants)
	case <-time.After(5 * time.Second):
		t.Fatal("handler never observed the request")
	}
}

// TestTransportAnnounceReceived verifies an announce frame reaches the
// receiver's HandleAnnounce callback.
func TestTransportAnnounceReceived(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 32)
	nodeB := newTestNode(t, ctx, 33)
	connectNodes(t, ctx, nodeA, nodeB)

	handlerB := &stubSyncHandler{
		announceCh: make(chan announceRecord, 1),
		provideFn: func(peer.ID, []plumbing.Hash, []plumbing.Hash) ([]byte, plumbing.Hash, error) {
			return nil, plumbing.ZeroHash, errors.New("not implemented")
		},
	}
	trB := NewTransport(nodeB.Host(), handlerB)
	go func() { _ = trB.Start(ctx) }()
	<-trB.Ready()
	waitSyncProtocol(t, nodeA, nodeB.ID())

	trA := NewTransport(nodeA.Host(), &stubSyncHandler{})
	head := testHash(0xCD)
	require.NoError(t, trA.AnnounceHead(ctx, nodeB.ID(), head))

	select {
	case rec := <-handlerB.announceCh:
		assert.Equal(t, nodeA.ID(), rec.from)
		assert.Equal(t, head, rec.head)
	case <-time.After(10 * time.Second):
		t.Fatal("announce never arrived")
	}
}

// TestTransportErrorFramePropagation verifies a handler error is returned to
// the fetch caller as a "remote error".
func TestTransportErrorFramePropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 34)
	nodeB := newTestNode(t, ctx, 35)
	connectNodes(t, ctx, nodeA, nodeB)

	handlerB := &stubSyncHandler{
		provideFn: func(peer.ID, []plumbing.Hash, []plumbing.Hash) ([]byte, plumbing.Hash, error) {
			return nil, plumbing.ZeroHash, errors.New("boom packfile")
		},
	}
	trB := NewTransport(nodeB.Host(), handlerB)
	go func() { _ = trB.Start(ctx) }()
	<-trB.Ready()
	waitSyncProtocol(t, nodeA, nodeB.ID())

	trA := NewTransport(nodeA.Host(), &stubSyncHandler{})
	_, _, err := trA.Fetch(ctx, nodeB.ID(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote error")
	assert.Contains(t, err.Error(), "boom packfile")
}

// TestTransportFetchTimeout verifies Fetch fails when the peer accepts the
// stream but never responds, once the packfile timeout expires.
func TestTransportFetchTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 36)
	nodeB := newTestNode(t, ctx, 37)
	connectNodes(t, ctx, nodeA, nodeB)

	release := make(chan struct{})
	defer close(release)
	handlerB := &stubSyncHandler{
		provideFn: func(peer.ID, []plumbing.Hash, []plumbing.Hash) ([]byte, plumbing.Hash, error) {
			<-release // block until the test finishes
			return []byte("late"), testHash(0xFF), nil
		},
	}
	trB := NewTransport(nodeB.Host(), handlerB)
	trB.requestTimeout = 10 * time.Second
	go func() { _ = trB.Start(ctx) }()
	<-trB.Ready()
	waitSyncProtocol(t, nodeA, nodeB.ID())

	trA := NewTransport(nodeA.Host(), &stubSyncHandler{})
	trA.packfileTimeout = 400 * time.Millisecond

	start := time.Now()
	_, _, err := trA.Fetch(ctx, nodeB.ID(), nil, nil)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 15*time.Second, "fetch did not time out promptly")
}

// TestTransportAnnounceUnresponsivePeer verifies AnnounceHead exhausts its
// retries and returns an error when the peer does not run the sync protocol.
func TestTransportAnnounceUnresponsivePeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 38)
	nodeB := newTestNode(t, ctx, 39)
	connectNodes(t, ctx, nodeA, nodeB)

	// Note: no transport is started on nodeB, so the sync protocol is not
	// registered and every NewStream attempt fails fast.
	trA := NewTransport(nodeA.Host(), &stubSyncHandler{})

	start := time.Now()
	err := trA.AnnounceHead(ctx, nodeB.ID(), testHash(0x42))
	require.Error(t, err)
	assert.Less(t, time.Since(start), 20*time.Second)
}
