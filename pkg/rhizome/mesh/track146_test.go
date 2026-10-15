package mesh

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// Track 146 — economy observability + reputation: econ_* op counters and
// outcome refs on the peer-score store, the settle-due edge event, the
// overdue sweep's non-settlement penalty, and the shared summary/view.

// newEconObsMesh is newEconVerbMesh with an economy config override —
// threshold/grace land under test control.
func newEconObsMesh(t *testing.T, econCfg config.MeshEconomyConfig) (*Mesh, *eventCollector) {
	m, col := newEconVerbMesh(t)
	econCfg.Enabled = true
	m.cfg.Economy = econCfg
	return m, col
}

// mustPID decodes a peer id string for the score store lookups.
func mustPID(t *testing.T, s string) peer.ID {
	t.Helper()
	pid, err := peer.Decode(s)
	require.NoError(t, err)
	return pid
}

// snapshot returns the collected event kinds (a copy under lock).
func (c *eventCollector) snapshot() []runtimeevents.Kind {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]runtimeevents.Kind(nil), c.kinds...)
}

// chargeOf builds a minimal wire charge for recordEconCharge.
func chargeOf(unit, amount string) *econ.Charge {
	return &econ.Charge{Unit: unit, Amount: amount}
}

func TestTrack146ChargeOutcomeRecorded(t *testing.T) {
	m, _ := newEconObsMesh(t, config.MeshEconomyConfig{Unit: "credits"})
	pid := testutil.NewIdentity(t).PeerID

	m.recordEconCharge(econ.DirectionPayable, pid, "", "corr-1", chargeOf("credits", "0.5"), nil)
	m.recordEconCharge(econ.DirectionReceivable, pid, "", "corr-2", chargeOf("credits", "0.25"), nil)

	sc, ok := m.scoreStore.Get(mustPID(t, pid))
	require.True(t, ok, "peer score must exist after econ charge")
	op := sc.OpStats[OpEconCharge]
	assert.Equal(t, 2, op.Successes)
	assert.Equal(t, 0, op.Failures)
}

func TestTrack146PeerScoreViewEconSplit(t *testing.T) {
	m, _ := newEconObsMesh(t, config.MeshEconomyConfig{Unit: "credits"})
	pid := testutil.NewIdentity(t).PeerID

	m.recordEconCharge(econ.DirectionPayable, pid, "task-a", "", chargeOf("credits", "0.5"), nil)
	m.recordEconCharge(econ.DirectionReceivable, pid, "task-b", "", chargeOf("credits", "0.75"), nil)

	view := m.peerEconView(pid)
	require.NotNil(t, view, "peer econ split must exist")
	assert.Equal(t, 1, view.ChargesIssued)
	assert.Equal(t, 1, view.ChargesReceived)
	require.Len(t, view.Balances, 1)
	assert.Equal(t, "0.5", view.Balances[0].PayableAccrued)
	assert.Equal(t, "0.75", view.Balances[0].ReceivableAccrued)
}

func TestTrack146SettleDueEdge(t *testing.T) {
	m, col := newEconObsMesh(t, config.MeshEconomyConfig{
		Unit:            "credits",
		SettleThreshold: "1.0",
	})
	pid := testutil.NewIdentity(t).PeerID

	// 0.6 under the threshold: no event.
	m.recordEconCharge(econ.DirectionPayable, pid, "", "c1", chargeOf("credits", "0.6"), nil)
	assert.False(t, col.seen(runtimeevents.KindMeshEconSettleDue))

	// 0.6 + 0.5 = 1.1 crosses: the edge fires once.
	m.recordEconCharge(econ.DirectionPayable, pid, "", "c2", chargeOf("credits", "0.5"), nil)
	require.True(t, col.waitFor(runtimeevents.KindMeshEconSettleDue))

	count := 0
	for _, k := range col.snapshot() {
		if k == runtimeevents.KindMeshEconSettleDue {
			count++
		}
	}
	assert.Equal(t, 1, count)

	// Already over: a further charge does not refire.
	m.recordEconCharge(econ.DirectionPayable, pid, "", "c3", chargeOf("credits", "0.4"), nil)
	time.Sleep(50 * time.Millisecond)
	count = 0
	for _, k := range col.snapshot() {
		if k == runtimeevents.KindMeshEconSettleDue {
			count++
		}
	}
	assert.Equal(t, 1, count, "settle_due is edge-triggered, not level")
}

func TestTrack146OverdueSweepPenalty(t *testing.T) {
	m, col := newEconObsMesh(t, config.MeshEconomyConfig{
		Unit:              "credits",
		SettleThreshold:   "1.0",
		SettleGracePeriod: time.Hour,
	})
	payer := testutil.NewIdentity(t).PeerID

	// An accrued receivable over threshold, older than the grace period.
	old := time.Now().UTC().Add(-2 * time.Hour)
	_, err := m.econLedger.Record(econ.Entry{
		PeerID:    payer,
		Direction: econ.DirectionReceivable,
		TaskID:    "task-stale",
		Unit:      "credits",
		Amount:    "1.5",
		State:     econ.StateAccrued,
		TS:        old,
	})
	require.NoError(t, err)

	m.econSweepOverdue(time.Now())

	sc, ok := m.scoreStore.Get(mustPID(t, payer))
	require.True(t, ok)
	op := sc.OpStats[OpEconSettle]
	assert.Equal(t, 0, op.Successes)
	assert.Equal(t, 1, op.Failures, "overdue epoch must land one econ_settle failure")
	require.True(t, col.waitFor(runtimeevents.KindMeshEconOverdue))

	// A second sweep over the same accrued set must not re-penalize.
	m.econSweepOverdue(time.Now().Add(30 * time.Minute))
	sc, _ = m.scoreStore.Get(mustPID(t, payer))
	assert.Equal(t, 1, sc.OpStats[OpEconSettle].Failures, "same debt epoch dedupes")

	// New accrual re-arms the epoch — a fresh charge lands another failure.
	_, err = m.econLedger.Record(econ.Entry{
		PeerID:    payer,
		Direction: econ.DirectionReceivable,
		TaskID:    "task-stale-2",
		Unit:      "credits",
		Amount:    "0.1",
		State:     econ.StateAccrued,
		TS:        old,
	})
	require.NoError(t, err)
	m.econSweepOverdue(time.Now())
	sc, _ = m.scoreStore.Get(mustPID(t, payer))
	assert.Equal(t, 2, sc.OpStats[OpEconSettle].Failures, "new accrual re-arms the epoch")

	// Peer view marks the unit overdue.
	require.Eventually(t, func() bool {
		v := m.peerEconView(payer)
		return v != nil && len(v.Overdue) == 1 && v.Overdue[0] == "credits"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestTrack146OverdueSweepRespectsThresholdAndGrace(t *testing.T) {
	m, _ := newEconObsMesh(t, config.MeshEconomyConfig{
		Unit:              "credits",
		SettleThreshold:   "1.0",
		SettleGracePeriod: time.Hour,
	})
	payer := testutil.NewIdentity(t).PeerID

	// Under the threshold: old but small — no penalty.
	_, err := m.econLedger.Record(econ.Entry{
		PeerID: payer, Direction: econ.DirectionReceivable,
		TaskID: "u1", Unit: "credits", Amount: "0.5",
		State: econ.StateAccrued, TS: time.Now().UTC().Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	// Over the threshold but fresh — inside the grace window.
	_, err = m.econLedger.Record(econ.Entry{
		PeerID: payer, Direction: econ.DirectionReceivable,
		TaskID: "u2", Unit: "hours", Amount: "2",
		State: econ.StateAccrued, TS: time.Now().UTC(),
	})
	require.NoError(t, err)

	m.econSweepOverdue(time.Now())
	sc, ok := m.scoreStore.Get(mustPID(t, payer))
	if ok {
		assert.Equal(t, 0, sc.OpStats[OpEconSettle].Failures)
	}
}

func TestTrack146DisputeResolveOutcomes(t *testing.T) {
	m, _ := newEconObsMesh(t, config.MeshEconomyConfig{Unit: "credits"})
	payee := testutil.NewIdentity(t).PeerID
	_, err := m.econLedger.Record(econ.Entry{
		PeerID:    payee,
		Direction: econ.DirectionPayable,
		TaskID:    "task-d1",
		Unit:      "credits",
		Amount:    "0.5",
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	})
	require.NoError(t, err)

	_, err = m.EconomyDispute("task-d1", "result unusable")
	require.NoError(t, err)

	sc, ok := m.scoreStore.Get(mustPID(t, payee))
	require.True(t, ok)
	found := false
	for _, o := range sc.Outcomes {
		if o.Op == OpEconDispute && o.Outcome == OutcomeDisputed {
			found = true
		}
	}
	assert.True(t, found, "dispute must land a pending econ_dispute outcome")

	// Resolve drop → written off, terminal outcome against the biller…
	// this is a payable entry: dropping rules against the payee.
	_, err = m.EconomyResolve("task-d1", true)
	require.NoError(t, err)
	sc, _ = m.scoreStore.Get(mustPID(t, payee))
	found = false
	for _, o := range sc.Outcomes {
		if o.Op == OpEconDispute && o.Outcome == OutcomeResolvedAgainst {
			found = true
		}
	}
	assert.True(t, found, "resolve must land a terminal econ_dispute outcome")
	assert.Equal(t, 1, sc.OpStats[OpEconDispute].Failures,
		"ruled-against resolves count against the biller")
}

func TestTrack146SettleHandshakeOutcomes(t *testing.T) {
	cfgA := config.MeshConfig{Enabled: true, Economy: track139CallerEcon()}
	cfgB := config.MeshConfig{Enabled: true, Economy: track139CalleeEcon()}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, nil)

	bID := meshB.node.ID().String()
	aID := meshA.node.ID().String()
	seedT142(t, meshA, bID, "task-146-a", "0.5", econ.DirectionPayable)
	seedT142(t, meshB, aID, "task-146-a", "0.5", econ.DirectionReceivable)

	_, settled, err := meshA.EconomySettle(context.Background(), bID, "credits")
	require.NoError(t, err)
	require.Len(t, settled, 1)

	// Payer side: completed econ_settle outcome + op counter.
	sc, ok := meshA.scoreStore.Get(meshB.node.ID())
	require.True(t, ok)
	assert.Equal(t, 1, sc.OpStats[OpEconSettle].Successes)
	found := false
	for _, o := range sc.Outcomes {
		if o.Op == OpEconSettle && o.Outcome == OutcomeCompleted {
			found = true
		}
	}
	assert.True(t, found, "payer records a settled outcome ref")

	// Payee side: the handler records its own completed outcome.
	sc, ok = meshB.scoreStore.Get(meshA.node.ID())
	require.True(t, ok)
	assert.Equal(t, 1, sc.OpStats[OpEconSettle].Successes)
}

func TestTrack146EconomyView(t *testing.T) {
	m, _ := newEconObsMesh(t, config.MeshEconomyConfig{
		Unit:            "credits",
		SettleThreshold: "1.0",
	})
	pid := testutil.NewIdentity(t).PeerID
	m.recordEconCharge(econ.DirectionPayable, pid, "", "c1", chargeOf("credits", "1.5"), nil)

	v := m.EconomyView()
	require.NotNil(t, v)
	assert.True(t, v.Enabled)
	assert.Equal(t, "credits", v.Unit)
	assert.Equal(t, "1.0", v.SettleThreshold)
	require.Contains(t, v.Peers, pid)
	s := v.Peers[pid]
	assert.Equal(t, 1, s.ChargesReceived)
	assert.Contains(t, s.OverThreshold, "credits")
	require.Len(t, v.RecentCharges, 1)
	assert.Equal(t, "c1", v.RecentCharges[0].CorrelationID)
}
