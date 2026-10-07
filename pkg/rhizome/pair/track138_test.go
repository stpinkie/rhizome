package pair

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
)

// track138Advert is the inviter-side advert wired into the Economy hook.
func track138Advert() *config.EconAdvert {
	return &config.EconAdvert{
		Unit:       "credits",
		PriceSheet: config.EconPriceSheet{PerTask: "0.01", MinCharge: "0.001"},
		Accepts:    []string{"credits"},
		Payout:     &config.EconPayout{ChainID: "84532", Address: "0xabc", Asset: "USDC"},
	}
}

// decodeRawBundle rewrites a bundle's inner JSON for tamper tests.
func decodeRawBundle(t *testing.T, bundleB64 string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(bundleB64)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func encodeRawBundle(t *testing.T, m map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// TestTrack138BundleCarriesEconomy: an economy-enabled inviter's bundle
// echoes its advert, double-signed (legacy Sig + EconSig), and Accept
// returns the terms to the joiner.
func TestTrack138BundleCarriesEconomy(t *testing.T) {
	pmA, pmB := newPairNodes(t)
	pmA.hooks.Economy = track138Advert

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	decoded, err := DecodeBundle(bundle)
	require.NoError(t, err)
	require.NotNil(t, decoded.Economy)
	assert.Equal(t, "credits", decoded.Economy.Unit)
	assert.Equal(t, "0.01", decoded.Economy.PriceSheet.PerTask)
	require.NotNil(t, decoded.Economy.Payout)
	assert.Equal(t, "0xabc", decoded.Economy.Payout.Address)
	require.NotEmpty(t, decoded.EconSig, "economy block must be signed")

	accepted, err := pmB.Accept(context.Background(), bundle)
	require.NoError(t, err)
	assert.Equal(t, pmA.host.ID().String(), accepted.PeerID)
	require.NotNil(t, accepted.Economy, "Accept must surface counterparty terms")
	assert.Equal(t, "credits", accepted.Economy.Unit)
}

// TestTrack138BundleEconomyTamper: editing the economy block (without
// regenerating EconSig) must fail DecodeBundle.
func TestTrack138BundleEconomyTamper(t *testing.T) {
	pmA, _ := newPairNodes(t)
	pmA.hooks.Economy = track138Advert

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	m := decodeRawBundle(t, bundle)
	econ := m["economy"].(map[string]any)
	econ["price_sheet"].(map[string]any)["per_task"] = "9999"
	_, err = DecodeBundle(encodeRawBundle(t, m))
	require.ErrorContains(t, err, "economy signature")
}

// TestTrack138BundleEconomyStrip: dropping the economy block while
// keeping EconSig must fail — a transit tamperer can't silently hide the
// inviter's terms.
func TestTrack138BundleEconomyStrip(t *testing.T) {
	pmA, _ := newPairNodes(t)
	pmA.hooks.Economy = track138Advert

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	m := decodeRawBundle(t, bundle)
	delete(m, "economy")
	_, err = DecodeBundle(encodeRawBundle(t, m))
	require.ErrorContains(t, err, "without economy terms")
}

// TestTrack138BundleNoEconomyUnchanged: without the Economy hook the wire
// carries no economy fields and the bundle round-trips exactly as a
// pre-economy bundle does.
func TestTrack138BundleNoEconomyUnchanged(t *testing.T) {
	pmA, _ := newPairNodes(t)

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	m := decodeRawBundle(t, bundle)
	assert.NotContains(t, m, "economy")
	assert.NotContains(t, m, "econ_sig")

	decoded, err := DecodeBundle(bundle)
	require.NoError(t, err)
	assert.Nil(t, decoded.Economy)
}

// TestTrack138OldJoinerCompat: a pre-economy build's decode path ignores
// the economy block (unknown JSON fields drop); the legacy invite
// signature still verifies. Modeled by decoding the wire into the
// pre-field bundle shape and running the same verification.
func TestTrack138OldJoinerCompat(t *testing.T) {
	pmA, _ := newPairNodes(t)
	pmA.hooks.Economy = track138Advert

	bundle, err := pmA.Create(time.Minute)
	require.NoError(t, err)

	// Old build's Bundle shape — no Economy/EconSig fields.
	type oldBundle struct {
		PeerID string   `json:"peer_id"`
		Addrs  []string `json:"addrs"`
		Code   string   `json:"code"`
		Exp    int64    `json:"exp"`
		Sig    []byte   `json:"sig"`
	}
	raw, err := base64.RawURLEncoding.DecodeString(bundle)
	require.NoError(t, err)
	var ob oldBundle
	require.NoError(t, json.Unmarshal(raw, &ob))

	// The old build verifies Sig over the legacy payload — succeeds
	// because the economy block lives under a separate signature.
	pid, err := peer.Decode(ob.PeerID)
	require.NoError(t, err)
	pub, err := pid.ExtractPublicKey()
	require.NoError(t, err)
	ok, err := pub.Verify(invitePayload(ob.PeerID, ob.Code, ob.Exp), ob.Sig)
	require.NoError(t, err)
	assert.True(t, ok, "legacy signature must verify for pre-economy builds")
}
