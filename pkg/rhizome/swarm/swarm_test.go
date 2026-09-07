package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// newTestPair starts two in-process nodes with swarm layers that trust each
// other and returns the swarm managers.
func newTestPair(t *testing.T, cfg config.SwarmConfig, homeA, homeB string) (*Swarm, *Swarm) {
	t.Helper()
	ctx := context.Background()

	idA, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 1)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
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

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	trusted := map[peer.ID]bool{nodeA.ID(): true, nodeB.ID(): true}
	trustFn := func(pid peer.ID) bool { return trusted[pid] }

	swarmA := New(nodeA, idA, cfg, trustFn, nil, homeA)
	require.NoError(t, swarmA.Start(ctx))
	t.Cleanup(func() { _ = swarmA.Stop() })

	swarmB := New(nodeB, idB, cfg, trustFn, nil, homeB)
	require.NoError(t, swarmB.Start(ctx))
	t.Cleanup(func() { _ = swarmB.Stop() })

	return swarmA, swarmB
}

func TestSwarmJoinAndRoster(t *testing.T) {
	swarmA, swarmB := newTestPair(t, config.DefaultSwarmConfig(), t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "ops"))
	require.NoError(t, swarmB.Join(context.Background(), "ops"))

	// Both sides should learn each other through JOIN/JOIN_ACK + gossip.
	require.Eventually(t, func() bool {
		return len(swarmA.Members("ops")) >= 1 && len(swarmB.Members("ops")) >= 1
	}, 10*time.Second, 100*time.Millisecond, "members should discover each other")

	membersA := swarmA.Members("ops")
	assert.Equal(t, swarmB.PeerID(), membersA[0].PeerID)
}

func TestSwarmUntrustedJoinRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(testMnemonic, 0)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 1)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	defer nodeB.Close()

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond)

	cfg := config.DefaultSwarmConfig()
	// A trusts nobody; B trusts A.
	swarmA := New(nodeA, idA, cfg, func(peer.ID) bool { return false }, nil, t.TempDir())
	require.NoError(t, swarmA.Start(ctx))
	defer swarmA.Stop()

	swarmB := New(nodeB, idB, cfg, func(pid peer.ID) bool { return pid == nodeA.ID() }, nil, t.TempDir())
	require.NoError(t, swarmB.Start(ctx))
	defer swarmB.Stop()

	require.NoError(t, swarmB.Join(ctx, "ops"))

	// A is not a member of "ops" anyway, but importantly A must never record
	// B's join because B is untrusted from A's perspective.
	swarmA.joinLocal("ops")
	env := Envelope{SwarmID: "ops", Type: MsgJoin}
	require.NoError(t, swarmB.sign(&env))
	resp, err := swarmB.transport.Call(ctx, nodeA.ID(), env)
	require.NoError(t, err)
	assert.Equal(t, MsgType("join_rejected"), resp.Type)
	assert.Empty(t, swarmA.Members("ops"))
}

func TestSwarmPersistence(t *testing.T) {
	home := t.TempDir()
	swarmA, swarmB := newTestPair(t, config.DefaultSwarmConfig(), home, t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "persist"))
	require.NoError(t, swarmB.Join(context.Background(), "persist"))

	require.Eventually(t, func() bool {
		return len(swarmA.Members("persist")) >= 1
	}, 10*time.Second, 100*time.Millisecond)

	require.NoError(t, swarmA.Stop())

	// A new Swarm over the same home reloads the roster.
	reloaded := &Swarm{path: home + "/swarms.json", swarms: make(map[string]*swarmState)}
	reloaded.load()
	assert.NotEmpty(t, reloaded.Members("persist"))
}

func TestSwarmIDValidation(t *testing.T) {
	assert.True(t, ValidSwarmID("ops"))
	assert.True(t, ValidSwarmID("research-team_1"))
	assert.False(t, ValidSwarmID(""))
	assert.False(t, ValidSwarmID("bad/id"))
	assert.False(t, ValidSwarmID("-leading-dash"))
	assert.False(t, ValidSwarmID(string(make([]byte, 100))))
}
