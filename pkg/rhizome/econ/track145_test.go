package econ

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stpinkie/rhizome/pkg/config"
)

// EstimateTaskCharge quotes per_task + a prompt-token task-size hint,
// floored at min_charge — the claimant's honest estimate for swarm claims.
func TestEstimateTaskCharge(t *testing.T) {
	sheet := config.EconPriceSheet{
		PerTask:           "0.01",
		Per1KPromptTokens: "1",
		MinCharge:         "0.005",
	}

	// No task text → per_task only.
	assert.Equal(t, "0.01", EstimateTaskCharge(sheet, ""))
	// 250 chars ≈ 100 prompt tokens (2.5 chars/token) → +0.1.
	assert.Equal(t, "0.11", EstimateTaskCharge(sheet, string(make([]byte, 250))))
	// min_charge floors a tiny sheet.
	floor := config.EconPriceSheet{MinCharge: "0.5"}
	assert.Equal(t, "0.5", EstimateTaskCharge(floor, "hi"))
	// An empty sheet prices a task at zero.
	assert.Equal(t, "0", EstimateTaskCharge(config.EconPriceSheet{}, "hi"))
}
