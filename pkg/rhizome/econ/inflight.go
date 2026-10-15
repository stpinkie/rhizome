package econ

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// The in-flight settle journal is the payer-side crash record for web3
// close-out (Track 143). A record is written BEFORE the payment is
// submitted — a restart that finds a non-terminal record resumes the
// exact pending entry or signed transaction instead of risking a
// second transfer. One record per (peer, unit) is live at a time; a
// fresh web3 settle for a pair with a live record reports the in-flight
// state rather than queuing a duplicate payment.
//
// States:
//
//	offered           — phase-1 offer journaled; delivery (or re-delivery
//	                    after a crash) pending. The payee's phase-1
//	                    handler is stateless, so resending is safe.
//	paying            — phase-1 ack received; payment submission pending
//	awaiting_approval — pending-queue entry queued (pending_id set)
//	broadcast         — tx hash known (and, wallet mode, signed raw
//	                    journaled); phase-2 delivery in progress
//	settled           — payee verified + both ledgers settled (terminal)
//	rejected          — payee rejected phase 1 or 2 (terminal)
//	failed            — approval rejected/expired or broadcast failed
//	stalled           — phase-2 retries exhausted; operator may resume
//	                    by re-running `economy settle`
//
// Terminal records are retained (audit trail) and reaped after
// journalReapAge.
const (
	SettleOffered           = "offered"
	SettlePaying            = "paying"
	SettleAwaitingApproval  = "awaiting_approval"
	SettleBroadcast         = "broadcast"
	SettleSettled           = "settled"
	SettleRejected          = "rejected"
	SettleFailed            = "failed"
	SettleStalled           = "stalled"
	inflightFileName        = "economy-settle-inflight.json"
	journalReapAge          = 7 * 24 * time.Hour
	defaultPendingIDRandLen = 8 // matches web3 pending id entropy
)

// InflightRecord is one in-flight web3 close-out.
type InflightRecord struct {
	Key       string         `json:"key"` // peerID|unit
	PeerID    string         `json:"peer_id"`
	Unit      string         `json:"unit"`
	State     string         `json:"state"`
	Offer     *SettleOffer   `json:"offer"` // signed phase-1 offer
	Payment   *SettlePayment `json:"payment,omitempty"`
	PendingID string         `json:"pending_id,omitempty"`
	TxHash    string         `json:"tx_hash,omitempty"`
	SignedRaw string         `json:"signed_raw,omitempty"` // wallet mode: 0x raw tx
	SettleID  string         `json:"settle_id,omitempty"`  // phase-2 id once acked
	Attempts  int            `json:"attempts"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Terminal reports whether the record can no longer transition.
func (r *InflightRecord) Terminal() bool {
	switch r.State {
	case SettleSettled, SettleRejected, SettleFailed, SettleStalled:
		return true
	}
	return false
}

// Redacted returns a copy of the record safe for read surfaces: the
// journaled raw signed transaction — needed for crash-resume, not for
// display — is stripped so status APIs never serve a ready-to-broadcast
// payload.
func (r *InflightRecord) Redacted() *InflightRecord {
	if r == nil {
		return nil
	}
	c := *r
	c.SignedRaw = ""
	return &c
}

// InflightJournal persists payer-side close-out records at
// <home>/economy-settle-inflight.json (atomic whole-file writes — the
// peer-adverts posture).
type InflightJournal struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

type inflightFile struct {
	Version int               `json:"version"`
	Records []*InflightRecord `json:"records"`
}

// OpenInflightJournal opens (creating on first write) the journal at
// path; "" disables it (the mesh degrades to reject-new-duplicates
// rather than crash).
func OpenInflightJournal(path string) *InflightJournal {
	return &InflightJournal{path: path, now: func() time.Time { return time.Now().UTC() }}
}

// InflightJournalPath returns the journal location under RHIZOME_HOME.
func InflightJournalPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, inflightFileName)
}

// MintPendingID allocates a fresh pending-queue id so the journal can
// record it BEFORE the queue submit — crash-resume then knows whether
// the submit landed (id present in queue) or must be re-driven.
func (j *InflightJournal) MintPendingID() (string, error) {
	b := make([]byte, defaultPendingIDRandLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mint pending id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Acquire returns the live record for (peer, unit) — creating+claiming
// it when none exists or every prior record is terminal. The second
// return reports whether the caller now owns a live record (false →
// a live record already exists; it is returned for status reporting).
func (j *InflightJournal) Acquire(
	peerID, unit string,
	offer *SettleOffer,
	payment *SettlePayment,
) (*InflightRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.read()
	if err != nil {
		return nil, false, err
	}
	key := peerID + "|" + unit
	for _, r := range f.Records {
		if r.Key == key && !r.Terminal() {
			return r, false, nil
		}
	}
	rec := &InflightRecord{
		Key:       key,
		PeerID:    peerID,
		Unit:      unit,
		State:     SettleOffered,
		Offer:     offer,
		Payment:   payment,
		CreatedAt: j.now(),
		UpdatedAt: j.now(),
	}
	f.Records = append(f.Records, rec)
	if err := j.write(f); err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

// Update patches the (peer, unit) record. Nil patch fields leave the
// record untouched — pass pointers to clear-vs-set intentionally via
// explicit zero values.
func (j *InflightJournal) Update(peerID, unit string, mutate func(*InflightRecord)) error {
	return j.UpdateByKey(peerID+"|"+unit, mutate)
}

// UpdateByKey patches a live record by its journal key — the mutation
// path for records whose own peer/unit fields may disagree with the
// key (a tampered write): derived-key lookups could never reach such a
// record, so failing it out requires matching the stored key directly.
func (j *InflightJournal) UpdateByKey(key string, mutate func(*InflightRecord)) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.read()
	if err != nil {
		return err
	}
	for _, r := range f.Records {
		if r.Key == key && !r.Terminal() {
			mutate(r)
			r.UpdatedAt = j.now()
			return j.write(f)
		}
	}
	return fmt.Errorf("no live in-flight record for %s", key)
}

// List returns all records (terminal included), newest first.
func (j *InflightJournal) List() ([]*InflightRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.read()
	if err != nil {
		return nil, err
	}
	out := make([]*InflightRecord, len(f.Records))
	copy(out, f.Records)
	sort.Slice(out, func(i, k int) bool { return out[i].UpdatedAt.After(out[k].UpdatedAt) })
	return out, nil
}

// Live returns non-terminal records (the watcher's work set).
func (j *InflightJournal) Live() ([]*InflightRecord, error) {
	all, err := j.List()
	if err != nil {
		return nil, err
	}
	out := make([]*InflightRecord, 0, len(all))
	for _, r := range all {
		if !r.Terminal() {
			out = append(out, r)
		}
	}
	return out, nil
}

// Reap drops terminal records older than journalReapAge.
func (j *InflightJournal) Reap() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.read()
	if err != nil {
		return err
	}
	cutoff := j.now().Add(-journalReapAge)
	kept := f.Records[:0]
	for _, r := range f.Records {
		if r.Terminal() && r.UpdatedAt.Before(cutoff) {
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) == len(f.Records) {
		return nil
	}
	f.Records = kept
	return j.write(f)
}

func (j *InflightJournal) read() (*inflightFile, error) {
	if j.path == "" {
		return &inflightFile{Version: 1}, nil
	}
	data, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return &inflightFile{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settle inflight journal: %w", err)
	}
	var f inflightFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse settle inflight journal: %w", err)
	}
	return &f, nil
}

func (j *InflightJournal) write(f *inflightFile) error {
	if j.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(j.path, data, 0o600)
}
