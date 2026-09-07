package network

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func TestTwoNodesPing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	if err != nil {
		t.Fatalf("identity A: %v", err)
	}

	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	if err != nil {
		t.Fatalf("identity B: %v", err)
	}

	nodeA, err := NewNode(ctx, idA.Libp2pPrivKey, Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		t.Fatalf("new node A: %v", err)
	}
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	if len(addrsA) == 0 {
		t.Fatalf("node A has no listen addrs")
	}

	nodeB, err := NewNode(ctx, idB.Libp2pPrivKey, Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
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
