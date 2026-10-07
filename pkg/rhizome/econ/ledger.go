package econ

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// LedgerFileName is the bilateral ledger's fixed name under RHIZOME_HOME.
const LedgerFileName = "economy-ledger.jsonl"

// Rotation posture mirrors mesh-audit.jsonl: the live file rotates at
// ~10 MB and three generations are kept (.1/.2/.3). Rotated history is
// archive — balance views and transitions index the live file only.
const (
	ledgerMaxBytes  = 10 << 20
	ledgerKeepFiles = 3
)

// Direction is whose money the entry tracks: "payable" is owed out
// (caller side), "receivable" is owed in (callee side).
type Direction string

const (
	DirectionPayable    Direction = "payable"
	DirectionReceivable Direction = "receivable"
)

// EntryState is the entry's settlement lifecycle:
//
//	accrued → settled | disputed | written_off
//	disputed → accrued (credited back) | written_off (dropped)
//
// settled and written_off are terminal; disputed entries never settle.
type EntryState string

const (
	StateAccrued    EntryState = "accrued"
	StateSettled    EntryState = "settled"
	StateDisputed   EntryState = "disputed"
	StateWrittenOff EntryState = "written_off"
)

// Entry is one ledger record. A transition appends the full updated entry
// under the same entry_id, so the file is self-describing history — the
// latest line for an id is its current state.
type Entry struct {
	// EntryID is the stable handle settle/dispute/credit verbs reference.
	// Assigned by Record when empty.
	EntryID string    `json:"entry_id"`
	TS      time.Time `json:"ts"`
	PeerID  string    `json:"peer_id"`
	// Direction is payable (we owe) or receivable (we are owed).
	Direction Direction `json:"direction"`
	// TaskID / CorrelationID identify the billed work — task id on the
	// async protocol, correlation id on the synchronous one.
	TaskID        string `json:"task_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Unit          string `json:"unit"`
	// Amount is the canonical decimal charge in Unit.
	Amount string                  `json:"amount"`
	Usage  *toolshared.RemoteUsage `json:"usage,omitempty"`
	// ResultSHA256 commits the entry to the exact result bytes.
	ResultSHA256 string     `json:"result_sha256,omitempty"`
	SheetDigest  string     `json:"sheet_digest,omitempty"`
	State        EntryState `json:"state"`
	// SettleID / SettleTX link the entry to a settle round (Track 142)
	// and the on-chain transaction when the backend is web3 (Track 143).
	SettleID string `json:"settle_id,omitempty"`
	SettleTX string `json:"settle_tx,omitempty"`
	// Note carries the operator-supplied reason on a dispute transition
	// (or the resolution note on re-accrue/write-off).
	Note string `json:"note,omitempty"`
}

// dedupeKey identifies the underlying charge — the same task id or
// resubmitted call must never double-accrue. Task id dominates when set;
// correlation id alone keys synchronous (task-id-less) calls.
func (e Entry) dedupeKey() string {
	if e.TaskID != "" {
		return string(e.Direction) + "|" + e.PeerID + "|task:" + e.TaskID
	}
	return string(e.Direction) + "|" + e.PeerID + "|corr:" + e.CorrelationID
}

// workRef is the settle-offer wire ref for an entry — the task id when
// present, else the correlation id (the ref the payee's receivable rows
// carry identically, unlike the local entry_id).
func (e Entry) workRef() string {
	if e.TaskID != "" {
		return e.TaskID
	}
	return e.CorrelationID
}

// Ledger is the node's append-only bilateral charge book. The mesh owns the
// writer handle; CLI verbs read the file daemonless via ReadEntries — the
// same posture as the peer-score store. Writes are single-line appends under
// a mutex; transitions append a fresh record under the entry's id. The
// in-memory index resyncs from the file whenever it changes on disk, so
// daemonless writes (settle --mark-only, dispute, resolve) land in the
// daemon's view on the next access instead of waiting for a restart.
type Ledger struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	keep    int
	modTime time.Time
	size    int64

	entries  map[string]*Entry // entry_id → latest record
	order    []string          // entry_ids in first-seen order
	byDedupe map[string]string // dedupeKey → entry_id
}

// OpenLedger loads (creating when absent) the ledger at path.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{
		path:     path,
		maxSize:  ledgerMaxBytes,
		keep:     ledgerKeepFiles,
		entries:  make(map[string]*Entry),
		byDedupe: make(map[string]string),
	}
	if path == "" {
		return l, nil
	}
	if err := l.reloadLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

// syncLocked reloads the index when the file changed on disk — an external
// append (a daemonless CLI write) or a rotation shows up on next access.
func (l *Ledger) syncLocked() {
	if l.path == "" {
		return
	}
	info, err := os.Stat(l.path)
	if os.IsNotExist(err) {
		if l.size != 0 {
			l.entries = make(map[string]*Entry)
			l.order = nil
			l.byDedupe = make(map[string]string)
			l.size, l.modTime = 0, time.Time{}
		}
		return
	}
	if err != nil {
		return
	}
	if info.ModTime().Equal(l.modTime) && info.Size() == l.size {
		return
	}
	_ = l.reloadLocked()
}

// reloadLocked rebuilds the in-memory index from the live file.
func (l *Ledger) reloadLocked() error {
	ents, err := ReadEntries(l.path)
	if err != nil {
		return err
	}
	l.entries = make(map[string]*Entry)
	l.order = nil
	l.byDedupe = make(map[string]string)
	for i := range ents {
		l.index(&ents[i])
	}
	if info, err := os.Stat(l.path); err == nil {
		l.modTime, l.size = info.ModTime(), info.Size()
	}
	return nil
}

// index folds one record into the in-memory view (latest per entry_id).
func (l *Ledger) index(e *Entry) {
	if e.EntryID == "" {
		return
	}
	if _, seen := l.entries[e.EntryID]; !seen {
		l.order = append(l.order, e.EntryID)
		if e.TaskID != "" || e.CorrelationID != "" {
			l.byDedupe[e.dedupeKey()] = e.EntryID
		}
	}
	l.entries[e.EntryID] = e
}

// Record accrues a new charge. It dedupes on (direction, peer, task_id,
// correlation_id): a resubmitted task or re-fetched result returns the
// existing accrual rather than double-billing.
func (l *Ledger) Record(e Entry) (*Entry, error) {
	if e.Amount == "" {
		return nil, fmt.Errorf("ledger entry requires an amount")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()

	if e.EntryID == "" {
		if e.TaskID != "" || e.CorrelationID != "" {
			if existing, ok := l.byDedupe[e.dedupeKey()]; ok {
				return l.entries[existing], nil
			}
		}
		e.EntryID = newEntryID()
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if e.State == "" {
		e.State = StateAccrued
	}
	if err := l.append(&e); err != nil {
		return nil, err
	}
	l.index(&e)
	ec := e
	return &ec, nil
}

// Transition moves an entry along its lifecycle. Valid moves:
//
//	accrued  → settled | disputed
//	disputed → accrued (--credit re-accrues) | written_off (--drop)
//
// settleID/settleTX annotate a settle transition (or clear a dispute's
// stale settle markers on re-accrual); note carries the dispute reason.
func (l *Ledger) Transition(entryID string, to EntryState, settleID, settleTX, note string) (*Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()

	cur, ok := l.entries[entryID]
	if !ok {
		return nil, fmt.Errorf("no ledger entry %q", entryID)
	}
	valid := map[EntryState][]EntryState{
		StateAccrued:  {StateSettled, StateDisputed},
		StateDisputed: {StateAccrued, StateWrittenOff},
	}
	allowed := false
	for _, s := range valid[cur.State] {
		if s == to {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("cannot transition entry %s from %s to %s", entryID, cur.State, to)
	}
	next := *cur
	next.State = to
	next.TS = time.Now().UTC()
	if to == StateAccrued {
		next.SettleID, next.SettleTX = "", ""
	} else {
		next.SettleID, next.SettleTX = settleID, settleTX
	}
	next.Note = note
	if err := l.append(&next); err != nil {
		return nil, err
	}
	l.index(&next)
	n := next
	return &n, nil
}

// Get returns the latest record for an entry id.
func (l *Ledger) Get(entryID string) (Entry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()
	e, ok := l.entries[entryID]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Entries returns the latest record per entry in first-seen order.
func (l *Ledger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()
	out := make([]Entry, 0, len(l.order))
	for _, id := range l.order {
		out = append(out, *l.entries[id])
	}
	return out
}

// UnitBalance is one unit's accrued/settled sums in both directions.
type UnitBalance struct {
	Unit              string `json:"unit"`
	PayableAccrued    string `json:"payable_accrued"`
	PayableSettled    string `json:"payable_settled"`
	ReceivableAccrued string `json:"receivable_accrued"`
	ReceivableSettled string `json:"receivable_settled"`
}

// Balance returns per-unit balances for one peer, units sorted.
func (l *Ledger) Balance(peerID string) []UnitBalance {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()
	return BalanceEntries(l.entries, func(e *Entry) bool { return e.PeerID == peerID })
}

// Balances returns per-unit balances for every peer present in the ledger.
func (l *Ledger) Balances() map[string][]UnitBalance {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()
	peers := make(map[string]bool)
	for _, e := range l.entries {
		peers[e.PeerID] = true
	}
	out := make(map[string][]UnitBalance, len(peers))
	for p := range peers {
		out[p] = BalanceEntries(l.entries, func(e *Entry) bool { return e.PeerID == p })
	}
	return out
}

// CommittedSince sums the caller's payable obligations in the window —
// accrued + settled + disputed all count as committed (a dispute may
// re-accrue); written_off is dead weight and does not count. Powers
// max_cost_per_day enforcement (Track 144).
func (l *Ledger) CommittedSince(peerID string, since time.Time) map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncLocked()
	return CommittedSinceEntries(l.entries, func(e *Entry) bool { return e.PeerID == peerID }, since)
}

// CommittedSinceEntries is the free-function form of CommittedSince for
// daemonless callers that have already read the entry stream.
func CommittedSinceEntries(entries map[string]*Entry, match func(*Entry) bool, since time.Time) map[string]string {
	sums := make(map[string]*big.Rat)
	for _, e := range entries {
		if !match(e) || e.Direction != DirectionPayable {
			continue
		}
		if e.State == StateWrittenOff || e.TS.Before(since) {
			continue
		}
		amt, ok := parseDecimal(e.Amount)
		if !ok {
			continue
		}
		if sums[e.Unit] == nil {
			sums[e.Unit] = new(big.Rat)
		}
		sums[e.Unit].Add(sums[e.Unit], amt)
	}
	out := make(map[string]string, len(sums))
	for u, s := range sums {
		out[u] = decimalString(s)
	}
	return out
}

// BalanceEntries is the free-function form of Balance for daemonless
// callers that have already read the entry stream.
func BalanceEntries(entries map[string]*Entry, match func(*Entry) bool) []UnitBalance {
	sums := make(map[string]*UnitBalance)
	rats := make(map[string][4]*big.Rat)
	add := func(u string, idx int, amount string) {
		r, ok := parseDecimal(amount)
		if !ok {
			return
		}
		if sums[u] == nil {
			sums[u] = &UnitBalance{Unit: u}
			rats[u] = [4]*big.Rat{new(big.Rat), new(big.Rat), new(big.Rat), new(big.Rat)}
		}
		rats[u][idx].Add(rats[u][idx], r)
	}
	for _, e := range entries {
		if !match(e) {
			continue
		}
		settled := e.State == StateSettled
		switch {
		case e.Direction == DirectionPayable && settled:
			add(e.Unit, 1, e.Amount)
		case e.Direction == DirectionPayable && e.State != StateWrittenOff:
			add(e.Unit, 0, e.Amount)
		case e.Direction == DirectionReceivable && settled:
			add(e.Unit, 3, e.Amount)
		case e.Direction == DirectionReceivable && e.State != StateWrittenOff:
			add(e.Unit, 2, e.Amount)
		}
	}
	units := make([]string, 0, len(sums))
	for u := range sums {
		units = append(units, u)
	}
	sort.Strings(units)
	out := make([]UnitBalance, 0, len(units))
	for _, u := range units {
		b := sums[u]
		b.PayableAccrued = decimalString(rats[u][0])
		b.PayableSettled = decimalString(rats[u][1])
		b.ReceivableAccrued = decimalString(rats[u][2])
		b.ReceivableSettled = decimalString(rats[u][3])
		out = append(out, *b)
	}
	return out
}

// append writes one record line; the file rotates first when over budget.
func (l *Ledger) append(e *Entry) error {
	if l.path == "" {
		return nil
	}
	if info, err := os.Stat(l.path); err == nil && info.Size() > l.maxSize {
		l.rotateLocked()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode ledger entry: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("mkdir ledger dir: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append ledger: %w", err)
	}
	if info, err := f.Stat(); err == nil {
		l.modTime, l.size = info.ModTime(), info.Size()
	}
	return nil
}

// rotateLocked shifts .N-1 → .N; identical posture to the audit trail.
func (l *Ledger) rotateLocked() {
	_ = os.Remove(fmt.Sprintf("%s.%d", l.path, l.keep))
	for i := l.keep - 1; i >= 1; i-- {
		_ = os.Remove(fmt.Sprintf("%s.%d", l.path, i+1))
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
}

// ReadEntries replays the ledger file into latest-state entries, ordered by
// first appearance — the daemonless read path for CLI verbs. A missing file
// is an empty ledger, not an error; corrupt lines are skipped (the writer
// remains authoritative; readers tolerate torn tails).
func ReadEntries(path string) ([]Entry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // ledger path is daemon-owned
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ledger: %w", err)
	}
	idx := make(map[string]*Entry)
	var order []string
	for line := range splitLines(data) {
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.EntryID == "" {
			continue
		}
		if _, seen := idx[e.EntryID]; !seen {
			order = append(order, e.EntryID)
			idx[e.EntryID] = &e
			continue
		}
		*idx[e.EntryID] = e
	}
	out := make([]Entry, 0, len(order))
	for _, id := range order {
		out = append(out, *idx[id])
	}
	return out, nil
}

// splitLines yields non-empty trimmed lines without holding the whole
// decoded set.
func splitLines(data []byte) func(func([]byte) bool) {
	return func(yield func([]byte) bool) {
		start := 0
		for i, b := range data {
			if b != '\n' {
				continue
			}
			line := data[start:i]
			start = i + 1
			if len(line) == 0 {
				continue
			}
			if !yield(line) {
				return
			}
		}
		if tail := data[start:]; len(tail) > 0 {
			yield(tail)
		}
	}
}

// newEntryID mints a collision-free entry handle.
func newEntryID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("entry-%d", time.Now().UnixNano())
	}
	return "entry-" + hex.EncodeToString(b[:])
}
