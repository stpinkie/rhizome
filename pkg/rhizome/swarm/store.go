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

// save writes the swarm registry atomically (temp file + rename).
func (s *Swarm) save() {
	if s.path == "" {
		return
	}
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
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	// os.Rename on Windows fails when the destination exists.
	_ = os.Remove(s.path)
	_ = os.Rename(tmp, s.path)
}
