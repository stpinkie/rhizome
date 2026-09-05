package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncerThreePeerConvergence starts three fully-connected syncers, commits
// a file on A, and verifies a single push propagates to both B and C via the
// announce→pull path.
func TestSyncerThreePeerConvergence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 40)
	nodeB := newTestNode(t, ctx, 41)
	nodeC := newTestNode(t, ctx, 42)

	wsA := newTestWorkspace(t)
	wsB := newTestWorkspace(t)
	wsC := newTestWorkspace(t)

	syncerA := newTestSyncer(t, ctx, "node-a", wsA, nodeA)
	syncerB := newTestSyncer(t, ctx, "node-b", wsB, nodeB)
	syncerC := newTestSyncer(t, ctx, "node-c", wsC, nodeC)

	// Fully connect the mesh.
	connectNodes(t, ctx, nodeA, nodeB)
	connectNodes(t, ctx, nodeA, nodeC)
	connectNodes(t, ctx, nodeB, nodeC)
	waitSyncProtocol(t, nodeB, nodeA.ID())
	waitSyncProtocol(t, nodeC, nodeA.ID())

	// Let the connect-time announce pulls (which carry the initial head)
	// settle so the commit below is always fetched by a fresh pull.
	waitSyncIdle(t, syncerA)
	waitSyncIdle(t, syncerB)
	waitSyncIdle(t, syncerC)

	require.NoError(t, os.WriteFile(filepath.Join(wsA, "NOTE.md"), []byte("from A\n"), 0o644))

	// PushTo commits locally and announces the new head; the announce goes to
	// every connected peer, so both B and C should pull and fast-forward.
	_, err := syncerA.PushTo(ctx, nodeB.ID())
	require.NoError(t, err)

	waitFileContent(t, filepath.Join(wsB, "NOTE.md"), "from A\n")
	waitFileContent(t, filepath.Join(wsC, "NOTE.md"), "from A\n")

	headA, err := currentHead(syncerA)
	require.NoError(t, err)
	for name, s := range map[string]*Syncer{"B": syncerB, "C": syncerC} {
		require.Eventually(t, func() bool {
			h, err := currentHead(s)
			return err == nil && h == headA
		}, 15*time.Second, 100*time.Millisecond, "peer %s head never matched A", name)
	}
}

// TestSyncerConflictingEditsConverge makes A and B edit the same file while
// disconnected, reconnects them, and verifies both sides merge (with conflict
// markers recorded) and eventually converge on the same HEAD.
func TestSyncerConflictingEditsConverge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Seed both workspaces with identical content so the initial commit hash
	// (and therefore the merge base) is shared.
	wsA := newTestWorkspace(t)
	wsB := newTestWorkspace(t)
	require.NoError(t, os.WriteFile(filepath.Join(wsA, "SHARED.md"), []byte("base\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wsB, "SHARED.md"), []byte("base\n"), 0o644))

	nodeA := newTestNode(t, ctx, 43)
	nodeB := newTestNode(t, ctx, 44)

	syncerA := newTestSyncer(t, ctx, "node-a", wsA, nodeA)
	syncerB := newTestSyncer(t, ctx, "node-b", wsB, nodeB)

	headA0, err := currentHead(syncerA)
	require.NoError(t, err)
	headB0, err := currentHead(syncerB)
	require.NoError(t, err)
	require.Equal(t, headA0, headB0, "initial commits must match for a shared merge base")

	// Divergent commits on the same file while disconnected.
	require.NoError(t, os.WriteFile(filepath.Join(wsA, "SHARED.md"), []byte("from A\n"), 0o644))
	_, err = Commit(syncerA.worktree, "node-a", "A edits SHARED.md")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wsB, "SHARED.md"), []byte("from B\n"), 0o644))
	_, err = Commit(syncerB.worktree, "node-b", "B edits SHARED.md")
	require.NoError(t, err)

	// Reconnect: each side's OnConnected handler announces its head, which
	// triggers a pull and three-way merge on the remote side.
	connectNodes(t, ctx, nodeA, nodeB)

	// The merge is asynchronous. Explicit pulls are driven as well so a single
	// transient failure (e.g. a Windows "Access is denied" rename inside
	// go-git's staging) cannot stall convergence — HandleAnnounce dedups on
	// head, so a repeated announce alone would not retry a failed pull.
	require.Eventually(t, func() bool {
		_ = syncerA.PullFrom(ctx, nodeB.ID())
		_ = syncerB.PullFrom(ctx, nodeA.ID())
		dataA, errA := os.ReadFile(filepath.Join(wsA, "SHARED.md"))
		dataB, errB := os.ReadFile(filepath.Join(wsB, "SHARED.md"))
		return errA == nil && errB == nil &&
			strings.Contains(string(dataA), "<<<<<<<") &&
			strings.Contains(string(dataB), "<<<<<<<")
	}, 60*time.Second, 500*time.Millisecond, "conflicting edits never produced merge markers on both sides")

	// Each side produced its own merge commit; exchange until HEADs match.
	convergePeers(t, ctx, syncerA, nodeA, syncerB, nodeB)

	// The conflict is recorded in the worktree on both sides.
	for name, s := range map[string]*Syncer{"A": syncerA, "B": syncerB} {
		paths, err := ConflictPaths(s.worktree)
		require.NoError(t, err)
		assert.Contains(t, paths, "SHARED.md", "peer %s should record the conflict", name)
	}

	dataA, err := os.ReadFile(filepath.Join(wsA, "SHARED.md"))
	require.NoError(t, err)
	dataB, err := os.ReadFile(filepath.Join(wsB, "SHARED.md"))
	require.NoError(t, err)
	assert.Equal(t, string(dataA), string(dataB), "worktrees diverged after convergence")
}

// TestSyncerReconnectCatchUp commits on A while B's node is not yet started,
// then connects B and verifies the OnConnected→announce→pull path brings B up
// to date without any explicit pull.
func TestSyncerReconnectCatchUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 45)
	wsA := newTestWorkspace(t)
	syncerA := newTestSyncer(t, ctx, "node-a", wsA, nodeA)

	// A commits while B is offline.
	require.NoError(t, os.WriteFile(filepath.Join(wsA, "CATCHUP.md"), []byte("offline edit\n"), 0o644))
	headA, err := Commit(syncerA.worktree, "node-a", "commit while B offline")
	require.NoError(t, err)

	// B starts its node with no bootstrap and its syncer afterwards; the
	// explicit connect below simulates the reconnect.
	nodeB := newTestNode(t, ctx, 46)
	wsB := newTestWorkspace(t)
	syncerB := newTestSyncer(t, ctx, "node-b", wsB, nodeB)

	connectNodes(t, ctx, nodeA, nodeB)

	// B learns A's head via the announce-on-connect path and converges.
	waitFileContent(t, filepath.Join(wsB, "CATCHUP.md"), "offline edit\n")

	require.Eventually(t, func() bool {
		return syncerB.PeerHeads()[nodeA.ID().String()] == headA.String()
	}, 15*time.Second, 100*time.Millisecond, "B never recorded A's announced head")
}

// TestSyncerBidirectionalSync writes different files on both peers and verifies
// repeated announce/pull rounds converge both workspaces to the same HEAD.
func TestSyncerBidirectionalSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeA := newTestNode(t, ctx, 47)
	nodeB := newTestNode(t, ctx, 48)

	wsA := newTestWorkspace(t)
	wsB := newTestWorkspace(t)

	syncerA := newTestSyncer(t, ctx, "node-a", wsA, nodeA)
	syncerB := newTestSyncer(t, ctx, "node-b", wsB, nodeB)

	connectNodes(t, ctx, nodeA, nodeB)
	waitSyncProtocol(t, nodeA, nodeB.ID())
	waitSyncProtocol(t, nodeB, nodeA.ID())
	waitSyncIdle(t, syncerA)
	waitSyncIdle(t, syncerB)

	require.NoError(t, os.WriteFile(filepath.Join(wsA, "A.md"), []byte("made by A\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wsB, "B.md"), []byte("made by B\n"), 0o644))

	convergePeers(t, ctx, syncerA, nodeA, syncerB, nodeB)

	waitFileContent(t, filepath.Join(wsB, "A.md"), "made by A\n")
	waitFileContent(t, filepath.Join(wsA, "B.md"), "made by B\n")

	headA, err := currentHead(syncerA)
	require.NoError(t, err)
	headB, err := currentHead(syncerB)
	require.NoError(t, err)
	assert.Equal(t, headA, headB)
}
