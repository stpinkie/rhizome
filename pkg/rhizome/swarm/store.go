package swarm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// persistedState is the on-disk form of the swarm registry at
// <RHIZOME_HOME>/swarms.json.
type persistedState struct {
	Swarms map[string]persistedSwarm `json:"swarms"`
}

type persistedSwarm struct {
	JoinedAt time.Time `json:"joined_at"`
	Members  []Member  `json:"members,omitempty"`
}

// load reads the persisted swarm registry. Missing or unreadable files are
// not an error — the node starts with an empty registry.
func (s *Swarm) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil || len(data) == 0 {
		return
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ps := range st.Swarms {
		swarm, ok := s.swarms[id]
		if !ok {
			swarm = &swarmState{ID: id, Members: make(map[string]Member)}
			s.swarms[id] = swarm
		}
		if swarm.JoinedAt.IsZero() {
			swarm.JoinedAt = ps.JoinedAt
		}
		for _, m := range ps.Members {
			if _, seen := swarm.Members[m.PeerID]; !seen {
				swarm.Members[m.PeerID] = m
			}
		}
	}
}

// save writes the swarm registry atomically (temp file + rename). The whole
// snapshot/write/rename sequence is serialized by saveMu: callers reach this
// concurrently, and two interleaved saves could remove swarms.json after the
// shared tmp file was already renamed away. Holding the lock across the
// snapshot also keeps the last write the newest.
func (s *Swarm) save() {
	if s.path == "" {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.RLock()
	st := persistedState{Swarms: make(map[string]persistedSwarm, len(s.swarms))}
	for id, swarm := range s.swarms {
		members := make([]Member, 0, len(swarm.Members))
		for _, m := range swarm.Members {
			members = append(members, m)
		}
		st.Swarms[id] = persistedSwarm{JoinedAt: swarm.JoinedAt, Members: members}
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return
	}
	// A unique tmp name per save (as in runStore.save) keeps the write safe
	// even across processes sharing one RHIZOME_HOME, where saveMu cannot
	// help.
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp.*")
	if err != nil {
		return
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if f.Close() != nil {
		_ = os.Remove(tmp)
		return
	}
	// Rename first: os.Rename replaces an existing destination on Windows
	// too (MoveFileEx with MOVEFILE_REPLACE_EXISTING), so removing the old
	// file up front would only open a window where a crash leaves no roster
	// at all. The remove is the fallback for a destination another process
	// holds open, and saveMu makes that sequence atomic for other savers.
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if os.Rename(tmp, s.path) != nil {
			_ = os.Remove(tmp)
		}
	}
}
