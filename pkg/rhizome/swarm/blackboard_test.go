package swarm

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
)

// contextWiring wires each swarm's blackboard dir to a per-node workspace.
func contextWiring(wsA, wsB string, a, b *Swarm) {
	a.SetContextDirFunc(func(id string) string { return filepath.Join(wsA, "swarm", id) })
	b.SetContextDirFunc(func(id string) string { return filepath.Join(wsB, "swarm", id) })
}

func TestPostNotePropagatesToMember(t *testing.T) {
	wsA, wsB := t.TempDir(), t.TempDir()
	swarmA, swarmB := newTestPair(t, config.DefaultSwarmConfig(), t.TempDir(), t.TempDir())
	contextWiring(wsA, wsB, swarmA, swarmB)

	ctx := context.Background()
	require.NoError(t, swarmA.Join(ctx, "ops"))
	require.NoError(t, swarmB.Join(ctx, "ops"))

	require.Eventually(t, func() bool {
		return len(swarmA.Members("ops")) >= 1 && len(swarmB.Members("ops")) >= 1
	}, 10*time.Second, 100*time.Millisecond)

	require.NoError(t, swarmA.PostNote(ctx, "ops", "decision", "deploy", "ship on friday", 0))

	// B should have the note in A's shard via the MsgNote push.
	require.Eventually(t, func() bool {
		for _, n := range swarmB.ContextNotes("ops", time.Time{}) {
			if n.Author == swarmA.PeerID() && n.Content == "ship on friday" {
				return true
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "note should propagate to member B")
}

func TestPostNoteRequiresMembership(t *testing.T) {
	swarmA, _ := newTestPair(t, config.DefaultSwarmConfig(), t.TempDir(), t.TempDir())
	swarmA.SetContextDirFunc(func(id string) string { return filepath.Join(t.TempDir(), id) })

	err := swarmA.PostNote(context.Background(), "notjoined", "", "", "hello", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a member")
}

func TestSetContextCoordinatorOnly(t *testing.T) {
	wsA, wsB := t.TempDir(), t.TempDir()
	swarmA, swarmB := newTestPair(t, config.DefaultSwarmConfig(), t.TempDir(), t.TempDir())
	contextWiring(wsA, wsB, swarmA, swarmB)

	ctx := context.Background()
	require.NoError(t, swarmA.Join(ctx, "ops"))
	require.NoError(t, swarmB.Join(ctx, "ops"))

	require.Eventually(t, func() bool {
		return swarmA.Coordinator("ops") != "" && swarmB.Coordinator("ops") != ""
	}, 10*time.Second, 100*time.Millisecond)

	// The coordinator (lowest peer id) may write; the other may not.
	coord := swarmA.Coordinator("ops")
	var writer, nonWriter *Swarm
	if coord == swarmA.PeerID() {
		writer, nonWriter = swarmA, swarmB
	} else {
		writer, nonWriter = swarmB, swarmA
	}
	require.NoError(t, writer.SetContext("ops", "focus: sprint v0.9"))
	assert.Contains(t, writer.ReadContext("ops"), "sprint v0.9")

	err := nonWriter.SetContext("ops", "hijack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "coordinator")
}

func TestContextDigestFeedsRunGoal(t *testing.T) {
	ws := t.TempDir()
	cfg := config.DefaultSwarmConfig()
	// Keep the unclaimed offer's resolution window short so the run finishes.
	cfg.Queue.OfferTTL = 300 * time.Millisecond
	cfg.Queue.ClaimWindow = 100 * time.Millisecond
	swarmA, _ := newTestPair(t, cfg, t.TempDir(), t.TempDir())
	swarmA.SetContextDirFunc(func(id string) string { return filepath.Join(ws, id) })

	require.NoError(t, swarmA.Join(context.Background(), "ops"))
	require.NoError(t, swarmA.PostNote(context.Background(), "ops", "note", "", "shared fact", 0))

	var gotGoal string
	swarmA.SetDecomposer(func(ctx context.Context, goal string) ([]Subtask, error) {
		gotGoal = goal
		return nil, nil
	})

	_, err := swarmA.RunGoal(context.Background(), "ops", "the goal", "main")
	require.NoError(t, err)
	assert.Contains(t, gotGoal, "shared fact")
	assert.Contains(t, gotGoal, "the goal")
}
