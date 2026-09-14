package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/require"
)

func TestSyncerTwoNodesShareEdits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx)

	addsA := nodeA.BootstrapAddrs()
	if len(addsA) == 0 {
		t.Fatalf("node A has no addrs")
	}

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

	// Edit on A *before* B connects: the commit must precede any packfile fetch
	// B might perform, because a background announce-on-connect pull would
	// otherwise race the explicit PullFrom below (a pull that started before
	// the commit can return a stale head and the dedup in PullFrom lets the
	// explicit call join it).
	if err = os.WriteFile(filepath.Join(dirA, "AGENT.md"), []byte("hello from A\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err = Commit(syncerA.worktree, "node-a", "test edit"); err != nil {
		t.Fatalf("commit A: %v", err)
	}

	nodeB := newTestNode(t, ctx, addsA[0])

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

	// Wait for the two libp2p nodes to connect.
	require.Eventually(t, func() bool {
		for _, p := range nodeB.ConnectedPeers() {
			if p == nodeA.ID() {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "node B did not connect to node A")

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

// TestSyncerStopConcurrentAnnounce exercises the Stop() shutdown path while
// inbound announces keep arriving: previously each announce did wg.Add(1)
// directly, so an Add landing on a zeroed counter while Stop's wg.Wait was in
// flight tripped Go's misuse detector ("WaitGroup is reused before previous
// Wait has returned"). The lifecycle gate now drops late work instead.
func TestSyncerStopConcurrentAnnounce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node := newTestNode(t, ctx)

	for i := 0; i < 15; i++ {
		syncer, err := NewSyncer(ctx, Config{
			Workspace:        t.TempDir(),
			NodeName:         "node-stop",
			Node:             node,
			AutoSync:         true,
			CommitInterval:   time.Hour,
			AnnounceInterval: time.Hour,
		})
		require.NoError(t, err)
		require.NoError(t, syncer.Start(ctx))

		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 100; j++ {
				syncer.HandleAnnounce(node.ID(), plumbing.NewHash(fmt.Sprintf("%040x", i*100+j)))
			}
		}()
		require.NoError(t, syncer.Stop())
		<-done
	}
}
