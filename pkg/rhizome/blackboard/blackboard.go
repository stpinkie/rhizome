// Package blackboard implements the swarm shared-context blackboard: a
// directory of append-only per-member note shards plus a coordinator-curated
// context.md document. It lives under the synced workspace
// (swarm/<id>/notes/<author>.jsonl) so workspace git sync propagates context
// between members without a bespoke replication protocol, and per-author
// shards keep concurrent appends merge-conflict-free.
package blackboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Note is one blackboard entry.
type Note struct {
	TS      time.Time `json:"ts"`
	Author  string    `json:"author"`
	Kind    string    `json:"kind,omitempty"` // note | decision | blocker | result | ...
	Key     string    `json:"key,omitempty"`
	Content string    `json:"content"`
	// ExpiresAt optionally bounds the note's visibility; zero means it never
	// expires.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Options bounds blackboard growth and digest size.
type Options struct {
	// MaxNoteBytes caps a single note's content. Defaults to 4 KiB.
	MaxNoteBytes int
	// MaxNotesPerShard bounds entries kept per author shard; the oldest are
	// dropped on overflow. Defaults to 500.
	MaxNotesPerShard int
	// DigestBytes caps Digest output. Defaults to 8 KiB.
	DigestBytes int
	// NoteTTL is the default expiry applied to notes without an explicit
	// expires_at. 0 keeps notes forever.
	NoteTTL time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxNoteBytes <= 0 {
		o.MaxNoteBytes = 4096
	}
	if o.MaxNotesPerShard <= 0 {
		o.MaxNotesPerShard = 500
	}
	if o.DigestBytes <= 0 {
		o.DigestBytes = 8192
	}
	return o
}

// authorPattern bounds shard file names to safe identifiers.
var authorPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// Blackboard is a file-backed shared context store for one swarm.
type Blackboard struct {
	dir  string
	opts Options
}

// New opens (creating if needed) the blackboard rooted at dir.
func New(dir string, opts Options) *Blackboard {
	return &Blackboard{dir: dir, opts: opts.withDefaults()}
}

// notesDir is the shard directory.
func (b *Blackboard) notesDir() string {
	return filepath.Join(b.dir, "notes")
}

// contextPath is the curated context document.
func (b *Blackboard) contextPath() string {
	return filepath.Join(b.dir, "context.md")
}

// shardPath maps an author id to its shard file.
func (b *Blackboard) shardPath(author string) (string, error) {
	author = strings.TrimSpace(author)
	if author == "" {
		return "", fmt.Errorf("author is required")
	}
	if !authorPattern.MatchString(author) {
		return "", fmt.Errorf("invalid author id %q", author)
	}
	return filepath.Join(b.notesDir(), author+".jsonl"), nil
}

// Append writes a note to the author's shard. The author is always the
// caller — notes are attributed by shard file, so a writer cannot forge
// another member's shard.
func (b *Blackboard) Append(author string, n Note) error {
	path, err := b.shardPath(author)
	if err != nil {
		return err
	}
	n.Content = strings.TrimSpace(n.Content)
	if n.Content == "" {
		return fmt.Errorf("note content is required")
	}
	if len(n.Content) > b.opts.MaxNoteBytes {
		n.Content = n.Content[:b.opts.MaxNoteBytes]
	}
	n.Author = author
	if n.TS.IsZero() {
		n.TS = time.Now().UTC()
	}
	if n.ExpiresAt.IsZero() && b.opts.NoteTTL > 0 {
		n.ExpiresAt = n.TS.Add(b.opts.NoteTTL)
	}
	if err := os.MkdirAll(b.notesDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return b.truncateShard(path)
}

// truncateShard rewrites a shard keeping only the newest MaxNotesPerShard
// entries. Append-heavy shards stay bounded without a lock: the rewrite
// goes through a unique temp file + rename like the swarm registry store.
func (b *Blackboard) truncateShard(path string) error {
	notes, err := readShard(path)
	if err != nil {
		return err
	}
	if len(notes) <= b.opts.MaxNotesPerShard {
		return nil
	}
	keep := notes[len(notes)-b.opts.MaxNotesPerShard:]
	var sb strings.Builder
	for _, n := range keep {
		data, err := json.Marshal(n)
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(b.notesDir(), ".shard-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(sb.String()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// readShard parses one shard file; unreadable lines are skipped.
func readShard(path string) ([]Note, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var notes []Note
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var n Note
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			continue
		}
		notes = append(notes, n)
	}
	return notes, sc.Err()
}

// Notes returns all non-expired notes across shards, sorted by timestamp.
// since filters out notes older than the given time (zero = all).
func (b *Blackboard) Notes(since time.Time) []Note {
	entries, err := os.ReadDir(b.notesDir())
	if err != nil {
		return nil
	}
	now := time.Now()
	var out []Note
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		notes, err := readShard(filepath.Join(b.notesDir(), e.Name()))
		if err != nil {
			continue
		}
		for _, n := range notes {
			if !n.ExpiresAt.IsZero() && now.After(n.ExpiresAt) {
				continue
			}
			if !since.IsZero() && n.TS.Before(since) {
				continue
			}
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	return out
}

// ReadContext returns the coordinator-curated context document, or "".
func (b *Blackboard) ReadContext() string {
	data, err := os.ReadFile(b.contextPath())
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteContext replaces the curated context document atomically.
func (b *Blackboard) WriteContext(content string) error {
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.dir, ".context-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, b.contextPath()); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// Digest renders the curated context plus the newest notes as a compact
// markdown blob for prompt injection, capped at DigestBytes.
func (b *Blackboard) Digest() string {
	var sb strings.Builder
	if ctx := strings.TrimSpace(b.ReadContext()); ctx != "" {
		sb.WriteString(ctx)
		sb.WriteString("\n\n")
	}
	notes := b.Notes(time.Time{})
	if len(notes) > 0 {
		sb.WriteString("### Recent swarm notes\n")
		// Newest first for the digest; truncate to the byte cap.
		for i := len(notes) - 1; i >= 0; i-- {
			n := notes[i]
			line := fmt.Sprintf("- [%s] %s", n.TS.Format("2006-01-02 15:04"), n.Author)
			if n.Kind != "" {
				line += " (" + n.Kind + ")"
			}
			if n.Key != "" {
				line += " " + n.Key + ":"
			}
			line += " " + n.Content + "\n"
			if sb.Len()+len(line) > b.opts.DigestBytes {
				sb.WriteString("- (older notes truncated)\n")
				break
			}
			sb.WriteString(line)
		}
	}
	out := sb.String()
	if len(out) > b.opts.DigestBytes {
		out = out[:b.opts.DigestBytes]
	}
	return strings.TrimSpace(out)
}
