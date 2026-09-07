package swarm

import (
	"context"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// pingPayload is carried by MsgPing heartbeats.
type pingPayload struct {
	// CapDigest is a short digest of the member's advertised capability.
	CapDigest string `json:"cap_digest,omitempty"`
	// ActiveTasks is the member's current non-terminal remote task count.
	ActiveTasks int `json:"active_tasks,omitempty"`
}

// capProbeFunc reports the local node's capability digest and active task
// count for heartbeat payloads. The daemon wires it to the mesh.
type capProbeFunc func() (digest string, activeTasks int)

// SetCapProbe registers the local capability probe used in PING heartbeats.
func (s *Swarm) SetCapProbe(fn func() (digest string, activeTasks int)) {
	s.presence.capProbe = capProbeFunc(fn)
}

// presence tracks swarm liveness: it heartbeats the local membership and
// evicts members that stay silent past expire_after.
type presence struct {
	capProbe capProbeFunc
}

// startPresence launches the heartbeat and expiry loops. Called from Start.
func (s *Swarm) startPresence() {
	s.wg.Add(2)
	go s.heartbeatLoop(s.ctx)
	go s.expiryLoop(s.ctx)
}

// heartbeatLoop broadcasts a signed PING to every joined swarm on the
// configured interval. Peers we have not discovered yet are skipped — the
// JOIN announce path handles discovery.
func (s *Swarm) heartbeatLoop(ctx context.Context) {
	defer s.wg.Done()
	interval := s.cfg.Presence.HeartbeatInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.heartbeatOnce(ctx)
	}
}

func (s *Swarm) heartbeatOnce(ctx context.Context) {
	var payload pingPayload
	if s.presence.capProbe != nil {
		payload.CapDigest, payload.ActiveTasks = s.presence.capProbe()
	}
	data, err := encodePayload(payload)
	if err != nil {
		return
	}
	for _, id := range s.joinedIDs() {
		env := Envelope{SwarmID: id, Type: MsgPing, Payload: data}
		if err := s.sign(&env); err != nil {
			continue
		}
		_ = s.bc.Publish(ctx, env)
	}
}

// expiryLoop evicts roster members whose LastSeen is older than expire_after.
// The local node and gossip-learned members are never evicted while the
// swarm is not joined.
func (s *Swarm) expiryLoop(ctx context.Context) {
	defer s.wg.Done()
	interval := s.cfg.Presence.ExpireAfter / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.expireOnce()
	}
}

func (s *Swarm) expireOnce() {
	cutoff := time.Now().Add(-s.cfg.Presence.ExpireAfter)
	var expired []struct {
		swarmID string
		pid     string
	}
	s.mu.Lock()
	for _, swarm := range s.swarms {
		for pidStr, m := range swarm.Members {
			if m.LastSeen.Before(cutoff) {
				delete(swarm.Members, pidStr)
				expired = append(expired, struct{ swarmID, pid string }{swarm.ID, pidStr})
			}
		}
	}
	s.mu.Unlock()
	if len(expired) == 0 {
		return
	}
	s.save()
	seen := make(map[string]bool)
	for _, e := range expired {
		s.publishEvent(runtimeevents.KindSwarmMemberExpired, map[string]any{
			"swarm_id": e.swarmID,
			"peer_id":  e.pid,
		})
		if !seen[e.swarmID] {
			seen[e.swarmID] = true
			s.afterMembershipChange(e.swarmID)
		}
	}
}

// notePing folds an inbound heartbeat into the roster: the sender must be a
// member (or become one) of a swarm we joined.
func (s *Swarm) notePing(from peer.ID, env Envelope) {
	var p pingPayload
	_ = decodePayload(env, &p)
	s.mu.Lock()
	swarm, ok := s.swarms[env.SwarmID]
	if !ok {
		// The caller (HandlePush) guards with isJoined, but a Leave can run
		// concurrently between that check and this lock. Defensively create
		// the entry so the ping is not lost if the swarm still exists on
		// another peer; the entry will be reaped by expiry if unused.
		swarm = &swarmState{ID: env.SwarmID, Members: make(map[string]Member)}
		s.swarms[env.SwarmID] = swarm
	}
	pidStr := from.String()
	m, seen := swarm.Members[pidStr]
	isNew := !seen
	m.PeerID = pidStr
	m.LastSeen = time.Now()
	m.Source = "direct"
	m.CapDigest = p.CapDigest
	m.ActiveTasks = p.ActiveTasks
	swarm.Members[pidStr] = m
	s.mu.Unlock()
	if isNew {
		s.save()
		s.publishEvent(runtimeevents.KindSwarmMemberJoined, map[string]any{
			"swarm_id": env.SwarmID,
			"peer_id":  pidStr,
			"source":   "direct",
		})
		s.afterMembershipChange(env.SwarmID)
	}
}
