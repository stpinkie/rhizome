package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Track 146 — settle_grace_period serializes as a duration string (never
// raw nanoseconds), omits cleanly when unset, round-trips, and validates.

func TestTrack146GracePeriodJSONShape(t *testing.T) {
	e := MeshEconomyConfig{
		Enabled:           true,
		Unit:              "credits",
		SettleThreshold:   "5",
		SettleGracePeriod: 72 * time.Hour,
	}
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"settle_grace_period":"72h0m0s"`)
	assert.NotContains(t, string(raw), "259200000000000",
		"duration must never serialize as nanoseconds")

	// Unset → the key is omitted entirely.
	raw, err = json.Marshal(MeshEconomyConfig{Enabled: true, Unit: "credits"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "settle_grace_period")
}

func TestTrack146GracePeriodRoundTrip(t *testing.T) {
	m := MeshConfig{
		Enabled: true,
		Economy: MeshEconomyConfig{
			Enabled:           true,
			Unit:              "credits",
			SettleThreshold:   "5",
			SettleGracePeriod: 7 * 24 * time.Hour,
		},
	}
	raw, err := json.Marshal(&m)
	require.NoError(t, err)
	var back MeshConfig
	require.NoError(t, json.Unmarshal(raw, &back))
	require.NoError(t, back.Validate())
	assert.Equal(t, 7*24*time.Hour, back.Economy.SettleGracePeriod)
	assert.Equal(t, "5", back.Economy.SettleThreshold)
}

func TestTrack146GracePeriodValidation(t *testing.T) {
	cases := []struct {
		name    string
		econ    MeshEconomyConfig
		wantErr string
	}{
		{
			name: "grace without threshold rejected",
			econ: MeshEconomyConfig{
				Enabled:           true,
				Unit:              "credits",
				SettleGracePeriod: time.Hour,
			},
			wantErr: "settle_grace_period",
		},
		{
			name: "negative grace rejected",
			econ: MeshEconomyConfig{
				Enabled:           true,
				Unit:              "credits",
				SettleThreshold:   "5",
				SettleGracePeriod: -time.Hour,
			},
			wantErr: "settle_grace_period",
		},
		{
			name: "grace with threshold ok",
			econ: MeshEconomyConfig{
				Enabled:           true,
				Unit:              "credits",
				SettleThreshold:   "5",
				SettleGracePeriod: time.Hour,
			},
		},
		{
			name: "threshold without grace ok",
			econ: MeshEconomyConfig{
				Enabled:         true,
				Unit:            "credits",
				SettleThreshold: "5",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.econ.Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}

	// A bad duration string in JSON fails decode, not just validation.
	var e MeshEconomyConfig
	err := json.Unmarshal([]byte(`{"settle_grace_period":"soon"}`), &e)
	require.Error(t, err)
}
