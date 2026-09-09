package swarm

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanWavesIndependent(t *testing.T) {
	waves, err := planWaves([]Subtask{
		{ID: "a", Task: "one"},
		{ID: "b", Task: "two"},
	})
	require.NoError(t, err)
	require.Len(t, waves, 1)
	assert.ElementsMatch(t, []int{0, 1}, waves[0])
}

func TestPlanWavesChain(t *testing.T) {
	waves, err := planWaves([]Subtask{
		{ID: "a", Task: "one"},
		{ID: "b", Task: "two", DependsOn: []string{"a"}},
		{ID: "c", Task: "three", DependsOn: []string{"b"}},
	})
	require.NoError(t, err)
	require.Len(t, waves, 3)
	assert.Equal(t, []int{0}, waves[0])
	assert.Equal(t, []int{1}, waves[1])
	assert.Equal(t, []int{2}, waves[2])
}

func TestPlanWavesFanIn(t *testing.T) {
	waves, err := planWaves([]Subtask{
		{ID: "a", Task: "one"},
		{ID: "b", Task: "two"},
		{ID: "c", Task: "three", DependsOn: []string{"a", "b"}},
	})
	require.NoError(t, err)
	require.Len(t, waves, 2)
	assert.ElementsMatch(t, []int{0, 1}, waves[0])
	assert.Equal(t, []int{2}, waves[1])
}

func TestPlanWavesByIndex(t *testing.T) {
	// depends_on entries may reference the subtask's array index.
	waves, err := planWaves([]Subtask{
		{ID: "0", Task: "one"},
		{ID: "1", Task: "two", DependsOn: []string{"0"}},
	})
	require.NoError(t, err)
	require.Len(t, waves, 2)
}

func TestPlanWavesCycleRejected(t *testing.T) {
	_, err := planWaves([]Subtask{
		{ID: "a", Task: "one", DependsOn: []string{"b"}},
		{ID: "b", Task: "two", DependsOn: []string{"a"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestPlanWavesUnknownDep(t *testing.T) {
	_, err := planWaves([]Subtask{
		{ID: "a", Task: "one", DependsOn: []string{"missing"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestRunStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swarm-runs.jsonl")
	rs := newRunStore(path)
	rec := RunRecord{
		RunID:   "run-1",
		SwarmID: "ops",
		Goal:    "test goal",
		Status:  "done",
		Subtasks: []SubtaskResult{
			{Subtask: Subtask{ID: "0", Task: "t1"}, Status: "done", Result: "r"},
		},
		Summary:    "all good",
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC(),
	}
	rs.Record(rec)

	fresh := newRunStore(path)
	require.NoError(t, fresh.Load())
	got, ok := fresh.Get("run-1")
	require.True(t, ok)
	assert.Equal(t, "ops", got.SwarmID)
	assert.Equal(t, "done", got.Status)
	assert.Equal(t, "all good", got.Summary)
	require.Len(t, got.Subtasks, 1)
	assert.Equal(t, "t1", got.Subtasks[0].Task)
}

func TestRunStoreBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "swarm-runs.jsonl")
	rs := newRunStore(path)
	for i := 0; i < maxStoredRuns+10; i++ {
		rs.Record(RunRecord{
			RunID:   fmt.Sprintf("run-%d", i),
			SwarmID: "ops",
			Goal:    "g",
			Status:  "done",
		})
	}
	assert.Len(t, rs.List(""), maxStoredRuns)
	fresh := newRunStore(path)
	require.NoError(t, fresh.Load())
	assert.Len(t, fresh.List(""), maxStoredRuns)
}
