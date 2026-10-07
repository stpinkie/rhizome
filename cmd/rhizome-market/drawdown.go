// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

// Drawdown settlement (Track 128): instead of one escrow per task, the
// buyer funds a session-keyed budget on the graduated rail and each
// task's verified receipt draws it down via release(sessionId, amount).
// The ledger is the buyer-side bookkeeper: headroom accounting, draw
// pacing (draw_interval), and the session-close sweep that refunds the
// unspent remainder (drawdown withdraw fires at the deadline — the
// contract disables seller claim on budget sessions).
//
// Rejected alternative per the design: cumulative off-chain vouchers
// (seller claims the latest signed balance once) — the extra signature-
// verification path isn't justified on cheap L2s.

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/settlement"
)

const (
	drawdownDirName    = "drawdowns"
	drawdownMaxRecords = 128
	// acquireHeadroomMargin is the minimum seconds left on a session's
	// deadline before it stops accepting new draws — a draw racing the
	// deadline can't dispute or settle cleanly.
	acquireDeadlineMargin = int64(120)
)

// drawdownSession is the durable record of one funded budget session.
type drawdownSession struct {
	V             int            `json:"v"`
	SessionID     string         `json:"session_id"`     // on-chain bytes32 session key
	CorrelationID string         `json:"correlation_id"` // open() derivation input
	SellerPeerID  string         `json:"seller_peer_id"`
	Seller        string         `json:"seller"` // payout address
	Buyer         string         `json:"buyer"`
	Token         string         `json:"token"`
	Budget        string         `json:"budget"` // base units, decimal
	Drawn         string         `json:"drawn"`  // base units committed (reserved + released)
	Deadline      int64          `json:"deadline"`
	State         string         `json:"state"` // open|closed|disputed
	OpenTx        string         `json:"open_tx,omitempty"`
	LastDrawAt    time.Time      `json:"last_draw_at,omitempty"`
	Draws         []drawdownDraw `json:"draws,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// drawdownDraw is one per-task draw against the session budget.
type drawdownDraw struct {
	PurchaseID string    `json:"purchase_id"`
	Amount     string    `json:"amount"` // base units
	Tx         string    `json:"tx,omitempty"`
	At         time.Time `json:"at"`
}

// headroom returns budget − drawn as a big.Int.
func (d *drawdownSession) headroom() *big.Int {
	budget, _ := new(big.Int).SetString(d.Budget, 10)
	drawn, _ := new(big.Int).SetString(d.Drawn, 10)
	return new(big.Int).Sub(budget, drawn)
}

// drawdownLedger owns the session map + persistence. All methods lock
// internally — acquire's reserve-on-acquire keeps concurrent buys from
// oversubscribing headroom.
type drawdownLedger struct {
	dir      string
	mu       sync.Mutex
	sessions map[string]*drawdownSession
}

// openDrawdownLedger loads persisted sessions at boot.
func openDrawdownLedger(moduleDir string) *drawdownLedger {
	l := &drawdownLedger{
		dir:      filepath.Join(moduleDir, drawdownDirName),
		sessions: map[string]*drawdownSession{},
	}
	ents, err := os.ReadDir(l.dir)
	if err != nil {
		return l
	}
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := readBounded(filepath.Join(l.dir, e.Name()), 256<<10)
		if err != nil {
			continue
		}
		var s drawdownSession
		if json.Unmarshal(data, &s) != nil || s.SessionID == "" {
			continue
		}
		l.sessions[s.SessionID] = &s
	}
	return l
}

func (l *drawdownLedger) saveLocked(s *drawdownSession) error {
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(l.dir, sanitizeSessionID(s.SessionID)+".json"), data)
}

// acquire returns a session with headroom for draw, reserving the draw
// amount immediately under the caller's purchase id (Drawn += draw).
// fresh=true means the caller must rail.Open the session before dialing.
// The session's deadline/identity are assigned here so the purchase
// record can carry them.
func (l *drawdownLedger) acquire(
	sellerPeer, seller, buyer, token, purchaseID string,
	draw, budget *big.Int, deadline int64,
	newSessionID func() (sessionID, correlationID string),
	nowFn func() time.Time,
) (*drawdownSession, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := nowFn().Unix()
	for _, s := range l.sessions {
		if s.State != "open" || s.OpenTx == "" {
			continue // un-opened or settled sessions don't serve draws
		}
		if s.SellerPeerID != sellerPeer ||
			!strings.EqualFold(s.Token, token) {
			continue
		}
		if s.Deadline <= now+acquireDeadlineMargin {
			continue
		}
		if s.headroom().Cmp(draw) < 0 {
			continue // budget exhausted for this draw
		}
		reserveDraw(s, purchaseID, draw)
		return s, false, l.saveLocked(s)
	}
	if budget == nil || budget.Sign() <= 0 {
		return nil, false, fmt.Errorf(
			"drawdown: session_budget is required when escrow_settlement=drawdown")
	}
	if draw.Cmp(budget) > 0 {
		return nil, false, fmt.Errorf(
			"drawdown: task draw %s exceeds session_budget %s", draw, budget)
	}
	sid, corr := newSessionID()
	s := &drawdownSession{
		V:             1,
		SessionID:     sid,
		CorrelationID: corr,
		SellerPeerID:  sellerPeer,
		Seller:        seller,
		Buyer:         buyer,
		Token:         token,
		Budget:        budget.String(),
		Drawn:         "0",
		Deadline:      deadline,
		State:         "open",
		CreatedAt:     nowFn(),
	}
	l.sessions[sid] = s
	reserveDraw(s, purchaseID, draw)
	l.boundLocked()
	return s, true, l.saveLocked(s)
}

// reserveDraw commits headroom at acquire time — pending until the
// release lands (recordDraw stamps the tx) or the buy fails
// (rollbackDraw frees it).
func reserveDraw(s *drawdownSession, purchaseID string, draw *big.Int) {
	drawn, _ := new(big.Int).SetString(s.Drawn, 10)
	s.Drawn = new(big.Int).Add(drawn, draw).String()
	s.Draws = append(s.Draws, drawdownDraw{
		PurchaseID: purchaseID, Amount: draw.String(),
	})
}

// recordDraw confirms a released draw: the purchase's entry gains its
// tx + timestamp; LastDrawAt paces draw_interval.
func (l *drawdownLedger) recordDraw(
	sessionID, purchaseID, tx string,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sessionID]
	if s == nil {
		return fmt.Errorf("drawdown: no session %s", sessionID)
	}
	for i := len(s.Draws) - 1; i >= 0; i-- {
		if s.Draws[i].PurchaseID == purchaseID && s.Draws[i].Tx == "" {
			s.Draws[i].Tx = tx
			s.Draws[i].At = time.Now()
			s.LastDrawAt = s.Draws[i].At
			return l.saveLocked(s)
		}
	}
	return fmt.Errorf("drawdown: %s has no pending draw for %s", sessionID, purchaseID)
}

// rollbackDraw releases an acquire reservation the purchase never used.
func (l *drawdownLedger) rollbackDraw(sessionID, purchaseID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sessionID]
	if s == nil {
		return
	}
	for i := len(s.Draws) - 1; i >= 0; i-- {
		d := s.Draws[i]
		if d.PurchaseID != purchaseID || d.Tx != "" {
			continue
		}
		amt, _ := new(big.Int).SetString(d.Amount, 10)
		drawn, _ := new(big.Int).SetString(s.Drawn, 10)
		s.Drawn = new(big.Int).Sub(drawn, amt).String()
		s.Draws = append(s.Draws[:i], s.Draws[i+1:]...)
		break
	}
	if err := l.saveLocked(s); err != nil {
		logger.WarnCF("market", "drawdown rollback persist failed",
			map[string]any{"session_id": sessionID, "error": err.Error()})
	}
}

// markOpened stamps the open tx — acquire-created sessions only serve
// draws once open is recorded (a crashed open leaves the session
// inert rather than half-live).
func (l *drawdownLedger) markOpened(sessionID, tx string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sessionID]
	if s == nil {
		return fmt.Errorf("drawdown: no session %s", sessionID)
	}
	s.OpenTx = tx
	return l.saveLocked(s)
}

// markClosed ends a session (post-withdraw or post-resolve).
func (l *drawdownLedger) markClosed(sessionID, state string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.sessions[sessionID]; s != nil {
		s.State = state
		if err := l.saveLocked(s); err != nil {
			logger.WarnCF("market", "drawdown close persist failed",
				map[string]any{"session_id": sessionID, "error": err.Error()})
		}
	}
}

// nextDrawAt paces draws on a session to draw_interval.
func (l *drawdownLedger) nextDrawAt(sessionID string, interval time.Duration) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sessions[sessionID]
	if s == nil || s.LastDrawAt.IsZero() {
		return time.Time{}
	}
	return s.LastDrawAt.Add(interval)
}

// expired lists open sessions whose deadline passed — the sweep
// withdraws their remainders (drawdown withdraw has no claim grace).
func (l *drawdownLedger) expired(now int64) []*drawdownSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*drawdownSession
	for _, s := range l.sessions {
		if s.State == "open" && s.OpenTx != "" && s.Deadline <= now {
			cp := *s
			out = append(out, &cp)
		}
	}
	return out
}

// boundLocked drops the oldest closed sessions past the record cap.
func (l *drawdownLedger) boundLocked() {
	if len(l.sessions) <= drawdownMaxRecords {
		return
	}
	var closed []string
	for id, s := range l.sessions {
		if s.State != "open" {
			closed = append(closed, id)
		}
	}
	// Oldest-first eviction of settled records only — live sessions are
	// never evicted under the cap.
	for len(l.sessions) > drawdownMaxRecords && len(closed) > 0 {
		oldest := ""
		var oldestAt time.Time
		for _, id := range closed {
			s := l.sessions[id]
			if s == nil {
				continue
			}
			if oldest == "" || s.UpdatedAt.Before(oldestAt) {
				oldest, oldestAt = id, s.UpdatedAt
			}
		}
		if oldest == "" {
			return
		}
		delete(l.sessions, oldest)
		closed = closed[1:]
	}
}

// --- purchase-manager integration ---------------------------------------

// drawdownAcquire finds-or-mints a funded session for the purchase's
// seller, reserving the draw amount against headroom. Returns the
// session and whether rail.Open must run (fresh sessions).
func (pm *purchaseMgr) drawdownAcquire(
	ctx context.Context,
	p *purchase,
	rail settlement.Rail,
	drawAmt *big.Int,
	mc *marketConfig,
) (*drawdownSession, bool, error) {
	mcRail := mc.rail
	if mcRail == nil {
		mcRail = &settlement.RailConfig{DisputeWindowSecs: 3600}
	}
	budget, err := moneyToBaseUnits(ctx, pm, mc.sessionBudget, rail)
	if err != nil {
		return nil, false, fmt.Errorf("drawdown: %w", err)
	}
	deadline := pm.nowFn().Unix() + mcRail.DisputeWindowSecs
	predict := func() (string, string) {
		corr := randomHex(16)
		sid, err := pm.predictSessionID(ctx, rail, corr)
		if err != nil {
			return "", corr
		}
		return sid, corr
	}
	return pm.drawdown.acquire(
		p.SellerPeerID, p.Seller, p.Buyer, p.Terms.Token, p.PurchaseID,
		drawAmt, budget, deadline, predict, pm.nowFn)
}

// waitDrawSlot sleeps until the session's next draw slot opens under
// draw_interval pacing (0 interval = immediate).
func (pm *purchaseMgr) waitDrawSlot(ctx context.Context, sessionID string) error {
	mc := pm.cfg.Load()
	if mc == nil || mc.drawInterval <= 0 {
		return nil
	}
	at := pm.drawdown.nextDrawAt(sessionID, mc.drawInterval)
	if at.IsZero() {
		return nil
	}
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// sweepDrawdowns withdraws remainders on expired open sessions — the
// drawdown close path (no claim grace applies to budget sessions).
func (pm *purchaseMgr) sweepDrawdowns(ctx context.Context) {
	mc := pm.cfg.Load()
	if mc == nil || pm.drawdown == nil || !mc.drawdownMode() {
		return
	}
	for _, s := range pm.drawdown.expired(pm.nowFn().Unix()) {
		rail := pm.railFor(&purchase{Buyer: s.Buyer, PurchaseID: "sweep"})
		if rail == nil {
			return
		}
		tx, err := rail.Withdraw(ctx, s.SessionID)
		if err != nil {
			logger.DebugCF("market", "drawdown sweep withdraw failed",
				map[string]any{"session_id": s.SessionID, "error": err.Error()})
			continue
		}
		pm.drawdown.markClosed(s.SessionID, "closed")
		pm.audit.log("market.drawdown.closed", map[string]any{
			"session_id": s.SessionID, "tx": tx,
			"drawn": s.Drawn, "budget": s.Budget,
		})
	}
}

// moneyToBaseUnits converts a decimal config amount to token base units
// using the rail's decimals (6 under the fixture posture).
func moneyToBaseUnits(
	ctx context.Context, pm *purchaseMgr, decimal string, rail settlement.Rail,
) (*big.Int, error) {
	if decimal == "" {
		return nil, fmt.Errorf("session_budget is required for drawdown settlement")
	}
	base, err := priceBaseUnits(ctx, pm, decimal, rail)
	if err != nil {
		return nil, fmt.Errorf("session_budget %q: %w", decimal, err)
	}
	return base, nil
}
