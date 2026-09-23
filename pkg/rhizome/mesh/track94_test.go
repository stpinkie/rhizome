package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// usageRun returns a full-signature runFunc that records whether each request
// negotiated usage and reports a fixed usage report.
func usageRun(saw chan bool) func(
	context.Context, agentrpc.Request,
) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
	return func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		select {
		case saw <- req.WantUsage:
		default:
		}
		return toolshared.NewToolResult("done"), &toolshared.RemoteUsage{
			LLMCalls:         2,
			PromptTokens:     120,
			CompletionTokens: 30,
			TotalTokens:      150,
			DurationMS:       42,
		}, nil
	}
}

func usageTestConfig() config.MeshConfig {
	return config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		AllowRemoteSpawn:    true,
		RemoteTimeout:       30 * time.Second,
	}
}

// TestRemoteUsageSyncNegotiated covers the synchronous agent RPC path: when
// the caller's stored manifest advertises allows.usage_report the request
// carries want_usage and the response usage reaches the caller's sink.
func TestRemoteUsageSyncNegotiated(t *testing.T) {
	saw := make(chan bool, 4)
	meshA, meshB := newTaskTestMeshes(t, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("unused"), nil
	}, usageTestConfig())
	meshB.SetRunFunc(usageRun(saw))

	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.ID().String(),
		Agents: []string{"main"},
		Allows: map[string]bool{"delegate": true, "usage_report": true},
	})

	var sink toolshared.RemoteUsage
	res, err := meshA.CallRemote(context.Background(), meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "meter me",
		UsageSink:     &sink,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, <-saw, "callee should see want_usage")
	assert.Equal(t, 2, sink.LLMCalls)
	assert.Equal(t, 120, sink.PromptTokens)
	assert.Equal(t, 30, sink.CompletionTokens)
	assert.Equal(t, 150, sink.TotalTokens)
	assert.Equal(t, int64(42), sink.DurationMS)
}

// TestRemoteUsageSyncNotNegotiated proves the inverse: when the stored
// manifest lacks allows.usage_report, want_usage stays off and no usage is
// returned even though the callee produced one internally.
func TestRemoteUsageSyncNotNegotiated(t *testing.T) {
	saw := make(chan bool, 4)
	meshA, meshB := newTaskTestMeshes(t, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("unused"), nil
	}, usageTestConfig())
	meshB.SetRunFunc(usageRun(saw))

	// Wait for B's real manifest (which advertises usage_report) to land,
	// then overwrite it without the advert so the negotiation is
	// deterministically off; no re-announce fires inside the test window.
	require.Eventually(t, func() bool {
		_, ok := meshA.PeerCapabilities(meshB.node.ID())
		return ok
	}, 10*time.Second, 50*time.Millisecond, "manifest should arrive")
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.ID().String(),
		Agents: []string{"main"},
		Allows: map[string]bool{"delegate": true},
	})

	var sink toolshared.RemoteUsage
	_, err := meshA.CallRemote(context.Background(), meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "no meter",
		UsageSink:     &sink,
	})
	require.NoError(t, err)
	require.False(t, <-saw, "callee must not see want_usage")
	assert.Equal(t, toolshared.RemoteUsage{}, sink, "unnegotiated run must not fill the sink")
}

// TestRemoteUsageAsyncTaskFlow covers the task protocol end to end: submit
// negotiates usage, the callee records it on the task, and the result op
// returns it.
func TestRemoteUsageAsyncTaskFlow(t *testing.T) {
	saw := make(chan bool, 4)
	meshA, meshB := newTaskTestMeshes(t, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("unused"), nil
	}, usageTestConfig())
	meshB.SetRunFunc(usageRun(saw))
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.ID().String(),
		Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true, "usage_report": true},
	})
	waitForTaskProtocol(t, meshA, meshB)

	ctx := context.Background()
	usedPeer, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "async meter",
		Async:         true,
	})
	require.NoError(t, err)

	var resp agenttask.Response
	require.Eventually(t, func() bool {
		resp, err = meshA.RemoteTaskResult(ctx, usedPeer, taskID, 2*time.Second)
		return err == nil && resp.Status == agenttask.StatusDone
	}, 20*time.Second, 200*time.Millisecond, "task should complete")

	require.True(t, <-saw, "callee should see want_usage on the async run")
	require.NotNil(t, resp.Usage, "task result should carry the recorded usage")
	assert.Equal(t, 150, resp.Usage.TotalTokens)

	// The callee's task store recorded the usage on the task itself.
	snap, ok := meshB.tasks.getOwned(taskID, meshA.node.ID())
	require.True(t, ok)
	require.NotNil(t, snap.Usage)
	assert.Equal(t, 2, snap.Usage.LLMCalls)

	// The list op surfaces usage on TaskInfo too.
	infos := meshB.tasks.List(meshA.node.ID())
	require.Len(t, infos, 1)
	require.NotNil(t, infos[0].Usage)
	assert.Equal(t, 150, infos[0].Usage.TotalTokens)
}

// TestRemoteUsageAsyncNotNegotiated covers the task path without the advert:
// the stored task carries no usage and the result response omits it.
func TestRemoteUsageAsyncNotNegotiated(t *testing.T) {
	saw := make(chan bool, 4)
	meshA, meshB := newTaskTestMeshes(t, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("unused"), nil
	}, usageTestConfig())
	meshB.SetRunFunc(usageRun(saw))
	// Same strip-after-arrival pattern as the sync test: the real manifest
	// advertises usage_report, so overwrite it once it lands.
	require.Eventually(t, func() bool {
		_, ok := meshA.PeerCapabilities(meshB.node.ID())
		return ok
	}, 10*time.Second, 50*time.Millisecond, "manifest should arrive")
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.ID().String(),
		Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true},
	})
	waitForTaskProtocol(t, meshA, meshB)

	ctx := context.Background()
	usedPeer, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "async no meter",
		Async:         true,
	})
	require.NoError(t, err)

	var resp agenttask.Response
	require.Eventually(t, func() bool {
		resp, err = meshA.RemoteTaskResult(ctx, usedPeer, taskID, 2*time.Second)
		return err == nil && resp.Status == agenttask.StatusDone
	}, 20*time.Second, 200*time.Millisecond, "task should complete")

	require.False(t, <-saw, "callee must not see want_usage")
	assert.Nil(t, resp.Usage)

	snap, ok := meshB.tasks.getOwned(taskID, meshA.node.ID())
	require.True(t, ok)
	assert.Nil(t, snap.Usage, "unnegotiated task must not store usage")
}

// TestRemoteUsageAuditEntry verifies task.finish audit entries carry the
// recorded usage into mesh-audit.jsonl.
func TestRemoteUsageAuditEntry(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "mesh-audit.jsonl")
	saw := make(chan bool, 4)
	meshA, meshB := newTaskTestMeshes(t, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("unused"), nil
	}, usageTestConfig())
	meshB.SetRunFunc(usageRun(saw))
	meshB.SetAuditPath(auditPath)
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.ID().String(),
		Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true, "usage_report": true},
	})
	waitForTaskProtocol(t, meshA, meshB)

	ctx := context.Background()
	usedPeer, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "audit usage",
		Async:         true,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		resp, err := meshA.RemoteTaskResult(ctx, usedPeer, taskID, 2*time.Second)
		return err == nil && resp.Status == agenttask.StatusDone
	}, 20*time.Second, 200*time.Millisecond)

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	found := false
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["op"] != "task.finish" || entry["ref"] != taskID {
			continue
		}
		found = true
		usage, ok := entry["usage"].(map[string]any)
		require.True(t, ok, "task.finish audit entry should carry usage: %v", entry)
		assert.Equal(t, float64(150), usage["total_tokens"])
		assert.Equal(t, float64(2), usage["llm_calls"])
	}
	require.True(t, found, "no task.finish audit entry for %s", taskID)
}

// TestTaskStorePersistsUsage round-trips a task's usage record through the
// JSONL persistence file (mesh-tasks.jsonl).
func TestTaskStorePersistsUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-tasks.jsonl")
	s := NewTaskStoreWithPath(path)
	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	task, _, err := s.Submit(owner, agenttask.Request{TargetAgentID: "main"})
	require.NoError(t, err)
	s.Start(task.ID, func() {})
	s.Finish(task.ID, agenttask.StatusDone, toolshared.NewToolResult("ok"), "", &toolshared.RemoteUsage{
		LLMCalls:         3,
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		DurationMS:       900,
	})
	s.flushSave()

	s2 := NewTaskStoreWithPath(path)
	_, err = s2.Load()
	require.NoError(t, err)

	snap, ok := s2.getOwned(task.ID, owner)
	require.True(t, ok)
	require.NotNil(t, snap.Usage)
	assert.Equal(t, 3, snap.Usage.LLMCalls)
	assert.Equal(t, 140, snap.Usage.TotalTokens)
	assert.Equal(t, int64(900), snap.Usage.DurationMS)
}

// TestTrack94UsageAdvertSurvivesVerify pins the load-bearing additive-compat
// assumption: allows.usage_report is a map key, not a new struct field, so a
// signed manifest carrying it decodes and re-marshals identically on a peer
// that knows nothing about the feature — the signature still verifies.
func TestTrack94UsageAdvertSurvivesVerify(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	capB := f.meshB.localCapability()
	require.NotEmpty(t, capB.Signature)
	require.True(t, capB.Allows["usage_report"], "local manifest should advertise usage_report")

	// Round-trip the wire form as an old build would: decode and re-marshal
	// (which is what verifyCapability does for the signature check).
	raw, err := json.Marshal(capB)
	require.NoError(t, err)
	var decoded Capability
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.True(t, decoded.Allows["usage_report"], "advert must survive decode")
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded))
}

// TestTrack94AgentRPCOldWireCompat pins the old↔new wire contract for the
// synchronous agent protocol: a pre-usage request marshals byte-identical to
// a zero-usage new request, and an old response decodes with nil usage.
func TestTrack94AgentRPCOldWireCompat(t *testing.T) {
	// Mirrors agentrpc.Request/Response before want_usage/usage existed.
	type oldRequest struct {
		CorrelationID string   `json:"correlation_id"`
		TargetAgentID string   `json:"target_agent_id"`
		Model         string   `json:"model,omitempty"`
		SystemPrompt  string   `json:"system_prompt"`
		Timeout       int64    `json:"timeout,omitempty"`
		Tools         []string `json:"tools,omitempty"`
		Nonce         string   `json:"nonce,omitempty"`
		Timestamp     int64    `json:"timestamp,omitempty"`
		Async         bool     `json:"async,omitempty"`
		Media         []string `json:"media,omitempty"`
	}
	type oldResponse struct {
		CorrelationID string `json:"correlation_id"`
		Nonce         string `json:"nonce,omitempty"`
		Status        string `json:"status"`
		Result        string `json:"result,omitempty"`
		Error         string `json:"error,omitempty"`
	}

	oldReq := oldRequest{
		CorrelationID: "c1", TargetAgentID: "main", SystemPrompt: "hi",
		Timeout: int64(5 * time.Second), Nonce: "n1", Timestamp: 1234,
	}
	oldBytes, err := json.Marshal(oldReq)
	require.NoError(t, err)

	var req agentrpc.Request
	require.NoError(t, json.Unmarshal(oldBytes, &req))
	assert.False(t, req.WantUsage, "old bytes must decode with want_usage off")

	newReq := agentrpc.Request{
		CorrelationID: "c1", TargetAgentID: "main", SystemPrompt: "hi",
		Timeout: 5 * time.Second, Nonce: "n1", Timestamp: 1234,
	}
	newBytes, err := json.Marshal(newReq)
	require.NoError(t, err)
	assert.Equal(t, string(oldBytes), string(newBytes),
		"unnegotiated new request must marshal byte-identical to the old shape")

	oldRespBytes, err := json.Marshal(oldResponse{CorrelationID: "c1", Status: "ok"})
	require.NoError(t, err)
	var resp agentrpc.Response
	require.NoError(t, json.Unmarshal(oldRespBytes, &resp))
	assert.Nil(t, resp.Usage)

	newRespBytes, err := json.Marshal(agentrpc.Response{CorrelationID: "c1", Status: "ok"})
	require.NoError(t, err)
	assert.Equal(t, string(oldRespBytes), string(newRespBytes))
}

// TestTrack94AgentTaskOldWireCompat is the same contract for the async task
// protocol's request/response and TaskInfo list entries.
func TestTrack94AgentTaskOldWireCompat(t *testing.T) {
	type oldRequest struct {
		Op            string   `json:"op"`
		TaskID        string   `json:"task_id,omitempty"`
		CorrelationID string   `json:"correlation_id,omitempty"`
		TargetAgentID string   `json:"target_agent_id,omitempty"`
		Model         string   `json:"model,omitempty"`
		SystemPrompt  string   `json:"system_prompt,omitempty"`
		Tools         []string `json:"tools,omitempty"`
		Timeout       int64    `json:"timeout,omitempty"`
		Media         []string `json:"media,omitempty"`
		Wait          int64    `json:"wait,omitempty"`
		Nonce         string   `json:"nonce,omitempty"`
		Timestamp     int64    `json:"timestamp,omitempty"`
	}
	type oldTaskInfo struct {
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		AgentID   string `json:"agent_id,omitempty"`
		Error     string `json:"error,omitempty"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	type oldResponse struct {
		TaskID string        `json:"task_id,omitempty"`
		Status string        `json:"status"`
		Tasks  []oldTaskInfo `json:"tasks,omitempty"`
		Error  string        `json:"error,omitempty"`
	}

	oldReq := oldRequest{
		Op: "submit", CorrelationID: "c1", TargetAgentID: "main",
		SystemPrompt: "hi", Timeout: int64(5 * time.Second), Nonce: "n1", Timestamp: 7,
	}
	oldBytes, err := json.Marshal(oldReq)
	require.NoError(t, err)

	var req agenttask.Request
	require.NoError(t, json.Unmarshal(oldBytes, &req))
	assert.False(t, req.WantUsage)

	newReq := agenttask.Request{
		Op: agenttask.OpSubmit, CorrelationID: "c1", TargetAgentID: "main",
		SystemPrompt: "hi", Timeout: 5 * time.Second, Nonce: "n1", Timestamp: 7,
	}
	newBytes, err := json.Marshal(newReq)
	require.NoError(t, err)
	assert.Equal(t, string(oldBytes), string(newBytes))

	now := time.Now().UTC().Truncate(time.Second)
	oldRespBytes, err := json.Marshal(oldResponse{
		TaskID: "t1", Status: "done",
		Tasks: []oldTaskInfo{{
			TaskID: "t1", Status: "done", AgentID: "main",
			CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
		}},
	})
	require.NoError(t, err)

	var resp agenttask.Response
	require.NoError(t, json.Unmarshal(oldRespBytes, &resp))
	assert.Nil(t, resp.Usage)
	require.Len(t, resp.Tasks, 1)
	assert.Nil(t, resp.Tasks[0].Usage)

	newRespBytes, err := json.Marshal(agenttask.Response{
		TaskID: "t1", Status: agenttask.StatusDone,
		Tasks: []agenttask.TaskInfo{{
			TaskID: "t1", Status: agenttask.StatusDone, AgentID: "main",
			CreatedAt: now, UpdatedAt: now,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, string(oldRespBytes), string(newRespBytes))
}
