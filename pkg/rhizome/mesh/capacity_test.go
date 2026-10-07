package mesh

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// blockingRun returns a runFunc that holds each task open until release is
// closed or the task's context ends — the callee stays at capacity.
func blockingRun(
	release <-chan struct{},
) func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
	return func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return toolshared.NewToolResult("done"), nil, nil
	}
}

func TestCheckCapacityUnit(t *testing.T) {
	owner, err := peer.Decode(testutil.NewIdentity(t).PeerID)
	require.NoError(t, err)
	other, err := peer.Decode(testutil.NewIdentity(t).PeerID)
	require.NoError(t, err)

	mk := func(cfg config.MeshConfig) *Mesh {
		return &Mesh{tasks: NewTaskStore(), cfg: cfg}
	}
	// One running task owned by owner; one terminal task that must not count.
	seed := func(m *Mesh) {
		_, _, err := m.tasks.Submit(owner, agenttask.Request{
			TargetAgentID: "main", SystemPrompt: "x",
		})
		require.NoError(t, err)
		t1, _, err := m.tasks.Submit(other, agenttask.Request{
			TargetAgentID: "main", SystemPrompt: "x",
		})
		require.NoError(t, err)
		m.tasks.Finish(t1.ID, agenttask.StatusDone, nil, "", nil, nil)
	}

	t.Run("global cap", func(t *testing.T) {
		m := mk(config.MeshConfig{MaxConcurrentTasks: 1})
		seed(m)
		err := m.checkCapacity(other)
		require.Error(t, err)
		assert.Equal(t, "capacity: node at 1/1 remote tasks", err.Error())
	})
	t.Run("global unlimited", func(t *testing.T) {
		m := mk(config.MeshConfig{MaxConcurrentTasks: 0})
		seed(m)
		require.NoError(t, m.checkCapacity(other))
	})
	t.Run("per-peer cap", func(t *testing.T) {
		m := mk(config.MeshConfig{
			MaxConcurrentTasks: 8,
			ACL: []config.MeshACLRule{
				{PeerID: owner.String(), MaxConcurrent: 1},
			},
		})
		seed(m)
		err := m.checkCapacity(owner)
		require.Error(t, err)
		assert.Equal(t, "capacity: peer at 1/1", err.Error())
		// A different peer is bounded by the global cap only.
		require.NoError(t, m.checkCapacity(other))
	})
	t.Run("per-peer exempt", func(t *testing.T) {
		m := mk(config.MeshConfig{
			MaxConcurrentTasks: 1, // already saturated globally
			ACL: []config.MeshACLRule{
				{PeerID: owner.String(), MaxConcurrent: -1},
			},
		})
		seed(m)
		require.NoError(t, m.checkCapacity(owner))
	})
}

func TestIsCapacityRejection(t *testing.T) {
	assert.True(t, isCapacityRejection(
		fmt.Errorf("task rejected: capacity: node at 8/8 remote tasks")))
	assert.True(t, isCapacityRejection(
		fmt.Errorf("task rejected: capacity: peer at 2/2")))
	assert.False(t, isCapacityRejection(fmt.Errorf("task rejected: forbidden")))
	assert.False(t, isCapacityRejection(nil))
}

// newCapacityMesh wires A→B with spawn allowed and the task protocol up;
// the callee's runFunc blocks on release so its slots stay occupied.
func newCapacityMesh(
	t *testing.T,
	cfg config.MeshConfig,
) (*Mesh, *Mesh, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	meshA, meshB := newTaskTestMeshes(t, func(
		ctx context.Context, req agentrpc.Request,
	) (*toolshared.ToolResult, error) {
		res, _, err := blockingRun(release)(ctx, req)
		return res, err
	}, cfg)
	waitForTaskProtocol(t, meshA, meshB)
	return meshA, meshB, release
}

func TestMeshCapacityGlobal(t *testing.T) {
	ctx := context.Background()
	cfg := config.MeshConfig{
		Enabled:            true,
		AllowRemoteSpawn:   true,
		RemoteTimeout:      30 * time.Second,
		MaxConcurrentTasks: 1,
	}
	meshA, meshB, _ := newCapacityMesh(t, cfg)

	call := RemoteCall{TargetAgentID: "main", SystemPrompt: "hold"}
	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), call)
	require.NoError(t, err)
	require.NotEmpty(t, taskID)
	require.Equal(t, 1, meshB.tasks.ActiveCount())

	// Second submission hits the node cap and carries the stable class.
	_, err = meshA.SubmitRemoteTask(ctx, meshB.node.ID(), call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capacity: node at 1/1 remote tasks")
}

func TestMeshCapacityPerPeer(t *testing.T) {
	ctx := context.Background()
	cfgB := config.MeshConfig{
		Enabled:            true,
		AllowRemoteSpawn:   true,
		RemoteTimeout:      30 * time.Second,
		MaxConcurrentTasks: 8,
	}
	meshA, meshB, _ := newCapacityMesh(t, cfgB)
	// Bind the ACL rule to A's real peer id.
	meshB.cfg.ACL = []config.MeshACLRule{
		{PeerID: meshA.node.ID().String(), MaxConcurrent: 1},
	}

	call := RemoteCall{TargetAgentID: "main", SystemPrompt: "hold"}
	_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), call)
	require.NoError(t, err)

	_, err = meshA.SubmitRemoteTask(ctx, meshB.node.ID(), call)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capacity: peer at 1/1")
}

func TestMeshCapacityDedupResubmit(t *testing.T) {
	ctx := context.Background()
	cfgB := config.MeshConfig{
		Enabled:            true,
		AllowRemoteSpawn:   true,
		RemoteTimeout:      30 * time.Second,
		MaxConcurrentTasks: 1,
	}
	meshA, meshB, _ := newCapacityMesh(t, cfgB)

	call := RemoteCall{TargetAgentID: "main", SystemPrompt: "hold"}
	corrID := newCorrelationID()
	taskID, err := meshA.submitRemoteTask(ctx, meshB.node.ID(), call, corrID)
	require.NoError(t, err)

	// Resubmitting the same correlation id while saturated returns the
	// existing task instead of a capacity rejection.
	taskID2, err := meshA.submitRemoteTask(ctx, meshB.node.ID(), call, corrID)
	require.NoError(t, err)
	assert.Equal(t, taskID, taskID2)
	assert.Equal(t, 1, meshB.tasks.ActiveCount())
}

func TestMeshCapacityFailover(t *testing.T) {
	ctx := context.Background()

	// B is saturated at cap 1; C has room. SubmitRemoteTaskWithPeer must
	// skip the remaining same-peer attempts and land on C.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	cfgB := config.MeshConfig{
		Enabled:            true,
		AllowRemoteSpawn:   true,
		RemoteTimeout:      30 * time.Second,
		MaxConcurrentTasks: 1,
	}
	cfgC := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB, _ := newCapacityMesh(t, cfgB)
	meshA.cfg.TaskRetries = 3
	meshA.cfg.TaskFailover = true

	idC := testutil.NewIdentity(t)
	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })

	meshC := NewMesh(nodeC, nil, idC, cfgC, blockingRun(release))
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	addrsC := nodeC.BootstrapAddrs()
	require.NotEmpty(t, addrsC)
	require.NoError(t, meshA.Connect(ctx, addrsC[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(nodeC.ID())
	meshC.TrustPeer(meshA.node.ID())
	waitForTaskProtocol(t, meshA, meshC)

	call := RemoteCall{TargetAgentID: "main", SystemPrompt: "hold"}
	// Saturate B.
	_, err = meshA.SubmitRemoteTask(ctx, meshB.node.ID(), call)
	require.NoError(t, err)
	require.Equal(t, 1, meshB.tasks.ActiveCount())

	// Both workers advertise task ops for "main". Capabilities are re-planted
	// inside the submit retry — a late real announce from either worker
	// would clobber them and leave B as the only candidate.
	capSpawn := func() {
		for _, pid := range []peer.ID{meshB.node.ID(), nodeC.ID()} {
			meshA.SetCapability(pid, Capability{
				PeerID: pid.String(), Agents: []string{"main"},
				Allows: map[string]bool{"spawn": true, "delegate": true},
			})
		}
	}

	// The next submit is preferred to B but fails over to C on capacity.
	var used peer.ID
	var taskID string
	var lastErr error
	ok := assert.Eventually(t, func() bool {
		capSpawn()
		var err error
		used, taskID, err = meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), call)
		lastErr = err
		return err == nil
	}, 15*time.Second, 250*time.Millisecond)
	require.True(t, ok, "last submit error: %v", lastErr)
	require.NotEmpty(t, taskID)
	assert.Equal(t, nodeC.ID(), used, "capacity rejection should fail over to C")
	assert.Equal(t, 1, meshB.tasks.ActiveCount())
	assert.GreaterOrEqual(t, meshC.tasks.ActiveCount(), 1)

	// The capacity miss is recorded against B's score — it shows up in
	// peer scoring as a failure with the capacity class in LastError.
	sc, ok := meshA.scoreStore.Get(meshB.node.ID())
	require.True(t, ok)
	assert.Greater(t, sc.Failures, 0)
	assert.Contains(t, sc.LastError, "capacity:")
}
