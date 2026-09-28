// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/settlement"
	shared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// Session states.
const (
	sessionOpen      = "open"      // gated, not yet bound to an ACP session
	sessionActive    = "active"    // spawned + bound; prompt pending/running
	sessionCompleted = "completed" // prompt done; receipt minted
	sessionClosed    = "closed"    // closed cleanly or dropped
	sessionFailed    = "failed"    // spawn/prompt failure; receipt minted w/ interrupted
)

// Per-peer limits (internal constants — the catalog's 20-field schema is
// the Track-107-pinned contract; a field knob lands only if ops demand it).
const (
	// peerOpenPerMinute bounds session_open calls per peer — the cheap
	// gate work (VerifyLock is eth_call) still costs the seller RPC quota.
	peerOpenPerMinute = 12
	// peerSessionShare bounds one peer's concurrent active sessions to a
	// share of the global cap, so a single buyer can't fill every slot.
	peerSessionShareDivisor = 2
	// scratchDirName holds per-session scratch roots under the module dir.
	scratchDirName = "scratch"
	// receiptDirName holds persisted receipts.
	receiptDirName = "receipts"
	// reaperTick is how often idle sessions are checked against
	// session_ttl.
	reaperTick = 30 * time.Second
)

// marketSession is one escrowed sell-side session. session_id doubles as
// the ACP session id returned to the buyer — it is the escrow clone
// address, globally unique and self-documenting.
type marketSession struct {
	ID       string // escrow clone address (the on-chain session key)
	Peer     string // authenticated peer id from the bridge hello
	ConnID   uint64 // module-local connection sequence (conn-drop finalize)
	Offer    offer
	Terms    settlement.Terms
	State    string
	OpenedAt time.Time
	EndedAt  time.Time

	taskHash [32]byte

	mu         sync.Mutex
	agent      *acp.BoundAgent
	agentSID   acpsdk.SessionId
	result     strings.Builder // accumulated assistant text -> result_sha256
	usage      *shared.RemoteUsage
	receipt    *receipt
	scratchDir string
	promptDone bool // single-prompt enforcement
}

// durationMS is the wall time the session was live (bind→end or now).
func (s *marketSession) durationMS() int64 {
	end := s.EndedAt
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(s.OpenedAt).Milliseconds()
}

// sessionMgr owns the sell-side session registry: gate bookkeeping, the
// spawn seam, caps/rate limits, the TTL reaper, and receipt minting.
type sessionMgr struct {
	moduleDir string
	audit     *auditLogger
	ident     atomic.Pointer[identity.Derived]

	mu   sync.Mutex
	cfg  atomic.Pointer[marketConfig]
	rail atomic.Pointer[settlement.Rail]

	sessions map[string]*marketSession
	byConn   map[uint64][]*marketSession
	peerOpen map[string][]time.Time // per-peer session_open window
	seq      atomic.Uint64          // conn id sequence
	total    atomic.Uint64          // sessions opened (health)

	// spawnFn is the spawn seam — tests inject an in-process BoundAgent
	// double so unit tests never start real processes.
	spawnFn func(
		ctx context.Context,
		cfg *config.Config,
		src acp.BoundSource,
		opts acp.BoundSpawn,
	) (*acp.BoundAgent, error)

	// agentBindings resolves offer.agent_binding to a BoundSource — built
	// once from agents.list at config (re)load.
	bindings atomic.Pointer[map[string]acp.BoundSource]

	baseCfg atomic.Pointer[config.Config] // shared home config for SpawnBound
}

func newSessionMgr(moduleDir string, audit *auditLogger) *sessionMgr {
	m := &sessionMgr{
		moduleDir: moduleDir,
		audit:     audit,
		sessions:  map[string]*marketSession{},
		byConn:    map[uint64][]*marketSession{},
		peerOpen:  map[string][]time.Time{},
	}
	m.spawnFn = acp.SpawnBound
	return m
}

// nextConnID assigns the conn sequence used for conn-drop finalization.
func (m *sessionMgr) nextConnID() uint64 { return m.seq.Add(1) }

// setConfig publishes a freshly resolved marketConfig + bindings + rail.
func (m *sessionMgr) setConfig(
	mc *marketConfig,
	cfg *config.Config,
	bindings map[string]acp.BoundSource,
	rail settlement.Rail,
) {
	m.cfg.Store(mc)
	m.baseCfg.Store(cfg)
	m.bindings.Store(&bindings)
	m.rail.Store(&rail)
}

// peerSessionCap is the per-peer concurrent-session bound: half the global
// cap, minimum 1.
func (m *sessionMgr) peerSessionCap() int {
	mc := m.cfg.Load()
	maxSessions := 4
	if mc != nil && mc.maxSessions > 0 {
		maxSessions = mc.maxSessions
	}
	perPeer := maxSessions / peerSessionShareDivisor
	if perPeer < 1 {
		perPeer = 1
	}
	return perPeer
}

// register installs a gated session and enforces the caps. The caller has
// already run the escrow gate; this enforces the resource bounds.
func (m *sessionMgr) register(s *marketSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mc := m.cfg.Load()
	maxSessions := 4
	if mc != nil && mc.maxSessions > 0 {
		maxSessions = mc.maxSessions
	}
	active := 0
	peerActive := 0
	for _, other := range m.sessions {
		if other.State == sessionOpen || other.State == sessionActive {
			active++
			if other.Peer == s.Peer {
				peerActive++
			}
		}
	}
	if active >= maxSessions {
		return fmt.Errorf("max_concurrent_sessions %d reached", maxSessions)
	}
	if peerActive >= m.peerSessionCap() {
		return fmt.Errorf("per-peer session cap %d reached", m.peerSessionCap())
	}
	key := strings.ToLower(s.ID)
	if _, dup := m.sessions[key]; dup {
		return fmt.Errorf("session %s already exists", s.ID)
	}
	m.sessions[key] = s
	m.byConn[s.ConnID] = append(m.byConn[s.ConnID], s)
	m.total.Add(1)
	return nil
}

// peerRateOK slides the per-peer open window; returns false over the limit.
func (m *sessionMgr) peerRateOK(peer string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := time.Now().Add(-time.Minute)
	opens := m.peerOpen[peer]
	keep := opens[:0]
	for _, t := range opens {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= peerOpenPerMinute {
		m.peerOpen[peer] = keep
		return false
	}
	m.peerOpen[peer] = append(keep, time.Now())
	return true
}

// lookup returns a session by id (any state).
func (m *sessionMgr) lookup(id string) *marketSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[strings.ToLower(id)]
}

// finalizeConn closes every session bound to a dropped connection —
// the buyer vanished; running agents are killed and interrupted receipts
// minted so the record (and the claim path's evidence) survives.
func (m *sessionMgr) finalizeConn(connID uint64) {
	m.mu.Lock()
	sessions := m.byConn[connID]
	delete(m.byConn, connID)
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		still := s.State == sessionOpen || s.State == sessionActive
		s.mu.Unlock()
		if still {
			m.finish(s, sessionFailed, "connection dropped")
		}
	}
}

// activate turns a gated session into a live one: resolve the offer's
// agent binding, allocate scratch, spawn the BoundAgent under the
// configured runtime with no egress, and open the agent-side ACP session
// the buyer's prompts forward onto.
func (m *sessionMgr) activate(ctx context.Context, a *marketAgent, s *marketSession) error {
	src, err := m.resolveBinding(s.Offer.ID)
	if err != nil {
		return err
	}
	scratch, err := m.scratchRoot(s)
	if err != nil {
		return err
	}
	rt := ""
	if mc := m.cfg.Load(); mc != nil {
		rt = mc.runtime
	}
	// The handler relays agent session/update traffic to the buyer and
	// accumulates result text — every capability it can't serve is denied.
	handler := &marketClientHandler{
		sess: s,
		upstream: func(uCtx context.Context, n acpsdk.SessionNotification) error {
			a.mu.Lock()
			up := a.upstream
			a.mu.Unlock()
			if up == nil {
				return nil
			}
			return up.SessionUpdate(uCtx, n)
		},
	}
	agent, err := m.spawnFn(ctx, m.baseCfg.Load(), src, acp.BoundSpawn{
		Runtime:    rt,
		ScratchDir: scratch,
		Handler:    handler,
		ClientName: "rhizome-market",
		NoEgress:   true,
	})
	if err != nil {
		return err
	}
	// The session cwd the agent sees: the scratch dir for exec/sandbox;
	// the container's own fs for container runtime (scratch isn't mounted).
	cwd := scratch
	if rt == "container" {
		cwd = strings.TrimSpace(src.ACP.Cwd)
		if cwd == "" {
			cwd = "/"
		}
	}
	resp, err := agent.Conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		agent.Close()
		return fmt.Errorf("agent session/new: %w", err)
	}
	s.mu.Lock()
	s.agent = agent
	s.agentSID = resp.SessionId
	s.scratchDir = scratch
	s.State = sessionActive
	s.mu.Unlock()
	m.audit.log("market.session.active", map[string]any{
		"session_id": s.ID,
		"peer":       s.Peer,
		"offer_id":   s.Offer.ID,
		"runtime":    rt,
	})
	return nil
}

// finish transitions a session to a terminal state, kills its agent,
// mints+persists the receipt, frees the scratch dir, and unregisters —
// the persisted receipt keeps serving /v1/receipt after the in-memory
// record is gone, so the registry never grows unboundedly.
func (m *sessionMgr) finish(s *marketSession, state, reason string) {
	s.mu.Lock()
	if s.State != sessionOpen && s.State != sessionActive {
		s.mu.Unlock()
		return // already terminal — finish is idempotent
	}
	s.State = state
	s.EndedAt = time.Now()
	agent := s.agent
	s.agent = nil
	usage := s.usage
	s.mu.Unlock()

	if agent != nil {
		agent.Close()
	}
	rcpt := m.mintReceipt(s)
	s.mu.Lock()
	s.receipt = rcpt
	s.mu.Unlock()
	m.audit.log("market.session.end", map[string]any{
		"session_id":  s.ID,
		"peer":        s.Peer,
		"state":       state,
		"reason":      reason,
		"duration_ms": s.durationMS(),
		"usage_set":   usage != nil,
	})
	if s.scratchDir != "" {
		_ = os.RemoveAll(s.scratchDir)
	}
	m.mu.Lock()
	delete(m.sessions, strings.ToLower(s.ID))
	if list := m.byConn[s.ConnID]; len(list) > 0 {
		kept := list[:0]
		for _, o := range list {
			if o != s {
				kept = append(kept, o)
			}
		}
		if len(kept) == 0 {
			delete(m.byConn, s.ConnID)
		} else {
			m.byConn[s.ConnID] = kept
		}
	}
	m.mu.Unlock()
}

// reapSessions closes sessions that outlive session_ttl — the DoS bound
// that returns abandoned escrowed slots. Both unbound (open) and running
// (active) sessions age out: an agent that never answers the prompt can't
// pin the slot forever.
func (m *sessionMgr) reapSessions() {
	m.mu.Lock()
	var expired []*marketSession
	mc := m.cfg.Load()
	ttl := 30 * time.Minute
	if mc != nil && mc.sessionTTL > 0 {
		ttl = mc.sessionTTL
	}
	for _, s := range m.sessions {
		s.mu.Lock()
		live := s.State == sessionOpen || s.State == sessionActive
		old := time.Since(s.OpenedAt) > ttl
		s.mu.Unlock()
		if live && old {
			expired = append(expired, s)
		}
	}
	m.mu.Unlock()
	for _, s := range expired {
		m.finish(s, sessionFailed, "session_ttl expired")
	}
}

// runReaper ticks until ctx ends.
func (m *sessionMgr) runReaper(ctx context.Context) {
	t := time.NewTicker(reaperTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.reapSessions()
		}
	}
}

// scratchRoot returns (and creates) a per-session scratch dir.
func (m *sessionMgr) scratchRoot(s *marketSession) (string, error) {
	dir := filepath.Join(m.moduleDir, scratchDirName, sanitizeSessionID(s.ID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func sanitizeSessionID(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

// sessionCount reports live sessions for health.
func (m *sessionMgr) sessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sessions {
		if s.State == sessionOpen || s.State == sessionActive {
			n++
		}
	}
	return n
}

// resolveBinding maps an offer's agent_binding to a BoundSource via the
// agents.list ACP bindings gathered at config load.
func (m *sessionMgr) resolveBinding(offerID string) (acp.BoundSource, error) {
	bindings := m.bindings.Load()
	if bindings == nil {
		return acp.BoundSource{}, fmt.Errorf("agent bindings not loaded")
	}
	mc := m.cfg.Load()
	if mc == nil {
		return acp.BoundSource{}, fmt.Errorf("config not loaded")
	}
	var off *offer
	for i := range mc.offers {
		if mc.offers[i].ID == offerID {
			off = &mc.offers[i]
			break
		}
	}
	if off == nil {
		return acp.BoundSource{}, fmt.Errorf("offer %q not found", offerID)
	}
	src, ok := (*bindings)[off.AgentBinding]
	if !ok {
		return acp.BoundSource{}, fmt.Errorf(
			"offer %q agent_binding %q has no usable acp binding", off.ID, off.AgentBinding)
	}
	return src, nil
}

// agentBindingsFromConfig gathers the local ACP-bound agents — remote
// bindings are skipped (the module has no mesh dialer).
func agentBindingsFromConfig(cfg *config.Config) map[string]acp.BoundSource {
	out := map[string]acp.BoundSource{}
	if cfg == nil {
		return out
	}
	for _, a := range cfg.Agents.List {
		if a.ACP == nil || strings.TrimSpace(a.ACP.Remote) != "" {
			continue
		}
		out[a.ID] = acp.BoundSource{ID: a.ID, Workspace: a.Workspace, ACP: a.ACP}
	}
	return out
}
