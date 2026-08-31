package network

import (
	"context"
	"strings"
	"testing"
	"time"

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

	peers := nodeB.Peers()
	if len(peers) == 0 {
		t.Fatalf("node B did not discover node A")
	}

	peerA := peers[0].ID.String()
	if !strings.Contains(peerA, nodeA.PeerID()) {
		t.Fatalf("expected peer %s, got %s", nodeA.PeerID(), peerA)
	}

	rtt, err := nodeB.Ping(ctx, nodeA.PeerID(), 5*time.Second)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}

	if rtt <= 0 {
		t.Fatalf("expected positive rtt, got %v", rtt)
	}
}
