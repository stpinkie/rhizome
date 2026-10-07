package config

import (
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPeerID mints a fresh Ed25519 peer id for bill_peers fixtures.
func testPeerID(t *testing.T) string {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	pid, err := peer.IDFromPrivateKey(priv)
	require.NoError(t, err)
	return pid.String()
}

// TestTrack138EconomyValidation covers the mesh.economy validation rules:
// unit required when enabled, bill_peers decode as peer ids, decimal
// fields parse as canonical decimals, payout required iff web3.
func TestTrack138EconomyValidation(t *testing.T) {
	validPeer := testPeerID(t)
	cases := []struct {
		name    string
		econ    MeshEconomyConfig
		wantErr string
	}{
		{
			name: "disabled empty",
			econ: MeshEconomyConfig{},
		},
		{
			name:    "enabled without unit",
			econ:    MeshEconomyConfig{Enabled: true},
			wantErr: "unit is required",
		},
		{
			name: "enabled minimal",
			econ: MeshEconomyConfig{Enabled: true, Unit: "credits"},
		},
		{
			name:    "bad settle backend",
			econ:    MeshEconomyConfig{SettleBackend: "paypal"},
			wantErr: "settle_backend",
		},
		{
			name:    "bill_peers reject non-peer-ids",
			econ:    MeshEconomyConfig{Enabled: true, Unit: "credits", BillPeers: []string{"not-a-peer"}},
			wantErr: "bill_peers[0]",
		},
		{
			name:    "bill_peers accept peer ids",
			econ:    MeshEconomyConfig{Enabled: true, Unit: "credits", BillPeers: []string{validPeer}},
			wantErr: "",
		},
		{
			name: "web3 requires payout",
			econ: MeshEconomyConfig{
				Enabled: true, Unit: "USDC", SettleBackend: "web3",
			},
			wantErr: "payout",
		},
		{
			name: "web3 with payout ok",
			econ: MeshEconomyConfig{
				Enabled: true, Unit: "USDC", SettleBackend: "web3",
				Payout: &EconPayout{ChainID: "84532", Address: "0xabc", Asset: "USDC"},
			},
		},
		{
			name:    "payout without web3 rejected",
			econ:    MeshEconomyConfig{Payout: &EconPayout{ChainID: "84532", Address: "0xabc", Asset: "USDC"}},
			wantErr: "settle_backend web3",
		},
		{
			name:    "empty accept_units entry",
			econ:    MeshEconomyConfig{AcceptUnits: []string{"credits", " "}},
			wantErr: "accept_units[1]",
		},
		{
			name:    "fraction decimal rejected",
			econ:    MeshEconomyConfig{PriceSheet: EconPriceSheet{PerTask: "1/2"}},
			wantErr: "per_task",
		},
		{
			name:    "negative decimal rejected",
			econ:    MeshEconomyConfig{MaxCostPerTask: "-1"},
			wantErr: "max_cost_per_task",
		},
		{
			name:    "exponent decimal rejected",
			econ:    MeshEconomyConfig{SettleThreshold: "1e3"},
			wantErr: "settle_threshold",
		},
		{
			name:    "empty-dot decimal rejected",
			econ:    MeshEconomyConfig{PriceSheet: EconPriceSheet{MinCharge: "."}},
			wantErr: "min_charge",
		},
		{
			name: "full sheet ok",
			econ: MeshEconomyConfig{
				Enabled: true, Unit: "credits",
				PriceSheet: EconPriceSheet{
					PerTask: "0.001", Per1KPromptTokens: "0.0002",
					Per1KCompletionTokens: "0.0005", PerSecond: "0.00001",
					MinCharge: "0.0005",
				},
				MaxCostPerTask: "0.05", MaxCostPerDay: "1", SettleThreshold: "10",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := MeshConfig{Enabled: true, Economy: tc.econ}
			err := m.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

// TestTrack138EconomyAdvertPrivacy pins the advert's privacy boundary:
// Advert() emits only seller-side terms — the buyer-private fields
// (bill_peers, accept_units' billing side, cost caps, settle backend)
// must never appear in the advertised bytes.
func TestTrack138EconomyAdvertPrivacy(t *testing.T) {
	e := MeshEconomyConfig{
		Enabled:         true,
		Unit:            "credits",
		PriceSheet:      EconPriceSheet{PerTask: "0.01", MinCharge: "0.001"},
		AcceptUnits:     []string{"credits", "spark"},
		BillPeers:       []string{testPeerID(t)},
		MaxCostPerTask:  "0.05",
		MaxCostPerDay:   "1",
		SettleThreshold: "10",
		SettleBackend:   "web3",
		Payout:          &EconPayout{ChainID: "84532", Address: "0xdead", Asset: "USDC"},
	}
	adv := e.Advert()
	require.NotNil(t, adv)
	assert.Equal(t, "credits", adv.Unit)
	assert.Equal(t, "0.01", adv.PriceSheet.PerTask)
	require.NotNil(t, adv.Payout)
	assert.Equal(t, "0xdead", adv.Payout.Address)
	assert.Equal(t, []string{"credits", "spark"}, adv.Accepts)

	raw, err := json.Marshal(adv)
	require.NoError(t, err)
	for _, priv := range []string{
		"bill_peers", "max_cost_per_task", "max_cost_per_day",
		"settle_threshold", "settle_backend", "accept_units",
	} {
		assert.NotContains(t, string(raw), priv, "private field %q leaked into advert", priv)
	}
	// Mutating the returned slices/maps must not alias the config.
	adv.Accepts[0] = "tampered"
	assert.Equal(t, "credits", e.AcceptUnits[0])
	adv.Payout.Address = "0xbeef"
	assert.Equal(t, "0xdead", e.Payout.Address)
}

// TestTrack138EconomyAdvertInert pins the inert-unless-enabled posture:
// disabled, unset, or unit-less configs advertise nothing.
func TestTrack138EconomyAdvertInert(t *testing.T) {
	var nilEcon *MeshEconomyConfig
	assert.Nil(t, nilEcon.Advert())
	assert.Nil(t, (&MeshEconomyConfig{}).Advert())
	assert.Nil(t, (&MeshEconomyConfig{Unit: "credits"}).Advert(), "disabled")
	assert.Nil(t, (&MeshEconomyConfig{Enabled: true}).Advert(), "no unit")
}

// TestTrack138EconomyJSONRoundTrip verifies the custom MeshConfig
// marshal/unmarshal path (duration field alias) preserves the economy
// block verbatim.
func TestTrack138EconomyJSONRoundTrip(t *testing.T) {
	m := MeshConfig{
		Enabled: true,
		Economy: MeshEconomyConfig{
			Enabled: true, Unit: "credits",
			PriceSheet:      EconPriceSheet{PerTask: "0.01"},
			BillPeers:       []string{testPeerID(t)},
			SettleBackend:   "web3",
			Payout:          &EconPayout{ChainID: "84532", Address: "0xabc", Asset: "USDC"},
			SettleThreshold: "5",
		},
	}
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var back MeshConfig
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NoError(t, back.Validate())
	assert.Equal(t, "credits", back.Economy.Unit)
	assert.Equal(t, "0.01", back.Economy.PriceSheet.PerTask)
	assert.Equal(t, "web3", back.Economy.SettleBackend)
	require.NotNil(t, back.Economy.Payout)
	assert.Equal(t, "0xabc", back.Economy.Payout.Address)
	assert.Equal(t, "5", back.Economy.SettleThreshold)
}
