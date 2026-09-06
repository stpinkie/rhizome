package mesh

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
	// defaultRequestMaxSkew is the accepted clock difference for signed
	// request timestamps when mesh.request_max_skew is unset.
	defaultRequestMaxSkew = 2 * time.Minute

	// nonceTTL is how long a seen nonce is remembered. Requests older than
	// this are already rejected by the skew check, so nonces only need to
	// outlive the skew window by a comfortable margin.
	nonceTTL = 10 * time.Minute
	// maxNoncesPerPeer bounds the per-peer nonce set.
	maxNoncesPerPeer = 1024
	// maxNoncePeers bounds the number of peers tracked for nonces.
	maxNoncePeers = 1024

	// capabilityMaxAge is how old a signed capability may be before it is
	// rejected as stale.
	capabilityMaxAge = 10 * time.Minute

	// auditMaxBytes is the per-file size cap for the audit log before
	// rotation. Three generations are kept (mesh-audit.jsonl.1 .. .3).
	auditMaxBytes  = 10 * 1024 * 1024
	auditKeepFiles = 3
)

// replayGuard enforces nonce uniqueness and timestamp freshness per peer.
type replayGuard struct {
	mu      sync.Mutex
	seen    map[peer.ID]map[string]int64 // peer -> nonce -> first-seen unix time
	order   map[peer.ID][]string         // peer -> nonce insertion order
	peerOrd []peer.ID                    // peer insertion order for bound
	maxSkew time.Duration
	now     func() time.Time
}

func newReplayGuard(maxSkew time.Duration) *replayGuard {
	if maxSkew <= 0 {
		maxSkew = defaultRequestMaxSkew
	}
	return &replayGuard{
		seen:    make(map[peer.ID]map[string]int64),
		order:   make(map[peer.ID][]string),
		maxSkew: maxSkew,
		now:     time.Now,
	}
}

// check validates a request's timestamp and nonce. A zero timestamp skips
// freshness checking (the caller decides whether to require one), but a
// missing nonce is always rejected. Returns nil when the timestamp is
// within the allowed skew and the nonce is new.
func (g *replayGuard) check(from peer.ID, nonce string, ts int64) error {
	if ts != 0 {
		skew := g.now().Unix() - ts
		if skew < 0 {
			skew = -skew
		}
		if time.Duration(skew)*time.Second > g.maxSkew {
			return fmt.Errorf("request timestamp outside allowed skew (%ds)", int64(g.maxSkew.Seconds()))
		}
	}
	if nonce == "" {
		return fmt.Errorf("missing nonce")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	peerSet, ok := g.seen[from]
	if !ok {
		peerSet = make(map[string]int64)
		g.seen[from] = peerSet
		g.peerOrd = append(g.peerOrd, from)
		// Bound the number of tracked peers.
		for len(g.peerOrd) > maxNoncePeers {
			oldest := g.peerOrd[0]
			g.peerOrd = g.peerOrd[1:]
			delete(g.seen, oldest)
			delete(g.order, oldest)
		}
	}

	now := g.now().Unix()
	// Prune expired nonces and evict oldest when over the per-peer bound.
	cutoff := now - int64(nonceTTL.Seconds())
	ord := g.order[from]
	kept := ord[:0]
	for _, n := range ord {
		seenAt, exists := peerSet[n]
		if !exists || seenAt < cutoff {
			delete(peerSet, n)
			continue
		}
		kept = append(kept, n)
	}
	g.order[from] = kept

	for len(g.order[from]) >= maxNoncesPerPeer {
		delete(peerSet, g.order[from][0])
		g.order[from] = g.order[from][1:]
	}

	if _, dup := peerSet[nonce]; dup {
		return fmt.Errorf("replay detected: nonce already used by peer %s", from)
	}
	peerSet[nonce] = now
	g.order[from] = append(g.order[from], nonce)
	return nil
}

// --- Per-peer authorization (ACL) ---

// aclRuleFor returns the ACL rule matching the peer, or nil when the peer
// falls back to the global allow_remote_* policy.
func (m *Mesh) aclRuleFor(pid peer.ID) *config.MeshACLRule {
	for i := range m.cfg.ACL {
		if m.cfg.ACL[i].PeerID == pid.String() {
			return &m.cfg.ACL[i]
		}
	}
	return nil
}

// checkRemoteAllowed enforces the per-peer ACL for a remote execution op.
// op is "delegate" or "spawn"; agentID is the requested target agent.
func (m *Mesh) checkRemoteAllowed(pid peer.ID, op, agentID string) error {
	rule := m.aclRuleFor(pid)
	if rule == nil {
		// Global fallback.
		allowed := m.cfg.AllowRemoteDelegate || m.cfg.AllowRemoteSpawn
		if op == "delegate" {
			allowed = m.cfg.AllowRemoteDelegate
		} else if op == "spawn" {
			allowed = m.cfg.AllowRemoteSpawn
		}
		if !allowed {
			return fmt.Errorf("remote %s is disabled for peer %s", op, pid)
		}
		return nil
	}

	allowed := true
	switch op {
	case "delegate":
		if rule.AllowDelegate != nil {
			allowed = *rule.AllowDelegate
		} else {
			allowed = m.cfg.AllowRemoteDelegate
		}
	case "spawn":
		if rule.AllowSpawn != nil {
			allowed = *rule.AllowSpawn
		} else {
			allowed = m.cfg.AllowRemoteSpawn
		}
	}
	if !allowed {
		return fmt.Errorf("peer %s is not allowed to %s", pid, op)
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
			return fmt.Errorf("peer %s is not allowed to run agent %q", pid, agentID)
		}
	}
	return nil
}

// --- Rate limiting ---

// allowRate checks the per-peer and global inbound rate limits. Limits are
// expressed in requests per minute.
func (m *Mesh) allowRate(pid peer.ID) bool {
	if lim := m.globalLimiter(); lim != nil && !lim.Allow() {
		return false
	}
	if lim := m.peerLimiter(pid); lim != nil && !lim.Allow() {
		return false
	}
	return true
}

func (m *Mesh) globalLimiter() *rate.Limiter {
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	if m.globalLim == nil {
		perMin := m.cfg.RateLimitGlobal
		if perMin <= 0 {
			return nil
		}
		m.globalLim = rate.NewLimiter(rate.Limit(perMin/60.0), int(perMin))
	}
	return m.globalLim
}

func (m *Mesh) peerLimiter(pid peer.ID) *rate.Limiter {
	perMin := m.cfg.RateLimitPerPeer
	if rule := m.aclRuleFor(pid); rule != nil && rule.RateLimit != 0 {
		if rule.RateLimit < 0 {
			return nil // explicit unlimited
		}
		perMin = rule.RateLimit
	}
	if perMin <= 0 {
		return nil
	}

	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	if m.peerLims == nil {
		m.peerLims = make(map[peer.ID]*rate.Limiter)
	}
	if lim, ok := m.peerLims[pid]; ok {
		return lim
	}
	// Trusted peers are a bounded set; cap defensively anyway.
	if len(m.peerLims) >= maxNoncePeers {
		// Evict an arbitrary entry to keep the map bounded.
		for k := range m.peerLims {
			delete(m.peerLims, k)
			break
		}
	}
	lim := rate.NewLimiter(rate.Limit(perMin/60.0), int(perMin))
	m.peerLims[pid] = lim
	return lim
}

// --- Audit log ---

// auditLogger appends JSONL entries to the mesh audit trail with size-based
// rotation.
type auditLogger struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
}

func newAuditLogger(path string) *auditLogger {
	return &auditLogger{path: path, maxSize: auditMaxBytes, keep: auditKeepFiles}
}

// Log appends one entry. Failures are silent — the audit trail must never
// break request handling.
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

// auditMesh records one remote-operation audit entry: event bus + JSONL file.
func (m *Mesh) auditMesh(from peer.ID, op, agentID, ref, status string, started time.Time, detail string) {
	entry := map[string]any{
		"ts":          time.Now().UTC().Format(time.RFC3339Nano),
		"peer_id":     from.String(),
		"op":          op,
		"agent_id":    agentID,
		"ref":         ref,
		"status":      status,
		"duration_ms": time.Since(started).Milliseconds(),
	}
	if detail != "" {
		entry["detail"] = detail
	}

	m.publishMeshEvent(runtimeevents.KindMeshRemoteAudit, entry)

	if m.auditLog != nil {
		m.auditLog.Log(entry)
	}
}

// defaultAuditPath returns the audit trail location under the Rhizome home.
func defaultAuditPath() string {
	home := config.GetHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "mesh-audit.jsonl")
}
