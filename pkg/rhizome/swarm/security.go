package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/time/rate"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

const (
	// auditMaxBytes caps the audit file before rotation; three generations
	// are kept, matching the mesh audit trail.
	auditMaxBytes  = 10 * 1024 * 1024
	auditKeepFiles = 3
)

// --- Per-swarm authorization (ACL) ---

// aclRuleFor returns the most specific ACL rule for a (swarm, peer) pair:
// an exact swarm+peer match first, then swarm+"*", then "*"+peer, then
// "*"+"*". Nil means the default policy applies (trusted peers may offer
// and claim).
func (s *Swarm) aclRuleFor(swarmID string, pid peer.ID) *config.SwarmACLRule {
	pidStr := pid.String()
	var swarmWildcard, peerWildcard, bothWildcard *config.SwarmACLRule
	for i := range s.cfg.ACL {
		r := &s.cfg.ACL[i]
		swarmMatch := r.SwarmID == swarmID || r.SwarmID == "*"
		peerMatch := r.PeerID == pidStr || r.PeerID == "*"
		if !swarmMatch || !peerMatch {
			continue
		}
		if r.SwarmID == swarmID && r.PeerID == pidStr {
			return r
		}
		if r.SwarmID == swarmID {
			swarmWildcard = r
		} else if r.PeerID == pidStr {
			peerWildcard = r
		} else {
			bothWildcard = r
		}
	}
	if swarmWildcard != nil {
		return swarmWildcard
	}
	if peerWildcard != nil {
		return peerWildcard
	}
	return bothWildcard
}

// checkSwarmOp enforces the per-swarm ACL for op ("offer" or "claim") with
// the target agent id. Errors carry the machine-readable "forbidden:"
// prefix used by mesh rejections.
func (s *Swarm) checkSwarmOp(pid peer.ID, swarmID, op, agentID string) error {
	rule := s.aclRuleFor(swarmID, pid)
	if rule == nil {
		return nil
	}
	allowed := true
	switch op {
	case "offer":
		if rule.AllowOffer != nil {
			allowed = *rule.AllowOffer
		}
	case "claim":
		if rule.AllowClaim != nil {
			allowed = *rule.AllowClaim
		}
	}
	if !allowed {
		return fmt.Errorf("forbidden: peer %s may not %s in swarm %q", pid, op, swarmID)
	}
	if len(rule.Agents) > 0 && agentID != "" {
		matched := false
		for _, a := range rule.Agents {
			if a == "*" || a == agentID {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("forbidden: peer %s may not use agent %q in swarm %q", pid, agentID, swarmID)
		}
	}
	return nil
}

// --- Rate limiting ---

// allowSwarmRate checks the per-peer and global inbound swarm message
// limits, in messages per minute.
func (s *Swarm) allowSwarmRate(pid peer.ID) bool {
	if lim := s.globalSwarmLimiter(); lim != nil && !lim.Allow() {
		return false
	}
	if lim := s.peerSwarmLimiter(pid); lim != nil && !lim.Allow() {
		return false
	}
	return true
}

func (s *Swarm) globalSwarmLimiter() *rate.Limiter {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.globalLim == nil {
		perMin := s.cfg.RateLimitGlobal
		if perMin <= 0 {
			return nil
		}
		s.globalLim = rate.NewLimiter(rate.Limit(perMin/60.0), int(perMin))
	}
	return s.globalLim
}

// peerSwarmLimiter returns the per-peer limiter, honoring the most specific
// ACL rate override across all swarms (a negative override means unlimited).
// Specificity follows the same precedence as aclRuleFor: an exact peer match
// beats a wildcard ("*") peer match, so a per-peer rule always wins over a
// global wildcard rule regardless of slice order.
func (s *Swarm) peerSwarmLimiter(pid peer.ID) *rate.Limiter {
	perMin := s.cfg.RateLimitPerPeer
	pidStr := pid.String()
	// Scan for the most specific rule with a non-zero rate override. An exact
	// peer match (any swarm) takes precedence over a wildcard peer match.
	var wildcardOverride float64
	hasWildcard := false
	for i := range s.cfg.ACL {
		r := &s.cfg.ACL[i]
		if r.RateLimit == 0 {
			continue
		}
		if r.PeerID == pidStr {
			// Exact match wins immediately.
			perMin = r.RateLimit
			break
		}
		if r.PeerID == "*" && !hasWildcard {
			wildcardOverride = r.RateLimit
			hasWildcard = true
		}
	}
	if perMin == s.cfg.RateLimitPerPeer && hasWildcard {
		perMin = wildcardOverride
	}
	if perMin < 0 {
		return nil
	}
	if perMin <= 0 {
		return nil
	}

	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if s.peerLims == nil {
		s.peerLims = make(map[peer.ID]*rate.Limiter)
	}
	if lim, ok := s.peerLims[pid]; ok {
		// Move to back of LRU order so the most recently active peer is
		// evicted last.
		s.touchPeerLimLocked(pid)
		return lim
	}
	if len(s.peerLims) >= maxNonceScopes {
		// Evict the least-recently-used peer (front of peerLimOrd).
		if len(s.peerLimOrd) > 0 {
			oldest := s.peerLimOrd[0]
			s.peerLimOrd = s.peerLimOrd[1:]
			delete(s.peerLims, oldest)
		}
	}
	lim := rate.NewLimiter(rate.Limit(perMin/60.0), int(perMin))
	s.peerLims[pid] = lim
	s.peerLimOrd = append(s.peerLimOrd, pid)
	return lim
}

// touchPeerLimLocked moves pid to the back of peerLimOrd (most-recently-used).
// Caller must hold s.rateMu.
func (s *Swarm) touchPeerLimLocked(pid peer.ID) {
	for i, p := range s.peerLimOrd {
		if p == pid {
			s.peerLimOrd = append(s.peerLimOrd[:i], s.peerLimOrd[i+1:]...)
			s.peerLimOrd = append(s.peerLimOrd, pid)
			return
		}
	}
}

// --- Audit trail ---

// auditLogger appends JSONL entries to the shared audit trail with
// size-based rotation, mirroring the mesh audit logger.
type auditLogger struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
}

func newAuditLogger(path string) *auditLogger {
	return &auditLogger{path: path, maxSize: auditMaxBytes, keep: auditKeepFiles}
}

// Log appends one entry. Failures are silent — auditing must never break
// request handling.
func (l *auditLogger) Log(entry map[string]any) {
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
	defer f.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

func (l *auditLogger) rotateLocked() {
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

// auditSwarm records one swarm operation to the shared audit trail and the
// runtime event bus.
func (s *Swarm) auditSwarm(from peer.ID, op, swarmID, ref, status string, started time.Time, detail string) {
	entry := map[string]any{
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"peer_id":     from.String(),
		"op":          "swarm." + op,
		"swarm_id":    swarmID,
		"ref":         ref,
		"status":      status,
		"duration_ms": time.Since(started).Milliseconds(),
	}
	if detail != "" {
		entry["detail"] = detail
	}
	s.publishEvent(runtimeevents.KindMeshRemoteAudit, entry)
	if s.auditLog != nil {
		s.auditLog.Log(entry)
	}
}

// defaultSwarmAuditPath returns the shared audit trail location.
func defaultSwarmAuditPath(home string) string {
	if home == "" {
		if h := config.GetHome(); h != "" {
			home = h
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "mesh-audit.jsonl")
}
