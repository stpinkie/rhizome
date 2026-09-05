package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

const syncTestMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// newTestNode creates a libp2p node on 127.0.0.1 with a deterministic
// identity derived from the shared test mnemonic.
func newTestNode(t *testing.T, ctx context.Context, index uint32, bootstrap ...string) *network.Node {
	t.Helper()
	id, _, err := identity.FromMnemonic(syncTestMnemonic, index)
	require.NoError(t, err)
	n, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: bootstrap,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = n.Close() })
	return n
}

// newTestWorkspace returns a fresh empty workspace directory nested inside its
// own temp dir so the persisted sync-status.json never collides between peers.
func newTestWorkspace(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	return ws
}

// newTestSyncer creates and starts a syncer with the file watcher and periodic
// loops effectively disabled so tests drive sync deterministically.
func newTestSyncer(t *testing.T, ctx context.Context, name, ws string, node *network.Node) *Syncer {
	t.Helper()
	s, err := NewSyncer(ctx, Config{
		Workspace:        ws,
		NodeName:         name,
		Node:             node,
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
	})
	require.NoError(t, err)
	require.NoError(t, s.Start(ctx))
	t.Cleanup(func() { _ = s.Stop() })
	return s
}

// connectNodes dials a from b and waits until both sides report the
// connection.
func connectNodes(t *testing.T, ctx context.Context, a, b *network.Node) {
	t.Helper()
	addrs := a.BootstrapAddrs()
	require.NotEmpty(t, addrs)
	require.NoError(t, b.Connect(ctx, addrs[0]))
	waitConnected(t, a, b.ID())
	waitConnected(t, b, a.ID())
}

func waitConnected(t *testing.T, n *network.Node, pid peer.ID) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, p := range n.ConnectedPeers() {
			if p == pid {
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "node %s never connected to %s", n.PeerID(), pid)
}

// waitSyncProtocol waits until from's peerstore has learned that to speaks the
// sync protocol (identify exchange can lag the connection itself).
func waitSyncProtocol(t *testing.T, from *network.Node, to peer.ID) {
	t.Helper()
	require.Eventually(t, func() bool {
		protos, err := from.Host().Peerstore().SupportsProtocols(to, ProtocolID)
		return err == nil && len(protos) > 0
	}, 15*time.Second, 50*time.Millisecond, "peer %s did not advertise %s", to, ProtocolID)
}

func waitFileContent(t *testing.T, path, want string) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && string(data) == want
	}, 30*time.Second, 100*time.Millisecond, "file %s never contained %q", path, want)
}

func waitFileContains(t *testing.T, path, substr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(data), substr)
	}, 30*time.Second, 100*time.Millisecond, "file %s never contained %q", path, substr)
}

// waitSyncIdle waits until the syncer has no in-flight pulls, then double
// checks after a short delay to cover the small gap between HandleAnnounce
// spawning a pull goroutine and the pull registering in the dedup map. Tests
// that connect peers before committing need this so a connect-time announce
// pull cannot fetch a stale head and let a later explicit/announce pull join
// it via the PullFrom dedup.
func waitSyncIdle(t *testing.T, s *Syncer) {
	t.Helper()
	idle := func() bool {
		s.pullingMu.Lock()
		defer s.pullingMu.Unlock()
		return len(s.pulling) == 0
	}
	require.Eventually(t, idle, 15*time.Second, 50*time.Millisecond, "syncer still has in-flight pulls")
	time.Sleep(300 * time.Millisecond)
	require.Eventually(t, idle, 15*time.Second, 50*time.Millisecond, "syncer still has in-flight pulls")
}

// currentHead returns the syncer's HEAD hash; safe to call in poll loops.
func currentHead(s *Syncer) (plumbing.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Head(s.repo)
}

// convergePeers alternates commit+announce between the two syncers until their
// HEADs match or the deadline passes. Announce→pull convergence is
// asynchronous and divergent histories can take a couple of exchange rounds to
// settle on a single merge commit.
func convergePeers(t *testing.T, ctx context.Context, sA *Syncer, nA *network.Node, sB *Syncer, nB *network.Node) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		_, _ = sA.PushTo(ctx, nB.ID())
		_ = sA.PullFrom(ctx, nB.ID())
		time.Sleep(200 * time.Millisecond)
		_, _ = sB.PushTo(ctx, nA.ID())
		_ = sB.PullFrom(ctx, nA.ID())
		time.Sleep(200 * time.Millisecond)
		hA, errA := currentHead(sA)
		hB, errB := currentHead(sB)
		if errA == nil && errB == nil && hA == hB {
			return
		}
	}
	hA, _ := currentHead(sA)
	hB, _ := currentHead(sB)
	t.Fatalf("peers did not converge: %s head=%s, %s head=%s", nA.PeerID(), hA, nB.PeerID(), hB)
}
