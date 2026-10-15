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

// filterClaimsByPrice drops quotes above max_price; unpriced claims
// (absent or unparseable quote — old builds, non-economy members) always
// stay eligible.
func TestFilterClaimsByPrice(t *testing.T) {
	claims := []Claim{
		{Claimant: "a", Quote: "0.3"},
		{Claimant: "b", Quote: "0.7"},
		{Claimant: "c"},                 // unpriced
		{Claimant: "d", Quote: "bogus"}, // unparseable → unpriced
		{Claimant: "e", Quote: "0.5"},   // exactly at cap stays
	}

	eligible, dropped := filterClaimsByPrice(claims, "0.5")
	assert.Equal(t, 1, dropped)
	require.Len(t, eligible, 4)
	assert.Equal(t, "a", eligible[0].Claimant)
	assert.Equal(t, "c", eligible[1].Claimant)
	assert.Equal(t, "d", eligible[2].Claimant)
	assert.Equal(t, "e", eligible[3].Claimant)

	// No cap or an unparseable cap → everything eligible.
	eligible, dropped = filterClaimsByPrice(claims, "")
	assert.Equal(t, 0, dropped)
	assert.Len(t, eligible, 5)
	_, dropped = filterClaimsByPrice(claims, "nonsense")
	assert.Equal(t, 0, dropped)
}

// Offer rejects a malformed max_price before publishing.
func TestSwarmOfferMaxPriceValidation(t *testing.T) {
	swarmA, _ := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	require.NoError(t, swarmA.Join(context.Background(), "work"))

	for _, bad := range []string{"abc", "1.2.3", "-1", "1e3", " 5", "0x5"} {
		_, err := swarmA.Offer(context.Background(), "work", OfferRequest{
			AgentID: "main", Task: "t", MaxPrice: bad,
		})
		assert.Error(t, err, "max_price=%q", bad)
	}
	// Canonical decimals are accepted.
	_, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "t", MaxPrice: "0.5",
	})
	require.NoError(t, err)
}

// An over-cap quote loses to an eligible claim; the eligible claim's quote
// surfaces in OfferInfo.Claims and the over-cap claim is filtered out.
func TestSwarmOfferMaxPricePicksEligibleClaim(t *testing.T) {
	cfg := fastPresenceConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	// B claims every offer and quotes 0.7 — over the 0.5 cap.
	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return o.AgentID == "main" })
	swarmB.SetQuoteFunc(func(_, _ string) string { return "0.7" })

	var gotClaim peer.ID
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		gotClaim = preferred
		return preferred, "task-1", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "priced work", MaxPrice: "0.5",
	})
	require.NoError(t, err)

	// A synthetic third claimant quotes under the cap; injected directly so
	// the filter race with B's real claim does not matter — whichever lands
	// in the window, only the under-cap claim is eligible.
	cheap := testutil.NewIdentity(t).PeerID
	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		if !ok || info.Status != OfferOpen {
			return false
		}
		swarmA.queue.onClaim("work", Claim{
			OfferID: offerID, Claimant: cheap, Attempt: info.Attempt, Quote: "0.3",
		})
		return true
	}, 10*time.Second, 50*time.Millisecond)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferAssigned
	}, 30*time.Second, 100*time.Millisecond, "offer should assign to the under-cap claim")
	assert.Equal(t, cheap, gotClaim.String())

	info, _ := swarmA.queue.offerInfo(offerID)
	require.Len(t, info.Claims, 1, "over-cap claim is filtered out of the pick view")
	assert.Equal(t, cheap, info.Claims[0].Claimant)
	assert.Equal(t, "0.3", info.Claims[0].Quote)
}

// When every claim quotes over max_price the offer expires with the
// filtered count recorded — nothing is submitted.
func TestSwarmOfferAllClaimsOverMaxPriceExpire(t *testing.T) {
	cfg := fastPresenceConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return o.AgentID == "main" })
	swarmB.SetQuoteFunc(func(_, _ string) string { return "9" })
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		t.Error("submitter must not run when all claims are over max_price")
		return preferred, "task-x", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "too pricey", MaxPrice: "0.5",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferExpired
	}, 30*time.Second, 100*time.Millisecond, "all-over-cap offer should expire")
	info, _ := swarmA.queue.offerInfo(offerID)
	assert.Contains(t, info.Error, "exceeded max_price 0.5")
}

// Claims without a quote proceed unpriced — old members ignore
// max_price/quote entirely (documented wire-compat posture).
func TestSwarmOfferUnpricedClaimProceeds(t *testing.T) {
	cfg := fastPresenceConfig()
	swarmA, swarmB := newTestPair(t, cfg, t.TempDir(), t.TempDir())

	require.NoError(t, swarmA.Join(context.Background(), "work"))
	require.NoError(t, swarmB.Join(context.Background(), "work"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("work")) == 1
	}, 10*time.Second, 100*time.Millisecond)

	swarmB.SetOfferEvaluator(func(_ string, o Offer) bool { return o.AgentID == "main" })
	var gotClaim peer.ID
	swarmA.SetTaskSubmitter(func(_ context.Context, preferred peer.ID, _ mesh.RemoteCall) (peer.ID, string, error) {
		gotClaim = preferred
		return preferred, "task-1", nil
	})

	offerID, err := swarmA.Offer(context.Background(), "work", OfferRequest{
		AgentID: "main", Task: "unpriced ok", MaxPrice: "0.0001",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		info, ok := swarmA.queue.offerInfo(offerID)
		return ok && info.Status == OfferAssigned
	}, 30*time.Second, 100*time.Millisecond, "unpriced claim should still be assignable")
	assert.Equal(t, swarmB.PeerID(), gotClaim.String())

	info, _ := swarmA.queue.offerInfo(offerID)
	require.Len(t, info.Claims, 1)
	assert.Equal(t, "", info.Claims[0].Quote)
}

// Offer/Claim stay byte-identical for pre-Track-145 encodings: the new
// fields are omitempty, so absent prices/quotes emit nothing.
func TestSwarmOfferClaimWireCompat(t *testing.T) {
	o := Offer{
		OfferID: "o1", SwarmID: "s", AgentID: "main", Task: "t",
		Offerer: "peer", CreatedAt: time.Unix(1700000000, 0).UTC(),
	}
	data, err := json.Marshal(o)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "max_price")

	o.MaxPrice = "0.5"
	data, err = json.Marshal(o)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"max_price":"0.5"`)

	c := Claim{OfferID: "o1", Claimant: "peer"}
	data, err = json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "quote")

	c.Quote = "0.3"
	data, err = json.Marshal(c)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"quote":"0.3"`)
}

// Subtask.max_price parses from decomposer JSON and rides into the offer.
func TestSubtaskMaxPriceParses(t *testing.T) {
	var st Subtask
	require.NoError(t, json.Unmarshal(
		[]byte(`{"agent_id":"main","task":"x","max_price":"0.25"}`), &st))
	assert.Equal(t, "0.25", st.MaxPrice)
}
