package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// fastPresenceConfig shortens heartbeat/expiry for tests.
func fastPresenceConfig() config.SwarmConfig {
	cfg := config.DefaultSwarmConfig()
	cfg.Presence.HeartbeatInterval = 200 * time.Millisecond
	cfg.Presence.ExpireAfter = 600 * time.Millisecond
	cfg.Queue.ClaimWindow = 2 * time.Second
	cfg.Queue.OfferTTL = 5 * time.Second
	return cfg
}

func TestSwarmPresenceHeartbeat(t *testing.T) {
	swarmA, swarmB := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "hb"))
	require.NoError(t, swarmB.Join(context.Background(), "hb"))

	require.Eventually(t, func() bool {
		return len(swarmA.Members("hb")) == 1 && len(swarmB.Members("hb")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	// Stop B's heartbeats by stopping the whole swarm; A should expire B.
	require.NoError(t, swarmB.Stop())
	require.Eventually(t, func() bool {
		return len(swarmA.Members("hb")) == 0
	}, 10*time.Second, 100*time.Millisecond, "stale member should be evicted")
}

func TestSwarmOfferClaimAssign(t *testing.T) {
	cfg := fastPresenceConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	// Roster membership arrives via JOIN_ACK before B's identify exchange has
	// necessarily advertised /rhizome/swarm/1.0.0; wait for protocol support
	// so the offer broadcast cannot race it. Probe the protocol with a short
	// timeout so the Eventually timer stays responsive.
	bPID, err := peer.Decode(swarmB.PeerID())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return swarmA.transport.Supported(context.Background(), bPID, 1*time.Second)
	}, 60*time.Second, 100*time.Millisecond)

	// B claims every offer for agent "main"; A's submitter is a stub that
	// records the chosen claimer instead of hitting the mesh.
	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return o.AgentID == "main" })

	var gotClaim peer.ID
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		gotClaim = preferred
		return preferred, "task-1", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main",
		Task:    "say hi",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferAssigned
	}, 10*time.Second, 100*time.Millisecond, "offer should be claimed and assigned")

	assert.Equal(t, swarmB.PeerID(), gotClaim.String())

	// B should have recorded the offer + assignment.
	require.Eventually(t, func() bool {
		for _, o := range swarmB.OffersFor("work") {
			if o.OfferID == offerID && o.Status == OfferAssigned {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
}

func TestSwarmOfferNoClaimsExpires(t *testing.T) {
	cfg := fastPresenceConfig()
	swarmA, _ := newTestPair(t, cfg, t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "quiet"))

	// No evaluator on B — nobody claims.
	offerID, err := swarmA.Offer(context.Background(), "quiet", OfferRequest{
		AgentID: "main",
		Task:    "nobody wants this",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferExpired
	}, 10*time.Second, 100*time.Millisecond)
}

func TestSwarmCoordinatorElection(t *testing.T) {
	swarmA, swarmB := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "coord"))
	require.NoError(t, swarmB.Join(context.Background(), "coord"))

	require.Eventually(t, func() bool {
		ca := swarmA.Coordinator("coord")
		cb := swarmB.Coordinator("coord")
		return ca != "" && ca == cb
	}, 10*time.Second, 100*time.Millisecond, "both sides should elect the same coordinator")

	// Deterministic rule: lowest peer id wins.
	expected := swarmA.PeerID()
	if swarmB.PeerID() < expected {
		expected = swarmB.PeerID()
	}
	assert.Equal(t, expected, swarmA.Coordinator("coord"))
}

func TestSwarmACLRejectsOffer(t *testing.T) {
	cfg := fastPresenceConfig()
	deny := false
	cfg.ACL = []config.SwarmACLRule{
		{SwarmID: "restricted", PeerID: "*", AllowOffer: &deny},
	}
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "restricted"))
	require.NoError(t, swarmB.Join(context.Background(), "restricted"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("restricted")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	bPID, err := peer.Decode(swarmB.PeerID())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return swarmA.transport.Supported(context.Background(), bPID, 1*time.Second)
	}, 60*time.Second, 100*time.Millisecond)

	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return true })

	offerID, err := swarmA.Offer(context.Background(), "restricted", OfferRequest{
		AgentID: "main", Task: "blocked by acl",
	})
	require.NoError(t, err)

	// B's ACL denies A's offers, so the offer never records on B's side and
	// expires on A's side with no claims.
	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferExpired
	}, 10*time.Second, 100*time.Millisecond)

	for _, o := range swarmB.OffersFor("restricted") {
		assert.NotEqual(t, offerID, o.OfferID, "denied offer must not be recorded")
	}
}
