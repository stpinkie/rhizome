package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherDetectsFileChange(t *testing.T) {
	dir := t.TempDir()

	var got []string
	gotCh := make(chan []string, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := NewWatcher(ctx, dir, []string{}, func(paths []string) {
		select {
		case gotCh <- paths:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// Write a file.
	path := filepath.Join(dir, "AGENT.md")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case got = <-gotCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for change")
	}

	if len(got) != 1 || got[0] != "AGENT.md" {
		t.Fatalf("expected [AGENT.md], got %v", got)
	}
}

func TestWatcherExcludesPaths(t *testing.T) {
	dir := t.TempDir()

	var got []string
	gotCh := make(chan []string, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := NewWatcher(ctx, dir, []string{"logs/"}, func(paths []string) {
		select {
		case gotCh <- paths:
		default:
		}
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "out.log"), []byte("log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The AGENT.md change should come through; logs/out.log should be ignored.
	select {
	case got = <-gotCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for change")
	}

	if len(got) != 1 || got[0] != "AGENT.md" {
		t.Fatalf("expected [AGENT.md], got %v", got)
	}
}
