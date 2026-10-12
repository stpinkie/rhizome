package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Track 143 — web3 close-out config: settle_signer/settle_from exist
// only under settle_backend=web3, and the advertised payout must carry
// a well-formed chain id, EVM destination, and asset ("native" or an
// ERC-20 contract address) — malformed money terms fail at load time,
// not mid-settlement.
func TestTrack143Web3CloseOutValidation(t *testing.T) {
	addr := "0x1111111111111111111111111111111111111111"
	token := "0x2222222222222222222222222222222222222222"
	valid := func() MeshEconomyConfig {
		return MeshEconomyConfig{
			Enabled: true, Unit: "USDC", SettleBackend: "web3",
			Payout: &EconPayout{ChainID: "31337", Address: addr, Asset: token},
		}
	}
	cases := []struct {
		name    string
		mutate  func(*MeshEconomyConfig)
		wantErr string
	}{
		{"baseline valid", func(e *MeshEconomyConfig) {}, ""},
		{"native asset valid", func(e *MeshEconomyConfig) { e.Payout.Asset = "native" }, ""},
		{"approval signer valid", func(e *MeshEconomyConfig) { e.SettleSigner = "approval" }, ""},
		{"wallet signer valid", func(e *MeshEconomyConfig) {
			e.SettleSigner = "wallet"
			e.SettleFrom = addr
		}, ""},
		{"bad signer", func(e *MeshEconomyConfig) { e.SettleSigner = "bot" }, "settle_signer"},
		{"signer without web3", func(e *MeshEconomyConfig) {
			e.SettleBackend = "ledger"
			e.SettleSigner = "wallet"
			e.Payout = nil
		}, "settle_signer"},
		{"settle_from non-address", func(e *MeshEconomyConfig) {
			e.SettleFrom = "abc"
		}, "settle_from"},
		{"settle_from without web3", func(e *MeshEconomyConfig) {
			e.SettleBackend = "ledger"
			e.SettleFrom = addr
			e.Payout = nil
		}, "settle_from"},
		{"payout address malformed", func(e *MeshEconomyConfig) {
			e.Payout.Address = "0xabc"
		}, "payout.address"},
		{"payout chain not decimal", func(e *MeshEconomyConfig) {
			e.Payout.ChainID = "0x7a69"
		}, "payout.chain_id"},
		{"payout asset malformed", func(e *MeshEconomyConfig) {
			e.Payout.Asset = "USDC"
		}, "payout.asset"},
		{"missing payout", func(e *MeshEconomyConfig) { e.Payout = nil }, "payout"},
		{"partial payout", func(e *MeshEconomyConfig) {
			e.Payout = &EconPayout{ChainID: "31337", Address: addr}
		}, "payout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := valid()
			tc.mutate(&e)
			err := e.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

// TestTrack143AdvertBackends pins the fail-closed capability signal: a
// web3 node advertises settle_backends ["ledger","web3"]; a ledger-only
// node never emits "web3" — the payer gate keys off it before offering.
func TestTrack143AdvertBackends(t *testing.T) {
	e := MeshEconomyConfig{
		Enabled: true, Unit: "USDC", SettleBackend: "web3",
		Payout: &EconPayout{
			ChainID: "31337",
			Address: "0x1111111111111111111111111111111111111111", Asset: "native",
		},
	}
	adv := e.Advert()
	require.NotNil(t, adv)
	assert.Equal(t, []string{"ledger", "web3"}, adv.SettleBackends)
	require.NotNil(t, adv.Payout)
	assert.Equal(t, "native", adv.Payout.Asset)

	l := MeshEconomyConfig{Enabled: true, Unit: "credits"}
	ladv := l.Advert()
	require.NotNil(t, ladv)
	assert.Equal(t, []string{"ledger"}, ladv.SettleBackends)
	assert.Nil(t, ladv.Payout)
}
