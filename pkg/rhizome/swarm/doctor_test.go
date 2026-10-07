package swarm

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

func doctorMember(report DoctorReport, pid string) (DoctorMember, bool) {
	for _, m := range report.Members {
		if m.PeerID == pid {
			return m, true
		}
	}
	return DoctorMember{}, false
}

func TestDoctorRosterDiff(t *testing.T) {
	swarmA, swarmB := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	ctx := context.Background()

	require.NoError(t, swarmA.Join(ctx, "ops"))
	require.NoError(t, swarmB.Join(ctx, "ops"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("ops")) >= 1 && len(swarmB.Members("ops")) >= 1
	}, 10*time.Second, 100*time.Millisecond, "members should discover each other")

	// A phantom sits in A's roster — no node answers its queries.
	phantom := testutil.NewIdentity(t)
	pidC, err := peer.Decode(phantom.PeerID)
	require.NoError(t, err)
	swarmA.addMember("ops", pidC, "test")

	report, err := swarmA.Doctor(ctx, "ops")
	require.NoError(t, err)
	require.Len(t, report.Members, 2, "one real member + one phantom")
	assert.Equal(t, swarmA.PeerID(), report.SelfID)
	assert.Equal(t, swarmA.Coordinator("ops"), report.Coordinator)

	b, ok := doctorMember(report, swarmB.PeerID())
	require.True(t, ok)
	assert.True(t, b.Reachable)
	assert.True(t, b.ListsUs, "B should list A after mutual discovery")

	c, ok := doctorMember(report, phantom.PeerID)
	require.True(t, ok)
	assert.False(t, c.Reachable)
	assert.NotEmpty(t, c.Error, "unreachable member reports the dial error")

	// Unreachable members are not roster asymmetry — only reachable
	// responders that lack us are.
	assert.NotContains(t, report.Asymmetric, phantom.PeerID)

	// Coordinator reachability is consistent with the member entries.
	switch report.Coordinator {
	case swarmA.PeerID():
		assert.True(t, report.CoordinatorUp, "self is always up")
	case swarmB.PeerID():
		assert.True(t, report.CoordinatorUp, "B is reachable")
	default:
		assert.False(t, report.CoordinatorUp, "phantom coordinator is unreachable")
	}
}

func TestDoctorReportsEpochAndUndiscovered(t *testing.T) {
	swarmA, swarmB := newTestPair(t, fastPresenceConfig(), t.TempDir(), t.TempDir())
	ctx := context.Background()

	require.NoError(t, swarmA.Join(ctx, "ops"))
	require.NoError(t, swarmB.Join(ctx, "ops"))
	require.Eventually(t, func() bool {
		return len(swarmA.Members("ops")) >= 1 && len(swarmB.Members("ops")) >= 1
	}, 10*time.Second, 100*time.Millisecond)

	// Whichever side won election becomes the coordinator that publishes
	// state; the probe then doctors from the other side.
	coordID := swarmA.Coordinator("ops")
	coordSw, probeSw := swarmA, swarmB
	if coordID != swarmA.PeerID() {
		coordSw, probeSw = swarmB, swarmA
	}
	require.Equal(t, coordSw.PeerID(), coordID)

	coordSw.SetStateWriter(func(string, []byte) error { return nil })
	coordSw.writeSharedState("ops")

	// The coordinator's roster lists a member the probe has never heard of.
	phantom := testutil.NewIdentity(t)
	pidD, err := peer.Decode(phantom.PeerID)
	require.NoError(t, err)
	coordSw.addMember("ops", pidD, "test")

	report, err := probeSw.Doctor(ctx, "ops")
	require.NoError(t, err)

	entry, ok := doctorMember(report, coordID)
	require.True(t, ok, "coordinator should be in the probe's roster")
	assert.True(t, entry.Reachable)
	assert.True(t, entry.ListsUs)
	assert.EqualValues(t, 1, entry.Epoch, "coordinator reports its published epoch")

	assert.Contains(t, report.Undiscovered, phantom.PeerID,
		"member the coordinator knows but the probe doesn't is undiscovered")
}

func TestReelectPreferConnected(t *testing.T) {
	idA := testutil.NewIdentity(t)
	sw := newTestSwarmNode(t, idA, t.TempDir(), func(peer.ID) bool { return true })
	sw.joinLocal("ops")

	// Generate a phantom member id smaller than ours so it wins the
	// deterministic election when connectivity is ignored.
	var phantom peer.ID
	for i := 0; i < 64; i++ {
		cand := testutil.NewIdentity(t)
		if cand.PeerID >= idA.PeerID {
			continue
		}
		pid, err := peer.Decode(cand.PeerID)
		require.NoError(t, err)
		phantom = pid
		break
	}
	require.NotNil(t, phantom, "could not mint a phantom id smaller than ours")
	sw.addMember("ops", phantom, "test")

	require.Equal(t, phantom.String(), sw.Coordinator("ops"),
		"default election is deterministic — smallest member wins")

	// prefer_connected excludes the never-connected phantom.
	sw.cfg.Coordination.PreferConnected = true
	sw.mu.Lock()
	changed := sw.reelectLocked("ops")
	sw.mu.Unlock()
	assert.True(t, changed, "excluding the phantom changes the winner")
	assert.Equal(t, idA.PeerID, sw.Coordinator("ops"),
		"with prefer_connected, only connected candidates (self) remain")
}

func TestSwarmInfoLastStateWrite(t *testing.T) {
	idA := testutil.NewIdentity(t)
	sw := newTestSwarmNode(t, idA, t.TempDir(), func(peer.ID) bool { return true })
	sw.joinLocal("ops")

	var written []byte
	sw.SetStateWriter(func(_ string, data []byte) error {
		written = data
		return nil
	})
	sw.writeSharedState("ops")
	require.NotEmpty(t, written, "coordinator should publish state")

	infos := sw.Swarms()
	require.Len(t, infos, 1)
	assert.EqualValues(t, 1, infos[0].Epoch)
	assert.False(t, infos[0].LastStateWrite.IsZero(),
		"a successful state write stamps last_state_write")
	assert.True(t, time.Since(infos[0].LastStateWrite) < time.Minute)
}
