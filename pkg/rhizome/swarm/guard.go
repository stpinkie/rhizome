package swarm

import (
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// nonceTTL is how long a seen nonce is remembered. Envelopes older than
	// the skew window are already rejected, so nonces only need to outlive
	// that window by a comfortable margin.
	nonceTTL = 10 * time.Minute
	// maxNoncesPerScope bounds the per (peer, swarm) nonce set.
	maxNoncesPerScope = 1024
	// maxNonceScopes bounds the number of tracked (peer, swarm) scopes.
	maxNonceScopes = 1024
)

// guard enforces nonce uniqueness and timestamp freshness per (peer, swarm)
// scope. It mirrors the mesh replay guard but keys nonces by swarm so the
// same nonce may legitimately appear in different swarms.
type guard struct {
	mu       sync.Mutex
	seen     map[string]map[string]int64 // scope -> nonce -> first-seen unix time
	order    map[string][]string         // scope -> nonce insertion order
	scopeOrd []string                    // scope insertion order for bound
	maxSkew  time.Duration
	now      func() time.Time
}

func newGuard(maxSkew time.Duration) *guard {
	if maxSkew <= 0 {
		maxSkew = 2 * time.Minute
	}
	return &guard{
		seen:    make(map[string]map[string]int64),
		order:   make(map[string][]string),
		maxSkew: maxSkew,
		now:     time.Now,
	}
}

func scopeKey(from peer.ID, swarmID string) string {
	return from.String() + "|" + swarmID
}

// check validates an envelope's timestamp and nonce within a (peer, swarm)
// scope. A zero timestamp skips freshness checking, but a missing nonce is
// always rejected.
func (g *guard) check(from peer.ID, swarmID, nonce string, ts int64) error {
	if ts != 0 {
		skew := g.now().Unix() - ts
		if skew < 0 {
			skew = -skew
		}
		if time.Duration(skew)*time.Second > g.maxSkew {
			return fmt.Errorf("envelope timestamp outside allowed skew (%ds)", int64(g.maxSkew.Seconds()))
		}
	}
	if nonce == "" {
		return fmt.Errorf("missing nonce")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	scope := scopeKey(from, swarmID)
	scopeSet, ok := g.seen[scope]
	if !ok {
		scopeSet = make(map[string]int64)
		g.seen[scope] = scopeSet
		g.scopeOrd = append(g.scopeOrd, scope)
		for len(g.scopeOrd) > maxNonceScopes {
			oldest := g.scopeOrd[0]
			g.scopeOrd = g.scopeOrd[1:]
			delete(g.seen, oldest)
			delete(g.order, oldest)
		}
	}

	now := g.now().Unix()
	cutoff := now - int64(nonceTTL.Seconds())
	ord := g.order[scope]
	kept := ord[:0]
	for _, n := range ord {
		seenAt, exists := scopeSet[n]
		if !exists || seenAt < cutoff {
			delete(scopeSet, n)
			continue
		}
		kept = append(kept, n)
	}
	g.order[scope] = kept

	for len(g.order[scope]) >= maxNoncesPerScope {
		delete(scopeSet, g.order[scope][0])
		g.order[scope] = g.order[scope][1:]
	}

	if _, dup := scopeSet[nonce]; dup {
		return fmt.Errorf("replay detected: nonce already used by peer %s in swarm %q", from, swarmID)
	}
	scopeSet[nonce] = now
	g.order[scope] = append(g.order[scope], nonce)
	return nil
}
