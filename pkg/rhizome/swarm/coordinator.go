package swarm

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// SharedState is the deterministic snapshot the coordinator publishes to the
// workspace sync layer under swarm/<id>/state.json.
type SharedState struct {
	SwarmID     string       `json:"swarm_id"`
	Coordinator string       `json:"coordinator"`
	Epoch       int64        `json:"epoch"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Members     []MemberInfo `json:"members"`
}

// StateWriter persists a swarm's shared state blob; the daemon wires it to
// write swarm/<id>/state.json in the synced workspace.
type StateWriter func(swarmID string, state []byte) error

// SetStateWriter registers the shared-state writer used by the coordinator.
func (s *Swarm) SetStateWriter(fn StateWriter) {
	s.coord.writer = fn
}

// coordination tracks deterministic coordinator election. The coordinator is
// the lexicographically smallest peer id among {self, if joined} ∪ roster
// members — every member computes the same answer from the same roster.
type coordination struct {
	writer StateWriter
}

// Coordinator returns the elected coordinator peer id for a swarm, or ""
// when the swarm is unknown.
func (s *Swarm) Coordinator(swarmID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.coordinators[swarmID]
}

// IsCoordinator reports whether the local node currently coordinates the
// swarm.
func (s *Swarm) IsCoordinator(swarmID string) bool {
	return s.Coordinator(swarmID) == s.host.ID().String()
}

// reelectLocked recomputes the coordinator for a swarm after a membership
// change. Caller holds s.mu. It returns true when the coordinator changed.
func (s *Swarm) reelectLocked(swarmID string) bool {
	swarm, ok := s.swarms[swarmID]
	if !ok {
		delete(s.coordinators, swarmID)
		return true
	}
	candidates := make([]string, 0, len(swarm.Members)+1)
	for pid := range swarm.Members {
		candidates = append(candidates, pid)
	}
	if swarm.Joined {
		candidates = append(candidates, s.host.ID().String())
	}
	if len(candidates) == 0 {
		if _, had := s.coordinators[swarmID]; !had {
			return false
		}
		delete(s.coordinators, swarmID)
		return true
	}
	sort.Strings(candidates)
	winner := candidates[0]
	if s.coordinators == nil {
		s.coordinators = make(map[string]string)
	}
	if s.coordinators[swarmID] == winner {
		return false
	}
	s.coordinators[swarmID] = winner
	return true
}

// afterMembershipChange reelects the coordinator outside the lock and emits
// events/state writes. Call it after every roster mutation.
func (s *Swarm) afterMembershipChange(swarmID string) {
	s.mu.Lock()
	changed := s.reelectLocked(swarmID)
	coord := s.coordinators[swarmID]
	s.mu.Unlock()
	if !changed || coord == "" {
		return
	}
	s.publishEvent(runtimeevents.KindSwarmCoordinatorElected, map[string]any{
		"swarm_id":    swarmID,
		"coordinator": coord,
		"is_self":     coord == s.host.ID().String(),
	})
	s.writeSharedState(swarmID)
}

// writeSharedState serializes and publishes the swarm's shared state when
// the local node is the coordinator and a writer is wired.
func (s *Swarm) writeSharedState(swarmID string) {
	if s.coord.writer == nil || !s.IsCoordinator(swarmID) {
		return
	}
	s.mu.Lock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		s.mu.Unlock()
		return
	}
	swarm.Epoch++
	epoch := swarm.Epoch
	s.mu.Unlock()

	state := SharedState{
		SwarmID:     swarmID,
		Coordinator: s.host.ID().String(),
		Epoch:       epoch,
		UpdatedAt:   time.Now().UTC(),
		Members:     s.Members(swarmID),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	if err := s.coord.writer(swarmID, data); err != nil {
		s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
			"stage":    "shared_state",
			"swarm_id": swarmID,
			"error":    err.Error(),
		})
		return
	}
	s.publishEvent(runtimeevents.KindSwarmStateWritten, map[string]any{
		"swarm_id": swarmID,
		"epoch":    epoch,
	})
}

// sharedStateLoop periodically refreshes shared state so late joiners and
// presence changes propagate even without a roster mutation.
func (s *Swarm) sharedStateLoop(ctx context.Context) {
	defer s.wg.Done()
	interval := s.cfg.Coordination.StateInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, id := range s.joinedIDs() {
			s.writeSharedState(id)
		}
	}
}
