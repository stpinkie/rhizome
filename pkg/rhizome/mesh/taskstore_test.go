package mesh

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestTaskStorePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-tasks.jsonl")
	s := NewTaskStoreWithPath(path)

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	// Submit and finish a terminal task.
	task, created, err := s.Submit(owner, agenttask.Request{CorrelationID: "c-1", TargetAgentID: "main"})
	require.NoError(t, err)
	require.True(t, created)
	s.Start(task.ID, func() {})
	s.Finish(task.ID, agenttask.StatusDone, toolshared.NewToolResult("ok"), "")

	// Force a synchronous save.
	s.flushSave()
	require.FileExists(t, path)

	// Load into a fresh store and verify the task is restored and terminal.
	s2 := NewTaskStoreWithPath(path)
	restarted, err := s2.Load()
	require.NoError(t, err)
	require.Empty(t, restarted)

	loaded, ok := s2.getOwned(task.ID, owner)
	require.True(t, ok)
	assert.Equal(t, agenttask.StatusDone, loaded.Status)
	require.NotNil(t, loaded.Result)
	assert.Equal(t, "ok", loaded.Result.ForLLM)
}

func TestTaskStoreRestartMarksRunningAsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-tasks.jsonl")
	s := NewTaskStoreWithPath(path)

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	task, created, err := s.Submit(owner, agenttask.Request{TargetAgentID: "main"})
	require.NoError(t, err)
	require.True(t, created)
	s.Start(task.ID, func() {})

	// Simulate an unclean shutdown: the task is running, but no Finish call.
	s.flushSave()

	s2 := NewTaskStoreWithPath(path)
	restarted, err := s2.Load()
	require.NoError(t, err)
	require.Len(t, restarted, 1)
	assert.Equal(t, task.ID, restarted[0].ID)
	assert.Equal(t, agenttask.StatusError, restarted[0].Status)
	assert.Equal(t, "daemon restarted", restarted[0].Err)

	// Wait on a restarted task should return immediately.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waited, ok := s2.Wait(ctx, task.ID, owner, time.Second)
	require.True(t, ok)
	assert.Equal(t, agenttask.StatusError, waited.Status)
}

func TestTaskStoreLoadMissingFile(t *testing.T) {
	s := NewTaskStoreWithPath(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	restarted, err := s.Load()
	require.NoError(t, err)
	assert.Empty(t, restarted)
}

func TestTaskStoreInMemoryNoPersistence(t *testing.T) {
	dir := t.TempDir()
	s := NewTaskStore()
	s.SetPath(filepath.Join(dir, "tasks.jsonl"))

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	task, _, err := s.Submit(owner, agenttask.Request{TargetAgentID: "main"})
	require.NoError(t, err)
	s.Start(task.ID, func() {})
	s.Finish(task.ID, agenttask.StatusDone, nil, "")
	s.flushSave()

	// A new in-memory store with the same path loads the file.
	s2 := NewTaskStoreWithPath(s.path)
	_, err = s2.Load()
	require.NoError(t, err)
	_, ok := s2.getOwned(task.ID, owner)
	assert.True(t, ok)
}

func TestTaskStoreSaveError(t *testing.T) {
	// Path in a non-existent parent directory with bad permissions can't be
	// created on Windows the same way as Unix, so use an invalid path shape.
	path := filepath.Join(t.TempDir(), "invalid?name.jsonl")
	s := NewTaskStoreWithPath(path)

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	_, _, err = s.Submit(owner, agenttask.Request{TargetAgentID: "main"})
	require.NoError(t, err)

	// Give the coalescing timer time to fire.
	time.Sleep(2 * taskSaveCoalesce)
	err = s.SaveError()
	assert.Error(t, err, "expected a save error for invalid path")
}

func TestTaskStoreCloseSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-tasks.jsonl")
	s := NewTaskStoreWithPath(path)

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	_, _, err = s.Submit(owner, agenttask.Request{TargetAgentID: "main"})
	require.NoError(t, err)

	s.Close()
	assert.FileExists(t, path)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(b), "mesh stopped")
}
