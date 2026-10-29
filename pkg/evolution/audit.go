package evolution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// auditMaxBytes is the per-file size cap for the evolution audit trail
// before rotation; auditKeepFiles bounds the number of rotated backups
// retained next to it. Mirrors the mesh audit trail posture.
const (
	auditMaxBytes  = 10 * 1024 * 1024
	auditKeepFiles = 3
)

// DefaultAuditPath returns the evolution audit trail location under the
// Rhizome home — one compliance trail per node, the mesh-audit.jsonl
// precedent. Entries carry the workspace they were recorded against.
func DefaultAuditPath() string {
	home := config.GetHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "evolution-audit.jsonl")
}

// auditLog appends JSONL entries to the evolution audit trail with
// size-based rotation. Same implementation posture as the mesh
// auditLogger: failures are silent — the audit trail must never break the
// operation it observes.
type auditLog struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
}

func newAuditLog(path string) *auditLog {
	return &auditLog{path: path, maxSize: auditMaxBytes, keep: auditKeepFiles}
}

// Log appends one entry. Failures are silent.
func (l *auditLog) Log(entry map[string]any) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if info, err := os.Stat(l.path); err == nil && info.Size() > l.maxSize {
		l.rotateLocked()
	}

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

func (l *auditLog) rotateLocked() {
	// Shift .N-1 → .N downward; os.Rename on Windows fails when the
	// destination exists, so remove each target first.
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.keep))
	for i := l.keep - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", l.path, i)
		newer := fmt.Sprintf("%s.%d", l.path, i+1)
		_ = os.Remove(newer)
		_ = os.Rename(old, newer)
	}
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
}

// AuditFilter narrows a read of the audit trail. Empty fields match
// everything. Op is an exact match, or a prefix match when it ends in
// ".*" (e.g. "draft.*"). Workspace matches the entry's workspace field.
// Since keeps entries at or after the instant.
type AuditFilter struct {
	Op        string
	Workspace string
	Since     time.Time
}

// matches reports whether entry passes every non-empty filter field.
func (f AuditFilter) matches(entry map[string]any) bool {
	if f.Op != "" {
		op, _ := entry["op"].(string)
		if strings.HasSuffix(f.Op, ".*") {
			if !strings.HasPrefix(op, strings.TrimSuffix(f.Op, "*")) {
				return false
			}
		} else if op != f.Op {
			return false
		}
	}
	if f.Workspace != "" {
		ws, _ := entry["workspace"].(string)
		if ws != f.Workspace {
			return false
		}
	}
	if !f.Since.IsZero() {
		ts, _ := entry["ts"].(string)
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || at.Before(f.Since) {
			return false
		}
	}
	return true
}

// ReadAuditTail returns the last n raw JSONL entries of the audit trail,
// oldest first; n<=0 returns every entry. A missing file yields nil, nil.
// The reader is the mesh ReadAuditTail windowing port: it starts with a
// 512 KiB window and grows to the full file when entries are large or n
// is big.
func ReadAuditTail(path string, n int) ([]json.RawMessage, error) {
	if path == "" {
		return nil, nil
	}

	//nolint:gosec // path is built from RHIZOME_HOME, not user-controlled.
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	const initialWindow = 512 * 1024
	window := int64(initialWindow)
	if window > size {
		window = size
	}

	var entries []json.RawMessage
	for {
		start := size - window
		if start < 0 {
			start = 0
		}
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		if start > 0 {
			// Drop the first, possibly partial, line of the window.
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				data = data[i+1:]
			} else {
				data = nil
			}
		}

		entries = collectAuditEntries(data, n)

		// Stop when we have enough entries or have already read the whole
		// file; an unbounded n only stops at the whole file.
		if (n > 0 && len(entries) >= n) || window >= size {
			if n > 0 && len(entries) > n {
				entries = entries[len(entries)-n:]
			}
			return entries, nil
		}
		// Grow the window and re-read. Doubling keeps the worst-case total
		// read within a small constant factor of the final window size.
		window *= 2
		if window > size {
			window = size
		}
	}
}

// collectAuditEntries splits data into lines, keeps the valid JSONL ones,
// and returns at most the last n. The slice is newest-last.
func collectAuditEntries(data []byte, n int) []json.RawMessage {
	lines := bytes.Split(data, []byte{'\n'})
	entries := make([]json.RawMessage, 0, n)
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !json.Valid(line) {
			continue
		}
		entries = append(entries, json.RawMessage(append([]byte(nil), line...)))
	}
	if n > 0 && len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries
}

// ReadAuditEntries returns the last n audit entries matching filter as
// decoded maps, oldest first. n<=0 returns every matching entry.
func ReadAuditEntries(path string, n int, filter AuditFilter) ([]map[string]any, error) {
	raw, err := ReadAuditTail(path, 0)
	if err != nil {
		return nil, err
	}
	entries := make([]map[string]any, 0, len(raw))
	for _, line := range raw {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if filter.matches(entry) {
			entries = append(entries, entry)
		}
	}
	if n > 0 && len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries, nil
}
