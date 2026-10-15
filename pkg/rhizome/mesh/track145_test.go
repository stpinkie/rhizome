package mesh

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// EconQuoteFor feeds swarm claim quotes from the local advertised sheet:
// unpriced when economy is off, "0" when the offerer is not billed, and the
// sheet estimate when the offerer is in bill_peers.
func TestMeshEconQuoteFor(t *testing.T) {
	peerID := testutil.NewIdentity(t).PeerID

	// Economy disabled → unpriced claim.
	m := &Mesh{cfg: config.DefaultMeshConfig()}
	assert.Equal(t, "", m.EconQuoteFor(peerID, "task"))

	econCfg := config.MeshEconomyConfig{
		Enabled: true,
		Unit:    "credits",
		PriceSheet: config.EconPriceSheet{
			PerTask:   "0.01",
			MinCharge: "0.005",
		},
	}
	m = &Mesh{cfg: config.MeshConfig{Economy: econCfg}}

	// Offerer not on bill_peers → the task runs unbilled, quote "0".
	assert.Equal(t, "0", m.EconQuoteFor(peerID, "task"))

	// Billable offerer → sheet estimate (per_task, task too small to add
	// a token term).
	econCfg.BillPeers = []string{peerID}
	m = &Mesh{cfg: config.MeshConfig{Economy: econCfg}}
	assert.Equal(t, "0.01", m.EconQuoteFor(peerID, "task"))

	// A nil mesh stays inert.
	var nilMesh *Mesh
	assert.Equal(t, "", nilMesh.EconQuoteFor(peerID, "task"))
}
