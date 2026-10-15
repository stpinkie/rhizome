package econ

import (
	"math/big"
	"sort"
	"strings"
	"time"
)

// PeerSummary is the bilateral-economy split for one peer — charge counts
// by direction plus state counts and the live per-unit balances. It is a
// view derived from the ledger entry stream at render time (Track 146):
// the mesh peer view, the daemon economy snapshot, and the launcher's
// daemonless fallback all build the same shape from the same entries.
type PeerSummary struct {
	// ChargesIssued counts receivable entries — charges this node billed
	// the peer for work it did for them.
	ChargesIssued int `json:"charges_issued"`
	// ChargesReceived counts payable entries — charges the peer billed
	// this node for work it did for us.
	ChargesReceived int `json:"charges_received"`
	// Settled counts entries (either direction) in settled state.
	Settled int `json:"settled"`
	// Disputed counts entries currently in disputed state.
	Disputed int `json:"disputed"`
	// WrittenOff counts dropped entries — disputes that ended in write-off.
	WrittenOff int `json:"written_off,omitempty"`
	// Balances carries the live accrued/settled sums per unit.
	Balances []UnitBalance `json:"balances,omitempty"`
	// OverThreshold lists units whose accrued balance exceeds the
	// configured settle_threshold (advisory — no auto-settle). Set only
	// when a threshold is configured.
	OverThreshold []string `json:"over_threshold,omitempty"`
	// Overdue lists units whose accrued receivable has sat over
	// settle_threshold past settle_grace_period — the non-settlement
	// reputation-pressure signal. Set only when a grace period is
	// configured.
	Overdue []string `json:"overdue,omitempty"`
}

// indexLatest folds the stream into the latest record per entry id — the
// same index BalanceEntries consumes (later lines win).
func indexLatest(entries []Entry) map[string]*Entry {
	out := make(map[string]*Entry, len(entries))
	for i := range entries {
		out[entries[i].EntryID] = &entries[i]
	}
	return out
}

// SummarizePeer builds the economy split for one peer from an entry stream.
// Counts fold to the latest record per entry id — a charge that accrued
// then settled is one charge, not two stream lines.
func SummarizePeer(entries []Entry, peerID string) *PeerSummary {
	indexed := indexLatest(entries)
	s := &PeerSummary{}
	for _, e := range indexed {
		if e.PeerID != peerID {
			continue
		}
		s.count(e)
	}
	s.Balances = BalanceEntries(indexed, func(e *Entry) bool {
		return e.PeerID == peerID
	})
	return s
}

// count folds one latest-state entry into the counters.
func (s *PeerSummary) count(e *Entry) {
	switch e.Direction {
	case DirectionPayable:
		s.ChargesReceived++
	case DirectionReceivable:
		s.ChargesIssued++
	}
	switch e.State {
	case StateSettled:
		s.Settled++
	case StateDisputed:
		s.Disputed++
	case StateWrittenOff:
		s.WrittenOff++
	}
}

// OverdueUnits returns the "peer|unit" keys whose accrued receivable sum
// exceeds threshold and whose oldest accrued receivable is older than the
// grace period. The grace clock runs from the oldest still-accrued charge —
// documented, deterministic, and restart-safe.
func OverdueUnits(
	entries []Entry,
	threshold string,
	grace time.Duration,
	now time.Time,
) []string {
	thr, ok := ParseDecimal(threshold)
	if !ok || thr.Sign() <= 0 || grace <= 0 {
		return nil
	}
	type group struct {
		total  *big.Rat
		oldest time.Time
	}
	groups := map[string]*group{}
	for _, e := range entries {
		if e.Direction != DirectionReceivable || e.State != StateAccrued {
			continue
		}
		amt, ok := ParseDecimal(e.Amount)
		if !ok {
			continue
		}
		k := e.PeerID + "|" + e.Unit
		g := groups[k]
		if g == nil {
			g = &group{total: new(big.Rat)}
			groups[k] = g
		}
		g.total.Add(g.total, amt)
		if g.oldest.IsZero() || e.TS.Before(g.oldest) {
			g.oldest = e.TS
		}
	}
	var out []string
	for k, g := range groups {
		if g.total.Cmp(thr) <= 0 {
			continue
		}
		if now.Sub(g.oldest) < grace {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FlagThresholds stamps the advisory flags on a peer summary:
// over_threshold for any accrued balance past settle_threshold (payable =
// "you should settle"; receivable = "the payer should"), overdue for
// accrued receivables stale past settle_grace_period.
func FlagThresholds(
	s *PeerSummary,
	peerID string,
	entries []Entry,
	threshold string,
	grace time.Duration,
	now time.Time,
) {
	thr, ok := ParseDecimal(threshold)
	if !ok || thr.Sign() <= 0 {
		return
	}
	over := map[string]bool{}
	for _, b := range s.Balances {
		if r, ok := ParseDecimal(b.PayableAccrued); ok && r.Cmp(thr) > 0 {
			over[b.Unit] = true
		}
		if r, ok := ParseDecimal(b.ReceivableAccrued); ok && r.Cmp(thr) > 0 {
			over[b.Unit] = true
		}
	}
	for u := range over {
		s.OverThreshold = append(s.OverThreshold, u)
	}
	sort.Strings(s.OverThreshold)
	if grace <= 0 {
		return
	}
	for _, key := range OverdueUnits(entries, threshold, grace, now) {
		p, u, _ := strings.Cut(key, "|")
		if p == peerID {
			s.Overdue = append(s.Overdue, u)
		}
	}
	sort.Strings(s.Overdue)
}

// Summarize builds the economy split for every peer in the stream —
// single pass over the latest records, same count semantics as
// SummarizePeer.
func Summarize(entries []Entry) map[string]*PeerSummary {
	indexed := indexLatest(entries)
	out := make(map[string]*PeerSummary)
	for _, e := range indexed {
		s := out[e.PeerID]
		if s == nil {
			s = &PeerSummary{}
			out[e.PeerID] = s
		}
		s.count(e)
	}
	for p, s := range out {
		s.Balances = BalanceEntries(indexed, func(e *Entry) bool {
			return e.PeerID == p
		})
	}
	return out
}
