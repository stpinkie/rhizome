package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

func TestTwoNodesPing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := NewNode(ctx, idA.Libp2pPrivKey, Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	if err != nil {
		t.Fatalf("new node A: %v", err)
	}
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	if len(addrsA) == 0 {
		t.Fatalf("node A has no listen addrs")
	}

	nodeB, err := NewNode(ctx, idB.Libp2pPrivKey, Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	if err != nil {
		t.Fatalf("new node B: %v", err)
	}
	defer nodeB.Close()

	// Wait for the connection to be established.
	time.Sleep(500 * time.Millisecond)

	var peerA string
	require.Eventually(t, func() bool {
		for _, p := range nodeB.Peers() {
			if p.ID.String() == nodeA.PeerID() {
				peerA = p.ID.String()
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "node B did not discover node A")

	if !strings.Contains(peerA, nodeA.PeerID()) {
		t.Fatalf("expected peer %s, got %s", nodeA.PeerID(), peerA)
	}

	rtt, err := nodeB.Ping(ctx, nodeA.PeerID(), 5*time.Second)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	if rtt < 0 {
		t.Fatalf("expected non-negative rtt, got %v", rtt)
	}
}

// TestMDNSDiscoversPeer exercises the production LAN discovery path that every
// other test disables: two nodes share a per-run service name, get no
// bootstrap peers, and must find and connect to each other purely via
// multicast. The unique service name keeps the discovery domain isolated from
// other test binaries and any real daemon on the LAN.
func TestMDNSDiscoversPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var suffix [3]byte
	_, err := rand.Read(suffix[:])
	require.NoError(t, err)
	cfg := Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		MDNSServiceName:   fmt.Sprintf("_rhizome-t%s._p2p", hex.EncodeToString(suffix[:])),
	}

	nodeA, err := NewNode(ctx, testutil.NewIdentity(t).Libp2pPrivKey, cfg)
	require.NoError(t, err)
	defer nodeA.Close()

	nodeB, err := NewNode(ctx, testutil.NewIdentity(t).Libp2pPrivKey, cfg)
	require.NoError(t, err)
	defer nodeB.Close()

	require.Eventually(t, func() bool {
		return IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 30*time.Second, 500*time.Millisecond, "mDNS never connected the nodes")
}
