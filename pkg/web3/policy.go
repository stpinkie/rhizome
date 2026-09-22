// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SigningPolicy is the evaluated form of tools.web3.signing. Every field
// is an independent bound; a request must pass ALL of them.
//
// Allowlist semantics:
//   - FromAddresses: EMPTY = deny all signing (operators must enumerate
//     signer addresses — this is what stops an agent from draining an
//     imported cold key by naming it `from`).
//   - AllowContracts: empty = any contract target; non-empty = the `to`
//     address must be listed (plain EOA sends are unaffected — contract
//     restriction applies only when calldata is present).
//   - AllowMethods: empty = any method; non-empty = the calldata selector
//     must match a listed "0x" selector or a "label:method" entry
//     resolved through the ABI registry (Track 78).
//   - ChainIDs: empty = any chain.
type SigningPolicy struct {
	Enabled           bool
	FromAddresses     []string
	AllowContracts    []string
	AllowMethods      []string
	MaxValueWeiPerTx  *big.Int
	MaxValueWeiPerDay *big.Int
	ChainIDs          []uint64
	// Registry resolves "label" / "label:method" / "0xaddr:method" entries
	// in AllowContracts/AllowMethods at eval time. nil = raw entries only.
	Registry *ABIRegistry `json:"-"`
}

// SignRequest is the policy-evaluated view of a signing operation.
type SignRequest struct {
	Kind     string   // "send" | "sign" | "approve" | "contract"
	ChainID  uint64   // 0 = not yet known; skips the chain check
	From     string   // 0x address
	To       string   // 0x address; "" for sign-kind
	ValueWei *big.Int // nil → 0
	Data     []byte   // calldata; selector is Data[:4]
}

// SelectorHex returns the 0x-prefixed 4-byte calldata selector, or "".
func (r *SignRequest) SelectorHex() string {
	if len(r.Data) < 4 {
		return ""
	}
	return "0x" + hex.EncodeToString(r.Data[:4])
}

// PolicyDecision is the allow/deny outcome with a human reason.
type PolicyDecision struct {
	Allowed bool
	Reason  string
}

func deny(format string, args ...any) *PolicyDecision {
	return &PolicyDecision{Allowed: false, Reason: fmt.Sprintf(format, args...)}
}

var allowDecision = &PolicyDecision{Allowed: true}

// Evaluate checks req against the policy. ledger may be nil when
// MaxValueWeiPerDay is unset.
func (p *SigningPolicy) Evaluate(req *SignRequest, ledger *SpendLedger, now time.Time) *PolicyDecision {
	if p == nil || !p.Enabled {
		return deny("web3 signing is disabled (tools.web3.signing.enabled)")
	}
	if !IsAddress(req.From) {
		return deny("from %q is not a valid address", req.From)
	}
	if len(p.FromAddresses) == 0 {
		return deny("no signer addresses configured — set tools.web3.signing.from_addresses")
	}
	if !containsFold(p.FromAddresses, req.From) {
		return deny("from %s is not in tools.web3.signing.from_addresses", req.From)
	}
	if len(p.ChainIDs) > 0 && req.ChainID != 0 {
		ok := false
		for _, id := range p.ChainIDs {
			if id == req.ChainID {
				ok = true
				break
			}
		}
		if !ok {
			return deny("chain %d not in signing chain_ids %v", req.ChainID, p.ChainIDs)
		}
	}
	if len(req.Data) >= 4 && len(p.AllowContracts) > 0 && !p.contractAllowed(req.To) {
		return deny("contract %s is not in allow_contracts", req.To)
	}
	if len(req.Data) >= 4 && len(p.AllowMethods) > 0 && !p.methodAllowed(req) {
		return deny("method selector %s is not in allow_methods", req.SelectorHex())
	}
	value := req.ValueWei
	if value == nil {
		value = new(big.Int)
	}
	if p.MaxValueWeiPerTx != nil && value.Cmp(p.MaxValueWeiPerTx) > 0 {
		return deny("value %s wei exceeds per-tx cap %s", value, p.MaxValueWeiPerTx)
	}
	if p.MaxValueWeiPerDay != nil && ledger != nil && req.From != "" {
		spent, err := ledger.SpendSince(req.From, now.Truncate(24*time.Hour))
		if err != nil {
			return deny("daily spend check failed: %v", err)
		}
		if new(big.Int).Add(spent, value).Cmp(p.MaxValueWeiPerDay) > 0 {
			return deny("value %s wei would exceed daily cap %s (spent %s today)",
				value, p.MaxValueWeiPerDay, spent)
		}
	}
	return allowDecision
}

func containsFold(list []string, v string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), v) {
			return true
		}
	}
	return false
}

// contractAllowed matches `to` against AllowContracts — entries may be
// addresses or registry labels (normalized to their bound address).
func (p *SigningPolicy) contractAllowed(to string) bool {
	for _, item := range p.AllowContracts {
		item = strings.TrimSpace(item)
		if strings.EqualFold(item, to) {
			return true
		}
		if p.Registry == nil || IsAddress(item) {
			continue
		}
		if e, err := p.Registry.Get(item); err == nil &&
			e.Address != "" && strings.EqualFold(e.Address, to) {
			return true
		}
	}
	return false
}

// methodAllowed matches the calldata selector against AllowMethods —
// entries may be raw "0x…" selectors or "<label-or-addr>:<method-name>"
// resolved to a selector through the ABI registry.
func (p *SigningPolicy) methodAllowed(req *SignRequest) bool {
	sel := req.SelectorHex()
	for _, item := range p.AllowMethods {
		item = strings.TrimSpace(item)
		if strings.EqualFold(item, sel) {
			return true
		}
		name, method, ok := strings.Cut(item, ":")
		if !ok || p.Registry == nil {
			continue
		}
		var entry *ABIEntry
		var err error
		if IsAddress(name) {
			if !strings.EqualFold(name, req.To) {
				continue // addr:method only applies to that contract
			}
			entry, err = p.Registry.ByAddress(name)
		} else {
			entry, err = p.Registry.Get(name)
			// A bound label scopes the method allowance to that contract —
			// "usdc:transfer" must not bless transfer() on other addresses.
			if err == nil && entry != nil && entry.Address != "" &&
				!strings.EqualFold(entry.Address, req.To) {
				continue
			}
		}
		if err != nil || entry == nil || entry.ABI == nil {
			continue
		}
		for _, cand := range entry.ABI.Methods {
			if cand.Name == method &&
				strings.EqualFold("0x"+hex.EncodeToString(cand.Selector()), sel) {
				return true
			}
		}
	}
	return false
}

// SpendEntry is one line of the append-only spend ledger.
type SpendEntry struct {
	TS       time.Time `json:"ts"`
	Kind     string    `json:"kind"` // "send"|"contract"|"approve"
	From     string    `json:"from"`
	To       string    `json:"to,omitempty"`
	ValueWei string    `json:"value_wei"` // decimal
	TxHash   string    `json:"tx_hash,omitempty"`
}

const (
	ledgerFileName   = "web3-ledger.jsonl"
	ledgerMaxBytes   = 10 << 20 // 10 MiB per file
	ledgerMaxRotated = 3        // keep .1 .2 .3
)

// SpendLedger is the append-only record of broadcast sends used for
// daily-cap enforcement and operator audit. Rotates at ~10 MiB keeping 3
// generations; SpendSince scans the active file plus the newest rotation
// so a mid-day rotation cannot launder spend.
type SpendLedger struct {
	path string
	mu   sync.Mutex
}

// OpenSpendLedger returns the ledger rooted at dir (<RHIZOME_HOME>/web3).
func OpenSpendLedger(dir string) *SpendLedger {
	return &SpendLedger{path: filepath.Join(dir, ledgerFileName)}
}

// Path exposes the ledger file path for docs/debugging.
func (l *SpendLedger) Path() string { return l.path }

// Record appends one entry, rotating the file first if it has grown past
// the cap.
func (l *SpendLedger) Record(e SpendEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(l.path), walletDirPerms); err != nil {
		return err
	}
	if st, err := os.Stat(l.path); err == nil && st.Size() >= ledgerMaxBytes {
		if err := l.rotate(); err != nil {
			return fmt.Errorf("rotate spend ledger: %w", err)
		}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, walletFilePerms)
	if err != nil {
		return fmt.Errorf("open spend ledger: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append spend ledger: %w", err)
	}
	return f.Sync()
}

// rotate shifts path → .1 → .2 → .3 (oldest dropped).
func (l *SpendLedger) rotate() error {
	for i := ledgerMaxRotated - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		dst := fmt.Sprintf("%s.%d", l.path, i+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SpendSince sums value_wei for entries from `from` at or after `since`.
// Scans the active file and the newest rotation so daily caps stay sound
// across a mid-period rotate.
func (l *SpendLedger) SpendSince(from string, since time.Time) (*big.Int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	total := new(big.Int)
	for _, path := range []string{l.path, l.path + ".1"} {
		if err := l.scan(path, from, since, total); err != nil {
			return nil, err
		}
	}
	return total, nil
}

func (l *SpendLedger) scan(path, from string, since time.Time, total *big.Int) error {
	//nolint:gosec // G304: path is derived from the configured ledger path.
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open spend ledger: %w", err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e SpendEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue // skip malformed lines — a bad line must not lift the cap
		}
		if e.TS.Before(since) || !strings.EqualFold(e.From, from) {
			continue
		}
		v, ok := new(big.Int).SetString(e.ValueWei, 10)
		if ok {
			total.Add(total, v)
		}
	}
	return sc.Err()
}
