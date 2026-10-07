package swarm

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// scoreLookupFor returns a ScoreLookup resolving the given claimant peer-id
// strings to fabricated scores (successes/failures ratio).
func scoreLookupFor(scores map[string]mesh.PeerScore) ScoreLookup {
	return func(pid peer.ID) (mesh.PeerScore, bool) {
		ps, ok := scores[pid.String()]
		return ps, ok
	}
}

func TestRankClaimsScoredFirst(t *testing.T) {
	q, s := newTestQueue(t)
	s.queue = q

	scored := testutil.NewIdentity(t).PeerID
	unscored := testutil.NewIdentity(t).PeerID
	s.SetScoreLookup(scoreLookupFor(map[string]mesh.PeerScore{
		scored: {PeerID: scored, Successes: 9, Failures: 1},
	}))

	// The unscored claimant is less loaded but must still lose — new members
	// rank below any scored peer.
	ranked := s.rankClaims([]Claim{
		{Claimant: unscored, ActiveTasks: 0},
		{Claimant: scored, ActiveTasks: 5},
	})
	require.Len(t, ranked, 2)
	assert.Equal(t, scored, ranked[0].claim.Claimant)
	require.NotNil(t, ranked[0].score)
	assert.Equal(t, unscored, ranked[1].claim.Claimant)
	assert.Nil(t, ranked[1].score)
}

func TestRankClaimsScoreDescThenLoadThenOrder(t *testing.T) {
	q, s := newTestQueue(t)
	s.queue = q

	high := testutil.NewIdentity(t).PeerID
	low := testutil.NewIdentity(t).PeerID
	s.SetScoreLookup(scoreLookupFor(map[string]mesh.PeerScore{
		high: {PeerID: high, Successes: 9, Failures: 1},
		low:  {PeerID: low, Successes: 5, Failures: 5},
	}))

	ranked := s.rankClaims([]Claim{
		{Claimant: low, ActiveTasks: 0},
		{Claimant: high, ActiveTasks: 3},
	})
	assert.Equal(t, high, ranked[0].claim.Claimant, "higher score wins despite more load")

	// Equal scores fall through to ActiveTasks, then claim order.
	mid1 := testutil.NewIdentity(t).PeerID
	mid2 := testutil.NewIdentity(t).PeerID
	s.SetScoreLookup(scoreLookupFor(map[string]mesh.PeerScore{
		mid1: {PeerID: mid1, Successes: 4, Failures: 1},
		mid2: {PeerID: mid2, Successes: 4, Failures: 1},
	}))
	ranked = s.rankClaims([]Claim{
		{Claimant: mid1, ActiveTasks: 4},
		{Claimant: mid2, ActiveTasks: 1},
	})
	assert.Equal(t, mid2, ranked[0].claim.Claimant, "equal score: fewer active tasks wins")

	ranked = s.rankClaims([]Claim{
		{Claimant: mid1, ActiveTasks: 1},
		{Claimant: mid2, ActiveTasks: 1},
	})
	assert.Equal(t, mid1, ranked[0].claim.Claimant, "equal score and load: earliest claim wins")
}

func TestRankClaimsNilLookupIsLoadOrdered(t *testing.T) {
	q, s := newTestQueue(t)
	s.queue = q

	a := testutil.NewIdentity(t).PeerID
	b := testutil.NewIdentity(t).PeerID
	ranked := s.rankClaims([]Claim{
		{Claimant: a, ActiveTasks: 7},
		{Claimant: b, ActiveTasks: 2},
	})
	assert.Equal(t, b, ranked[0].claim.Claimant, "nil lookup: least-loaded claims first")
	assert.Nil(t, ranked[0].score)
}

func TestSwarmOfferScoredAssign(t *testing.T) {
	swarmA, _ := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "scored"))

	// Two claimants: a newcomer with no score record but zero load, and a
	// proven peer under load. The proven claimant must win.
	newcomer := testutil.NewIdentity(t).PeerID
	proven := testutil.NewIdentity(t).PeerID
	swarmA.SetScoreLookup(scoreLookupFor(map[string]mesh.PeerScore{
		proven: {PeerID: proven, Successes: 19, Failures: 1},
	}))

	var gotClaim peer.ID
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		gotClaim = preferred
		return preferred, "task-1", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "scored", OfferRequest{
		AgentID: "main", Task: "rank me",
	})
	require.NoError(t, err)

	injectClaim(t, swarmA, "scored", offerID, newcomer)
	injectClaim(t, swarmA, "scored", offerID, proven)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferAssigned
	}, 30*time.Second, 100*time.Millisecond)

	require.NotNil(t, gotClaim)
	assert.Equal(t, proven, gotClaim.String(), "scored claimant should win over unscored")

	// The claims view exposes the pick rationale in rank order.
	info, ok := swarmA.queue.offerInfo(offerID)
	require.True(t, ok)
	require.Len(t, info.Claims, 2)
	assert.Equal(t, proven, info.Claims[0].Claimant)
	require.NotNil(t, info.Claims[0].Score)
	assert.Equal(t, newcomer, info.Claims[1].Claimant)
	assert.Nil(t, info.Claims[1].Score)
}

func TestSwarmOfferTimeoutSetsTTL(t *testing.T) {
	swarmA, _ := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "ttl"))

	offerID, err := swarmA.Offer(context.Background(), "ttl", OfferRequest{
		AgentID: "main", Task: "short fuse", Timeout: 2 * time.Minute,
	})
	require.NoError(t, err)
	info, ok := swarmA.queue.offerInfo(offerID)
	require.True(t, ok)
	assert.Equal(t, int64(120), info.TTLSeconds)

	// Zero timeout falls back to the configured offer TTL.
	offerID2, err := swarmA.Offer(context.Background(), "ttl", OfferRequest{
		AgentID: "main", Task: "default fuse",
	})
	require.NoError(t, err)
	info2, ok := swarmA.queue.offerInfo(offerID2)
	require.True(t, ok)
	assert.Equal(t, int64(60), info2.TTLSeconds)
}

func TestRunGoalForwardsSubtaskFields(t *testing.T) {
	swarmA, _ := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "goalreq"))

	swarmA.SetDecomposer(func(_ context.Context, goal string) ([]Subtask, error) {
		return []Subtask{{
			ID:      "research",
			AgentID: "main",
			Task:    "deep dive",
			Model:   "llama3",
			Tools:   []string{"web_search"},
			Timeout: 90 * time.Second,
			Requires: &OfferRequirements{
				Models: []string{"llama3"},
				Skills: []string{"search"},
			},
		}}, nil
	})

	res, err := swarmA.RunGoal(context.Background(), "goalreq", "study it", "main")
	require.NoError(t, err)
	require.Len(t, res.Subtasks, 1)
	offerID := res.Subtasks[0].OfferID
	require.NotEmpty(t, offerID)

	info, ok := swarmA.queue.offerInfo(offerID)
	require.True(t, ok)
	assert.Equal(t, "llama3", info.Model)
	assert.Equal(t, []string{"web_search"}, info.Tools)
	assert.Equal(t, int64(90), info.TTLSeconds)
	require.NotNil(t, info.Requires)
	assert.Equal(t, []string{"llama3"}, info.Requires.Models)
	assert.Equal(t, []string{"search"}, info.Requires.Skills)
}

func TestSubtaskJSONRoundTrip(t *testing.T) {
	// Duration-string timeout (the decomposer-friendly form).
	var st Subtask
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"a","agent_id":"main","task":"t",
		"model":"llama3","tools":["x"],"timeout":"5m",
		"requires":{"models":["llama3"]}
	}`), &st))
	assert.Equal(t, 5*time.Minute, st.Timeout)
	assert.Equal(t, "llama3", st.Model)
	assert.Equal(t, []string{"x"}, st.Tools)
	require.NotNil(t, st.Requires)
	assert.Equal(t, []string{"llama3"}, st.Requires.Models)

	// Nanosecond-number timeout (Go-native marshal form) round-trips.
	data, err := json.Marshal(st)
	require.NoError(t, err)
	var back Subtask
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, st.Timeout, back.Timeout)
	assert.Equal(t, st.Requires.Models, back.Requires.Models)

	// A bogus timeout string is a parse error, not silent data loss.
	var bad Subtask
	require.Error(t, json.Unmarshal([]byte(`{"task":"t","timeout":"soon"}`), &bad))
}
