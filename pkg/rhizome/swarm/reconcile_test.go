package swarm

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// newTestSwarmNode starts a single-node swarm with a caller-supplied
// identity and home dir — the restart-reconciliation tests need to control
// both.
func newTestSwarmNode(
	t *testing.T,
	id *identity.Derived,
	home string,
	trust func(peer.ID) bool,
) *Swarm {
	t.Helper()
	ctx := context.Background()
	node, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Close() })

	sw := New(node, id, fastPresenceConfig(), trust, nil, home)
	require.NoError(t, sw.Start(ctx))
	t.Cleanup(func() { _ = sw.Stop() })
	return sw
}

func TestRunStoreSweepsRunningToInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swarm-runs.jsonl")
	rs := newRunStore(path)

	doneAt := time.Now().Add(-time.Hour)
	rs.Record(
		RunRecord{RunID: "r-done", SwarmID: "s", Goal: "g", Status: "done", StartedAt: doneAt, FinishedAt: doneAt},
	)
	rs.Record(RunRecord{RunID: "r-live", SwarmID: "s", Goal: "g2", Status: "running", StartedAt: doneAt})

	fresh := newRunStore(path)
	interrupted, err := fresh.Load()
	require.NoError(t, err)
	require.Len(t, interrupted, 1)
	assert.Equal(t, "r-live", interrupted[0].RunID)

	rec, ok := fresh.Get("r-live")
	require.True(t, ok)
	assert.Equal(t, "interrupted", rec.Status)
	assert.False(t, rec.FinishedAt.IsZero(), "sweep stamps a finish time")

	// The sweep is persisted — a second load finds no running records.
	again := newRunStore(path)
	second, err := again.Load()
	require.NoError(t, err)
	assert.Empty(t, second)
}

func TestOfferStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swarm-offers.jsonl")
	st := newOfferStore(path)
	st.Save([]offerRecord{
		{Info: OfferInfo{Offer: Offer{OfferID: "o1", SwarmID: "s"}, Status: OfferOpen}, Retries: 0},
		{Info: OfferInfo{Offer: Offer{OfferID: "o2", SwarmID: "s"}, Status: OfferDone}, Retries: 0},
		{Info: OfferInfo{Offer: Offer{OfferID: "o3", SwarmID: "s"}, Status: OfferAssigned}, Retries: 2},
	})

	loaded, err := newOfferStore(path).Load()
	require.NoError(t, err)
	require.Len(t, loaded, 2, "terminal records are not re-driven")
	assert.Equal(t, "o1", loaded[0].Info.OfferID)
	assert.Equal(t, "o3", loaded[1].Info.OfferID)
	assert.Equal(t, 2, loaded[1].Retries)
}

func TestOfferRedriveOnStart(t *testing.T) {
	home := t.TempDir()
	idA := testutil.NewIdentity(t)
	claimant := testutil.NewIdentity(t)

	// Seed the store as a previous incarnation of this node left it: one
	// open offer and one whose TTL already elapsed.
	storePath := filepath.Join(home, "swarm-offers.jsonl")
	st := newOfferStore(storePath)
	st.Save([]offerRecord{
		{Info: OfferInfo{Offer: Offer{
			OfferID:    "open-offer",
			SwarmID:    "work",
			AgentID:    "main",
			Task:       "resume me",
			Offerer:    idA.PeerID,
			CreatedAt:  time.Now(),
			TTLSeconds: 3600,
		}, Status: OfferOpen}},
		{Info: OfferInfo{Offer: Offer{
			OfferID:    "stale-offer",
			SwarmID:    "work",
			AgentID:    "main",
			Task:       "too late",
			Offerer:    idA.PeerID,
			CreatedAt:  time.Now().Add(-3 * time.Hour),
			TTLSeconds: 60,
		}, Status: OfferAssigned}, Retries: 1},
	})

	trust := func(pid peer.ID) bool { return pid.String() == claimant.PeerID }
	sw := newTestSwarmNode(t, idA, home, trust)

	var submitted peer.ID
	sw.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		submitted = preferred
		return preferred, "task-1", nil
	})

	// The stale offer lands expired without being re-broadcast.
	require.Eventually(t, func() bool {
		info, ok := sw.queue.offerInfo("stale-offer")
		return ok && info.Status == OfferExpired
	}, 10*time.Second, 50*time.Millisecond)

	// The live offer re-opens with a bumped attempt and takes fresh claims.
	require.Eventually(t, func() bool {
		info, ok := sw.queue.offerInfo("open-offer")
		return ok && info.Status == OfferOpen && info.Attempt >= 1
	}, 10*time.Second, 50*time.Millisecond, "persisted offer should re-open at attempt+1")

	injectClaim(t, sw, "work", "open-offer", claimant.PeerID)
	require.Eventually(t, func() bool {
		info, ok := sw.queue.offerInfo("open-offer")
		return ok && info.Status == OfferAssigned
	}, 30*time.Second, 100*time.Millisecond)
	assert.Equal(t, claimant.PeerID, submitted.String())
}

func TestSwarmRunCancel(t *testing.T) {
	home := t.TempDir()
	idA := testutil.NewIdentity(t)
	sw := newTestSwarmNode(t, idA, home, func(peer.ID) bool { return true })
	sw.joinLocal("ops")

	offerID, err := sw.Offer(context.Background(), "ops", OfferRequest{AgentID: "main", Task: "work"})
	require.NoError(t, err)

	sw.orch.runs.Record(RunRecord{
		RunID: "run-1", SwarmID: "ops", Goal: "g", Status: "running",
		Subtasks: []SubtaskResult{
			{Subtask: Subtask{ID: "0", AgentID: "main", Task: "work"}, Status: "offered", OfferID: offerID},
			{Subtask: Subtask{ID: "1", AgentID: "main", Task: "next"}, Status: "pending"},
		},
	})

	require.NoError(t, sw.CancelRun(context.Background(), "ops", "run-1"))

	info, ok := sw.queue.offerInfo(offerID)
	require.True(t, ok)
	assert.Equal(t, OfferCancelled, info.Status, "live offer should be cancelled")

	rec, ok := sw.RunRecord("run-1")
	require.True(t, ok)
	assert.Equal(t, "cancelled", rec.Status)
	assert.Equal(t, "cancelled", rec.Subtasks[0].Status)
	assert.Equal(t, "cancelled", rec.Subtasks[1].Status, "pending subtask is cancelled too")

	require.Error(t, sw.CancelRun(context.Background(), "ops", "nope"), "unknown run must error")
	require.Error(t, sw.CancelRun(context.Background(), "other-swarm", "run-1"),
		"run of another swarm must error")
}

func TestSwarmRunRetryDepGating(t *testing.T) {
	home := t.TempDir()
	idA := testutil.NewIdentity(t)
	claimant := testutil.NewIdentity(t)
	trust := func(pid peer.ID) bool { return pid.String() == claimant.PeerID }
	sw := newTestSwarmNode(t, idA, home, trust)
	sw.joinLocal("ops")

	sw.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		return preferred, "task-1", nil
	})

	// Source run: a done dep, a failed leaf (retryable), a failed subtask
	// whose dep failed (not retryable), and a skipped one.
	sw.orch.runs.Record(RunRecord{
		RunID: "src", SwarmID: "ops", Goal: "g", Status: "partial",
		Subtasks: []SubtaskResult{
			{Subtask: Subtask{ID: "a", AgentID: "main", Task: "prep"}, Status: "done", Result: "ok"},
			{
				Subtask: Subtask{ID: "b", AgentID: "main", Task: "work", DependsOn: []string{"a"}},
				Status:  "failed",
				Error:   "boom",
			},
			{
				Subtask: Subtask{ID: "c", AgentID: "main", Task: "chain", DependsOn: []string{"b"}},
				Status:  "failed",
				Error:   "dep failed",
			},
			{Subtask: Subtask{ID: "d", AgentID: "main", Task: "skip", DependsOn: []string{"c"}}, Status: "skipped"},
		},
	})

	// Re-offer b in the background — it needs a claim during the window.
	go func() {
		require.Eventually(t, func() bool {
			for _, o := range sw.OffersFor("ops") {
				if o.Task == "work" && o.Status == OfferOpen {
					sw.queue.onClaim("ops", Claim{OfferID: o.OfferID, Claimant: claimant.PeerID, Attempt: o.Attempt})
					return true
				}
			}
			return false
		}, 15*time.Second, 100*time.Millisecond)
	}()

	res, err := sw.RetryRun(context.Background(), "ops", "src", "main")
	require.NoError(t, err)
	require.Len(t, res.Subtasks, 4)

	assert.Equal(t, "done", res.Subtasks[0].Status, "done subtask carries over")
	assert.Equal(t, "ok", res.Subtasks[0].Result)

	assert.Equal(t, "dispatched", res.Subtasks[1].Status, "failed subtask with done deps is re-offered")
	assert.NotEmpty(t, res.Subtasks[1].OfferID)

	assert.Equal(t, "failed", res.Subtasks[2].Status, "dep b failed in source — c stays")
	assert.Contains(t, res.Subtasks[2].Error, "not retried")

	assert.Equal(t, "skipped", res.Subtasks[3].Status)

	// The new record links back to the source run.
	rec, ok := sw.RunRecord(res.RunID)
	require.True(t, ok)
	assert.Equal(t, "src", rec.RetryOf)
	assert.Equal(t, "partial", rec.Status)
}

// TestRunRecordRetryOfRoundTrip pins the additive JSON field.
func TestRunRecordRetryOfRoundTrip(t *testing.T) {
	rec := RunRecord{RunID: "r2", SwarmID: "s", Goal: "g", Status: "done", RetryOf: "r1"}
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	var back RunRecord
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, "r1", back.RetryOf)

	// Older records without retry_of decode fine.
	var old RunRecord
	require.NoError(t, json.Unmarshal([]byte(`{"run_id":"r0","status":"done"}`), &old))
	assert.Empty(t, old.RetryOf)
}
