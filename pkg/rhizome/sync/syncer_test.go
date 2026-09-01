package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func TestSyncerTwoNodesShareEdits(t *testing.T) {
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

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		t.Fatalf("new node A: %v", err)
	}
	defer nodeA.Close()

	addsA := nodeA.BootstrapAddrs()
	if len(addsA) == 0 {
		t.Fatalf("node A has no addrs")
	}

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addsA[0]},
	})
	if err != nil {
		t.Fatalf("new node B: %v", err)
	}
	defer nodeB.Close()

	dirA := t.TempDir()
	dirB := t.TempDir()

	syncerA, err := NewSyncer(ctx, Config{
		Workspace:        dirA,
		NodeName:         "node-a",
		Node:             nodeA,
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("new syncer A: %v", err)
	}
	if err = syncerA.Start(ctx); err != nil {
		t.Fatalf("start syncer A: %v", err)
	}
	defer syncerA.Stop()

	syncerB, err := NewSyncer(ctx, Config{
		Workspace:        dirB,
		NodeName:         "node-b",
		Node:             nodeB,
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("new syncer B: %v", err)
	}
	if err = syncerB.Start(ctx); err != nil {
		t.Fatalf("start syncer B: %v", err)
	}
	defer syncerB.Stop()

	// Wait for the two libp2p nodes to connect and exchange protocols.
	require.Eventually(t, func() bool {
		for _, p := range nodeB.ConnectedPeers() {
			if p == nodeA.ID() {
				protos, protoErr := nodeB.Host().Peerstore().SupportsProtocols(p, ProtocolID)
				return protoErr == nil && len(protos) > 0
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "node B did not see sync protocol on node A")

	// Edit on A.
	if err = os.WriteFile(filepath.Join(dirA, "AGENT.md"), []byte("hello from A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err = Commit(syncerA.worktree, "node-a", "test edit"); err != nil {
		t.Fatalf("commit A: %v", err)
	}

	if err = syncerB.PullFrom(ctx, nodeA.ID()); err != nil {
		t.Fatalf("pull from A: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dirB, "AGENT.md"))
	if err != nil {
		t.Fatalf("read B AGENT.md: %v", err)
	}
	if string(data) != "hello from A\n" {
		t.Fatalf("B AGENT.md = %q, want %q", data, "hello from A\n")
	}
}
