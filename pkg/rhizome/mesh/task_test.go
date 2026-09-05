package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// newTaskTestMeshes starts two meshed nodes that trust each other. The
// returned cleanup stops both meshes and nodes.
func newTaskTestMeshes(
	t *testing.T,
	runFunc func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error),
	cfg config.MeshConfig,
) (*Mesh, *Mesh) {
	t.Helper()
	ctx := context.Background()

	idA, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 1)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() })

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() })

	time.Sleep(500 * time.Millisecond)

	meshA := NewMesh(nodeA, nil, idA, cfg, runFunc)
	require.NoError(t, meshA.Start(ctx))
	t.Cleanup(func() { _ = meshA.Stop() })

	meshB := NewMesh(nodeB, nil, idB, cfg, runFunc)
	require.NoError(t, meshB.Start(ctx))
	t.Cleanup(func() { _ = meshB.Stop() })

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	return meshA, meshB
}

func TestMeshRemoteTaskAsync(t *testing.T) {
	ctx := context.Background()

	var gotReq agentrpc.Request
	runFunc := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
		gotReq = req
		return toolshared.NewToolResult("async result"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	result, err := meshA.CallRemote(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		Model:         "gpt-test",
		SystemPrompt:  "do async work",
		Tools:         []string{"read_file"},
		Async:         true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "async result", result.ForLLM)

	// The task protocol must forward the full dispatch fields.
	assert.Equal(t, "main", gotReq.TargetAgentID)
	assert.Equal(t, "gpt-test", gotReq.Model)
	assert.Equal(t, "do async work", gotReq.SystemPrompt)
	require.Len(t, gotReq.Tools, 1)
	assert.Equal(t, "read_file", gotReq.Tools[0].Name)
	assert.True(t, gotReq.Async)
}

func TestMeshTaskLifecycle(t *testing.T) {
	ctx := context.Background()

	release := make(chan struct{})
	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		<-release
		return toolshared.NewToolResult("finished"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "long task",
	})
	require.NoError(t, err)
	require.NotEmpty(t, taskID)

	// Status should reflect a live task.
	status, err := meshA.RemoteTaskStatus(ctx, meshB.node.ID(), taskID)
	require.NoError(t, err)
	assert.Contains(t, []agenttask.TaskStatus{
		agenttask.StatusAccepted, agenttask.StatusRunning,
	}, status.Status)

	// The task should be visible in the peer's list.
	tasks, err := meshA.ListRemoteTasks(ctx, meshB.node.ID())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, taskID, tasks[0].TaskID)

	// Let the task finish and fetch the result.
	close(release)
	resp, err := meshA.RemoteTaskResult(ctx, meshB.node.ID(), taskID, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusDone, resp.Status)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "finished", resp.Result.ForLLM)
}

func TestMeshTaskCancel(t *testing.T) {
	ctx := context.Background()

	started := make(chan struct{})
	runFunc := func(ctx context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "cancellable task",
	})
	require.NoError(t, err)

	<-started
	resp, err := meshA.CancelRemoteTask(ctx, meshB.node.ID(), taskID)
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusCancelled, resp.Status)

	// A cancelled task reports cancelled to later status queries.
	status, err := meshA.RemoteTaskStatus(ctx, meshB.node.ID(), taskID)
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusCancelled, status.Status)
}

func TestMeshTaskUntrusted(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("nope"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	// B stops trusting A, so A's submission is rejected callee-side. (A still
	// trusts B, so the caller-side trust check does not fire first.)
	meshB.UntrustPeer(meshA.node.ID())

	_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "untrusted task",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not trusted")
}

func TestMeshTaskSpawnDisabled(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("nope"), nil
	}
	cfg := config.MeshConfig{
		Enabled:       true,
		RemoteTimeout: 30 * time.Second,
		// AllowRemoteSpawn intentionally false.
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "rejected task",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestMeshTaskOwnershipIsolation(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "owned task",
	})
	require.NoError(t, err)

	// The task on B is owned by A: looking it up as another peer must fail.
	_, ok := meshB.tasks.getOwned(taskID, meshB.node.ID())
	assert.False(t, ok)
	_, ok = meshB.tasks.getOwned(taskID, meshA.node.ID())
	assert.True(t, ok)

	// A nonexistent task id reports not_found over the wire.
	resp, err := meshA.RemoteTaskStatus(ctx, meshB.node.ID(), "task-nonexistent")
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusNotFound, resp.Status)
}
