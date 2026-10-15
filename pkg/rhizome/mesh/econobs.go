package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// Track 146 — economy observability + reputation.
//
// Two surfaces share one derivation: the peer-score split (PeerScoreView.Econ
// — charge counts, settle/dispute counts, accrued balances) and the daemon
// economy snapshot (GET /network/economy). Score writes keep the market
// posture — session refs + value commitments in Outcomes, never raw amounts;
// op counters under econ_charge/econ_settle/econ_dispute.

// EconRecentLimit bounds the recent-charges tail on the economy snapshot.
const EconRecentLimit = 25

// overdueNotifyTTL bounds the in-memory overdue dedupe — an epoch still
// unpaid this long re-fires once rather than going silent.
const overdueNotifyTTL = 30 * 24 * time.Hour

// econValueHash commits charged/settled value without persisting amounts —
// sha256 over the joined parts, same posture as the market value_hash.
func econValueHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}

// chargeOutcomeHash commits a charge's billed value for the outcome ref —
// sha256(amount|unit|sheet_digest); "" when no charge arrived.
func chargeOutcomeHash(c *econ.Charge) string {
	if c == nil {
		return ""
	}
	return econValueHash(c.Amount, c.Unit, c.SheetDigest)
}

// recordEconOutcome writes one economy outcome ref against the peer's score.
// Terminal outcomes move the econ_* op counters; pending ones (disputed,
// attested) land in the bounded Outcomes list only.
func (m *Mesh) recordEconOutcome(pidStr, op, outcome, sessionID, valueHash string) {
	if m == nil || m.scoreStore == nil {
		return
	}
	pid, err := peer.Decode(pidStr)
	if err != nil {
		return
	}
	m.scoreStore.RecordOutcome(pid, op, outcome, sessionID, valueHash)
}

// recordEconOp moves just the econ_* op counters — used for high-frequency
// charge bookkeeping where an Outcomes ref per charge would churn the
// bounded ref list. Latency is not meaningful for ledger writes.
func (m *Mesh) recordEconOp(pidStr, op string, success bool, callErr error) {
	if m == nil || m.scoreStore == nil {
		return
	}
	pid, err := peer.Decode(pidStr)
	if err != nil {
		return
	}
	m.scoreStore.Record(pid, op, success, 0, callErr)
}

// peerEconView derives the economy split for one peer from the ledger —
// nil when the ledger is unavailable or the peer has no entries.
func (m *Mesh) peerEconView(peerID string) *econ.PeerSummary {
	if m == nil || m.econLedger == nil {
		return nil
	}
	entries := m.econLedger.Entries()
	s := econ.SummarizePeer(entries, peerID)
	if s.ChargesIssued == 0 && s.ChargesReceived == 0 {
		return nil
	}
	m.flagPeerThresholds(s, peerID, entries)
	return s
}

// flagPeerThresholds stamps the advisory threshold flags on a summary —
// delegates to econ.FlagThresholds so the daemonless launcher fallback
// derives the identical view.
func (m *Mesh) flagPeerThresholds(s *econ.PeerSummary, peerID string, entries []econ.Entry) {
	econ.FlagThresholds(s, peerID, entries,
		m.cfg.Economy.SettleThreshold, m.cfg.Economy.SettleGracePeriod, time.Now())
}

// resolveOutcomeFor maps a dispute resolution to the peer-facing outcome:
// credit upholds a payable charge (for peer) but re-asserts a receivable
// debt (against); drop flips both — a bogus bill is ruled against the
// biller, a forgiven debt rules for the debtor.
func resolveOutcomeFor(dir econ.Direction, drop bool) string {
	forPeer := (dir == econ.DirectionPayable) != drop
	if forPeer {
		return OutcomeResolvedForPeer
	}
	return OutcomeResolvedAgainst
}

// econWorkRef is the settle-offer wire ref — task id when present, else the
// correlation id (mirrors Entry.workRef in econ, which is unexported).
func econWorkRef(e econ.Entry) string {
	if e.TaskID != "" {
		return e.TaskID
	}
	return e.CorrelationID
}

// recordDisputeOutcomes books a pending econ_dispute outcome on each peer
// covered by the disputed entries — terminal outcomes land at resolve.
func (m *Mesh) recordDisputeOutcomes(entries []econ.Entry) {
	for peerID, ents := range groupEntriesByPeer(entries) {
		m.recordEconOutcome(peerID, OpEconDispute, OutcomeDisputed,
			econWorkRef(ents[0]), "")
	}
}

// recordResolveOutcomes books the terminal econ_dispute outcome per
// (peer, direction) — the score counter moves here.
func (m *Mesh) recordResolveOutcomes(entries []econ.Entry, drop bool) {
	type key struct {
		peerID string
		dir    econ.Direction
	}
	seen := map[key]bool{}
	for _, e := range entries {
		k := key{e.PeerID, e.Direction}
		if seen[k] {
			continue
		}
		seen[k] = true
		m.recordEconOutcome(e.PeerID, OpEconDispute, resolveOutcomeFor(e.Direction, drop),
			econWorkRef(e), econValueHash(e.Amount, e.Unit, econWorkRef(e)))
	}
}

// groupEntriesByPeer folds entries into peer → entries.
func groupEntriesByPeer(entries []econ.Entry) map[string][]econ.Entry {
	out := map[string][]econ.Entry{}
	for _, e := range entries {
		out[e.PeerID] = append(out[e.PeerID], e)
	}
	return out
}

// econEntryAuditFields builds the unit/amount/task audit extras for an
// entry set: a single-unit set carries unit+amount directly; mixed sets
// carry the per-unit sums. Spec fields: unit, amount, task_id, outcome.
func econEntryAuditFields(entries []econ.Entry, outcome string) map[string]any {
	sums := map[string]*big.Rat{}
	var taskID string
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.EntryID)
		if taskID == "" {
			taskID = e.TaskID
		}
		if amt, ok := econ.ParseDecimal(e.Amount); ok {
			if sums[e.Unit] == nil {
				sums[e.Unit] = new(big.Rat)
			}
			sums[e.Unit].Add(sums[e.Unit], amt)
		}
	}
	extra := map[string]any{
		"entry_ids": ids,
		"outcome":   outcome,
	}
	if taskID != "" {
		extra["task_id"] = taskID
	}
	if len(sums) == 1 {
		for u, s := range sums {
			extra["unit"] = u
			extra["amount"] = econ.FormatDecimal(s)
		}
	} else if len(sums) > 1 {
		amounts := map[string]string{}
		for u, s := range sums {
			amounts[u] = econ.FormatDecimal(s)
		}
		extra["amounts"] = amounts
	}
	return extra
}

// checkSettleDue emits mesh.econ.settle_due when this charge just pushed the
// peer's accrued payable over settle_threshold — the advisory "time to
// settle" edge. Edge-triggered: a charge landing on an already-over balance
// does not refire.
func (m *Mesh) checkSettleDue(peerID string, charge *econ.Charge) {
	if m == nil || m.econLedger == nil || charge == nil {
		return
	}
	thr, ok := econ.ParseDecimal(m.cfg.Economy.SettleThreshold)
	if !ok || thr.Sign() <= 0 {
		return
	}
	amt, ok := econ.ParseDecimal(charge.Amount)
	if !ok {
		return
	}
	for _, b := range m.econLedger.Balance(peerID) {
		if b.Unit != charge.Unit {
			continue
		}
		accrued, ok := econ.ParseDecimal(b.PayableAccrued)
		if !ok || accrued.Cmp(thr) <= 0 {
			continue
		}
		before := new(big.Rat).Sub(accrued, amt)
		if before.Cmp(thr) <= 0 {
			m.publishMeshEvent(runtimeevents.KindMeshEconSettleDue, map[string]any{
				"peer_id":          peerID,
				"unit":             charge.Unit,
				"payable_accrued":  b.PayableAccrued,
				"settle_threshold": m.cfg.Economy.SettleThreshold,
			})
		}
		return
	}
}

// econSweepOverdue records one econ_settle score failure per overdue
// (peer, unit) epoch on the payee's view — the Track 146 non-settlement
// reputation pressure. Dedupe keys on the contributing entry set: while
// the same accrued set stays overdue the failure lands once; new accrual
// or a fresh debt epoch re-arms it.
func (m *Mesh) econSweepOverdue(now time.Time) {
	if m == nil || m.econLedger == nil || m.scoreStore == nil {
		return
	}
	grace := m.cfg.Economy.SettleGracePeriod
	threshold := m.cfg.Economy.SettleThreshold
	entries := m.econLedger.Entries()
	for _, key := range econ.OverdueUnits(entries, threshold, grace, now) {
		peerID, unit, _ := strings.Cut(key, "|")
		var ids []string
		var total *big.Rat
		var oldest time.Time
		for _, e := range entries {
			if e.PeerID != peerID || e.Unit != unit ||
				e.Direction != econ.DirectionReceivable || e.State != econ.StateAccrued {
				continue
			}
			ids = append(ids, e.EntryID)
			if amt, ok := econ.ParseDecimal(e.Amount); ok {
				if total == nil {
					total = new(big.Rat)
				}
				total.Add(total, amt)
			}
			if oldest.IsZero() || e.TS.Before(oldest) {
				oldest = e.TS
			}
		}
		sort.Strings(ids)
		sessionID := "overdue|" + unit + "|" + econValueHash(ids...)[:16]
		// The persisted outcome ref is the restart-safe dedupe, but it
		// can evict from the bounded Outcomes list under churn — the
		// in-memory set covers eviction within a process lifetime and
		// also records the ref's own hits, so an evicted epoch stays
		// deduped until the TTL lapses.
		if m.overdueDedup(sessionID, now) ||
			m.hasOutcomeSession(peerID, OpEconSettle, sessionID) {
			continue
		}
		amount := "0"
		if total != nil {
			amount = econ.FormatDecimal(total)
		}
		m.recordEconOutcome(peerID, OpEconSettle, OutcomeFailed, sessionID,
			econValueHash(amount, unit, sessionID))
		m.publishMeshEvent(runtimeevents.KindMeshEconOverdue, map[string]any{
			"peer_id":            peerID,
			"unit":               unit,
			"receivable_accrued": amount,
			"settle_threshold":   threshold,
			"grace":              grace.String(),
			"oldest_entry_ts":    oldest.UTC().Format(time.RFC3339),
		})
		if pid, err := peer.Decode(peerID); err == nil {
			m.auditMeshFields(pid, "econ.overdue", "", sessionID, "failed",
				time.Now(), "payer over settle_threshold past grace",
				map[string]any{
					"unit": unit, "amount": amount,
					"entry_ids": ids, "outcome": OutcomeFailed,
				})
		}
	}
}

// overdueDedup reports whether this process already notified the epoch —
// the dedupe that survives Outcomes-list eviction (the score ref lives
// only 32 entries deep). Entries hold their FIRST notification time:
// hits inside the TTL do not refresh, so an epoch still unpaid past it
// re-fires once (a fresh stamp) rather than going silent; epochs that
// left the overdue set expire out of the map on the same TTL.
func (m *Mesh) overdueDedup(sessionID string, now time.Time) bool {
	m.overdueNotifiedMu.Lock()
	defer m.overdueNotifiedMu.Unlock()
	for k, t := range m.overdueNotified {
		if now.Sub(t) > overdueNotifyTTL {
			delete(m.overdueNotified, k)
		}
	}
	_, seen := m.overdueNotified[sessionID]
	if !seen {
		m.overdueNotified[sessionID] = now
	}
	return seen
}

// hasOutcomeSession reports whether the peer's score already carries an
// outcome ref for (op, sessionID) — the restart-safe dedupe for the
// overdue penalty.
func (m *Mesh) hasOutcomeSession(pidStr, op, sessionID string) bool {
	pid, err := peer.Decode(pidStr)
	if err != nil || m.scoreStore == nil {
		return false
	}
	sc, ok := m.scoreStore.Get(pid)
	if !ok {
		return false
	}
	for _, o := range sc.Outcomes {
		if o.Op == op && o.SessionID == sessionID {
			return true
		}
	}
	return false
}

// econWatchLoop runs the overdue sweep on a slow cadence while the mesh is
// up. The tick tracks the configured grace period (bounded); the sweep
// itself is a cheap ledger scan.
func (m *Mesh) econWatchLoop(ctx context.Context) {
	grace := m.cfg.Economy.SettleGracePeriod
	tick := grace / 4
	if tick < 30*time.Second {
		tick = 30 * time.Second
	}
	if tick > 5*time.Minute {
		tick = 5 * time.Minute
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.econSweepOverdue(now)
		}
	}
}

// EconomyView is the read-side economy snapshot served on
// GET /network/economy and the launcher proxy — per-peer splits, the
// recent charge tail, and live in-flight settle records.
type EconomyView struct {
	Enabled           bool                         `json:"enabled"`
	Unit              string                       `json:"unit,omitempty"`
	SettleThreshold   string                       `json:"settle_threshold,omitempty"`
	SettleGracePeriod string                       `json:"settle_grace_period,omitempty"`
	Peers             map[string]*econ.PeerSummary `json:"peers,omitempty"`
	RecentCharges     []econ.Entry                 `json:"recent_charges,omitempty"`
	Inflight          []*econ.InflightRecord       `json:"inflight,omitempty"`
}

// EconomyView builds the snapshot from the live ledger + journal.
func (m *Mesh) EconomyView() *EconomyView {
	v := &EconomyView{Enabled: m != nil && m.cfg.Economy.Enabled}
	if m == nil || m.econLedger == nil {
		return v
	}
	v.Unit = m.cfg.Economy.Unit
	v.SettleThreshold = m.cfg.Economy.SettleThreshold
	if m.cfg.Economy.SettleGracePeriod > 0 {
		v.SettleGracePeriod = m.cfg.Economy.SettleGracePeriod.String()
	}
	entries := m.econLedger.Entries()
	v.Peers = econ.Summarize(entries)
	for pid, s := range v.Peers {
		m.flagPeerThresholds(s, pid, entries)
	}
	if n := len(entries); n > 0 {
		start := n - EconRecentLimit
		if start < 0 {
			start = 0
		}
		v.RecentCharges = append([]econ.Entry(nil), entries[start:]...)
	}
	if m.inflight != nil {
		if live, err := m.inflight.Live(); err == nil && len(live) > 0 {
			// Strip journaled raw signed transactions — crash-resume
			// material, not something a status endpoint should serve.
			v.Inflight = make([]*econ.InflightRecord, len(live))
			for i, r := range live {
				v.Inflight[i] = r.Redacted()
			}
		}
	}
	return v
}
