package econ

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

// SettleOffer is the signed settlement proposal a payer hands the payee
// over the /rhizome/econ/1.0.0 handshake (Track 142). It commits the
// payer's accrued payable entries for one peer/unit to a single summed
// total; the payee verifies the signature and compares its own receivable
// view before accepting. `rhizome mesh economy invoice` emits this exact
// document as a dry-run — same shape, same signing rules, no handshake.
type SettleOffer struct {
	// Protocol is the handshake version tag ("econ/1.0.0").
	Protocol string `json:"protocol"`
	// Issuer is the payer's peer id (signer); PeerID is the payee.
	Issuer string `json:"issuer"`
	PeerID string `json:"peer_id"`
	Unit   string `json:"unit"`
	// Entries lists the covered work refs — task ids preferred,
	// correlation ids for synchronous calls — since the payee's ledger
	// holds its own receivable entry ids. The payee matches refs against
	// its receivable rows before accepting.
	Entries []string `json:"entries"`
	// Total is the canonical-decimal sum of the covered amounts.
	Total string `json:"total"`
	// Cutoff binds the offer to accrued entries at-or-before this time,
	// so races against new accruals are unambiguous.
	Cutoff    time.Time `json:"cutoff_ts"`
	Nonce     string    `json:"nonce"`
	TS        time.Time `json:"ts"`
	Signature []byte    `json:"sig,omitempty"`
}

// BuildOffer constructs an unsigned offer over the accrued payable
// entries for peer/unit at-or-before cutoff. Returns nil when nothing is
// owed. Entries are carried newest-first (descending ts) so the doc lists
// the most recent charges first.
func BuildOffer(issuer, peerID, unit string, entries []Entry, cutoff time.Time) (*SettleOffer, error) {
	if issuer == "" || peerID == "" || unit == "" {
		return nil, fmt.Errorf("settle offer requires issuer, peer and unit")
	}
	total := new(big.Rat)
	refs := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.PeerID != peerID || e.Unit != unit ||
			e.Direction != DirectionPayable || e.State != StateAccrued ||
			e.TS.After(cutoff) {
			continue
		}
		amt, ok := parseDecimal(e.Amount)
		if !ok {
			return nil, fmt.Errorf("entry %s has unparseable amount %q", e.EntryID, e.Amount)
		}
		total.Add(total, amt)
		refs = append(refs, e.workRef())
	}
	if len(refs) == 0 {
		return nil, nil
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("mint offer nonce: %w", err)
	}
	return &SettleOffer{
		Protocol: "econ/1.0.0",
		Issuer:   issuer,
		PeerID:   peerID,
		Unit:     unit,
		Entries:  refs,
		Total:    decimalString(total),
		Cutoff:   cutoff.UTC(),
		Nonce:    hex.EncodeToString(nonce[:]),
		TS:       time.Now().UTC(),
	}, nil
}

// Sign signs the offer with the payer's identity key — the signature
// covers the canonical marshaled doc with the signature field cleared.
func (o *SettleOffer) Sign(priv ed25519.PrivateKey) error {
	o.Signature = nil
	payload, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("encode settle offer: %w", err)
	}
	o.Signature = identity.Sign(priv, payload)
	return nil
}

// Verify checks the offer signature against the issuer's public key —
// peer pubkeys verify Ed25519 signatures over the same canonical payload.
func (o *SettleOffer) Verify(pub crypto.PubKey) error {
	if len(o.Signature) == 0 {
		return fmt.Errorf("settle offer missing signature")
	}
	sig := o.Signature
	o.Signature = nil
	defer func() { o.Signature = sig }()
	payload, err := json.Marshal(o)
	if err != nil {
		return fmt.Errorf("encode settle offer: %w", err)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify settle offer: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid settle offer signature")
	}
	return nil
}

// ID is the offer's content address — sha256 over the unsigned doc,
// hex-prefixed like result hashes. Ledger settle transitions reference it
// as settle_id.
func (o *SettleOffer) ID() string {
	sig := o.Signature
	o.Signature = nil
	defer func() { o.Signature = sig }()
	payload, _ := json.Marshal(o) // marshaled doc is fixed-shape
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MarkOnlySettleTX is the settle_tx sentinel on locally-attested settle
// markers — written by `economy settle --mark-only` instead of an
// on-chain transaction or handshake ack. Peers reading the entry see the
// mark is local-only (the documented divergence-risk fallback).
const MarkOnlySettleTX = "mark-local"

// MarkSettled signs an offer over the issuer's accrued payable entries for
// peer/unit and transitions them to settled with the offer id — the
// shared core of `economy settle --mark-only`, callable daemonless (CLI)
// or through the mesh (which additionally publishes mesh.econ.settle).
// The payee is not contacted: the marker attests the operator settled out
// of band, and the peer's receivable side diverges until it settles on
// its own view or the Track 142 handshake closes the loop.
func MarkSettled(l *Ledger, issuer, peerID, unit string, priv ed25519.PrivateKey) (*SettleOffer, []Entry, error) {
	if l == nil {
		return nil, nil, fmt.Errorf("economy ledger unavailable")
	}
	entries := l.Entries()
	if unit == "" {
		units := map[string]bool{}
		for _, e := range entries {
			if e.PeerID == peerID && e.Direction == DirectionPayable &&
				e.State == StateAccrued {
				units[e.Unit] = true
			}
		}
		switch {
		case len(units) > 1:
			list := make([]string, 0, len(units))
			for u := range units {
				list = append(list, u)
			}
			sort.Strings(list)
			return nil, nil, fmt.Errorf(
				"peer %s owes in multiple units %v — settle one unit at a time", peerID, list)
		case len(units) == 1:
			for u := range units {
				unit = u
			}
		}
	}
	if unit == "" {
		return nil, nil, fmt.Errorf("no accrued payable balance for peer %s", peerID)
	}
	cutoff := time.Now().UTC()
	offer, err := BuildOffer(issuer, peerID, unit, entries, cutoff)
	if err != nil {
		return nil, nil, err
	}
	if offer == nil {
		return nil, nil, fmt.Errorf("no accrued payable balance for peer %s", peerID)
	}
	if priv != nil {
		if err := offer.Sign(priv); err != nil {
			return nil, nil, err
		}
	}
	settleID := offer.ID()
	out := make([]Entry, 0, len(offer.Entries))
	for _, e := range entries {
		if e.PeerID != peerID || e.Unit != unit ||
			e.Direction != DirectionPayable || e.State != StateAccrued ||
			e.TS.After(cutoff) {
			continue
		}
		rec, err := l.Transition(e.EntryID, StateSettled, settleID, MarkOnlySettleTX, "")
		if err != nil {
			return nil, out, err
		}
		out = append(out, *rec)
	}
	return offer, out, nil
}

// DisputeTask moves every accrued entry matching ref (task id or
// correlation id) to disputed with the operator's reason attached. Shared
// core of `economy dispute` — callable daemonless or through the mesh
// (which additionally publishes mesh.econ.dispute).
func DisputeTask(l *Ledger, ref, reason string) ([]Entry, error) {
	if l == nil {
		return nil, fmt.Errorf("economy ledger unavailable")
	}
	var out []Entry
	for _, e := range l.Entries() {
		if e.TaskID != ref && e.CorrelationID != ref {
			continue
		}
		if e.State != StateAccrued {
			continue
		}
		rec, err := l.Transition(e.EntryID, StateDisputed, "", "", reason)
		if err != nil {
			return out, err
		}
		out = append(out, *rec)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no accrued ledger entries for task %s", ref)
	}
	return out, nil
}

// ResolveTask closes a dispute: credit re-accrues the entries (the charge
// stands), drop writes them off. Written-off entries stay in the file —
// the trail is append-only — they just stop counting. Shared core of
// `economy resolve`.
func ResolveTask(l *Ledger, ref string, drop bool) ([]Entry, error) {
	if l == nil {
		return nil, fmt.Errorf("economy ledger unavailable")
	}
	to := StateAccrued
	action := "credit"
	if drop {
		to = StateWrittenOff
		action = "drop"
	}
	var out []Entry
	for _, e := range l.Entries() {
		if e.TaskID != ref && e.CorrelationID != ref {
			continue
		}
		if e.State != StateDisputed {
			continue
		}
		rec, err := l.Transition(e.EntryID, to, "", "", "resolve "+action)
		if err != nil {
			return out, err
		}
		out = append(out, *rec)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no disputed ledger entries for task %s", ref)
	}
	return out, nil
}
