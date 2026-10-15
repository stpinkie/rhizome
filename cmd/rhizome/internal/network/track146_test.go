package network

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// Track 146 — the balance view's settle-threshold prompt and the peer
// detail economy split.

// writeEconomyConfig drops a config.json with the given economy block
// into home so the balance command resolves settle_threshold.
func writeEconomyConfig(t *testing.T, home, econBlock string) {
	t.Helper()
	raw := `{"mesh":{"economy":` + econBlock + `}}`
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(raw), 0o600))
}

func TestEconomyBalanceSettleDuePrompt(t *testing.T) {
	home := setupEconomyHome(t)
	payee := testutil.NewIdentity(t).PeerID
	seedLedger(t, home, []econ.Entry{
		entry(payee, econ.DirectionPayable, "credits", "1.5"),
		entry(payee, econ.DirectionReceivable, "credits", "0.25"),
	})
	writeEconomyConfig(t, home,
		`{"enabled":true,"unit":"credits","settle_threshold":"1.0"}`)

	out, err := runEconomy(t, "balance", payee)
	require.NoError(t, err)
	require.Contains(t, out, "over settle_threshold")
	require.Contains(t, out, "economy settle "+payee)

	// JSON output carries no prompt text — unchanged shape.
	out, err = runEconomy(t, "balance", payee, "--json")
	require.NoError(t, err)
	require.NotContains(t, out, "over settle_threshold")
	var bals map[string][]econ.UnitBalance
	require.NoError(t, json.Unmarshal([]byte(out), &bals))
}

func TestEconomyBalanceNoPromptBelowThreshold(t *testing.T) {
	home := setupEconomyHome(t)
	payee := testutil.NewIdentity(t).PeerID
	seedLedger(t, home, []econ.Entry{
		entry(payee, econ.DirectionPayable, "credits", "0.5"),
	})
	writeEconomyConfig(t, home,
		`{"enabled":true,"unit":"credits","settle_threshold":"1.0"}`)

	out, err := runEconomy(t, "balance", payee)
	require.NoError(t, err)
	require.NotContains(t, out, "over settle_threshold")
}

func TestPrintPeerDetailEconSplit(t *testing.T) {
	p := &mesh.PeerStatus{
		PeerID:  testutil.NewIdentity(t).PeerID,
		Trusted: true,
		Score: &mesh.PeerScoreView{
			Score:     80,
			Successes: 4,
			Failures:  1,
			Econ: &econ.PeerSummary{
				ChargesIssued:   3,
				ChargesReceived: 2,
				Settled:         1,
				Disputed:        1,
				Balances: []econ.UnitBalance{{
					Unit:              "credits",
					PayableAccrued:    "1.5",
					ReceivableAccrued: "0.25",
				}},
				OverThreshold: []string{"credits"},
				Overdue:       []string{"credits"},
			},
		},
	}
	var buf bytes.Buffer
	printPeerDetail(&buf, p)
	out := buf.String()
	require.Contains(t, out, "economy:")
	require.Contains(t, out, "3 issued / 2 received")
	require.Contains(t, out, "credits")
	require.Contains(t, out, "payable accrued 1.5")
	require.Contains(t, out, "over settle_threshold")
	require.Contains(t, out, "OVERDUE")
}

func TestPrintPeerDetailNoEcon(t *testing.T) {
	p := &mesh.PeerStatus{
		PeerID: testutil.NewIdentity(t).PeerID,
		Score:  &mesh.PeerScoreView{Score: 50},
	}
	var buf bytes.Buffer
	printPeerDetail(&buf, p)
	require.False(t, strings.Contains(buf.String(), "economy:"))
}
