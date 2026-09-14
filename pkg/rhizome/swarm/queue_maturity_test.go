package swarm

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// fastQueueConfig tightens queue timings for maturity tests.
func fastQueueConfig() config.SwarmConfig {
	cfg := fastPresenceConfig()
	// Reuse the already-lengthened claim window from fastPresenceConfig and
	// keep the assignment watch tight for retry/dead-letter paths.
	cfg.Queue.AssignTimeout = 2 * time.Second
	return cfg
}

func TestSwarmOfferCancel(t *testing.T) {
	cfg := fastQueueConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return o.AgentID == "main" })
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		return preferred, "task-x", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "cancel me",
	})
	require.NoError(t, err)

	// B must have observed the offer before the cancel: broadcasts travel on
	// a fresh stream per message, so a cancel that outruns the offer is
	// dropped and B's observed copy would never transition to cancelled.
	waitOfferObserved(t, swarmB, "work", offerID)

	// Cancel while the offer is still collecting claims.
	require.NoError(t, swarmA.CancelOffer(context.Background(), "work", offerID))

	info, ok := swarmA.queue.offerInfo(offerID)
	require.True(t, ok)
	assert.Equal(t, OfferCancelled, info.Status)

	// B's observed copy should be marked cancelled once the broadcast lands.
	require.Eventually(t, func() bool {
		for _, o := range swarmB.OffersFor("work") {
			if o.OfferID == offerID && o.Status == OfferCancelled {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "observed offer should be cancelled")
}

func TestSwarmOfferRetryThenDeadLetter(t *testing.T) {
	cfg := fastQueueConfig()
	cfg.Queue.MaxRetries = 1
	cfg.Queue.ClaimWindow = 5 * time.Second
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	var submits atomic.Int32
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		submits.Add(1)
		return preferred, "task-fail", nil
	})
	// The assigned task always errors — the offer should re-broadcast once
	// (MaxRetries=1) then land in dead_letter.
	swarmA.SetResultFetcher(func(
		_ context.Context, _ peer.ID, _ string, _ time.Duration,
	) (agenttask.Response, error) {
		return agenttask.Response{Status: agenttask.StatusError, Error: "boom"}, nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "always fails",
	})
	require.NoError(t, err)

	// Inject claims directly rather than relying on B receiving the offer
	// broadcast and replying over libp2p: under parallel package load that
	// round trip can outlast the claim window, expiring the offer before the
	// retry/dead-letter path under test ever runs.
	stopClaims := make(chan struct{})
	defer close(stopClaims)
	go func() {
		for {
			select {
			case <-stopClaims:
				return
			case <-time.After(25 * time.Millisecond):
			}
			info, ok := swarmA.queue.offerInfo(offerID)
			if !ok || info.Status != OfferOpen {
				continue
			}
			swarmA.queue.onClaim("work", Claim{
				OfferID:  offerID,
				Claimant: swarmB.PeerID(),
				Attempt:  info.Attempt,
			})
		}
	}()

	// Two claim windows plus scheduling: ~10s floor, with margin for loaded
	// parallel runs.
	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferDeadLetter
	}, 60*time.Second, 100*time.Millisecond, "offer should dead-letter after retries")

	info, _ := swarmA.queue.offerInfo(offerID)
	assert.Equal(t, 1, info.Retries)
	assert.Equal(t, int32(2), submits.Load(), "initial submit + one retry")
}

func TestSwarmOfferCapMismatchNotClaimed(t *testing.T) {
	cfg := fastQueueConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return true })
	// B cannot satisfy the requirement, so it must not claim.
	swarmB.SetCapMatcher(func(_ string, req OfferRequirements) bool { return false })

	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		return preferred, "task-y", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID:  "main",
		Task:     "needs special hardware",
		Requires: OfferRequirements{Agents: []string{"gpu-agent"}},
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferExpired
	}, 10*time.Second, 100*time.Millisecond, "offer should expire unclaimed")
}

func TestSwarmOfferDoneResult(t *testing.T) {
	cfg := fastQueueConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		return preferred, "task-ok", nil
	})
	swarmA.SetResultFetcher(func(
		_ context.Context, _ peer.ID, _ string, _ time.Duration,
	) (agenttask.Response, error) {
		return agenttask.Response{
			Status: agenttask.StatusDone,
			Result: &toolshared.ToolResult{ForLLM: "all done"},
		}, nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "finish cleanly",
	})
	require.NoError(t, err)

	// Inject the claim rather than relying on B receiving the offer broadcast
	// and replying over libp2p: claim transport is not the subject here — the
	// assign→submit→fetch→done lifecycle is.
	injectClaim(t, swarmA, "work", offerID, swarmB.PeerID())

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferDone
	}, 30*time.Second, 100*time.Millisecond, "offer should complete")

	info, _ := swarmA.queue.offerInfo(offerID)
	assert.Equal(t, "all done", info.Result)
}
