package acp

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// maxSessionRecords bounds the session index; oldest entries are evicted
// FIFO.
const maxSessionRecords = 256

// SessionRecord maps one ACP session id to its Rhizome session so clients
// can resume conversations via session/load after an agent restart.
type SessionRecord struct {
	SessionID  string    `json:"session_id"`
	AgentID    string    `json:"agent_id"`
	SessionKey string    `json:"session_key"`
	Cwd        string    `json:"cwd,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// Mode is the session's permission-mode override ("" = inherit the
	// server policy); restored on session/load.
	Mode string `json:"mode,omitempty"`
	// AllowAlways/DenyAlways restore cached permission decisions.
	AllowAlways []string `json:"allow_always,omitempty"`
	DenyAlways  []string `json:"deny_always,omitempty"`
}

// SessionStore is a small JSON index of ACP session records. It is safe for
// concurrent use.
type SessionStore struct {
	mu      sync.Mutex
	path    string
	records map[string]SessionRecord
	order   []string // insertion order for FIFO eviction
}

// OpenSessionStore loads the index at path, or starts empty when missing.
// Corrupt files are moved aside (.corrupt) rather than failing startup.
func OpenSessionStore(path string) (*SessionStore, error) {
	s := &SessionStore{path: path, records: make(map[string]SessionRecord)}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is the operator-configured store location
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("reading acp sessions: %w", err)
	}
	var list []SessionRecord
	if err := json.Unmarshal(data, &list); err != nil {
		if renErr := os.Rename(path, path+".corrupt"); renErr != nil {
			return nil, fmt.Errorf("parsing acp sessions: %w", err)
		}
		return s, nil
	}
	for _, r := range list {
		if r.SessionID == "" || r.SessionKey == "" {
			continue
		}
		s.records[r.SessionID] = r
		s.order = append(s.order, r.SessionID)
	}
	return s, nil
}

// GetSession returns the record for an ACP session id.
func (s *SessionStore) GetSession(id string) (SessionRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	return r, ok
}

// PutSession inserts or replaces a record and persists the index atomically.
func (s *SessionStore) PutSession(r SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.SessionID == "" || r.SessionKey == "" {
		return fmt.Errorf("acp session record requires session_id and session_key")
	}
	if _, exists := s.records[r.SessionID]; !exists {
		s.order = append(s.order, r.SessionID)
	}
	s.records[r.SessionID] = r
	s.evictLocked()
	return s.saveLocked()
}

// UpdateDecisions stores the session's cached allow/deny decisions so a later
// session/load can restore them.
func (s *SessionStore) UpdateDecisions(id string, allow, deny []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return nil
	}
	r.AllowAlways = append([]string(nil), allow...)
	r.DenyAlways = append([]string(nil), deny...)
	s.records[id] = r
	return s.saveLocked()
}

// UpdateMode stores the session's mode override so a later session/load can
// restore it.
func (s *SessionStore) UpdateMode(id string, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return nil
	}
	r.Mode = mode
	s.records[id] = r
	return s.saveLocked()
}

// RemoveSession deletes a record.
func (s *SessionStore) RemoveSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return nil
	}
	delete(s.records, id)
	for i, k := range s.order {
		if k == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return s.saveLocked()
}

func (s *SessionStore) evictLocked() {
	for len(s.records) > maxSessionRecords && len(s.order) > 0 {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.records, oldest)
	}
}

func (s *SessionStore) saveLocked() error {
	list := make([]SessionRecord, 0, len(s.records))
	for _, id := range s.order {
		if r, ok := s.records[id]; ok {
			list = append(list, r)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	//nolint:gosec // G117: "session_key" is a routing key string, not a credential.
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.path, data, 0o600)
}
