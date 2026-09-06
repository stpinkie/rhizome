package agentrpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// newTestHosts creates two connected libp2p hosts on the loopback interface.
func newTestHosts(t *testing.T, ctx context.Context) (host.Host, host.Host) {
	t.Helper()

	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = hA.Close() })

	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = hB.Close() })

	require.NoError(t, hA.Connect(ctx, peer.AddrInfo{ID: hB.ID(), Addrs: hB.Addrs()}))
	return hA, hB
}

// stubHandler records invocations and returns a canned response or error.
type stubHandler struct {
	mu       sync.Mutex
	calls    int
	lastFrom peer.ID
	lastReq  Request
	resp     Response
	err      error
}

func (h *stubHandler) HandleRequest(from peer.ID, req Request) (Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	h.lastFrom = from
	h.lastReq = req
	if h.err != nil {
		return Response{}, h.err
	}
	return h.resp, nil
}

func (h *stubHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// startServer registers the protocol handler on hB and returns the transport.
func startServer(t *testing.T, ctx context.Context, hB host.Host, handler Handler) *Transport {
	t.Helper()
	tr := NewTransport(hB, handler)
	go func() { _ = tr.Start(ctx) }()
	return tr
}

func TestCallRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{
		Status: "ok",
		Result: toolshared.NewToolResult("pong"),
	}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	resp, err := tr.Call(ctx, hB.ID(), Request{
		CorrelationID: "corr-1",
		TargetAgentID: "main",
		SystemPrompt:  "ping",
		Nonce:         "nonce-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "pong", resp.Result.ForLLM)
	// The transport echoes the request correlation id and nonce.
	assert.Equal(t, "nonce-1", resp.Nonce)

	require.Equal(t, 1, handler.callCount())
	handler.mu.Lock()
	assert.Equal(t, hA.ID(), handler.lastFrom)
	assert.Equal(t, "corr-1", handler.lastReq.CorrelationID)
	assert.Equal(t, "main", handler.lastReq.TargetAgentID)
	handler.mu.Unlock()
}

func TestCallHandlerErrorBecomesErrorResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{err: errors.New("boom: agent exploded")}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	resp, err := tr.Call(ctx, hB.ID(), Request{
		CorrelationID: "corr-err",
		Nonce:         "nonce-err",
	})
	require.NoError(t, err)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "boom")
	assert.Equal(t, "corr-err", resp.CorrelationID)
	assert.Equal(t, "nonce-err", resp.Nonce)
}

func TestNonceEchoedWhenHandlerOmitsIt(t *testing.T) {
	tr := &Transport{
		handler:   &stubHandler{resp: Response{Status: "ok"}},
		results:   make(map[string]cachedResult),
		resultTTL: time.Minute,
	}

	resp := tr.handleRequestWithCache(peer.ID("peer-a"), Request{
		CorrelationID: "corr-nonce",
		Nonce:         "nonce-echo",
	})
	assert.Equal(t, "nonce-echo", resp.Nonce)
}

func TestIdempotentDuplicateCorrelationID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{Status: "ok", Result: toolshared.NewToolResult("once")}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	req := Request{CorrelationID: "dup-1", Nonce: "n-1"}

	first, err := tr.Call(ctx, hB.ID(), req)
	require.NoError(t, err)
	second, err := tr.Call(ctx, hB.ID(), req)
	require.NoError(t, err)

	assert.Equal(t, first.Status, second.Status)
	assert.Equal(t, first.Result.ForLLM, second.Result.ForLLM)
	// The duplicate was served from the idempotency cache, not re-executed.
	assert.Equal(t, 1, handler.callCount())
}

func TestResultCacheScopedByPeer(t *testing.T) {
	handler := &stubHandler{resp: Response{Status: "ok"}}
	tr := &Transport{
		handler:   handler,
		results:   make(map[string]cachedResult),
		resultTTL: time.Minute,
	}
	req := Request{CorrelationID: "shared-corr", Nonce: "n"}

	tr.handleRequestWithCache(peer.ID("peer-a"), req)
	tr.handleRequestWithCache(peer.ID("peer-a"), req) // cache hit
	assert.Equal(t, 1, handler.callCount())

	// A different peer presenting the same correlation id must not receive
	// peer A's cached response — the key is scoped by the authenticated peer.
	tr.handleRequestWithCache(peer.ID("peer-b"), req)
	assert.Equal(t, 2, handler.callCount())
}

func TestResultCacheEvictsOldestBeyondBound(t *testing.T) {
	handler := &stubHandler{resp: Response{Status: "ok"}}
	tr := &Transport{
		handler:   handler,
		results:   make(map[string]cachedResult),
		resultTTL: time.Minute,
	}
	from := peer.ID("peer-a")

	total := maxCachedResults + 10
	for i := 0; i < total; i++ {
		tr.handleRequestWithCache(from, Request{CorrelationID: fmt.Sprintf("c-%d", i)})
	}
	assert.Equal(t, total, handler.callCount())
	assert.LessOrEqual(t, len(tr.results), maxCachedResults)

	// The earliest entries were evicted; recent ones remain cached.
	_, ok := tr.results[resultCacheKey(from, "c-0")]
	assert.False(t, ok, "oldest entry should have been evicted")
	_, ok = tr.results[resultCacheKey(from, fmt.Sprintf("c-%d", total-1))]
	assert.True(t, ok, "newest entry should still be cached")
}

func TestResultCacheExpiry(t *testing.T) {
	handler := &stubHandler{resp: Response{Status: "ok"}}
	tr := &Transport{
		handler:   handler,
		results:   make(map[string]cachedResult),
		resultTTL: -time.Second, // entries expire immediately
	}
	req := Request{CorrelationID: "expiring", Nonce: "n"}

	tr.handleRequestWithCache(peer.ID("peer-a"), req)
	tr.handleRequestWithCache(peer.ID("peer-a"), req)
	// Expired entries are never served, so the handler ran twice.
	assert.Equal(t, 2, handler.callCount())
}

func TestWaitForPeerProtocolUnsupported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	tr := NewTransport(hA, &stubHandler{})

	// hB never registered the protocol, so detection fails fast with a short
	// timeout instead of hanging for the Call default.
	assert.False(t, tr.waitForPeerProtocol(ctx, hB.ID(), 300*time.Millisecond))
}

func TestHandleStreamIgnoresUnknownFrameType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{Status: "ok"}}
	startServer(t, ctx, hB, handler)

	// Wait until B advertises the protocol, then open a stream and send a
	// frame with a type the server does not handle.
	tr := NewTransport(hA, handler)
	require.True(t, tr.waitForPeerProtocol(ctx, hB.ID(), 5*time.Second))

	s, err := hA.NewStream(ctx, hB.ID(), ProtocolID)
	require.NoError(t, err)
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(1500*time.Millisecond))
	defer rc.Close()

	// The server drops the frame and closes; the write may race the close.
	_ = rc.WriteFrame(0x7F, []byte("not a request"))
	_, _, err = rc.ReadFrame()
	require.Error(t, err)
	assert.Equal(t, 0, handler.callCount())
}

func TestHandleStreamIgnoresMalformedJSON(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{Status: "ok"}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	require.True(t, tr.waitForPeerProtocol(ctx, hB.ID(), 5*time.Second))

	s, err := hA.NewStream(ctx, hB.ID(), ProtocolID)
	require.NoError(t, err)
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(1500*time.Millisecond))
	defer rc.Close()

	_ = rc.WriteFrame(frameRequest, []byte("{not json"))
	_, _, err = rc.ReadFrame()
	require.Error(t, err)
	assert.Equal(t, 0, handler.callCount())
}
