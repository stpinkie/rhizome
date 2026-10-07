package econ

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func sheet() config.EconPriceSheet {
	return config.EconPriceSheet{
		PerTask:               "0.01",
		Per1KPromptTokens:     "0.0002",
		Per1KCompletionTokens: "0.0005",
		PerSecond:             "0.00001",
		MinCharge:             "0.005",
	}
}

func TestComputeSheetUsage(t *testing.T) {
	// 0.01 + 0.0002*2 + 0.0005*1 + 0.00001*3 = 0.01093
	u := &toolshared.RemoteUsage{
		PromptTokens: 2000, CompletionTokens: 1000, DurationMS: 3000,
	}
	amount, truncated, err := Compute(sheet(), u, "")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, "0.01093", amount)
}

func TestComputePerTaskOnly(t *testing.T) {
	amount, truncated, err := Compute(sheet(), nil, "")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, "0.01", amount)
}

func TestComputeMinChargeFloor(t *testing.T) {
	s := sheet()
	s.PerTask = "0"
	// Zero usage → sheet price 0 → floor bumps to min_charge.
	amount, truncated, err := Compute(s, nil, "")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, "0.005", amount)
}

func TestComputeMaxChargeTruncates(t *testing.T) {
	u := &toolshared.RemoteUsage{PromptTokens: 900000}
	// sheet·usage = 0.01 + 0.0002*900 = 0.19 → capped at 0.1.
	amount, truncated, err := Compute(sheet(), u, "0.1")
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Equal(t, "0.1", amount)
}

func TestComputeZeroSheet(t *testing.T) {
	amount, _, err := Compute(config.EconPriceSheet{}, nil, "")
	require.NoError(t, err)
	assert.Equal(t, "0", amount)
}

func TestNegotiate(t *testing.T) {
	cfg := &config.MeshEconomyConfig{
		Enabled:    true,
		Unit:       "credits",
		PriceSheet: sheet(),
		BillPeers:  []string{"peer-a"},
	}
	// Unbilled caller — terms optional.
	require.NoError(t, Negotiate(cfg, "peer-b", nil))
	require.NoError(t, Negotiate(cfg, "peer-b", &Terms{Accept: true, Unit: "credits"}))

	// Billed caller without terms → econ:.
	require.ErrorContains(t, Negotiate(cfg, "peer-a", nil), RejectEcon)
	// Wrong unit → econ:.
	require.ErrorContains(t,
		Negotiate(cfg, "peer-a", &Terms{Accept: true, Unit: "spark"}), RejectEcon)
	// Accepted alt unit ok.
	cfg.AcceptUnits = []string{"spark"}
	require.NoError(t,
		Negotiate(cfg, "peer-a", &Terms{Accept: true, Unit: "spark"}))
	// max_charge below the floor → price_floor:.
	require.ErrorContains(t,
		Negotiate(cfg, "peer-a", &Terms{Accept: true, Unit: "credits", MaxCharge: "0.001"}),
		RejectPriceFloor)
	// Disabled economy never negotiates.
	require.NoError(t,
		Negotiate(&config.MeshEconomyConfig{}, "peer-a", nil))
}

func TestSheetDigestStable(t *testing.T) {
	adv := &config.EconAdvert{
		Unit:       "credits",
		PriceSheet: sheet(),
		Accepts:    []string{"credits"},
	}
	d1 := SheetDigest(adv)
	assert.Len(t, d1, 64)
	assert.Equal(t, d1, SheetDigest(adv))
	adv.PriceSheet.PerTask = "0.02"
	assert.NotEqual(t, d1, SheetDigest(adv))
	assert.Empty(t, SheetDigest(nil))
}

func TestVerifyCharge(t *testing.T) {
	adv := &config.EconAdvert{
		Unit: "credits", PriceSheet: sheet(), Accepts: []string{"credits"},
	}
	u := &toolshared.RemoteUsage{PromptTokens: 2000}
	amount, truncated, err := Compute(adv.PriceSheet, u, "")
	require.NoError(t, err)

	good := &Charge{
		Unit: "credits", Amount: amount, Usage: u,
		SheetDigest: SheetDigest(adv), Truncated: truncated,
	}
	require.NoError(t, VerifyCharge(adv, good))

	// Wrong digest.
	bad := *good
	bad.SheetDigest = "deadbeef"
	require.ErrorContains(t, VerifyCharge(adv, &bad), MismatchCharge)

	// Overcharge.
	bad = *good
	bad.Amount = "9.99"
	require.ErrorContains(t, VerifyCharge(adv, &bad), "exceeds sheet bound")

	// Unaccepted unit.
	bad = *good
	bad.Unit = "spark"
	require.ErrorContains(t, VerifyCharge(adv, &bad), "unit")

	// Nil advert — no manifest to verify against.
	require.ErrorContains(t, VerifyCharge(nil, good), MismatchCharge)

	// Truncated charge: amount = max_charge ≤ bound.
	bad2 := *good
	bad2.Amount = "0.008" // under max(min_charge, usage) — undercharge passes bound check
	require.NoError(t, VerifyCharge(adv, &bad2))
}

func TestDecimalString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0", "0"},
		{"1", "1"},
		{"0.001", "0.001"},
		{"12.340", "12.34"},
		{"10.00", "10"},
	}
	for _, tc := range cases {
		r, ok := parseDecimal(tc.in)
		require.True(t, ok, tc.in)
		assert.Equal(t, tc.want, decimalString(r), tc.in)
	}
}
