package mesh

// Track 118 — observability depth: durable activity log, filters, trace
// lookups, per-op score counters, read-time score decay.

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
)

func TestActivityFeedPersistsToLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-activity.jsonl")
	f := newActivityFeed(newAuditLogger(path))

	f.push(runtimeevents.Event{
		Kind:  runtimeevents.Kind("mesh.task.update"),
		Time:  time.Now().UTC(),
		Attrs: map[string]any{"task_id": "t-1", "peer_id": "12D3KooWpeer"},
	})
	f.push(runtimeevents.Event{
		Kind:  runtimeevents.Kind("swarm.offer.open"),
		Attrs: map[string]any{"swarm_id": "ops", "offer_id": "o-9"},
	})

	raws, err := ReadAuditTail(path, 10)
	require.NoError(t, err)
	require.Len(t, raws, 2)
	assert.Contains(t, string(raws[0]), `"kind":"mesh.task.update"`)
	assert.Contains(t, string(raws[0]), `"task_id":"t-1"`)
	assert.Contains(t, string(raws[1]), `"kind":"swarm.offer.open"`)
}

// TestActivityFeedWarmLoadsOnStart exercises the restart path: a second mesh
// instance pointed at the same log file sees the persisted entries before
// any new bus traffic arrives.
func TestActivityFeedWarmLoadsOnStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-activity.jsonl")

	old := &Mesh{}
	old.SetActivityPath(path)
	old.SetEventBus(runtimeevents.NewBus())
	oldBus := old.eventBus
	oldBus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("mesh.remote.delegate"),
		Time:  time.Now().Add(-time.Hour).UTC(),
		Attrs: map[string]any{"correlation_id": "c-restart"},
	})
	// push writes the ring before the file — wait on the durable copy so the
	// warm-load below is deterministic.
	require.Eventually(t, func() bool {
		raws, err := ReadAuditTail(path, 10)
		return err == nil && len(raws) == 1
	}, 5*time.Second, 10*time.Millisecond)

	fresh := &Mesh{}
	fresh.SetActivityPath(path)
	fresh.SetEventBus(runtimeevents.NewBus())

	entries := fresh.Activity(0)
	require.Len(t, entries, 1)
	assert.Equal(t, "mesh.remote.delegate", entries[0].Kind)
	assert.Equal(t, "c-restart", entries[0].Attrs["correlation_id"])

	// New bus events append after the warmed tail and land on disk.
	fresh.eventBus.PublishNonBlocking(runtimeevents.Event{
		Kind: runtimeevents.Kind("swarm.note.posted"),
	})
	require.Eventually(t, func() bool {
		return len(fresh.Activity(0)) == 2
	}, 5*time.Second, 10*time.Millisecond)
	// Same ring-before-file ordering as above — wait on the durable copy.
	require.Eventually(t, func() bool {
		raws, err := ReadAuditTail(path, 10)
		return err == nil && len(raws) == 2
	}, 5*time.Second, 10*time.Millisecond)
}

// TestActivityFeedLogDisabled covers the no-log posture: feed works, no file.
func TestActivityFeedLogDisabled(t *testing.T) {
	m := &Mesh{}
	m.SetActivityPath("")
	m.SetEventBus(runtimeevents.NewBus())
	m.eventBus.PublishNonBlocking(runtimeevents.Event{Kind: runtimeevents.Kind("mesh.x")})
	require.Eventually(t, func() bool {
		return len(m.Activity(0)) == 1
	}, 5*time.Second, 10*time.Millisecond)
	assert.Empty(t, m.ActivityLogPath())
}

func TestActivityFilterMatching(t *testing.T) {
	now := time.Now().UTC()
	entry := ActivityEntry{
		Time: now,
		Kind: "mesh.task.update",
		Attrs: map[string]any{
			"task_id":   "t-42",
			"peer_id":   "12D3KooWpeer",
			"swarm_id":  "ops",
			"nested_id": "t-42",
		},
	}

	assert.True(t, entry.matches(ActivityFilter{}))
	assert.True(t, entry.matches(ActivityFilter{Kind: "mesh.*"}))
	assert.False(t, entry.matches(ActivityFilter{Kind: "swarm.*"}))
	assert.True(t, entry.matches(ActivityFilter{Kind: "mesh.task.update"}))
	assert.False(t, entry.matches(ActivityFilter{Kind: "mesh.task"}))

	assert.True(t, entry.matches(ActivityFilter{Peer: "12D3KooWpeer"}))
	assert.False(t, entry.matches(ActivityFilter{Peer: "12D3KooWother"}))

	assert.True(t, entry.matches(ActivityFilter{Swarm: "ops"}))
	assert.False(t, entry.matches(ActivityFilter{Swarm: "ops2"}))

	assert.True(t, entry.matches(ActivityFilter{Since: now.Add(-time.Minute)}))
	assert.False(t, entry.matches(ActivityFilter{Since: now.Add(time.Minute)}))

	assert.True(t, entry.matches(ActivityFilter{Contains: "t-42"}))
	assert.False(t, entry.matches(ActivityFilter{Contains: "t-99"}))
}

func TestActivityFilteredQuery(t *testing.T) {
	f := newActivityFeed(nil)
	base := time.Now().UTC()
	push := func(kind string, attrs map[string]any) {
		f.push(runtimeevents.Event{Kind: runtimeevents.Kind(kind), Time: base, Attrs: attrs})
	}
	push("mesh.a", map[string]any{"peer_id": "p1"})
	push("mesh.b", map[string]any{"peer_id": "p2"})
	push("swarm.c", map[string]any{"peer_id": "p1", "swarm_id": "s1"})

	require.Len(t, f.filtered(0, ActivityFilter{Kind: "mesh.*"}), 2)
	require.Len(t, f.filtered(0, ActivityFilter{Peer: "p1"}), 2)
	require.Len(t, f.filtered(0, ActivityFilter{Swarm: "s1"}), 1)

	// tail applies after filtering: newest match wins.
	out := f.filtered(1, ActivityFilter{Peer: "p1"})
	require.Len(t, out, 1)
	assert.Equal(t, "swarm.c", out[0].Kind)
}

func TestPeerScoreOpStats(t *testing.T) {
	s := NewPeerScoreStore()
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	s.Record(pid, "delegate", true, 10*time.Millisecond, nil)
	s.Record(pid, "delegate", false, 20*time.Millisecond, errors.New("boom"))
	s.Record(pid, "submit", true, 5*time.Millisecond, nil)
	s.Record(pid, "", true, time.Millisecond, nil) // unlabeled: aggregate only

	sc, ok := s.Get(pid)
	require.True(t, ok)
	assert.Equal(t, 4, sc.Successes+sc.Failures)
	require.Len(t, sc.OpStats, 2)
	assert.Equal(t, OpStat{Successes: 1, Failures: 1}, normalizeOp(sc.OpStats["delegate"]))
	assert.Equal(t, 1, sc.OpStats["submit"].Successes)
	assert.NotZero(t, sc.OpStats["delegate"].AvgLatency)
}

// normalizeOp zeroes the latency field so table assertions stay readable.
func normalizeOp(st OpStat) OpStat {
	st.AvgLatency = 0
	return st
}

func TestPeerScoreOpStatsPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-peer-scores.json")
	s := NewPeerScoreStoreWithPath(path)
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)
	s.Record(pid, "spawn", true, 7*time.Millisecond, nil)
	s.Close()

	fresh := NewPeerScoreStoreWithPath(path)
	require.NoError(t, fresh.Load())
	sc, ok := fresh.Get(pid)
	require.True(t, ok)
	require.Contains(t, sc.OpStats, "spawn")
	assert.Equal(t, 1, sc.OpStats["spawn"].Successes)
}

// TestPeerScoreHalfLifeDecay proves the read-time decay: counters stay raw,
// Score() reflects the elapsed idle window.
func TestPeerScoreHalfLifeDecay(t *testing.T) {
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	base := PeerScore{PeerID: pid.String(), Successes: 10, LastSeen: time.Now().UTC()}

	fresh := base
	fresh.halfLife = time.Hour
	assert.InDelta(t, 1000.0, fresh.Score(), 1.0)

	stale := base
	stale.halfLife = time.Hour
	stale.LastSeen = time.Now().Add(-2 * time.Hour).UTC() // two half-lives
	assert.InDelta(t, 250.0, stale.Score(), 5.0, "two half-lives ≈ 25%")

	disabled := base
	disabled.halfLife = 0
	disabled.LastSeen = time.Now().Add(-30 * 24 * time.Hour).UTC()
	assert.InDelta(t, 1000.0, disabled.Score(), 1.0, "no decay when half-life unset")
}

// TestScoreStoreHalfLifeFlowsToReads covers the store-side stamping: SetHalfLife
// applies to copies returned by Get/All and to Ranked ordering.
func TestScoreStoreHalfLifeFlowsToReads(t *testing.T) {
	s := NewPeerScoreStore()
	s.SetHalfLife(time.Hour)
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	s.Record(pid, "delegate", true, time.Millisecond, nil)
	s.scores[pid].LastSeen = time.Now().Add(-time.Hour).UTC()

	sc, ok := s.Get(pid)
	require.True(t, ok)
	assert.InDelta(t, 500.0, sc.Score(), 5.0, "one half-life idle ≈ 50%")
}

func TestTaskStoreGetUnscoped(t *testing.T) {
	ts := NewTaskStore()
	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)
	other, err := peer.Decode("12D3KooWGRcjvRUBXU3bJvCKkQvR5ME7zByZNddT5d5nhCFoHVDx")
	require.NoError(t, err)

	snap, _, err := ts.Submit(owner, agenttask.Request{
		Op:            agenttask.OpSubmit,
		TargetAgentID: "main",
		Timeout:       time.Minute,
	})
	require.NoError(t, err)

	// Owner-scoped lookup still gates on owner.
	_, ok := ts.getOwned(snap.ID, other)
	assert.False(t, ok)

	// Get is unscoped — the operator trace surface sees any owner.
	got, ok := ts.Get(snap.ID)
	require.True(t, ok)
	assert.Equal(t, snap.ID, got.ID)
	assert.Equal(t, owner, got.Owner)
}
