// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// The pending-approval queue is the signing gate: gated tools enqueue a
// PendingEntry and return its id; a human resolves it via the CLI, the
// daemon API, or a scope-allowlisted channel command. Resolution is a
// status transition on disk — the executing side (daemon, or the CLI when
// no daemon runs) then signs and broadcasts. Approval expiry denies by
// default: entries past ExpiresAt report StatusExpired and can no longer
// be approved.

type PendingKind string

const (
	KindSend     PendingKind = "send"
	KindSign     PendingKind = "sign"     // EIP-191 message signature
	KindApprove  PendingKind = "approve"  // ERC-20 approve convenience
	KindContract PendingKind = "contract" // ABI-encoded contract call
)

type PendingStatus string

const (
	StatusPending  PendingStatus = "pending"
	StatusApproved PendingStatus = "approved" // resolved, awaiting execution
	StatusSent     PendingStatus = "sent"     // broadcast ok — see TxHash
	StatusDone     PendingStatus = "done"     // sign-kind complete — see Result
	StatusRejected PendingStatus = "rejected"
	StatusExpired  PendingStatus = "expired"
	StatusFailed   PendingStatus = "failed" // broadcast/sign error — see Error
)

// IsTerminal reports whether the entry can no longer change state.
func (s PendingStatus) IsTerminal() bool {
	switch s {
	case StatusSent, StatusDone, StatusRejected, StatusExpired, StatusFailed:
		return true
	default:
		return false
	}
}

// PendingEntry is one queued signing request. ValueWei is decimal,
// Data/Message hex-encoded — the file is the record a human approves.
type PendingEntry struct {
	ID         string        `json:"id"`
	Kind       PendingKind   `json:"kind"`
	ChainID    uint64        `json:"chain_id"`
	From       string        `json:"from"`
	To         string        `json:"to,omitempty"`
	ValueWei   string        `json:"value_wei,omitempty"` // decimal
	Data       string        `json:"data,omitempty"`      // 0x hex calldata
	Selector   string        `json:"selector,omitempty"`  // 0x + 4 bytes
	Message    string        `json:"message,omitempty"`   // sign kind — 0x hex of message bytes
	Summary    string        `json:"summary"`             // human-readable approval line
	CreatedAt  time.Time     `json:"created_at"`
	ExpiresAt  time.Time     `json:"expires_at"`
	Status     PendingStatus `json:"status"`
	ResolvedBy string        `json:"resolved_by,omitempty"` // "cli"|"daemon"|"channel:<scope>"
	TxHash     string        `json:"tx_hash,omitempty"`
	Result     string        `json:"result,omitempty"` // sign kind: 0x signature
	Error      string        `json:"error,omitempty"`
}

const (
	pendingFileName    = "web3-pending.json"
	pendingMaxQueued   = 100
	defaultApprovalTTL = 15 * time.Minute
)

// PendingStore persists the approval queue at <dir>/web3-pending.json
// (0600, atomic writes). When the daemon runs it owns resolution; a
// daemonless CLI may resolve too — last writer wins, each write is
// whole-file atomic so a lost race loses cleanly rather than corrupting.
type PendingStore struct {
	path string
	mu   sync.Mutex
	now  func() time.Time // test hook
}

// OpenPendingStore returns the queue rooted at dir (<RHIZOME_HOME>/web3).
func OpenPendingStore(dir string) *PendingStore {
	return &PendingStore{
		path: filepath.Join(dir, pendingFileName),
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (s *PendingStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

type pendingFile struct {
	Version int             `json:"version"`
	Entries []*PendingEntry `json:"entries"`
}

// Submit validates, stamps, and enqueues a pending entry. TTL defaults to
// defaultApprovalTTL when ExpiresAt is zero.
func (s *PendingStore) Submit(e *PendingEntry) (*PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	s.sweepLocked(f)
	if e.Kind == "" {
		return nil, fmt.Errorf("pending entry needs a kind")
	}
	// Every kind signs — a signer address is mandatory.
	if !IsAddress(e.From) {
		return nil, fmt.Errorf("invalid or missing from address %q", e.From)
	}
	if e.To != "" && !IsAddress(e.To) {
		return nil, fmt.Errorf("invalid to address %q", e.To)
	}
	id, err := s.newID(f)
	if err != nil {
		return nil, err
	}
	e.ID = id
	e.CreatedAt = s.clock()
	if e.ExpiresAt.IsZero() {
		e.ExpiresAt = e.CreatedAt.Add(defaultApprovalTTL)
	}
	e.Status = StatusPending
	f.Entries = append(f.Entries, e)
	if err := s.boundLocked(f); err != nil {
		return nil, err
	}
	if err := s.write(f); err != nil {
		return nil, err
	}
	return e, nil
}

// List returns all entries (newest first), sweeping expired pending
// entries to StatusExpired.
func (s *PendingStore) List() ([]*PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	changed := s.sweepLocked(f)
	if changed {
		_ = s.write(f) // best-effort expiry persist; readers still see truth
	}
	out := make([]*PendingEntry, len(f.Entries))
	copy(out, f.Entries)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Get returns one entry by id (expiry-swept first).
func (s *PendingStore) Get(id string) (*PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	if s.sweepLocked(f) {
		_ = s.write(f)
	}
	e := findPending(f, id)
	if e == nil {
		return nil, fmt.Errorf("no pending request %q", id)
	}
	return e, nil
}

// Resolve transitions a pending entry to approved or rejected. Expired or
// already-resolved entries cannot transition — expiry denies by default.
func (s *PendingStore) Resolve(id string, approve bool, resolvedBy string) (*PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	s.sweepLocked(f)
	e := findPending(f, id)
	if e == nil {
		return nil, fmt.Errorf("no pending request %q", id)
	}
	switch e.Status {
	case StatusPending:
		// ok
	case StatusExpired:
		return nil, fmt.Errorf("request %s expired at %s", id, e.ExpiresAt.Format(time.RFC3339))
	default:
		return nil, fmt.Errorf("request %s is already %s", id, e.Status)
	}
	if approve {
		e.Status = StatusApproved
	} else {
		e.Status = StatusRejected
	}
	e.ResolvedBy = resolvedBy
	if err := s.write(f); err != nil {
		return nil, err
	}
	return e, nil
}

// Complete marks an approved entry sent/done/failed after execution.
func (s *PendingStore) Complete(id, txHash, result string, execErr error) (*PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	e := findPending(f, id)
	if e == nil {
		return nil, fmt.Errorf("no pending request %q", id)
	}
	if e.Status != StatusApproved {
		return nil, fmt.Errorf("request %s is %s, not approved", id, e.Status)
	}
	if execErr != nil {
		e.Status = StatusFailed
		e.Error = execErr.Error()
	} else {
		e.TxHash = txHash
		e.Result = result
		if e.Kind == KindSign {
			e.Status = StatusDone
		} else {
			e.Status = StatusSent
		}
	}
	if err := s.write(f); err != nil {
		return nil, err
	}
	return e, nil
}

// ExecuteApproved runs an approved entry: loads the key, signs, and
// broadcasts (send/approve/contract) or signs the message (sign). The
// entry is then completed. This is the single execution path shared by
// the daemon handler and the daemonless CLI.
func (s *PendingStore) ExecuteApproved(ctx context.Context, id string,
	wallets *WalletStore, provider *Provider, ledger *SpendLedger,
) (*PendingEntry, error) {
	e, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if e.Status != StatusApproved {
		return nil, fmt.Errorf("request %s is %s, not approved", id, e.Status)
	}
	fail := func(err error) (*PendingEntry, error) {
		done, _ := s.Complete(id, "", "", err)
		return done, err
	}
	priv, err := wallets.PrivateKey(e.From)
	if err != nil {
		return fail(err)
	}
	defer priv.Zero()

	switch e.Kind {
	case KindSign:
		msg, err := ParseHexBytes(e.Message)
		if err != nil {
			return fail(fmt.Errorf("decode message: %w", err))
		}
		sig, err := SignMessage(priv, msg)
		if err != nil {
			return fail(err)
		}
		return s.Complete(id, "", "0x"+hex.EncodeToString(sig), nil)

	default: // send | approve | contract — all tx broadcasts
		req, err := e.txRequest()
		if err != nil {
			return fail(err)
		}
		// Verify the endpoint's live chain matches what was approved —
		// refuse rather than sign on the wrong network.
		liveChain, err := ChainID(ctx, provider)
		if err != nil {
			return fail(fmt.Errorf("chain id: %w", err))
		}
		if e.ChainID != 0 && liveChain != e.ChainID {
			return fail(fmt.Errorf("endpoint is on chain %d, request was approved for %d",
				liveChain, e.ChainID))
		}
		req.ChainID = liveChain
		if err := FillTxDefaults(ctx, provider, e.From, req); err != nil {
			return fail(fmt.Errorf("fill tx: %w", err))
		}
		raw, _, err := SignTx(req, priv)
		if err != nil {
			return fail(err)
		}
		txHash, err := SendRawTransaction(ctx, provider, raw)
		if err != nil {
			return fail(fmt.Errorf("broadcast: %w", err))
		}
		if ledger != nil {
			_ = ledger.Record(SpendEntry{
				Kind:     string(e.Kind),
				From:     e.From,
				To:       e.To,
				ValueWei: e.ValueWei,
				TxHash:   txHash,
			})
		}
		return s.Complete(id, txHash, "", nil)
	}
}

// txRequest reconstructs the unsigned transaction from entry fields.
func (e *PendingEntry) txRequest() (*TxRequest, error) {
	req := &TxRequest{ChainID: e.ChainID, To: e.To}
	if e.ValueWei != "" {
		v, ok := new(big.Int).SetString(e.ValueWei, 10)
		if !ok {
			return nil, fmt.Errorf("bad value_wei %q", e.ValueWei)
		}
		req.ValueWei = v
	}
	data, err := ParseHexBytes(e.Data)
	if err != nil {
		return nil, err
	}
	req.Data = data
	return req, nil
}

// newID returns a fresh 16-hex-char id not already in the queue.
func (s *PendingStore) newID(f *pendingFile) (string, error) {
	for range 8 {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("id rand: %w", err)
		}
		id := hex.EncodeToString(b)
		if findPending(f, id) == nil {
			return id, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique pending id")
}

// sweepLocked marks expired pending entries; returns whether it changed
// anything.
func (s *PendingStore) sweepLocked(f *pendingFile) bool {
	now := s.clock()
	changed := false
	for _, e := range f.Entries {
		if e.Status == StatusPending && now.After(e.ExpiresAt) {
			e.Status = StatusExpired
			changed = true
		}
	}
	return changed
}

// boundLocked enforces the queue cap: terminal entries are dropped
// oldest-first; if everything is live the submit is refused.
func (s *PendingStore) boundLocked(f *pendingFile) error {
	if len(f.Entries) <= pendingMaxQueued {
		return nil
	}
	sort.SliceStable(f.Entries, func(i, j int) bool {
		return f.Entries[i].CreatedAt.Before(f.Entries[j].CreatedAt)
	})
	for len(f.Entries) > pendingMaxQueued {
		if !f.Entries[0].Status.IsTerminal() {
			return fmt.Errorf("pending queue is full (%d live requests)", pendingMaxQueued)
		}
		f.Entries = f.Entries[1:]
	}
	return nil
}

func findPending(f *pendingFile, id string) *PendingEntry {
	for _, e := range f.Entries {
		if e.ID == id {
			return e
		}
	}
	return nil
}

func (s *PendingStore) read() (*pendingFile, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &pendingFile{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending queue: %w", err)
	}
	var f pendingFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse pending queue: %w", err)
	}
	return &f, nil
}

func (s *PendingStore) write(f *pendingFile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), walletDirPerms); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.path, data, walletFilePerms)
}
