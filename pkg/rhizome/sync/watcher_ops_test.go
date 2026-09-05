package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEventWatcher starts a watcher on dir and delivers each debounced batch on
// the returned channel.
func newEventWatcher(t *testing.T, dir string, exclude []string) (*Watcher, chan []string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan []string, 16)
	w, err := NewWatcher(ctx, dir, exclude, func(paths []string) {
		select {
		case ch <- paths:
		default:
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(); cancel() })
	return w, ch
}

func nextBatch(t *testing.T, ch chan []string, timeout time.Duration) []string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(timeout):
		t.Fatal("timeout waiting for watcher event")
		return nil
	}
}

// TestWatcherDetectsDelete verifies removing a tracked file produces a change
// event for that path.
func TestWatcherDetectsDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	_, ch := newEventWatcher(t, dir, nil)

	require.NoError(t, os.Remove(path))
	got := nextBatch(t, ch, 10*time.Second)
	assert.Contains(t, got, "gone.txt")
}

// TestWatcherDetectsRename verifies renaming a file produces change events.
// The debounced batch may contain the old name, the new name, or both
// depending on how the OS reports the rename.
func TestWatcherDetectsRename(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(oldPath, []byte("x"), 0o644))

	_, ch := newEventWatcher(t, dir, nil)

	require.NoError(t, os.Rename(oldPath, newPath))
	got := nextBatch(t, ch, 10*time.Second)

	found := false
	for _, p := range got {
		if p == "new.txt" || p == "old.txt" {
			found = true
		}
	}
	assert.True(t, found, "expected rename event for old.txt/new.txt, got %v", got)
}

// TestWatcherDebouncesRapidSaves verifies a burst of writes to the same file
// collapses into a single debounced batch.
func TestWatcherDebouncesRapidSaves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")

	_, ch := newEventWatcher(t, dir, nil)

	// Ten writes at 100ms intervals stay well under the 2s debounce window,
	// so the timer keeps resetting and everything lands in one flush.
	for i := 0; i < 10; i++ {
		require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("v%d\n", i)), 0o644))
		time.Sleep(100 * time.Millisecond)
	}

	got := nextBatch(t, ch, 10*time.Second)
	assert.Contains(t, got, "note.txt")

	// No further batch should arrive within another debounce window.
	select {
	case extra := <-ch:
		t.Fatalf("unexpected extra watcher batch after debounce: %v", extra)
	case <-time.After(3 * time.Second):
	}
}

// TestWatcherIgnoresSymlinkedDirs verifies that a symlinked directory inside
// the workspace is not followed: changes inside the link target produce no
// events. Skipped when the platform/user cannot create symlinks (common on
// Windows without Developer Mode or elevation).
func TestWatcherIgnoresSymlinkedDirs(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()

	link := filepath.Join(dir, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	_, ch := newEventWatcher(t, dir, nil)

	// Write through the symlink into the external directory.
	require.NoError(t, os.WriteFile(filepath.Join(link, "inside.txt"), []byte("x"), 0o644))
	// Write a real file so we can confirm the watcher is alive.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real.txt"), []byte("x"), 0o644))

	got := nextBatch(t, ch, 10*time.Second)
	assert.Contains(t, got, "real.txt")
	for _, p := range got {
		assert.False(t, strings.HasPrefix(p, "linked"), "event leaked through symlinked dir: %v", got)
	}

	// Wait past one more debounce window for any late event mentioning the
	// symlinked path.
	for {
		select {
		case batch := <-ch:
			for _, p := range batch {
				assert.False(t, strings.HasPrefix(p, "linked"), "late event leaked through symlinked dir: %v", batch)
			}
		case <-time.After(3 * time.Second):
			return
		}
	}
}
