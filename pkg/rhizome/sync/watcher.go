package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher observes the workspace and emits a batch of changed paths after a
// debounce window.
type Watcher struct {
	workspace string
	exclude   []string
	onChange  func(paths []string)
	debounce  time.Duration

	watcher *fsnotify.Watcher
	pending map[string]struct{}
	mu      sync.Mutex
	timer   *time.Timer
	stop    chan struct{}
	wg      sync.WaitGroup
}

// NewWatcher creates a recursive workspace watcher. exclude is a list of
// patterns for files and directories that should be ignored (e.g. "logs/",
// "*.sqlite"). onChange is called with a sorted list of changed paths
// (workspace-relative) after the debounce window.
func NewWatcher(
	ctx context.Context,
	workspace string,
	exclude []string,
	onChange func(paths []string),
) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create fsnotify watcher: %w", err)
	}

	w := &Watcher{
		workspace: workspace,
		exclude:   normalizeExcludes(exclude),
		onChange:  onChange,
		debounce:  2 * time.Second,
		watcher:   fsWatcher,
		pending:   make(map[string]struct{}),
		stop:      make(chan struct{}),
	}

	if err := w.addRecursive(workspace); err != nil {
		_ = fsWatcher.Close()
		return nil, fmt.Errorf("add recursive watch: %w", err)
	}

	w.wg.Add(1)
	go w.loop(ctx)

	return w, nil
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			if err != nil {
				// Log is unavailable here; surface via a no-op for now.
			}
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Resolve symlinks and get relative path.
	rel, err := filepath.Rel(w.workspace, event.Name)
	if err != nil {
		return
	}

	// Ignore .git and excluded paths.
	if w.isExcluded(rel) || strings.HasPrefix(rel, ".git") {
		return
	}

	// If a new directory was created, watch it too.
	if event.Op&fsnotify.Create == fsnotify.Create {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = w.addDir(event.Name)
		}
	}

	w.mu.Lock()
	w.pending[rel] = struct{}{}

	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.debounce, w.flush)
	w.mu.Unlock()
}

func (w *Watcher) flush() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}

	paths := make([]string, 0, len(w.pending))
	for p := range w.pending {
		paths = append(paths, p)
	}
	w.pending = make(map[string]struct{})
	w.mu.Unlock()

	sort.Strings(paths)
	w.onChange(paths)
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // continue walking
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil //nolint:nilerr // continue walking
		}
		if w.isExcluded(rel) || strings.HasPrefix(rel, ".git") {
			return filepath.SkipDir
		}
		return w.watcher.Add(path)
	})
}

func (w *Watcher) addDir(path string) error {
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // continue walking
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(w.workspace, p)
		if err != nil {
			return nil //nolint:nilerr // continue walking
		}
		if w.isExcluded(rel) || strings.HasPrefix(rel, ".git") {
			return filepath.SkipDir
		}
		return w.watcher.Add(p)
	})
}

func (w *Watcher) isExcluded(rel string) bool {
	for _, pat := range w.exclude {
		if pat == "" {
			continue
		}
		if strings.HasSuffix(pat, "/") {
			if strings.HasPrefix(rel+"/", pat) || rel == strings.TrimSuffix(pat, "/") {
				return true
			}
		}
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, filepath.Base(rel)); matched {
			return true
		}
	}
	return false
}

func normalizeExcludes(excludes []string) []string {
	var out []string
	for _, e := range excludes {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if strings.HasSuffix(e, "/") {
			out = append(out, e)
			continue
		}
		// Treat directory-looking excludes as directory patterns.
		if !strings.Contains(e, ".") && !strings.ContainsAny(e, "*?[]") {
			out = append(out, e+"/")
			continue
		}
		out = append(out, e)
	}
	return out
}

// Close stops the watcher.
func (w *Watcher) Close() error {
	close(w.stop)
	_ = w.watcher.Close()
	w.wg.Wait()
	return nil
}
