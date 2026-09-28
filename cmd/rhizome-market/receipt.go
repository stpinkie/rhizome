// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	shared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// receipt is the signed escrow-release payload Track 102 mints per
// completed session — the buyer presents it to justify release(), the
// seller keeps it as the claim-path record. Canonical sign bytes are the
// JSON of this struct with Signature empty: Go marshals struct fields in
// declaration order, so the bytes are deterministic across processes.
type receipt struct {
	V            int                 `json:"v"`
	SessionID    string              `json:"session_id"`
	OfferID      string              `json:"offer_id"`
	SellerPeerID string              `json:"seller_peer_id"`
	Buyer        string              `json:"buyer"`
	DurationMS   int64               `json:"duration_ms"`
	Usage        *shared.RemoteUsage `json:"usage,omitempty"`
	ResultSHA256 string              `json:"result_sha256"`
	Terms        receiptTerms        `json:"terms"`
	Settlement   string              `json:"settlement"` // "configured" | "fixture"
	Interrupted  bool                `json:"interrupted,omitempty"`
	IssuedAt     string              `json:"issued_at"`
	Signature    string              `json:"signature"` // hex ed25519 over the canonical bytes
}

type receiptTerms struct {
	Price   string `json:"price"` // base units, decimal string
	Asset   string `json:"asset"` // offer price_sheet.asset (label; token addr in session)
	ChainID int64  `json:"chain_id"`
}

// signBytes returns the canonical bytes the signature covers — the
// receipt with Signature cleared. Deterministic: struct field order is
// fixed, map keys do not appear in this shape.
func (r receipt) signBytes() []byte {
	r.Signature = ""
	data, _ := json.Marshal(r)
	return data
}

// mintReceipt builds and signs the session's receipt, then persists it
// under <module_dir>/receipts/. A missing identity yields an unsigned
// receipt — honest: the field is empty, the session record still exists.
func (m *sessionMgr) mintReceipt(s *marketSession) *receipt {
	s.mu.Lock()
	result := s.result.String()
	usage := s.usage
	failed := s.State == sessionFailed
	s.mu.Unlock()

	sum := sha256.Sum256([]byte(result))
	mc := m.cfg.Load()
	r := &receipt{
		V:            1,
		SessionID:    s.ID,
		OfferID:      s.Offer.ID,
		Buyer:        s.Terms.Buyer,
		DurationMS:   s.durationMS(),
		Usage:        usage,
		ResultSHA256: "0x" + hex.EncodeToString(sum[:]),
		Settlement:   "fixture",
		Interrupted:  failed,
		IssuedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if mc != nil && mc.rail != nil {
		r.Settlement = "configured"
	}
	r.Terms = receiptTerms{
		Price:   amountString(s.Terms.Amount),
		Asset:   s.Offer.PriceSheet.Asset,
		ChainID: s.Offer.PriceSheet.ChainID,
	}
	if id := m.ident.Load(); id != nil {
		r.SellerPeerID = id.PeerID
		r.Signature = hex.EncodeToString(identity.Sign(id.PrivateKey, r.signBytes()))
	}
	if err := m.persistReceipt(r); err != nil {
		m.audit.log("market.receipt.persist_failed", map[string]any{
			"session_id": s.ID, "error": err.Error(),
		})
	}
	m.audit.log("market.receipt.mint", map[string]any{
		"session_id": s.ID,
		"signed":     r.Signature != "",
		"result_sha": r.ResultSHA256,
	})
	return r
}

func amountString(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}

// persistReceipt writes receipts/<session_id>.json atomically at 0600.
func (m *sessionMgr) persistReceipt(r *receipt) error {
	dir := filepath.Join(m.moduleDir, receiptDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(receiptPath(m.moduleDir, r.SessionID), data)
}

// receiptPath maps a session id to its stored receipt file.
func receiptPath(moduleDir, sessionID string) string {
	return filepath.Join(
		moduleDir, receiptDirName, sanitizeSessionID(sessionID)+".json")
}

// loadReceipt reads a stored receipt — used by /v1/receipt and _rhizome.receipt.
func loadReceipt(moduleDir, sessionID string) (*receipt, error) {
	data, err := readBounded(receiptPath(moduleDir, sessionID), 64<<10)
	if err != nil {
		return nil, err
	}
	var r receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("receipt parse: %w", err)
	}
	return &r, nil
}

// VerifyReceipt checks a stored/minted receipt's signature against the
// seller's peer id — the same check the buyer runs before release().
// Returns (false, nil) for an unsigned or mismatched signature; errors are
// only for malformed input.
func VerifyReceipt(r *receipt) (bool, error) {
	if r.Signature == "" || r.SellerPeerID == "" {
		return false, nil
	}
	pid, err := peer.Decode(r.SellerPeerID)
	if err != nil {
		return false, fmt.Errorf("seller peer id: %w", err)
	}
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return false, fmt.Errorf("seller peer id carries no inline key: %w", err)
	}
	raw, err := pub.Raw()
	if err != nil {
		return false, err
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(r.Signature, "0x"))
	if err != nil {
		return false, fmt.Errorf("signature hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return false, fmt.Errorf("seller key is not ed25519")
	}
	ok := identity.Verify(ed25519.PublicKey(raw), r.signBytes(), sig)
	return ok, nil
}
