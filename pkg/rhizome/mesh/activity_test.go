package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

func TestActivityFeedRingOrder(t *testing.T) {
	f := newActivityFeed()
	for i := 0; i < activityCapacity+25; i++ {
		f.push(runtimeevents.Event{
			Kind:  runtimeevents.Kind("mesh.test"),
			Time:  time.Unix(int64(i), 0).UTC(),
			Attrs: map[string]any{"i": i},
		})
	}

	all := f.tail(0)
	require.Len(t, all, activityCapacity)
	// Oldest surviving entry is i=25; newest is i=activityCapacity+24.
	for idx, e := range all {
		assert.Equal(t, idx+25, e.Attrs["i"], "entry %d out of order", idx)
	}

	last := f.tail(3)
	require.Len(t, last, 3)
	assert.Equal(t, activityCapacity+22, last[0].Attrs["i"])
	assert.Equal(t, activityCapacity+24, last[2].Attrs["i"])
}

func TestActivityFeedBelowCapacity(t *testing.T) {
	f := newActivityFeed()
	for i := 0; i < 10; i++ {
		f.push(runtimeevents.Event{
			Kind: runtimeevents.Kind("mesh.test"),
			Attrs: map[string]any{
				"i": i,
			},
		})
	}
	all := f.tail(0)
	require.Len(t, all, 10)
	assert.Equal(t, 0, all[0].Attrs["i"])
	assert.Equal(t, 9, all[9].Attrs["i"])
	assert.NotZero(t, all[0].Time, "zero event time should be stamped")
}

func TestMatchMeshSwarmKind(t *testing.T) {
	cases := map[string]bool{
		"mesh.task.update":    true,
		"mesh.remote.audit":   true,
		"swarm.offer.claimed": true,
		"agent.turn":          false,
		"gateway.request":     false,
		"meshx.not":           false,
	}
	for kind, want := range cases {
		got := matchMeshSwarmKind(runtimeevents.Event{Kind: runtimeevents.Kind(kind)})
		assert.Equal(t, want, got, kind)
	}
}

func TestMeshActivityFeedCollectsBusEvents(t *testing.T) {
	bus := runtimeevents.NewBus()
	m := &Mesh{}
	m.SetEventBus(bus)
	// Second call must not create a duplicate subscription.
	m.SetEventBus(bus)

	bus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("mesh.test"),
		Attrs: map[string]any{"i": 1},
	})
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("agent.unrelated"),
		Attrs: map[string]any{"i": 2},
	})
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("swarm.test"),
		Attrs: map[string]any{"i": 3},
	})

	require.Eventually(t, func() bool {
		return len(m.Activity(0)) == 2
	}, 5*time.Second, 10*time.Millisecond)

	entries := m.Activity(0)
	assert.Equal(t, "mesh.test", entries[0].Kind)
	assert.Equal(t, "swarm.test", entries[1].Kind)
}

func TestMeshEventsStream(t *testing.T) {
	bus := runtimeevents.NewBus()
	m := &Mesh{}
	m.SetEventBus(bus)

	ctx, cancel := context.WithCancel(context.Background())
	ch, cleanup, err := m.Events(ctx)
	require.NoError(t, err)
	defer cleanup()

	bus.PublishNonBlocking(runtimeevents.Event{
		Kind: runtimeevents.Kind("mesh.test.stream"),
	})
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind: runtimeevents.Kind("agent.nope"),
	})

	select {
	case evt := <-ch:
		assert.Equal(t, "mesh.test.stream", evt.Kind.String())
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for streamed event")
	}
	cancel()
}

func TestMeshEventsNoBus(t *testing.T) {
	m := &Mesh{}
	_, _, err := m.Events(context.Background())
	assert.Error(t, err)
	assert.Nil(t, m.Activity(10))
}

func TestDetectTransport(t *testing.T) {
	assert.Equal(t, "relay",
		detectTransport("/ip4/1.2.3.4/tcp/4001/p2p/abc/p2p-circuit/p2p/def"))
	assert.Equal(t, "quic",
		detectTransport("/ip4/1.2.3.4/udp/4001/quic-v1/p2p/abc"))
	assert.Equal(t, "tcp",
		detectTransport("/ip4/1.2.3.4/tcp/4001/p2p/abc"))
	assert.Equal(t, "other", detectTransport("/dns4/x.example"))
}

// TestNetworkStatusPeerObservability wires two real nodes and asserts the
// conns/latency/score/bandwidth fields land on PeerStatus.
func TestNetworkStatusPeerObservability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	meshA := NewMesh(nodeA, nil, idA, config.MeshConfig{Enabled: true}, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()
	meshA.TrustPeer(nodeB.ID())

	var status NetworkStatus
	require.Eventually(t, func() bool {
		status = meshA.NetworkStatus(t.TempDir())
		if len(status.Peers) != 1 {
			return false
		}
		p := status.Peers[0]
		if len(p.Conns) == 0 {
			return false
		}
		if p.Conns[0].Transport != "tcp" ||
			(p.Conns[0].Direction != "inbound" && p.Conns[0].Direction != "outbound") {
			return false
		}
		// Wait until handshake/identify traffic lands on the reporter.
		return status.Bandwidth != nil &&
			status.Bandwidth.TotalIn+status.Bandwidth.TotalOut > 0
	}, 10*time.Second, 100*time.Millisecond)

	require.Len(t, status.Peers, 1)
	p := status.Peers[0]
	require.NotEmpty(t, p.Conns)
	assert.Equal(t, "tcp", p.Conns[0].Transport)
	assert.NotEmpty(t, p.Conns[0].RemoteMultiaddr)

	// Recording a score surfaces it on PeerStatus.
	meshA.scoreStore.Record(nodeB.ID(), true, 5*time.Millisecond, nil)
	status = meshA.NetworkStatus(t.TempDir())
	require.Len(t, status.Peers, 1)
	require.NotNil(t, status.Peers[0].Score)
	assert.Equal(t, 1, status.Peers[0].Score.Successes)
}
