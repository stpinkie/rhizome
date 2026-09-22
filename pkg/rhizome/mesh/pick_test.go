package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestMeshPickPeer(t *testing.T) {
	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	// No capability advertised yet → nothing to pick.
	_, _, err := meshA.PickPeer("main", "spawn")
	require.Error(t, err)

	// Advertise B's manifest directly.
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID:      meshB.node.PeerID(),
		Agents:      []string{"main"},
		Allows:      map[string]bool{"spawn": true, "delegate": false},
		ActiveTasks: 3,
	})

	pid, c, err := meshA.PickPeer("main", "spawn")
	require.NoError(t, err)
	assert.Equal(t, meshB.node.ID(), pid)
	assert.Equal(t, 3, c.ActiveTasks)

	// Unknown agent or disallowed op are refused.
	_, _, err = meshA.PickPeer("other-agent", "spawn")
	require.Error(t, err)
	_, _, err = meshA.PickPeer("main", "delegate")
	require.Error(t, err)

	// An empty op skips the Allows check.
	_, _, err = meshA.PickPeer("main", "")
	require.NoError(t, err)
}

func TestMeshPickPeerPrefersLeastLoaded(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{Enabled: true, RemoteTimeout: 30 * time.Second}
	meshA, _ := newTaskTestMeshes(t, runFunc, cfg)

	// Bring up a third node connected to A.
	idC := testutil.NewIdentity(t)
	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })

	meshC := NewMesh(nodeC, nil, idC, cfg, runFunc)
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	addrsC := nodeC.BootstrapAddrs()
	require.NotEmpty(t, addrsC)
	require.NoError(t, meshA.Connect(ctx, addrsC[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(nodeC.ID())

	// B is busy, C is idle — both serve the agent.
	// newTaskTestMeshes already connected B; give both manifests.
	for _, m := range meshA.ConnectedTrustedPeers() {
		meshA.SetCapability(m, Capability{
			PeerID: m.String(), Agents: []string{"main"},
			Allows: map[string]bool{"spawn": true}, ActiveTasks: 5,
		})
	}
	meshA.SetCapability(nodeC.ID(), Capability{
		PeerID: nodeC.PeerID(), Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true}, ActiveTasks: 0,
	})

	pid, _, err := meshA.PickPeer("main", "spawn")
	require.NoError(t, err)
	assert.Equal(t, nodeC.ID(), pid, "least-loaded peer should win")
}

func TestRoleScoreBonus(t *testing.T) {
	// Task ops prefer workers; infra ops prefer full nodes. An absent
	// role is a full node (manifests omit the default tier).
	assert.Equal(t, int64(1<<19), roleScoreBonus("spawn", config.MeshRoleWorker))
	assert.Equal(t, int64(1<<19), roleScoreBonus("delegate", config.MeshRoleWorker))
	assert.Zero(t, roleScoreBonus("spawn", config.MeshRoleFull))
	assert.Zero(t, roleScoreBonus("spawn", ""))
	assert.Equal(t, int64(1<<19), roleScoreBonus("sync", config.MeshRoleFull))
	assert.Equal(t, int64(1<<19), roleScoreBonus("sync", ""))
	assert.Zero(t, roleScoreBonus("sync", config.MeshRoleWorker))
	// Bonus stays below the direct-connection term.
	assert.Less(t, int64(1<<19), int64(1<<20))
}

func TestMeshPickPeerRoleAware(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{
		Enabled:       true,
		RemoteTimeout: 30 * time.Second,
		TaskRetries:   0,
		TaskFailover:  false,
		Routing:       config.MeshRoutingConfig{RoleAware: true},
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	// Third node — a full peer connected to A.
	idC := testutil.NewIdentity(t)
	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })

	meshC := NewMesh(nodeC, nil, idC, cfg, runFunc)
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	addrsC := nodeC.BootstrapAddrs()
	require.NotEmpty(t, addrsC)
	require.NoError(t, meshA.Connect(ctx, addrsC[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(nodeC.ID())

	// Equal load: B advertises worker, C advertises full (role omitted).
	for _, m := range meshA.ConnectedTrustedPeers() {
		if m == nodeC.ID() {
			continue
		}
		meshA.SetCapability(m, Capability{
			PeerID: m.String(), Agents: []string{"main"},
			Allows: map[string]bool{"spawn": true, "sync": true},
			Role:   config.MeshRoleWorker,
		})
	}
	meshA.SetCapability(nodeC.ID(), Capability{
		PeerID: nodeC.PeerID(), Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true, "sync": true},
	})

	// Task op: the worker wins at equal load.
	pid, _, err := meshA.PickPeer("main", "spawn")
	require.NoError(t, err)
	assert.Equal(t, meshB.node.ID(), pid, "worker should win spawn at equal load")

	// Infra op: the full node wins.
	pid, _, err = meshA.PickPeer("main", "sync")
	require.NoError(t, err)
	assert.Equal(t, nodeC.ID(), pid, "full node should win sync at equal load")

	// role_aware off: identical scores → deterministic peer-id ordering.
	meshA.cfg.Routing.RoleAware = false
	ranked := meshA.PickPeerRanked("main", "spawn", nil)
	require.Len(t, ranked, 2)
	assert.Equal(t, ranked[0].Score, ranked[1].Score)
}
