package mesh

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// scoreLatencyAlpha is the exponential moving average weight for latency.
	// Higher values make the average track recent measurements more closely.
	scoreLatencyAlpha = 0.3
	// maxScoreSamples is the cap on success/failure counts to prevent an
	// unbounded history from dominating the score.
	maxScoreSamples = 100
	// scoreSaveCoalesce is how long to wait after a mutation before writing
	// the score store to disk. Multiple rapid mutations are coalesced into
	// one write, matching the TaskStore behaviour.
	scoreSaveCoalesce = time.Second
)

// OpStat tracks call quality for one operation kind (delegate, spawn,
// submit, status, result, cancel, …).
type OpStat struct {
	Successes  int           `json:"successes"`
	Failures   int           `json:"failures"`
	AvgLatency time.Duration `json:"avg_latency_ns,omitempty"`
}

// PeerScore tracks observed quality of a mesh peer over time.
type PeerScore struct {
	PeerID      string            `json:"peer_id"`
	Successes   int               `json:"successes"`
	Failures    int               `json:"failures"`
	LastLatency time.Duration     `json:"last_latency_ns"`
	AvgLatency  time.Duration     `json:"avg_latency_ns"`
	LastSeen    time.Time         `json:"last_seen"`
	LastError   string            `json:"last_error,omitempty"`
	OpStats     map[string]OpStat `json:"op_stats,omitempty"`

	// halfLife is the read-time decay window stamped by the store from
	// mesh.score_half_life. Counters are stored raw; Score() evaluates the
	// decayed view. A zero halfLife disables decay.
	halfLife time.Duration
}

// Score returns a composite quality score for the peer. Higher is better.
// It favours high success rates and low average latency, but only uses
// latency after enough samples so early measurements do not dominate.
// When a score half-life is configured the composite is scaled by the
// evidence's remaining freshness: success/failure counts halve per idle
// half-life since LastSeen, so stale peers decay toward zero rather than
// ranking forever on old history.
func (s PeerScore) Score() float64 {
	return s.scoreAt(time.Now().UTC())
}

func (s PeerScore) scoreAt(now time.Time) float64 {
	total := s.Successes + s.Failures
	if total == 0 {
		return 0
	}
	successRate := float64(s.Successes) / float64(total)
	score := successRate * 1000.0

	if s.AvgLatency > 0 && total >= 3 {
		// Latency is measured in nanoseconds. Convert to milliseconds and
		// penalise slow peers, but keep the penalty bounded.
		ms := float64(s.AvgLatency) / float64(time.Millisecond)
		if ms > 0 {
			score -= 50.0 / ms
		}
	}
	return score * s.decayFactor(now)
}

// DecayFactor reports the current evidence-freshness weight applied by
// Score — 1 when decay is disabled or the peer was just seen.
func (s PeerScore) DecayFactor() float64 {
	return s.decayFactor(time.Now().UTC())
}

// decayFactor returns the fraction of the peer's evidence that is still
// fresh: 1 when decay is disabled or the peer was just seen, halving per
// idle half-life since LastSeen.
func (s PeerScore) decayFactor(now time.Time) float64 {
	if s.halfLife <= 0 || s.LastSeen.IsZero() {
		return 1
	}
	idle := now.Sub(s.LastSeen)
	if idle <= 0 {
		return 1
	}
	return math.Pow(0.5, float64(idle)/float64(s.halfLife))
}

// peerScoreRecord is the on-disk JSON representation.
type peerScoreRecord struct {
	PeerID      string            `json:"peer_id"`
	Successes   int               `json:"successes"`
	Failures    int               `json:"failures"`
	LastLatency int64             `json:"last_latency_ns"`
	AvgLatency  int64             `json:"avg_latency_ns"`
	LastSeen    int64             `json:"last_seen_ns"`
	LastError   string            `json:"last_error,omitempty"`
	OpStats     map[string]OpStat `json:"op_stats,omitempty"`
}

// PeerScoreStore persists a bounded history of call quality per peer.
type PeerScoreStore struct {
	mu       sync.RWMutex
	path     string
	scores   map[peer.ID]*PeerScore
	halfLife time.Duration

	saveMu    sync.Mutex
	saveErr   error
	saveTimer *time.Timer
}

// NewPeerScoreStore creates an in-memory peer score store.
func NewPeerScoreStore() *PeerScoreStore {
	return &PeerScoreStore{
		scores: make(map[peer.ID]*PeerScore),
	}
}

// NewPeerScoreStoreWithPath creates a peer score store backed by a JSON file.
func NewPeerScoreStoreWithPath(path string) *PeerScoreStore {
	s := NewPeerScoreStore()
	s.path = path
	return s
}

// SetPath enables persistence at the given path. It is safe to call before
// any records have been added.
func (s *PeerScoreStore) SetPath(path string) {
	s.saveMu.Lock()
	s.path = path
	s.saveMu.Unlock()
}

// Load reads the persisted score file. Missing files are treated as empty.
func (s *PeerScoreStore) Load() error {
	s.saveMu.Lock()
	path := s.path
	s.saveMu.Unlock()
	if path == "" {
		return nil
	}

	//nolint:gosec // path is built from RHIZOME_HOME, not user-controlled.
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open peer score store: %w", err)
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		var rec peerScoreRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode peer score: %w", err)
		}
		pid, err := peer.Decode(rec.PeerID)
		if err != nil {
			continue
		}
		s.scores[pid] = &PeerScore{
			PeerID:      rec.PeerID,
			Successes:   rec.Successes,
			Failures:    rec.Failures,
			LastLatency: time.Duration(rec.LastLatency),
			AvgLatency:  time.Duration(rec.AvgLatency),
			LastSeen:    time.Unix(0, rec.LastSeen),
			LastError:   rec.LastError,
			OpStats:     rec.OpStats,
			halfLife:    s.halfLife,
		}
	}
	return nil
}

// Save writes the current scores to disk atomically.
func (s *PeerScoreStore) Save() error {
	s.saveMu.Lock()
	path := s.path
	s.saveMu.Unlock()
	if path == "" {
		return nil
	}

	// Snapshot the current scores under a single read lock.
	s.mu.RLock()
	scores := make([]peer.ID, 0, len(s.scores))
	for pid := range s.scores {
		scores = append(scores, pid)
	}
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].String() < scores[j].String()
	})
	records := make([]peerScoreRecord, 0, len(scores))
	for _, pid := range scores {
		sc := s.scores[pid]
		records = append(records, peerScoreRecord{
			PeerID:      sc.PeerID,
			Successes:   sc.Successes,
			Failures:    sc.Failures,
			LastLatency: int64(sc.LastLatency),
			AvgLatency:  int64(sc.AvgLatency),
			LastSeen:    sc.LastSeen.UnixNano(),
			LastError:   sc.LastError,
			OpStats:     sc.OpStats,
		})
	}
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir peer score store: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("create peer score temp: %w", err)
	}
	tmp := f.Name()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("encode peer score: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync peer score store: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close peer score temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename peer score store: %w", err)
	}
	return nil
}

// scheduleSave coalesces writes; multiple rapid mutations result in one disk
// write about a second after activity stops.
func (s *PeerScoreStore) scheduleSave() {
	s.saveMu.Lock()
	if s.path == "" {
		s.saveMu.Unlock()
		return
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(scoreSaveCoalesce, func() {
		s.saveMu.Lock()
		s.saveTimer = nil
		s.saveMu.Unlock()
		if err := s.Save(); err != nil {
			s.saveMu.Lock()
			s.saveErr = err
			s.saveMu.Unlock()
		}
	})
	s.saveMu.Unlock()
}

// flushSave cancels any pending coalesced write and runs save synchronously.
// It is used during shutdown.
func (s *PeerScoreStore) flushSave() {
	s.saveMu.Lock()
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.saveMu.Unlock()
	if err := s.Save(); err != nil {
		s.saveMu.Lock()
		s.saveErr = err
		s.saveMu.Unlock()
	}
}

// SaveError returns the most recent persistence error, if any.
func (s *PeerScoreStore) SaveError() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	return s.saveErr
}

// Close flushes any pending coalesced write. It is safe to call multiple times.
func (s *PeerScoreStore) Close() {
	s.flushSave()
}

// SetHalfLife configures the read-time score decay window. Records with an
// idle gap longer than the half-life score proportionally lower; 0 disables
// decay. Safe to call at any time — the stamp is applied to returned copies.
func (s *PeerScoreStore) SetHalfLife(d time.Duration) {
	s.mu.Lock()
	s.halfLife = d
	s.mu.Unlock()
}

// stamped returns a copy of sc carrying the store's current half-life.
func (s *PeerScoreStore) stamped(sc *PeerScore) PeerScore {
	out := *sc
	out.halfLife = s.halfLife
	return out
}

// ema folds one latency sample into the moving average.
func ema(avg, sample time.Duration) time.Duration {
	return time.Duration(
		float64(avg)*(1-scoreLatencyAlpha) + float64(sample)*scoreLatencyAlpha)
}

// Record updates the score for a peer after a call. Latency and outcome are
// used to maintain a rolling success rate and exponential moving average.
// op is the operation label ("delegate", "spawn", "submit", "result", …)
// recorded in the per-operation breakdown; an empty op skips it.
func (s *PeerScoreStore) Record(
	pid peer.ID,
	op string,
	success bool,
	latency time.Duration,
	callErr error,
) {
	s.mu.Lock()

	sc, ok := s.scores[pid]
	if !ok {
		sc = &PeerScore{PeerID: pid.String()}
		s.scores[pid] = sc
	}
	sc.halfLife = s.halfLife

	now := time.Now().UTC()
	sc.LastSeen = now
	// A zero latency means "record outcome only" — used for long-poll ops
	// (e.g. OpResult) where the round-trip time is dominated by task execution
	// and would distort the latency moving average.
	if latency != 0 {
		sc.LastLatency = latency
	}

	if success {
		sc.Successes++
	} else {
		sc.Failures++
		if callErr != nil {
			sc.LastError = callErr.Error()
		} else {
			sc.LastError = ""
		}
	}

	total := sc.Successes + sc.Failures
	if total > maxScoreSamples {
		// Decay both counts proportionally to stay under the cap.
		ratio := float64(maxScoreSamples) / float64(total)
		sc.Successes = int(float64(sc.Successes) * ratio)
		sc.Failures = int(float64(sc.Failures) * ratio)
		if sc.Successes+sc.Failures == 0 {
			sc.Successes = 1
		}
	}

	if latency != 0 {
		if sc.AvgLatency == 0 {
			sc.AvgLatency = latency
		} else {
			sc.AvgLatency = ema(sc.AvgLatency, latency)
		}
	}

	if op != "" {
		if sc.OpStats == nil {
			sc.OpStats = make(map[string]OpStat, 4)
		}
		st := sc.OpStats[op]
		if success {
			st.Successes++
		} else {
			st.Failures++
		}
		if latency != 0 {
			if st.AvgLatency == 0 {
				st.AvgLatency = latency
			} else {
				st.AvgLatency = ema(st.AvgLatency, latency)
			}
		}
		if tot := st.Successes + st.Failures; tot > maxScoreSamples {
			ratio := float64(maxScoreSamples) / float64(tot)
			st.Successes = int(float64(st.Successes) * ratio)
			st.Failures = int(float64(st.Failures) * ratio)
		}
		sc.OpStats[op] = st
	}

	s.mu.Unlock()
	s.scheduleSave()
}

// Get returns the current score for a peer. The second value is false if the
// peer has no recorded score.
func (s *PeerScoreStore) Get(pid peer.ID) (PeerScore, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.scores[pid]
	if !ok {
		return PeerScore{}, false
	}
	return s.stamped(sc), true
}

// All returns a snapshot of all known peer scores.
func (s *PeerScoreStore) All() map[peer.ID]PeerScore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[peer.ID]PeerScore, len(s.scores))
	for pid, sc := range s.scores {
		out[pid] = s.stamped(sc)
	}
	return out
}

// Ranked returns the peer ids sorted by descending score, with a deterministic
// tie-break on peer id string.
func (s *PeerScoreStore) Ranked() []peer.ID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]peer.ID, 0, len(s.scores))
	for pid := range s.scores {
		out = append(out, pid)
	}
	now := time.Now().UTC()
	sort.Slice(out, func(i, j int) bool {
		is := s.stamped(s.scores[out[i]]).scoreAt(now)
		js := s.stamped(s.scores[out[j]]).scoreAt(now)
		if is != js {
			return is > js
		}
		return out[i].String() < out[j].String()
	})
	return out
}
