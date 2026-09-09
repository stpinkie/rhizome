package swarm

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxStoredRuns bounds persisted orchestration runs so the file stays small.
const maxStoredRuns = 256

// RunRecord is the persisted view of one RunGoal orchestration.
type RunRecord struct {
	RunID      string          `json:"run_id"`
	SwarmID    string          `json:"swarm_id"`
	Goal       string          `json:"goal"`
	Status     string          `json:"status"` // running | done | partial | failed
	Subtasks   []SubtaskResult `json:"subtasks,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at,omitempty"`
	DurationMS int64           `json:"duration_ms,omitempty"`
}

// runStore keeps recent orchestration runs in memory and, when a path is
// configured, persists them as a bounded JSONL file at
// <RHIZOME_HOME>/swarm-runs.jsonl.
type runStore struct {
	mu   sync.Mutex
	path string
	runs []RunRecord
}

func newRunStore(path string) *runStore {
	return &runStore{path: path}
}

// Record upserts a run (keyed by RunID) and persists the store. The save
// happens under rs.mu so concurrent Record calls cannot interleave their
// snapshots — the on-disk file always reflects the latest in-memory state.
func (rs *runStore) Record(rec RunRecord) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	replaced := false
	for i := range rs.runs {
		if rs.runs[i].RunID == rec.RunID {
			rs.runs[i] = rec
			replaced = true
			break
		}
	}
	if !replaced {
		rs.runs = append(rs.runs, rec)
	}
	if len(rs.runs) > maxStoredRuns {
		rs.runs = append([]RunRecord(nil), rs.runs[len(rs.runs)-maxStoredRuns:]...)
	}
	snapshot := append([]RunRecord(nil), rs.runs...)
	rs.save(snapshot)
}

// List returns recorded runs, newest last. Empty swarmID returns all runs.
func (rs *runStore) List(swarmID string) []RunRecord {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]RunRecord, 0, len(rs.runs))
	for _, r := range rs.runs {
		if swarmID == "" || r.SwarmID == swarmID {
			out = append(out, r)
		}
	}
	return out
}

// Get returns one run by id.
func (rs *runStore) Get(runID string) (RunRecord, bool) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, r := range rs.runs {
		if r.RunID == runID {
			return r, true
		}
	}
	return RunRecord{}, false
}

// Load reads the persisted run file; missing/corrupt files are tolerated.
func (rs *runStore) Load() error {
	if rs.path == "" {
		return nil
	}
	f, err := os.Open(rs.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open run store: %w", err)
	}
	defer f.Close()

	rs.mu.Lock()
	defer rs.mu.Unlock()
	dec := json.NewDecoder(f)
	for {
		var rec RunRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode run record: %w", err)
		}
		rs.runs = append(rs.runs, rec)
	}
	if len(rs.runs) > maxStoredRuns {
		rs.runs = rs.runs[len(rs.runs)-maxStoredRuns:]
	}
	return nil
}

// save rewrites the run file atomically.
func (rs *runStore) save(records []RunRecord) {
	if rs.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(rs.path), 0o700); err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(rs.path), filepath.Base(rs.path)+".tmp.*")
	if err != nil {
		return
	}
	tmp := f.Name()
	enc := json.NewEncoder(f)
	for _, rec := range records {
		if enc.Encode(rec) != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return
		}
	}
	if f.Close() != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, rs.path); err != nil {
		_ = os.Remove(tmp)
	}
}
