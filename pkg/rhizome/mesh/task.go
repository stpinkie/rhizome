package mesh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// taskPollWait is the server-side long-poll bound for result requests. It is
// kept well under the agent turn timeout so a dropped connection only costs
// one extra poll.
const taskPollWait = 30 * time.Second

// RemoteCall describes a task sent to a trusted mesh peer.
type RemoteCall struct {
	TargetAgentID string
	Model         string
	SystemPrompt  string
	Tools         []string
	// Async submits the work through the task protocol (/rhizome/agent-task)
	// and polls for completion instead of holding one RPC stream open. If the
	// peer does not support the task protocol, the call falls back to the
	// synchronous agent protocol.
	Async bool
}

func newTaskNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// --- Callee side: agenttask.Handler ---

// HandleTaskRequest implements agenttask.Handler for incoming task ops.
func (m *Mesh) HandleTaskRequest(from peer.ID, req agenttask.Request) agenttask.Response {
	started := time.Now()
	reject := func(taskID, msg string) agenttask.Response {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "task.request",
			"error":   msg,
			"peer_id": from.String(),
			"op":      string(req.Op),
			"task_id": taskID,
		})
		ref := taskID
		if ref == "" {
			ref = req.CorrelationID
		}
		m.auditMesh(from, string(req.Op), req.TargetAgentID, ref, "rejected", started, msg)
		return m.signedTaskResponse(agenttask.Response{
			TaskID: taskID,
			Status: agenttask.StatusRejected,
			Error:  msg,
		})
	}

	if !m.isTrusted(from) {
		return reject(req.TaskID, fmt.Sprintf("peer %s is not trusted", from))
	}
	if err := m.verifyTaskRequest(from, req); err != nil {
		return reject(req.TaskID, fmt.Sprintf("verify request: %v", err))
	}
	// The task protocol always carries nonce+timestamp (set by
	// signTaskRequest), so replay checks are strict here.
	if err := m.replay.check(from, req.Nonce, req.Timestamp); err != nil {
		return reject(req.TaskID, fmt.Sprintf("replay check failed: %v", err))
	}
	if !m.allowRate(from) {
		return reject(req.TaskID, "rate_limited: too many requests")
	}

	switch req.Op {
	case agenttask.OpSubmit:
		return m.handleTaskSubmit(from, req, started)
	case agenttask.OpStatus:
		resp := m.handleTaskStatus(from, req)
		m.auditMesh(from, string(req.Op), "", req.TaskID, string(resp.Status), started, resp.Error)
		return resp
	case agenttask.OpResult:
		resp := m.handleTaskResult(from, req)
		m.auditMesh(from, string(req.Op), "", req.TaskID, string(resp.Status), started, resp.Error)
		return resp
	case agenttask.OpCancel:
		resp := m.handleTaskCancel(from, req)
		m.auditMesh(from, string(req.Op), "", req.TaskID, string(resp.Status), started, resp.Error)
		return resp
	case agenttask.OpList:
		m.auditMesh(from, string(req.Op), "", "", "ok", started, "")
		return m.signedTaskResponse(agenttask.Response{
			Status: agenttask.StatusDone,
			Tasks:  m.tasks.List(from),
		})
	default:
		return reject(req.TaskID, fmt.Sprintf("unknown task op %q", req.Op))
	}
}

func (m *Mesh) handleTaskSubmit(from peer.ID, req agenttask.Request, started time.Time) agenttask.Response {
	if err := m.checkRemoteAllowed(from, "spawn", req.TargetAgentID); err != nil {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "task.submit",
			"error":   fmt.Sprintf("forbidden: %v", err),
			"peer_id": from.String(),
		})
		m.auditMesh(from, "submit", req.TargetAgentID, req.CorrelationID, "rejected", started, err.Error())
		return m.signedTaskResponse(agenttask.Response{
			Status: agenttask.StatusRejected,
			Error:  fmt.Sprintf("forbidden: %v", err),
		})
	}
	if strings.TrimSpace(req.SystemPrompt) == "" {
		return m.signedTaskResponse(agenttask.Response{
			Status: agenttask.StatusRejected,
			Error:  "system_prompt is required",
		})
	}

	task, created, err := m.tasks.Submit(from, req)
	if err != nil {
		return m.signedTaskResponse(agenttask.Response{
			Status: agenttask.StatusRejected,
			Error:  err.Error(),
		})
	}
	if !created {
		// Idempotent resubmit: report the existing task.
		return m.signedTaskResponse(agenttask.Response{
			TaskID: task.ID,
			Status: task.Status,
		})
	}

	m.publishMeshEvent(runtimeevents.KindMeshTaskSubmit, map[string]any{
		"peer_id":        from.String(),
		"task_id":        task.ID,
		"agent_id":       req.TargetAgentID,
		"model":          req.Model,
		"correlation_id": req.CorrelationID,
	})
	m.auditMesh(from, "submit", req.TargetAgentID, task.ID, string(task.Status), started, "")

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.runMeshTask(task, req)
	}()

	return m.signedTaskResponse(agenttask.Response{
		TaskID: task.ID,
		Status: task.Status,
	})
}

// runMeshTask executes an accepted task through the mesh run function and
// records the terminal state in the task store.
func (m *Mesh) runMeshTask(task *MeshTask, req agenttask.Request) {
	started := time.Now()
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = m.cfg.RemoteTimeout
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	m.tasks.Start(task.ID, cancel)

	m.publishMeshEvent(runtimeevents.KindMeshTaskUpdate, map[string]any{
		"peer_id": task.Owner.String(),
		"task_id": task.ID,
		"status":  string(agenttask.StatusRunning),
	})

	var result *toolshared.ToolResult
	var runErr error
	if m.runFunc == nil {
		runErr = fmt.Errorf("remote agent execution is not configured")
	} else {
		result, runErr = m.runFunc(ctx, agentrpc.Request{
			CorrelationID: task.ID,
			TargetAgentID: req.TargetAgentID,
			Model:         req.Model,
			SystemPrompt:  req.SystemPrompt,
			Timeout:       timeout,
			Tools:         toolNamesToRefs(req.Tools),
			Async:         true,
		})
	}

	status := agenttask.StatusDone
	errMsg := ""
	if runErr != nil {
		status = agenttask.StatusError
		errMsg = runErr.Error()
	}
	m.tasks.Finish(task.ID, status, result, errMsg)

	m.publishMeshEvent(runtimeevents.KindMeshTaskUpdate, map[string]any{
		"peer_id":  task.Owner.String(),
		"task_id":  task.ID,
		"agent_id": req.TargetAgentID,
		"status":   string(status),
		"error":    errMsg,
	})
	m.auditMesh(task.Owner, "task.finish", req.TargetAgentID, task.ID, string(status), started, errMsg)
}

func (m *Mesh) handleTaskStatus(from peer.ID, req agenttask.Request) agenttask.Response {
	task, ok := m.tasks.getOwned(req.TaskID, from)
	if !ok {
		return m.signedTaskResponse(agenttask.Response{
			TaskID: req.TaskID,
			Status: agenttask.StatusNotFound,
			Error:  "task not found",
		})
	}
	return m.signedTaskResponse(agenttask.Response{
		TaskID: task.ID,
		Status: task.Status,
		Error:  task.Err,
	})
}

func (m *Mesh) handleTaskResult(from peer.ID, req agenttask.Request) agenttask.Response {
	wait := req.Wait
	if wait > taskPollWait {
		wait = taskPollWait
	}
	task, ok := m.tasks.Wait(m.ctx, req.TaskID, from, wait)
	if !ok {
		return m.signedTaskResponse(agenttask.Response{
			TaskID: req.TaskID,
			Status: agenttask.StatusNotFound,
			Error:  "task not found",
		})
	}
	return m.signedTaskResponse(agenttask.Response{
		TaskID: task.ID,
		Status: task.Status,
		Result: task.Result,
		Error:  task.Err,
	})
}

func (m *Mesh) handleTaskCancel(from peer.ID, req agenttask.Request) agenttask.Response {
	if !m.tasks.Cancel(req.TaskID, from) {
		return m.signedTaskResponse(agenttask.Response{
			TaskID: req.TaskID,
			Status: agenttask.StatusNotFound,
			Error:  "task not found",
		})
	}
	m.publishMeshEvent(runtimeevents.KindMeshTaskUpdate, map[string]any{
		"peer_id": from.String(),
		"task_id": req.TaskID,
		"status":  string(agenttask.StatusCancelled),
	})
	return m.signedTaskResponse(agenttask.Response{
		TaskID: req.TaskID,
		Status: agenttask.StatusCancelled,
	})
}

// --- Task request/response signing ---

func (m *Mesh) signTaskRequest(req *agenttask.Request) error {
	req.Signature = nil
	req.Timestamp = time.Now().Unix()
	if req.Nonce == "" {
		req.Nonce = newTaskNonce()
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode task request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)
	return nil
}

func (m *Mesh) verifyTaskRequest(from peer.ID, req agenttask.Request) error {
	if len(req.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	sig := req.Signature
	req.Signature = nil

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", from)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify request signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid request signature from peer %s", from)
	}
	return nil
}

func (m *Mesh) signedTaskResponse(resp agenttask.Response) agenttask.Response {
	resp.Signature = nil
	payload, err := json.Marshal(resp)
	if err != nil {
		return resp
	}
	resp.Signature = identity.Sign(m.id.PrivateKey, payload)
	return resp
}

func (m *Mesh) verifyTaskResponse(pid peer.ID, resp *agenttask.Response) error {
	if len(resp.Signature) == 0 {
		return fmt.Errorf("missing response signature")
	}
	sig := resp.Signature
	resp.Signature = nil
	defer func() { resp.Signature = sig }()

	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	pub := m.host.Peerstore().PubKey(pid)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", pid)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify response signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid response signature from peer %s", pid)
	}
	return nil
}

// callRemoteTask runs an async remote call over the task protocol: submit the
// task, then long-poll for its result. It returns ok=false when the peer does
// not advertise the task protocol so the caller can fall back to the
// synchronous agent protocol.
func (m *Mesh) callRemoteTask(
	ctx context.Context,
	pid peer.ID,
	call RemoteCall,
) (*toolshared.ToolResult, error, bool) {
	if !m.taskRPC.Supported(ctx, pid, 3*time.Second) {
		return nil, nil, false
	}

	startKind, endKind := m.remoteAgentEventKinds(true)
	m.publishMeshEvent(startKind, map[string]any{
		"peer_id":  pid.String(),
		"agent_id": call.TargetAgentID,
		"async":    true,
		"protocol": string(agenttask.ProtocolID),
	})

	var taskID string
	var lastErr error
	// All retries share one correlation id so a resubmission after a lost
	// response is deduplicated by the callee's task store instead of
	// spawning a duplicate task.
	corrID := newCorrelationID()
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			m.node.ForceReconnect(ctx, pid)
			select {
			case <-ctx.Done():
				return nil, ctx.Err(), true
			case <-time.After(250 * time.Millisecond):
			}
		}
		taskID, lastErr = m.submitRemoteTask(ctx, pid, call, corrID)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		m.publishMeshEvent(endKind, map[string]any{
			"peer_id":  pid.String(),
			"agent_id": call.TargetAgentID,
			"async":    true,
			"error":    lastErr.Error(),
		})
		return nil, fmt.Errorf("submit task: %w", lastErr), true
	}

	m.publishMeshEvent(runtimeevents.KindMeshTaskSubmit, map[string]any{
		"peer_id":  pid.String(),
		"agent_id": call.TargetAgentID,
		"task_id":  taskID,
		"outgoing": true,
	})

	result, err := m.pollRemoteTask(ctx, pid, taskID)
	m.publishMeshEvent(endKind, map[string]any{
		"peer_id":  pid.String(),
		"agent_id": call.TargetAgentID,
		"task_id":  taskID,
		"async":    true,
		"error":    errString(err),
	})
	return result, err, true
}

// pollRemoteTask long-polls a remote task until it reaches a terminal state,
// the caller's context is canceled, or too many consecutive transport errors
// occur. On caller cancellation it makes a best-effort remote cancel.
func (m *Mesh) pollRemoteTask(
	ctx context.Context,
	pid peer.ID,
	taskID string,
) (*toolshared.ToolResult, error) {
	const maxConsecutiveFailures = 5
	failures := 0
	for {
		resp, err := m.RemoteTaskResult(ctx, pid, taskID, taskPollWait)
		if err != nil {
			if ctx.Err() != nil {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = m.CancelRemoteTask(cancelCtx, pid, taskID)
				cancel()
				return nil, ctx.Err()
			}
			failures++
			if failures >= maxConsecutiveFailures {
				return nil, fmt.Errorf("poll task %s: %w", taskID, err)
			}
			m.node.ForceReconnect(ctx, pid)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(failures) * 500 * time.Millisecond):
			}
			continue
		}
		failures = 0

		switch resp.Status {
		case agenttask.StatusDone:
			return resp.Result, nil
		case agenttask.StatusError:
			return nil, fmt.Errorf("remote task failed: %s", resp.Error)
		case agenttask.StatusCancelled:
			return nil, fmt.Errorf("remote task %s was cancelled", taskID)
		case agenttask.StatusNotFound:
			return nil, fmt.Errorf("remote task %s not found", taskID)
		case agenttask.StatusRejected:
			return nil, fmt.Errorf("remote task rejected: %s", resp.Error)
		default:
			// accepted/running: keep polling.
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func toolNamesToRefs(names []string) []agentrpc.ToolRef {
	if len(names) == 0 {
		return nil
	}
	refs := make([]agentrpc.ToolRef, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			refs = append(refs, agentrpc.ToolRef{Name: n})
		}
	}
	return refs
}

// --- Caller side ---

// SubmitRemoteTask submits an asynchronous task to a trusted peer and returns
// the server-assigned task id.
func (m *Mesh) SubmitRemoteTask(ctx context.Context, pid peer.ID, call RemoteCall) (string, error) {
	return m.submitRemoteTask(ctx, pid, call, newCorrelationID())
}

// submitRemoteTask submits with an explicit correlation id so that retries of
// the same logical call are deduplicated by the callee's task store.
func (m *Mesh) submitRemoteTask(ctx context.Context, pid peer.ID, call RemoteCall, corrID string) (string, error) {
	if !m.isTrusted(pid) {
		return "", fmt.Errorf("peer %s is not trusted", pid)
	}
	req := agenttask.Request{
		Op:            agenttask.OpSubmit,
		CorrelationID: corrID,
		TargetAgentID: call.TargetAgentID,
		Model:         call.Model,
		SystemPrompt:  call.SystemPrompt,
		Tools:         call.Tools,
		Timeout:       m.cfg.RemoteTimeout,
	}
	if err := m.signTaskRequest(&req); err != nil {
		return "", err
	}
	start := time.Now()
	resp, err := m.taskRPC.Call(ctx, pid, req)
	latency := time.Since(start)
	if err != nil {
		m.recordPeerCall(pid, false, latency, err)
		return "", err
	}
	if err := m.verifyTaskResponse(pid, &resp); err != nil {
		m.recordPeerCall(pid, false, latency, err)
		return "", fmt.Errorf("verify response: %w", err)
	}
	if resp.Status == agenttask.StatusRejected {
		err := fmt.Errorf("task rejected: %s", resp.Error)
		m.recordPeerCall(pid, false, latency, err)
		return "", err
	}
	if resp.TaskID == "" {
		err := fmt.Errorf("peer returned no task id")
		m.recordPeerCall(pid, false, latency, err)
		return "", err
	}
	m.recordPeerCall(pid, true, latency, nil)
	return resp.TaskID, nil
}

// RemoteTaskStatus fetches the current status of a task on a trusted peer.
func (m *Mesh) RemoteTaskStatus(ctx context.Context, pid peer.ID, taskID string) (agenttask.Response, error) {
	return m.taskCall(ctx, pid, agenttask.Request{Op: agenttask.OpStatus, TaskID: taskID})
}

// RemoteTaskResult fetches a task result, long-polling up to wait.
func (m *Mesh) RemoteTaskResult(ctx context.Context, pid peer.ID, taskID string, wait time.Duration) (agenttask.Response, error) {
	return m.taskCall(ctx, pid, agenttask.Request{Op: agenttask.OpResult, TaskID: taskID, Wait: wait})
}

// CancelRemoteTask asks a peer to cancel a running task.
func (m *Mesh) CancelRemoteTask(ctx context.Context, pid peer.ID, taskID string) (agenttask.Response, error) {
	return m.taskCall(ctx, pid, agenttask.Request{Op: agenttask.OpCancel, TaskID: taskID})
}

// ListRemoteTasks lists the tasks this node has submitted to a peer.
func (m *Mesh) ListRemoteTasks(ctx context.Context, pid peer.ID) ([]agenttask.TaskInfo, error) {
	resp, err := m.taskCall(ctx, pid, agenttask.Request{Op: agenttask.OpList})
	if err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// taskCall signs, sends, and verifies a task-protocol request.
func (m *Mesh) taskCall(ctx context.Context, pid peer.ID, req agenttask.Request) (agenttask.Response, error) {
	if !m.isTrusted(pid) {
		return agenttask.Response{}, fmt.Errorf("peer %s is not trusted", pid)
	}
	if err := m.signTaskRequest(&req); err != nil {
		return agenttask.Response{}, err
	}
	start := time.Now()
	resp, err := m.taskRPC.Call(ctx, pid, req)
	latency := time.Since(start)
	if err != nil {
		m.recordPeerCall(pid, false, latency, err)
		return agenttask.Response{}, err
	}
	if err := m.verifyTaskResponse(pid, &resp); err != nil {
		m.recordPeerCall(pid, false, latency, err)
		return agenttask.Response{}, fmt.Errorf("verify response: %w", err)
	}
	if resp.Status == agenttask.StatusRejected {
		err := fmt.Errorf("task request rejected: %s", resp.Error)
		m.recordPeerCall(pid, false, latency, err)
		return resp, err
	}
	m.recordPeerCall(pid, true, latency, nil)
	return resp, nil
}
