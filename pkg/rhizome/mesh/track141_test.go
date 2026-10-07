package mesh

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// Track 141 — mesh-side economy verbs: dispute / resolve / mark-settled
// plus their runtime events.

// newEconVerbMesh builds a single-node mesh with a live ledger and a
// subscribed event collector. No peer connectivity is needed — the verbs
// are local ledger operations.
func newEconVerbMesh(t *testing.T) (*Mesh, *eventCollector) {
	t.Helper()
	t.Setenv("RHIZOME_HOME", t.TempDir())
	id := testutil.NewIdentity(t)
	node, err := network.NewNode(context.Background(), id.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Close() })

	cfg := config.DefaultMeshConfig()
	cfg.Enabled = true
	cfg.AuditLog = false
	cfg.ActivityLog = false
	m := NewMesh(
		node,
		nil,
		id,
		cfg,
		func(context.Context, agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
			return toolshared.NewToolResult("ok"), nil, nil
		},
	)
	require.NotNil(t, m.EconomyLedger())

	bus := runtimeevents.NewBus()
	m.SetEventBus(bus)
	t.Cleanup(func() { _ = bus.Close() })
	col := &eventCollector{}
	_, err = bus.Channel().Subscribe(context.Background(), runtimeevents.SubscribeOptions{}, col.handle)
	require.NoError(t, err)
	return m, col
}

type eventCollector struct {
	mu    sync.Mutex
	kinds []runtimeevents.Kind
	attrs []map[string]any
}

func (c *eventCollector) handle(_ context.Context, e runtimeevents.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kinds = append(c.kinds, e.Kind)
	c.attrs = append(c.attrs, e.Attrs)
	return nil
}

func (c *eventCollector) seen(kind runtimeevents.Kind) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (c *eventCollector) waitFor(kind runtimeevents.Kind) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.seen(kind) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestMeshEconomyDisputeResolve(t *testing.T) {
	m, col := newEconVerbMesh(t)
	payee := testutil.NewIdentity(t).PeerID
	rec, err := m.econLedger.Record(econ.Entry{
		PeerID:    payee,
		Direction: econ.DirectionPayable,
		TaskID:    "task-m1",
		Unit:      "credits",
		Amount:    "0.5",
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	})
	require.NoError(t, err)

	out, err := m.EconomyDispute("task-m1", "result unusable")
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, econ.StateDisputed, out[0].State)
	require.True(t, col.waitFor(runtimeevents.KindMeshEconDispute))

	out, err = m.EconomyResolve("task-m1", true)
	require.NoError(t, err)
	require.Equal(t, econ.StateWrittenOff, out[0].State)
	require.True(t, col.waitFor(runtimeevents.KindMeshEconResolve))

	got, ok := m.econLedger.Get(rec.EntryID)
	require.True(t, ok)
	require.Equal(t, econ.StateWrittenOff, got.State)

	// Terminal entries can't be re-disputed.
	_, err = m.EconomyDispute("task-m1", "")
	require.Error(t, err)
}

func TestMeshEconomyMarkSettled(t *testing.T) {
	m, col := newEconVerbMesh(t)
	payee := testutil.NewIdentity(t).PeerID
	for _, tid := range []string{"task-a", "task-b"} {
		_, err := m.econLedger.Record(econ.Entry{
			PeerID:    payee,
			Direction: econ.DirectionPayable,
			TaskID:    tid,
			Unit:      "credits",
			Amount:    "0.5",
			State:     econ.StateAccrued,
			TS:        time.Now().UTC(),
		})
		require.NoError(t, err)
	}
	// A receivable + a foreign peer's payable must not be touched.
	_, err := m.econLedger.Record(econ.Entry{
		PeerID: payee, Direction: econ.DirectionReceivable, TaskID: "task-r",
		Unit: "credits", Amount: "0.9", State: econ.StateAccrued, TS: time.Now().UTC(),
	})
	require.NoError(t, err)

	offer, settled, err := m.EconomyMarkSettled(payee, "credits")
	require.NoError(t, err)
	require.Len(t, settled, 2)
	require.True(t, col.waitFor(runtimeevents.KindMeshEconSettle))
	for _, e := range settled {
		require.Equal(t, econ.StateSettled, e.State)
		require.Equal(t, offer.ID(), e.SettleID)
	}
	// The signed marker verifies under the node identity.
	require.NoError(t, offer.Verify(m.id.Libp2pPubKey))

	// Bad peer id is rejected before touching the ledger.
	_, _, err = m.EconomyMarkSettled("not-a-peer", "")
	require.Error(t, err)
}
