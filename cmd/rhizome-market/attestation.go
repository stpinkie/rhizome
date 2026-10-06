// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

// Portable completion attestations (Track 130) — a buyer-signed claim
// the seller carries across buyers and meshes. The schema is the
// design's: provider + session + outcome + terms/task hashes, issued by
// the buyer's node key. Sign bytes are the canonical JSON with
// Signature empty — same convention as _rhizome.receipt.
//
// Honest limits (docs/guides/market.md): attestations are additive
// evidence. A seller can withhold negatives and a buyer can refuse to
// issue — they raise confidence in a provider, they are not a ledger.
// Issuing is strictly opt-in (`market attest`), never automatic.

// attestation is the signed portable-reputation payload.
type attestation struct {
	V              int    `json:"v"`
	ProviderPeerID string `json:"provider_peer_id"`
	SessionID      string `json:"session_id"`
	Outcome        string `json:"outcome"` // purchase-terminal label
	TermsHash      string `json:"terms_hash"`
	TaskHash       string `json:"task_hash"`
	IssuedAt       string `json:"issued_at"`
	BuyerPeerID    string `json:"buyer_peer_id"`
	Signature      string `json:"signature"` // hex ed25519 over canonical bytes
}

// signBytes returns the canonical bytes the signature covers.
func (a attestation) signBytes() []byte {
	a.Signature = ""
	data, _ := json.Marshal(a)
	return data
}

// attestableStates are purchase terminals a buyer may attest — the
// outcome label rides through verbatim. Disputed sessions stay
// un-attestable until the arbiter rules (resolved_* lands then).
var attestableStates = map[string]bool{
	purchaseCompleted: true,
	purchaseResolved:  true,
	purchaseRefunded:  true,
	purchaseFailed:    true,
}

// mintAttestation issues a signed attestation for a terminal purchase —
// buyer-side only (the signature is the buyer's node key). Unsigned
// attestations are refused: an unattributed claim is worthless and the
// schema's whole point is the sig→peer chain.
func (pm *purchaseMgr) mintAttestation(p *purchase, id *identity.Derived) (*attestation, error) {
	if id == nil || id.PeerID == "" {
		return nil, fmt.Errorf("no local identity — attestations need a buyer node key")
	}
	if !attestableStates[p.State] {
		return nil, fmt.Errorf(
			"purchase %s is %s — attest on a terminal state (completed/resolved/refunded/failed)",
			p.PurchaseID, p.State)
	}
	sellerPeer := p.SellerPeerID
	if sellerPeer == "" {
		return nil, fmt.Errorf(
			"purchase %s has no seller peer id — cannot bind provider_peer_id", p.PurchaseID)
	}
	a := &attestation{
		V:              1,
		ProviderPeerID: sellerPeer,
		SessionID:      p.SessionID,
		Outcome:        p.State,
		TermsHash: termsHash(
			p.SessionID, p.Terms.Token, p.Terms.Amount, p.TaskHash),
		TaskHash:    p.TaskHash,
		IssuedAt:    pm.nowFn().UTC().Format(time.RFC3339),
		BuyerPeerID: id.PeerID,
	}
	a.Signature = hex.EncodeToString(identity.Sign(id.PrivateKey, a.signBytes()))
	return a, nil
}

// VerifyAttestation checks the signature → buyer peer id chain: the
// signature must verify against the Ed25519 key embedded in
// buyer_peer_id over the canonical bytes. Returns (false, nil) for an
// unsigned/bad signature; errors are malformed input only.
func VerifyAttestation(a *attestation) (bool, error) {
	if a == nil || a.Signature == "" || a.BuyerPeerID == "" {
		return false, nil
	}
	pid, err := peer.Decode(a.BuyerPeerID)
	if err != nil {
		return false, fmt.Errorf("buyer peer id: %w", err)
	}
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return false, fmt.Errorf("buyer peer id carries no inline key: %w", err)
	}
	raw, err := pub.Raw()
	if err != nil {
		return false, err
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(a.Signature, "0x"))
	if err != nil {
		return false, fmt.Errorf("signature hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return false, fmt.Errorf("buyer key is not ed25519")
	}
	return identity.Verify(ed25519.PublicKey(raw), a.signBytes(), sig), nil
}

// attestationStore is the seller's received-attestation ledger:
// newest-N bound, deduped by (session_id, buyer_peer_id), JSONL-free —
// the file rewrites whole since 32 entries is tiny.
const (
	attestationFile     = "attestations.json"
	attestationMaxLive  = 32 // newest-N the advert carries
	attestationBoundLen = 64 // stored bound — older entries drop on rewrite
)

type attestationStore struct {
	mu    sync.Mutex
	path  string
	items []attestation // newest-first
}

func openAttestationStore(moduleDir string) *attestationStore {
	s := &attestationStore{path: filepath.Join(moduleDir, attestationFile)}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	_ = json.Unmarshal(data, &s.items) // corrupt → start fresh; entries re-register
	return s
}

// register validates and stores an attestation; duplicates by
// (session_id, buyer_peer_id) replace the older entry in place.
func (s *attestationStore) register(a attestation) error {
	ok, err := VerifyAttestation(&a)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("attestation signature does not verify")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.items {
		if x.SessionID == a.SessionID && x.BuyerPeerID == a.BuyerPeerID {
			s.items = append(s.items[:i], s.items[i+1:]...)
			break
		}
	}
	s.items = append([]attestation{a}, s.items...)
	if len(s.items) > attestationBoundLen {
		s.items = s.items[:attestationBoundLen]
	}
	return s.save()
}

func (s *attestationStore) save() error {
	data, err := json.MarshalIndent(s.items, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// latest returns the newest n for the advert — copies, not the live
// slice; nil when empty so omitempty keeps the field out entirely.
func (s *attestationStore) latest(n int) []attestation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) < n {
		n = len(s.items)
	}
	if n == 0 {
		return nil
	}
	out := make([]attestation, n)
	copy(out, s.items[:n])
	return out
}

// attest issues a signed attestation for a terminal purchase — strictly
// opt-in. The minted claim is recorded on the purchase and reported to
// the mesh score store as an `attested` outcome (additive evidence).
func (pm *purchaseMgr) attest(id string) (*attestation, error) {
	p := pm.lookupAny(id)
	if p == nil {
		return nil, buyErr("not_found", "no purchase %q", id)
	}
	a, err := pm.mintAttestation(p, pm.ident.Load())
	if err != nil {
		return nil, buyErr("bad_state", "%s", err.Error())
	}
	pm.mutate(p, func(pp *purchase) { pp.Attestation = a })
	// Issuance lands on the seller's peer record — additive evidence,
	// neutral for counters (the score vocabulary treats it as ref-only).
	vh := valueHash(p.Terms.Amount, p.Terms.Token, p.TaskHash)
	go func(peer, sid string) {
		if err := reportOutcome(peer, "market_buy", "attested", sid, vh); err != nil && pm.audit != nil {
			pm.audit.log("market.peer_score.failed", map[string]any{
				"purchase_id": p.PurchaseID, "peer": peer,
				"outcome": "attested", "error": err.Error(),
			})
		}
	}(p.SellerPeerID, p.SessionID)
	pm.audit.log("market.attestation.mint", map[string]any{
		"purchase_id": p.PurchaseID, "session_id": p.SessionID,
		"outcome": a.Outcome,
	})
	return a, nil
}

// attestationCheck reports which verification legs ran — sig chain
// always; terms_hash cross-check only when a local record carries the
// session facts (the verifier without records still gets a sig verdict).
type attestationCheck struct {
	Verified    bool   `json:"verified"`
	SignatureOK bool   `json:"signature_ok"`
	TermsMatch  *bool  `json:"terms_match,omitempty"` // nil = no local record to check
	Reason      string `json:"reason,omitempty"`
}

// verifyAttestation runs the sig → peer → terms chain. Cross-checks the
// terms hash against the local purchase record when the session id is
// known locally — foreign attestations verify signature-only honestly.
func (pm *purchaseMgr) verifyAttestation(raw json.RawMessage) (*attestationCheck, error) {
	var a attestation
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, buyErr("bad_request", "attestation parse: %s", err)
	}
	ok, err := VerifyAttestation(&a)
	if err != nil {
		return nil, buyErr("bad_request", "%s", err)
	}
	chk := &attestationCheck{SignatureOK: ok, Verified: ok}
	if !ok {
		chk.Reason = "signature does not verify against buyer_peer_id"
		return chk, nil
	}
	// Local cross-check: if we hold a purchase for this session, the
	// terms hash must match — a forged-terms attestation fails here.
	if p := pm.bySessionOne(a.SessionID); p != nil {
		want := termsHash(p.SessionID, p.Terms.Token, p.Terms.Amount, p.TaskHash)
		match := strings.EqualFold(want, a.TermsHash)
		chk.TermsMatch = &match
		chk.Verified = chk.Verified && match
		if !match {
			chk.Reason = "terms_hash does not match the local session record"
		}
	}
	return chk, nil
}

// bySessionOne returns the first purchase matching a session id — the
// singular variant for lookups where duplicates can't matter.
func (pm *purchaseMgr) bySessionOne(sessionID string) *purchase {
	if l := pm.bySession(sessionID); len(l) > 0 {
		return l[0]
	}
	return nil
}
