package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// Track 142 — the /rhizome/econ/1.0.0 settle handshake: signed offer →
// payee receivable verification → shared settle_id on both ledgers;
// divergence → settle_reject + econ:diverged audit; ledger_query for
// reconciliation.

// seedT142 records an accrued ledger entry (payable or receivable).
func seedT142(t *testing.T, m *Mesh, peerID, taskID, amount string, dir econ.Direction) econ.Entry {
	t.Helper()
	rec, err := m.EconomyLedger().Record(econ.Entry{
		PeerID:    peerID,
		Direction: dir,
		TaskID:    taskID,
		Unit:      "credits",
		Amount:    amount,
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	})
	require.NoError(t, err)
	return *rec
}

func entryStateT142(t *testing.T, m *Mesh, id string) econ.EntryState {
	t.Helper()
	e, ok := m.EconomyLedger().Get(id)
	require.True(t, ok)
	return e.State
}

func TestTrack142HandshakeSettlesBothSides(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	// Mirror-image books: A owes B for two tasks; B holds the receivables.
	pa1 := seedT142(t, meshA, bID, "task-142-a", "0.5", econ.DirectionPayable)
	pa2 := seedT142(t, meshA, bID, "task-142-b", "0.25", econ.DirectionPayable)
	rb1 := seedT142(t, meshB, aID, "task-142-a", "0.5", econ.DirectionReceivable)
	rb2 := seedT142(t, meshB, aID, "task-142-b", "0.25", econ.DirectionReceivable)

	offer, settled, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.NoError(t, err)
	require.NotNil(t, offer)
	assert.Len(t, settled, 2)
	settleID := offer.ID()

	// Payer side: both payables settled under the shared id, handshake-tagged.
	for _, e := range []econ.Entry{pa1, pa2} {
		got, ok := meshA.EconomyLedger().Get(e.EntryID)
		require.True(t, ok)
		assert.Equal(t, econ.StateSettled, got.State)
		assert.Equal(t, settleID, got.SettleID)
		assert.Equal(t, econ.HandshakeSettleTX, got.SettleTX)
	}
	// Payee side: same settle_id on the receivables.
	for _, e := range []econ.Entry{rb1, rb2} {
		got, ok := meshB.EconomyLedger().Get(e.EntryID)
		require.True(t, ok)
		assert.Equal(t, econ.StateSettled, got.State)
		assert.Equal(t, settleID, got.SettleID)
		assert.Equal(t, econ.HandshakeSettleTX, got.SettleTX)
	}
}

func TestTrack142HandshakeDivergedAmount(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	pa := seedT142(t, meshA, bID, "task-142-div", "0.5", econ.DirectionPayable)
	rb := seedT142(t, meshB, aID, "task-142-div", "0.75", econ.DirectionReceivable)

	_, _, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "econ:diverged")

	// Both sides stay accrued — nothing was settled on a diverged offer.
	assert.Equal(t, econ.StateAccrued, entryStateT142(t, meshA, pa.EntryID))
	assert.Equal(t, econ.StateAccrued, entryStateT142(t, meshB, rb.EntryID))
}

func TestTrack142HandshakeMissingReceivable(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()

	// A owes B, but B never recorded the receivable.
	seedT142(t, meshA, bID, "task-142-ghost", "0.5", econ.DirectionPayable)

	_, _, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "econ:diverged")
}

func TestTrack142HandshakeIdempotentResubmit(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()

	seedT142(t, meshA, bID, "task-142-once", "0.5", econ.DirectionPayable)
	seedT142(t, meshB, aID, "task-142-once", "0.5", econ.DirectionReceivable)

	_, settled, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.NoError(t, err)
	require.Len(t, settled, 1)

	// A second settle over the same (now-settled) entries is a no-op error,
	// not a re-charge.
	_, _, err = meshA.EconomySettle(context.Background(), bID, "credits")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no accrued payable balance")
}

func TestTrack142LedgerQuery(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()
	seedT142(t, meshB, aID, "task-142-q", "0.5", econ.DirectionReceivable)

	resp, err := meshA.EconomyLedgerQuery(context.Background(), bID, "credits", time.Time{})
	require.NoError(t, err)
	require.True(t, resp.OK)
	assert.Equal(t, "ledger_view", resp.Kind)
	assert.NotEmpty(t, resp.EntriesDigest)
	require.Len(t, resp.Balance, 1)
	assert.Equal(t, "credits", resp.Balance[0].Unit)
	assert.Equal(t, "0.5", resp.Balance[0].ReceivableAccrued)
}

func TestTrack142RejectsEconomyDisabled(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true} // economy off on the payee
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	seedT142(t, meshA, bID, "task-142-off", "0.5", econ.DirectionPayable)

	_, _, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "economy is not enabled")
}

func TestTrack142RejectsUntrustedPeer(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgC := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	meshA, meshC := newEconTestMeshes(t, cfgA, cfgC, nil)

	aID := meshA.node.ID().String()

	// A does not trust C. C trusts A so its outbound call reaches A's
	// handler — which must still reject the untrusted inbound peer.
	meshA.UntrustPeer(meshC.node.ID())

	seedT142(t, meshC, aID, "task-142-untrusted", "0.5", econ.DirectionPayable)
	_, _, err := meshC.EconomySettle(context.Background(), aID, "credits")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not trusted")
}
