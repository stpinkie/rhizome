// Package agenttask implements the asynchronous mesh task protocol
// (/rhizome/agent-task/1.0.0). Unlike the synchronous /rhizome/agent/1.0.0
// request/response protocol, tasks are submitted with a task id and their
// status, result, and cancellation are addressed by later short-lived RPCs,
// so a long-running remote agent turn does not hold a libp2p stream open.
package agenttask

import (
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

// ProtocolID is the libp2p protocol for asynchronous mesh agent tasks.
const ProtocolID = protocol.ID("/rhizome/agent-task/1.0.0")

const (
	frameRequest  = byte(1)
	frameResponse = byte(2)

	// serverReadTimeout bounds how long the server-side reliable reader waits
	// for the next frame. It must comfortably exceed the longest handler
	// execution — a result request may long-poll for task completion — or
	// the reader's deadline would tear down the connection before the
	// response frame is written.
	serverReadTimeout = 90 * time.Second
)

// Op identifies which task operation a request performs.
type Op string

const (
	// OpSubmit creates a new task and returns its task id.
	OpSubmit Op = "submit"
	// OpStatus returns the current status of a task.
	OpStatus Op = "status"
	// OpResult returns the result of a task, optionally long-polling up to
	// Request.Wait for completion.
	OpResult Op = "result"
	// OpCancel requests cancellation of a running task.
	OpCancel Op = "cancel"
	// OpList lists the tasks owned by the requesting peer.
	OpList Op = "list"
)

// TaskStatus is the lifecycle state of a remote task.
type TaskStatus string

const (
	StatusAccepted  TaskStatus = "accepted"
	StatusRunning   TaskStatus = "running"
	StatusDone      TaskStatus = "done"
	StatusError     TaskStatus = "error"
	StatusCancelled TaskStatus = "cancelled"
	StatusNotFound  TaskStatus = "not_found"
	StatusRejected  TaskStatus = "rejected"
)

// Terminal reports whether the status is a final state.
func (s TaskStatus) Terminal() bool {
	switch s {
	case StatusDone, StatusError, StatusCancelled, StatusNotFound, StatusRejected:
		return true
	}
	return false
}

// Request is a single task-protocol operation sent to a peer.
type Request struct {
	Op Op `json:"op"`

	// TaskID addresses an existing task for status/result/cancel. For submit
	// it is ignored; the server assigns the id.
	TaskID string `json:"task_id,omitempty"`

	// CorrelationID is an optional caller-supplied idempotency key for submit.
	// Resubmitting the same correlation id returns the existing task.
	CorrelationID string `json:"correlation_id,omitempty"`

	TargetAgentID string        `json:"target_agent_id,omitempty"`
	Model         string        `json:"model,omitempty"`
	SystemPrompt  string        `json:"system_prompt,omitempty"`
	Tools         []string      `json:"tools,omitempty"`
	Timeout       time.Duration `json:"timeout,omitempty"`

	// Wait bounds how long a result request may long-poll for completion.
	Wait time.Duration `json:"wait,omitempty"`

	// Nonce and Timestamp are anti-replay fields. The nonce must be unique per
	// sender and the timestamp must be within the mesh replay window.
	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// TaskInfo is a compact view of a task for list responses and status output.
type TaskInfo struct {
	TaskID    string     `json:"task_id"`
	Status    TaskStatus `json:"status"`
	AgentID   string     `json:"agent_id,omitempty"`
	Model     string     `json:"model,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Error     string     `json:"error,omitempty"`
}

// Response is returned for every task-protocol request, including rejections.
type Response struct {
	TaskID    string                 `json:"task_id,omitempty"`
	Status    TaskStatus             `json:"status"`
	Result    *toolshared.ToolResult `json:"result,omitempty"`
	Tasks     []TaskInfo             `json:"tasks,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Signature []byte                 `json:"signature,omitempty"`
}

// Handler processes incoming task requests. Returning a Response (rather than
// an error) lets the callee sign rejections so callers can authenticate them.
type Handler interface {
	HandleTaskRequest(from peer.ID, req Request) Response
}

// Transport provides libp2p stream handling for the task protocol.
type Transport struct {
	host    host.Host
	handler Handler
}

// NewTransport creates a task protocol transport.
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

// Supported reports whether the peer advertises the task protocol.
func (t *Transport) Supported(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	return t.waitForPeerProtocol(ctx, pid, timeout)
}

// waitForPeerProtocol polls until the given peer advertises support for the
// task protocol. It returns false if the context is canceled or the timeout
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

// Call sends one task-protocol request to a peer and returns the response.
func (t *Transport) Call(ctx context.Context, pid peer.ID, req Request) (Response, error) {
	if !t.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return Response{}, fmt.Errorf("peer %s does not support %s", pid, ProtocolID)
	}

	s, err := t.host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return Response{}, fmt.Errorf("open task stream: %w", err)
	}

	rto := 30 * time.Second
	if req.Wait > 0 && req.Wait+10*time.Second > rto {
		rto = req.Wait + 10*time.Second
	}
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(rto), stream.WithWriteTimeout(30*time.Second))
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
		return Response{}, fmt.Errorf("unexpected task frame type: %d", typ)
	}

	var resp Response
	if err = json.Unmarshal(payload, &resp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

func (t *Transport) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(s, stream.WithReadTimeout(serverReadTimeout), stream.WithWriteTimeout(30*time.Second))
	defer rc.Close()

	typ, payload, err := rc.ReadFrame()
	if err != nil || typ != frameRequest {
		return
	}

	var req Request
	if err = json.Unmarshal(payload, &req); err != nil {
		return
	}

	resp := t.handler.HandleTaskRequest(s.Conn().RemotePeer(), req)

	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(frameResponse, data)
}
