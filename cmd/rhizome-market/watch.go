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

	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// Escrow event watcher (Track 104): polls eth_getLogs per non-terminal
// configured-rail purchase for the lifecycle events the escrow clone
// emits — Release, Lock, Withdraw, Resolve, Verified — and drives
// purchase transitions + audit. The daemon's web3 WatchRunner is built
// for a fixed small set of configured contracts (8-watch cap, one
// resolved address per watch); purchase escrows are per-session clones,
// so the module scans them directly — same wire surface, bounded by live
// purchases rather than a global watch cap.
//
// Per-purchase progress persists in the record's watch_block, so a
// restart resumes where it left off; the first scan looks back
// watchFirstLookback blocks (bounded — the chain state the purchase
// itself records is always the authority, logs are observation).

const (
	watchFirstLookback = 500 // blocks scanned on a purchase's first pass
	watchMaxPerTick    = 50  // bounded logs per purchase per tick
	watchMaxRange      = 2000
)

// Lifecycle event topic hashes — keccak256 of the canonical signature.
var (
	topicRelease  = eventTopic("Release(uint256,uint256)")
	topicLock     = eventTopic("Lock(address,bytes32)")
	topicWithdraw = eventTopic("Withdraw(uint256)")
	topicResolve  = eventTopic("Resolve(address,uint256,uint256,uint256,bytes32)")
	topicVerified = eventTopic("Verified(address,address)")

	// Graduated rail (Track 127 RhizomeEscrow) — sessionId rides topic[1].
	topicGradOpened    = eventTopic("Opened(bytes32,address,address,address,uint128,uint64,bytes32)")
	topicGradReleased  = eventTopic("Released(bytes32,uint128,uint128)")
	topicGradDisputed  = eventTopic("Disputed(bytes32,address,bytes32)")
	topicGradResolved  = eventTopic("Resolved(bytes32,uint128,uint128,bytes32)")
	topicGradClaimed   = eventTopic("Claimed(bytes32,uint128)")
	topicGradWithdrawn = eventTopic("Withdrawn(bytes32,uint128)")
)

func eventTopic(sig string) string {
	return "0x" + hex.EncodeToString(web3.Keccak256([]byte(sig)))
}

// runWatcher ticks until ctx ends; each tick reads the live config —
// fixture rail, watchInterval=0, or no endpoint all skip cleanly.
func (pm *purchaseMgr) runWatcher(ctx context.Context) {
	for {
		mc := pm.cfg.Load()
		interval := time.Duration(0)
		if mc != nil {
			interval = mc.watchInterval
		}
		if interval == 0 {
			interval = time.Minute // idle cadence — config reload re-arms
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		pm.scanEscrowEvents(ctx)
		// Expired drawdown sessions withdraw their remainders — the
		// close path runs even under the fixture posture (no endpoint
		// or watch cadence needed for the ledger sweep).
		pm.sweepDrawdowns(ctx)
	}
}

// scanEscrowEvents polls the lifecycle logs for every non-terminal
// configured purchase and applies observed transitions.
func (pm *purchaseMgr) scanEscrowEvents(ctx context.Context) {
	mc := pm.cfg.Load()
	ep := pm.endpoint.Load()
	if mc == nil || ep == nil || mc.rail == nil || mc.watchInterval == 0 {
		return
	}
	client := web3.NewClient(ep.URL, ep.APIKey, nil)
	head, err := ethBlockNumber(ctx, client)
	if err != nil {
		logger.DebugCF("market", "escrow watch: head lookup failed",
			map[string]any{"error": err.Error()})
		return
	}
	for _, p := range pm.watchTargets() {
		if ctx.Err() != nil {
			return
		}
		pm.scanPurchaseEvents(ctx, client, p, head)
	}
}

// watchTargets snapshots purchases worth scanning: has an escrow, on the
// configured rail, not pending review, and not in a truly final state.
// disputed stays watched — an arbiter's resolve() still lands after lock.
func (pm *purchaseMgr) watchTargets() []*purchase {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	var out []*purchase
	for _, p := range pm.byID {
		if p.SessionID == "" || p.Settlement != "configured" ||
			p.State == purchasePendingReview {
			continue
		}
		switch p.State {
		case purchaseCompleted, purchaseResolved, purchaseRefunded, purchaseFailed:
			continue
		}
		out = append(out, p)
	}
	return out
}

// scanPurchaseEvents fetches new lifecycle logs for one escrow and
// applies them — transitions for state-moving events, audit for all.
func (pm *purchaseMgr) scanPurchaseEvents(
	ctx context.Context, client *web3.Client, p *purchase, head uint64,
) {
	snap := pm.snapshot(p)
	from := snap.WatchBlock + 1
	if snap.WatchBlock == 0 {
		from = 1
		if head > watchFirstLookback {
			from = head - watchFirstLookback
		}
	}
	if from > head {
		return
	}
	to := head
	if to-from+1 > watchMaxRange {
		to = from + watchMaxRange - 1
	}
	// Smart Invoice escrows are per-session clones — the session_id IS
	// the contract address. The graduated rail shares one deployment:
	// filter contract + indexed sessionId topic instead.
	mc := pm.cfg.Load()
	filter := map[string]any{
		"address":   snap.SessionID,
		"fromBlock": fmt.Sprintf("0x%x", from),
		"toBlock":   fmt.Sprintf("0x%x", to),
		"topics": []any{
			[]any{topicRelease, topicLock, topicWithdraw, topicResolve, topicVerified},
		},
	}
	if mc != nil && mc.rail != nil && mc.rail.Kind == settlement.RailKindRhizome {
		sid := strings.TrimPrefix(strings.ToLower(snap.SessionID), "0x")
		filter["address"] = mc.rail.Factory
		filter["topics"] = []any{
			[]any{
				topicGradOpened, topicGradReleased, topicGradDisputed,
				topicGradResolved, topicGradClaimed, topicGradWithdrawn,
			},
			"0x" + strings.Repeat("0", 64-len(sid)) + sid,
		}
	}
	raw, err := client.Call(ctx, "eth_getLogs", []any{filter})
	if err != nil {
		logger.DebugCF("market", "escrow watch: getLogs failed",
			map[string]any{"escrow": snap.SessionID, "error": err.Error()})
		return
	}
	logs, _, err := web3.DecodeLogs(raw, watchMaxPerTick)
	if err != nil {
		return
	}
	for _, lg := range logs {
		pm.applyEscrowEvent(p, lg)
	}
	pm.mutate(p, func(pp *purchase) { pp.WatchBlock = to })
}

// applyEscrowEvent maps one escrow log to an audit line and, for
// state-moving events the module didn't necessarily initiate (a dispute
// the seller raised, an arbiter's resolve), a purchase transition.
func (pm *purchaseMgr) applyEscrowEvent(p *purchase, lg web3.Log) {
	if len(lg.Topics) == 0 {
		return
	}
	var event, toState string
	switch lg.Topics[0] {
	case topicRelease:
		event, toState = "release", purchaseCompleted
	case topicLock:
		event, toState = "lock", purchaseDisputed
	case topicWithdraw:
		event, toState = "withdraw", purchaseRefunded
	case topicResolve:
		event, toState = "resolve", purchaseResolved
	case topicVerified:
		event = "verified" // informational — client-marked verification
	case topicGradOpened:
		event = "opened"
	case topicGradReleased:
		// Amount-partials aren't terminal — the purchase completes on
		// claim/resolve/withdraw or the module's own release confirm.
		// A Released whose cumulative covers the budget settles it.
		// Under drawdown Released is per-draw and un-attributable — the
		// module's own recordDraw is authoritative; never transitions.
		event = "released"
		if !p.Drawdown && graduatedReleaseIsFull(p, lg) {
			toState = purchaseCompleted
		}
	case topicGradDisputed:
		event, toState = "disputed", purchaseDisputed
		if p.Drawdown {
			pm.drawdown.markClosed(p.SessionID, "disputed")
		}
	case topicGradResolved:
		event, toState = "resolved", purchaseResolved
		if p.Drawdown {
			pm.drawdown.markClosed(p.SessionID, "resolved")
		}
	case topicGradClaimed:
		event, toState = "claimed", purchaseCompleted
	case topicGradWithdrawn:
		event, toState = "withdrawn", purchaseRefunded
		if p.Drawdown {
			pm.drawdown.markClosed(p.SessionID, "closed")
		}
	default:
		return
	}
	if pm.audit != nil {
		pm.audit.log("market.escrow.event", map[string]any{
			"event": event, "purchase_id": p.PurchaseID,
			"session_id": p.SessionID, "tx": lg.TxHash,
			"block": lg.BlockNumber, "data": lg.Data,
		})
	}
	if toState == "" {
		return
	}
	cur := pm.snapshot(p)
	if cur.State == toState {
		return // idempotent — our own verb already transitioned it
	}
	// Observed (not initiated) transitions leave the error fields clean —
	// the audit line above carries the chain evidence.
	pm.transition(p, toState, "", "")
}

// graduatedReleaseIsFull decodes a Released event's (amount, released)
// data pair and reports whether cumulative releases cover the purchase's
// committed amount — the settle condition for the graduated rail.
func graduatedReleaseIsFull(p *purchase, lg web3.Log) bool {
	data, err := hex.DecodeString(strings.TrimPrefix(lg.Data, "0x"))
	if err != nil || len(data) < 64 {
		return false
	}
	released := new(big.Int).SetBytes(data[32:64])
	amount, ok := new(big.Int).SetString(p.Terms.Amount, 10)
	return ok && amount.Sign() > 0 && released.Cmp(amount) >= 0
}

// ethBlockNumber returns the chain head for range bounding.
func ethBlockNumber(ctx context.Context, client *web3.Client) (uint64, error) {
	raw, err := client.Call(ctx, "eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}
	var hexHead string
	if err := json.Unmarshal(raw, &hexHead); err != nil {
		return 0, fmt.Errorf("eth_blockNumber: %w", err)
	}
	return web3.QuantityUint64(hexHead)
}
