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

// newFanoutTestMeshes starts a caller mesh A and two worker meshes B and C
// that trust A and advertise spawn capability for agent "main" over the caps
// protocol. The returned meshes are (caller, workerB, workerC).
func newFanoutTestMeshes(
	t *testing.T,
	runFuncB, runFuncC func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error),
) (*Mesh, *Mesh, *Mesh) {
	t.Helper()
	ctx := context.Background()

	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}

	idA, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 1)
	require.NoError(t, err)
	idC, _, err := identity.FromMnemonic(testMnemonic, 7)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() })

	// Start the caller mesh first so its protocol handlers are registered
	// before worker nodes connect; this avoids identify races where workers
	// do not see the caller's capability/task protocols.
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	t.Cleanup(func() { _ = meshA.Stop() })

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() })

	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })

	// Start worker meshes before connecting them so their protocol handlers
	// are registered before the libp2p identify exchange with the caller.
	meshB := NewMesh(nodeB, nil, idB, cfg, runFuncB)
	require.NoError(t, meshB.Start(ctx))
	t.Cleanup(func() { _ = meshB.Stop() })

	meshC := NewMesh(nodeC, nil, idC, cfg, runFuncC)
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	require.NoError(t, nodeB.Connect(ctx, addrsA[0]))
	require.NoError(t, nodeC.Connect(ctx, addrsA[0]))

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID())) &&
			network.IsConnectednessUp(nodeA.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond, "workers should connect to nodeA")

	meshA.TrustPeer(nodeB.ID())
	meshA.TrustPeer(nodeC.ID())
	meshB.TrustPeer(nodeA.ID())
	meshC.TrustPeer(nodeA.ID())

	// Workers push their capability manifests to A over /rhizome/caps/1.0.0.
	// Re-advertise on each poll in case the first announce raced protocol
	// identification. Also wait for the task protocol to be advertised by
	// both workers so the first FanoutTask does not race the libp2p identify
	// exchange. Under concurrent test load the identify exchange can take
	// a while, so use a generous timeout.
	require.Eventually(t, func() bool {
		meshB.Advertise(ctx)
		meshC.Advertise(ctx)
		capB, okB := meshA.PeerCapabilities(nodeB.ID())
		capC, okC := meshA.PeerCapabilities(nodeC.ID())
		if !okB || !okC ||
			!capabilityServes(capB, "main", "spawn") ||
			!capabilityServes(capC, "main", "spawn") {
			return false
		}
		// Probe the task protocol with a short timeout rather than relying on
		// the peerstore being updated by an identify push.
		return meshA.taskRPC.Supported(ctx, nodeB.ID(), 10*time.Second) &&
			meshA.taskRPC.Supported(ctx, nodeC.ID(), 10*time.Second)
	}, 60*time.Second, 100*time.Millisecond, "worker capabilities and task protocol should reach A")

	return meshA, meshB, meshC
}

func TestMeshFanoutAll(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok-" + req.TargetAgentID), nil
	}
	meshA, meshB, meshC := newFanoutTestMeshes(t, runFunc, runFunc)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID: "main",
		Task:    "say hi",
		// N=0 defaults to all capable peers; empty strategy defaults to "all".
	})
	require.NoError(t, err)
	assert.Equal(t, FanoutStrategyAll, res.Strategy)
	assert.NotEmpty(t, res.FanoutID)
	require.Len(t, res.Branches, 2)

	peers := map[string]bool{}
	for _, b := range res.Branches {
		assert.Equal(t, string(agenttask.StatusDone), b.Status)
		assert.NotEmpty(t, b.TaskID)
		require.NotNil(t, b.Result)
		assert.Equal(t, "ok-main", b.Result.ForLLM)
		peers[b.PeerID] = true
	}
	assert.True(t, peers[meshB.node.ID().String()], "worker B should be a branch")
	assert.True(t, peers[meshC.node.ID().String()], "worker C should be a branch")
	assert.NotEmpty(t, res.Winner)
}

func TestMeshFanoutFirst(t *testing.T) {
	ctx := context.Background()

	fast := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("fast"), nil
	}
	slow := func(ctx context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	meshA, meshB, meshC := newFanoutTestMeshes(t, fast, slow)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID:  "main",
		Task:     "race",
		N:        2,
		Strategy: FanoutStrategyFirst,
		Wait:     5 * time.Second,
	})
	require.NoError(t, err)
	require.Len(t, res.Branches, 2)
	assert.Equal(t, meshB.node.ID().String(), res.Winner)

	byPeer := map[string]FanoutBranch{}
	for _, b := range res.Branches {
		byPeer[b.PeerID] = b
	}
	winner := byPeer[res.Winner]
	assert.Equal(t, string(agenttask.StatusDone), winner.Status)
	require.NotNil(t, winner.Result)
	assert.Equal(t, "fast", winner.Result.ForLLM)

	// The losing branch is best-effort cancelled once the winner is known.
	loser := byPeer[meshC.node.ID().String()]
	assert.NotEmpty(t, loser.TaskID)
	assert.Contains(t, []string{
		string(agenttask.StatusCancelled), "error",
	}, loser.Status)
}

func TestMeshFanoutQuorum(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("same answer"), nil
	}
	meshA, meshB, meshC := newFanoutTestMeshes(t, runFunc, runFunc)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID:  "main",
		Task:     "vote",
		N:        2,
		Strategy: FanoutStrategyQuorum,
		K:        2,
	})
	require.NoError(t, err)
	require.Len(t, res.Branches, 2)
	for _, b := range res.Branches {
		assert.Equal(t, string(agenttask.StatusDone), b.Status)
	}
	// Identical results: the plurality winner is whichever finished first.
	assert.Contains(t, []string{
		meshB.node.ID().String(), meshC.node.ID().String(),
	}, res.Winner)
}

func TestMeshFanoutQuorumPending(t *testing.T) {
	ctx := context.Background()

	fast := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("early"), nil
	}
	slow := func(ctx context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	meshA, meshB, meshC := newFanoutTestMeshes(t, fast, slow)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID:  "main",
		Task:     "partial quorum",
		N:        2,
		Strategy: FanoutStrategyQuorum,
		K:        1,
		Wait:     5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, meshB.node.ID().String(), res.Winner)

	byPeer := map[string]FanoutBranch{}
	for _, b := range res.Branches {
		byPeer[b.PeerID] = b
	}
	assert.Equal(t, string(agenttask.StatusDone), byPeer[res.Winner].Status)
	// Quorum does not cancel stragglers; the unfinished branch is pending.
	assert.Equal(t, fanoutStatusPending, byPeer[meshC.node.ID().String()].Status)
}

func TestMeshFanoutNoCapablePeer(t *testing.T) {
	ctx := context.Background()

	id, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	node, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Close() })

	m := NewMesh(node, nil, id, config.MeshConfig{Enabled: true, AllowRemoteSpawn: true}, nil)
	require.NoError(t, m.Start(ctx))
	t.Cleanup(func() { _ = m.Stop() })

	_, err = m.FanoutTask(ctx, FanoutRequest{AgentID: "main", Task: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no capable trusted peer")

	_, err = m.FanoutTask(ctx, FanoutRequest{AgentID: "main", Task: "x", Strategy: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown fanout strategy")
}
