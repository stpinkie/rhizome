package agenttask

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
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

// stubHandler records invocations and returns a canned response.
type stubHandler struct {
	mu       sync.Mutex
	calls    int
	lastFrom peer.ID
	lastReq  Request
	resp     Response
}

func (h *stubHandler) HandleTaskRequest(from peer.ID, req Request) Response {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	h.lastFrom = from
	h.lastReq = req
	return h.resp
}

func (h *stubHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func startServer(t *testing.T, ctx context.Context, hB host.Host, handler Handler) *Transport {
	t.Helper()
	tr := NewTransport(hB, handler)
	go func() { _ = tr.Start(ctx) }()
	return tr
}

func TestTaskStatusTerminal(t *testing.T) {
	terminal := []TaskStatus{
		StatusDone, StatusError, StatusCancelled, StatusNotFound, StatusRejected,
	}
	for _, s := range terminal {
		assert.True(t, s.Terminal(), "%s should be terminal", s)
	}
	for _, s := range []TaskStatus{StatusAccepted, StatusRunning, TaskStatus("")} {
		assert.False(t, s.Terminal(), "%s should not be terminal", s)
	}
}

func TestOpsAreDistinct(t *testing.T) {
	ops := map[Op]bool{}
	for _, op := range []Op{OpSubmit, OpStatus, OpResult, OpCancel, OpList} {
		assert.NotEmpty(t, op)
		assert.False(t, ops[op], "duplicate op value %q", op)
		ops[op] = true
	}
}

func TestRequestJSONRoundTrip(t *testing.T) {
	req := Request{
		Op:            OpSubmit,
		TaskID:        "task-1",
		CorrelationID: "corr-1",
		TargetAgentID: "main",
		Model:         "openai/gpt-5",
		SystemPrompt:  "do work",
		Tools:         []string{"read_file", "shell"},
		Timeout:       90 * time.Second,
		Wait:          5 * time.Second,
		Nonce:         "abc123",
		Timestamp:     1757000000,
		Signature:     []byte{1, 2, 3},
	}
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var got Request
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, req, got)
}

func TestCallRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{
		TaskID: "task-42",
		Status: StatusAccepted,
	}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	resp, err := tr.Call(ctx, hB.ID(), Request{
		Op:            OpSubmit,
		CorrelationID: "corr-9",
		TargetAgentID: "main",
		SystemPrompt:  "submit me",
		Nonce:         "nonce-9",
		Timestamp:     time.Now().Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, StatusAccepted, resp.Status)
	assert.Equal(t, "task-42", resp.TaskID)

	require.Equal(t, 1, handler.callCount())
	handler.mu.Lock()
	assert.Equal(t, hA.ID(), handler.lastFrom)
	assert.Equal(t, OpSubmit, handler.lastReq.Op)
	assert.Equal(t, "corr-9", handler.lastReq.CorrelationID)
	handler.mu.Unlock()
}

func TestCallListReturnsTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{
		Status: StatusDone,
		Tasks: []TaskInfo{
			{TaskID: "t-1", Status: StatusDone, AgentID: "main"},
			{TaskID: "t-2", Status: StatusRunning, AgentID: "main"},
		},
	}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	resp, err := tr.Call(ctx, hB.ID(), Request{Op: OpList})
	require.NoError(t, err)
	require.Len(t, resp.Tasks, 2)
	assert.Equal(t, "t-1", resp.Tasks[0].TaskID)

	handler.mu.Lock()
	assert.Equal(t, OpList, handler.lastReq.Op)
	handler.mu.Unlock()
}

func TestSupportedFalseForPeerWithoutProtocol(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	tr := NewTransport(hA, &stubHandler{})

	// hB has no task-protocol handler registered.
	assert.False(t, tr.Supported(ctx, hB.ID(), 300*time.Millisecond))
}

func TestSupportedTrueForPeerWithProtocol(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	startServer(t, ctx, hB, &stubHandler{resp: Response{Status: StatusAccepted}})

	tr := NewTransport(hA, &stubHandler{})
	assert.True(t, tr.Supported(ctx, hB.ID(), 5*time.Second))
}

func TestHandleStreamIgnoresUnknownFrameType(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, hB := newTestHosts(t, ctx)
	handler := &stubHandler{resp: Response{Status: StatusAccepted}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	require.True(t, tr.Supported(ctx, hB.ID(), 5*time.Second))

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
	handler := &stubHandler{resp: Response{Status: StatusAccepted}}
	startServer(t, ctx, hB, handler)

	tr := NewTransport(hA, handler)
	require.True(t, tr.Supported(ctx, hB.ID(), 5*time.Second))

	s, err := hA.NewStream(ctx, hB.ID(), ProtocolID)
	require.NoError(t, err)
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(1500*time.Millisecond))
	defer rc.Close()

	_ = rc.WriteFrame(frameRequest, []byte("{not json"))
	_, _, err = rc.ReadFrame()
	require.Error(t, err)
	assert.Equal(t, 0, handler.callCount())
}

func TestRequestFieldsCoverSignedEnvelope(t *testing.T) {
	// Guard against accidentally dropping fields that the mesh security layer
	// signs over: the signed payload is the JSON encoding of the whole
	// request, so every replay-protection field must serialize.
	var req Request
	data := []byte(`{
		"op": "status",
		"task_id": "t-7",
		"correlation_id": "c-7",
		"target_agent_id": "main",
		"model": "m",
		"system_prompt": "p",
		"tools": ["x"],
		"timeout": 1000000000,
		"wait": 2000000000,
		"nonce": "n-7",
		"timestamp": 1757000001,
		"signature": "AAE="
	}`)
	require.NoError(t, json.Unmarshal(data, &req))
	assert.Equal(t, OpStatus, req.Op)
	assert.Equal(t, "t-7", req.TaskID)
	assert.Equal(t, "c-7", req.CorrelationID)
	assert.Equal(t, "n-7", req.Nonce)
	assert.Equal(t, int64(1757000001), req.Timestamp)
	assert.Equal(t, time.Second, req.Timeout)
	assert.Equal(t, 2*time.Second, req.Wait)
	assert.Equal(t, []byte{0, 1}, req.Signature)
	assert.Equal(t, []string{"x"}, req.Tools)

	var resp Response
	require.NoError(t, json.Unmarshal([]byte(`{
		"task_id": "t-7",
		"status": "done",
		"result": {"for_llm": "ok"},
		"signature": "AAE="
	}`), &resp))
	assert.Equal(t, StatusDone, resp.Status)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "ok", resp.Result.ForLLM)
}
