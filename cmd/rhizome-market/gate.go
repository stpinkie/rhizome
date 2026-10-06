// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// sessionPresentation is the _rhizome.session_open payload: the buyer's
// proof that an escrow exists for this exact task. session_id is the
// escrow clone address (the on-chain session key); task_hash binds the
// session to sha256 of the prompt text the buyer committed on-chain;
// terms carry the chain facts the seller can't know otherwise (buyer
// identity, locked amount, token).
type sessionPresentation struct {
	SessionID string `json:"session_id"`
	TaskHash  string `json:"task_hash"`
	OfferID   string `json:"offer_id"`
	Buyer     string `json:"buyer"`
	Terms     struct {
		Amount  string `json:"amount"`   // token base units, decimal string
		Token   string `json:"token"`    // ERC-20 address (defaults to escrow_token)
		ChainID int64  `json:"chain_id"` // informational; verified via Terms
	} `json:"terms"`
}

// fixtureToken is the sentinel ERC-20 address used for fixture-posture
// escrows — the module only verifies locks it opened itself against its
// own MockRail, so the token only needs to be a well-formed constant.
const fixtureToken = "0x00000000000000000000000000000000000000ff"

// gateError is a session_open refusal with a stable machine-readable code
// for buyers to branch on (insufficient_funds vs terms_mismatch, etc).
type gateError struct {
	code string
	msg  string
}

func (e *gateError) Error() string { return e.msg }

func gateReject(code, format string, args ...any) *gateError {
	return &gateError{code: code, msg: fmt.Sprintf(format, args...)}
}

// gateSessionOpen validates the presentation and runs the escrow gate —
// VerifyLock is eth_call-only, so an unpaid prompt can never burn LLM
// budget: nothing spawns until a confirmed lock with matching terms
// exists on-chain. The caller (sessionMgr/adverts) supplies the current
// marketConfig + rail; mc is re-read here so a mid-flight config reload
// applies immediately.
func gateSessionOpen(
	ctx context.Context,
	m *sessionMgr,
	peer string,
	connID uint64,
	raw json.RawMessage,
) (*marketSession, error) {
	mc := m.cfg.Load()
	if mc == nil {
		return nil, gateReject("not_ready", "module config not loaded")
	}
	var p sessionPresentation
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, gateReject("bad_request", "session_open params are not a JSON object")
	}
	if !mc.serveEnabled {
		return nil, gateReject("serve_disabled", "serve_enabled is off on this seller")
	}
	// Per-peer open-rate limit — VerifyLock costs the seller's RPC quota,
	// so the cheapest gate runs before it.
	if !m.peerRateOK(peer) {
		return nil, gateReject("rate_limited",
			"session_open rate exceeded %d/min for this peer", peerOpenPerMinute)
	}
	if !web3.IsAddress(p.SessionID) {
		return nil, gateReject("bad_session_id",
			"session_id %q is not a 0x address (the escrow clone)", p.SessionID)
	}
	hashBytes, err := parseBytes32(p.TaskHash)
	if err != nil {
		return nil, gateReject("bad_task_hash", "task_hash: %s", err)
	}
	var off *offer
	for i := range mc.offers {
		if mc.offers[i].ID == p.OfferID {
			off = &mc.offers[i]
			break
		}
	}
	if off == nil {
		return nil, gateReject("unknown_offer", "offer %q is not served by this module", p.OfferID)
	}
	// per_task is the v1 settlement unit — the lock must equal it exactly
	// (Smart Invoice release() pays the whole milestone; no partials).
	if strings.TrimSpace(off.PriceSheet.PerTask) == "" {
		return nil, gateReject("offer_not_servable",
			"offer %q has no price_sheet.per_task — only fixed-price offers are servable in v1",
			p.OfferID)
	}
	if !web3.IsAddress(p.Buyer) {
		return nil, gateReject("bad_buyer", "buyer %q is not a 0x address", p.Buyer)
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(p.Terms.Amount), 10)
	if !ok || amount.Sign() <= 0 {
		return nil, gateReject("bad_amount", "terms.amount %q is not a positive integer", p.Terms.Amount)
	}
	rail := m.rail.Load()
	if rail == nil || *rail == nil {
		return nil, gateReject("rail_unavailable", "no settlement rail configured")
	}
	token := strings.TrimSpace(p.Terms.Token)
	if rc := mc.rail; rc != nil {
		if token != "" && !strings.EqualFold(token, rc.Token) {
			return nil, gateReject("terms_mismatch",
				"terms.token %q does not match the configured escrow_token %s", token, rc.Token)
		}
		if p.Terms.ChainID != 0 && p.Terms.ChainID != int64(rc.ChainID) {
			return nil, gateReject("terms_mismatch",
				"terms.chain_id %d does not match the configured escrow chain %d",
				p.Terms.ChainID, rc.ChainID)
		}
	}
	if token == "" {
		if mc.rail != nil {
			token = mc.rail.Token
		} else {
			token = fixtureToken
		}
	}
	// Expected lock = the offer's per_task price in token base units (the
	// same conversion the module's own buy path uses, so the fixture
	// self-loop stays consistent).
	expected, err := m.expectedAmount(ctx, off)
	if err != nil {
		return nil, gateReject("price_resolution", "cannot price offer %q: %s", off.ID, err)
	}
	if expected.Cmp(amount) != 0 {
		return nil, gateReject("terms_mismatch",
			"terms.amount %s does not equal offer %q per_task (%s base units)",
			amount, off.ID, expected)
	}
	terms := settlement.Terms{
		Buyer:    p.Buyer,
		Seller:   mc.payoutAddress,
		Token:    token,
		Amount:   amount,
		TaskHash: hashBytes,
	}
	ok2, err := (*rail).VerifyLock(ctx, p.SessionID, terms)
	if err != nil {
		m.audit.log("market.gate.error", map[string]any{
			"peer": peer, "session_id": p.SessionID, "error": err.Error(),
		})
		return nil, gateReject("verify_failed", "escrow verification failed: %s", err)
	}
	if !ok2 {
		return nil, gateReject("terms_mismatch",
			"escrow %s does not hold a confirmed lock matching this offer's terms", p.SessionID)
	}
	s := &marketSession{
		ID:       strings.ToLower(p.SessionID),
		Peer:     peer,
		ConnID:   connID,
		Offer:    *off,
		Terms:    terms,
		State:    sessionOpen,
		OpenedAt: time.Now(),
		taskHash: hashBytes,
	}
	if err := m.register(s); err != nil {
		return nil, gateReject("cap_reached", "%s", err)
	}
	m.audit.log("market.gate.open", map[string]any{
		"peer":       peer,
		"session_id": s.ID,
		"offer_id":   off.ID,
		"amount":     amount.String(),
	})
	return s, nil
}

// expectedAmount converts the offer's per_task decimal price into token
// base units via the token's decimals.
func (m *sessionMgr) expectedAmount(ctx context.Context, off *offer) (*big.Int, error) {
	price, ok := new(big.Rat).SetString(strings.TrimSpace(off.PriceSheet.PerTask))
	if !ok || price.Sign() <= 0 {
		return nil, fmt.Errorf("per_task %q is not a positive decimal", off.PriceSheet.PerTask)
	}
	decimals, err := m.tokenDecimals(ctx)
	if err != nil {
		return nil, err
	}
	// base = price * 10^decimals, integer floor is honest (no overpay).
	mul := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	base := new(big.Rat).Mul(price, new(big.Rat).SetInt(mul))
	if !base.IsInt() {
		return nil, fmt.Errorf(
			"per_task %q is not expressible in base units at %d decimals",
			off.PriceSheet.PerTask, decimals)
	}
	return base.Num(), nil
}

// tokenDecimals resolves the payment token's decimals — RPC rail reads
// decimals() on-chain; the fixture posture uses 6 (the module only ever
// verifies escrows it opened itself with the same conversion).
func (m *sessionMgr) tokenDecimals(ctx context.Context) (uint8, error) {
	rail := m.rail.Load()
	if rail == nil || *rail == nil {
		return 0, fmt.Errorf("no rail")
	}
	switch r := (*rail).(type) {
	case *settlement.RPCRail:
		return r.TokenDecimals(ctx)
	case *settlement.GraduatedRail:
		return r.TokenDecimals(ctx)
	}
	return 6, nil
}

// parseBytes32 parses a 0x-prefixed 32-byte hex string.
func parseBytes32(s string) ([32]byte, error) {
	var out [32]byte
	v := strings.TrimSpace(strings.TrimPrefix(s, "0x"))
	if len(v) != 64 {
		return out, fmt.Errorf("not a 32-byte hex string (%d hex chars)", len(v))
	}
	raw, err := hex.DecodeString(v)
	if err != nil {
		return out, err
	}
	copy(out[:], raw)
	return out, nil
}
