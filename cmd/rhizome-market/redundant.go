// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 133: buy-side depth — redundant fan-out purchases and the
// local session ledger (`market sessions`).

package main

import (
	"context"
	"math/big"
	"sort"
	"strings"
	"time"
)

// redundantMaxN bounds the fan-out — every branch is a real escrow at
// N× price, and the daily spend cap still binds per purchase.
const redundantMaxN = 8

// redundantBuy is one branch outcome in a redundant batch.
type redundantBuy struct {
	Provider   string    `json:"provider"`
	PeerID     string    `json:"peer_id"`
	OfferID    string    `json:"offer_id"`
	PerTask    string    `json:"per_task,omitempty"`
	Asset      string    `json:"asset,omitempty"`
	PurchaseID string    `json:"purchase_id,omitempty"`
	ReviewID   string    `json:"review_id,omitempty"`
	Purchase   *purchase `json:"purchase,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// beginRedundant fans one task out to up to N providers — separate
// escrows per branch so results are independently settled and
// disputable (the threat model's redundancy mitigation for unverifiable
// work quality). Candidates come from the same merged findRows query as
// `market find`, first row per peer, our own peer excluded. Cost is
// N× price — the operator opted in; buy_max_cost_* caps still apply per
// branch so a runaway fan can't overspend the daily budget.
func (pm *purchaseMgr) beginRedundant(
	ctx context.Context, req buyRequest,
) (string, []redundantBuy, error) {
	n := req.Redundant
	if n < 2 {
		return "", nil, buyErr("bad_request",
			"redundant buys need --redundant ≥ 2 (got %d)", n)
	}
	if n > redundantMaxN {
		return "", nil, buyErr("bad_request",
			"--redundant caps at %d branches", redundantMaxN)
	}
	if strings.TrimSpace(req.Task) == "" {
		return "", nil, buyErr("bad_request", "task is required")
	}
	if len(req.Task) > taskMaxBytes {
		return "", nil, buyErr("bad_request",
			"task exceeds %d bytes", taskMaxBytes)
	}

	rows, _, _, err := pm.findRows(ctx, req.Query)
	if err != nil {
		return "", nil, err
	}
	self := ""
	if id := pm.ident.Load(); id != nil {
		self = id.PeerID
	}
	// One branch per peer — first matching row wins; skip providers we
	// can't honestly buy from (self, expired adverts, down runtimes).
	seen := map[string]bool{}
	var cands []findRow
	for _, r := range rows {
		if r.PeerID == "" || seen[r.PeerID] || r.PeerID == self ||
			r.OfferID == "" || r.PerTask == "" {
			continue
		}
		if r.Expired || (r.Runtime != "" && !r.RuntimeAvailable) {
			continue
		}
		seen[r.PeerID] = true
		cands = append(cands, r)
		if len(cands) == n {
			break
		}
	}
	if len(cands) == 0 {
		return "", nil, buyErr("no_providers",
			"no buyable providers match %q — widen the query or market find first",
			req.Query)
	}

	group := "red-" + randomHex(6)
	branches := make([]redundantBuy, 0, len(cands))
	for _, c := range cands {
		b := redundantBuy{
			Provider: c.PeerID, PeerID: c.PeerID, OfferID: c.OfferID,
			PerTask: c.PerTask, Asset: c.Asset,
		}
		p, reviewID, berr := pm.begin(ctx, buyRequest{
			Provider:       c.PeerID,
			Offer:          c.OfferID,
			Task:           req.Task,
			Attachments:    req.Attachments,
			MaxCost:        req.MaxCost,
			RedundantGroup: group,
		})
		if berr != nil {
			b.Error = berr.Error()
		} else {
			b.PurchaseID = p.PurchaseID
			b.ReviewID = reviewID
			snap := pm.snapshot(p)
			b.Purchase = &snap
		}
		branches = append(branches, b)
	}
	return group, branches, nil
}

// sessionRow is one ledger line in `market sessions` — enough to read
// status, terms, settlement state, and result-comparison fields at a
// glance.
type sessionRow struct {
	PurchaseID     string     `json:"purchase_id"`
	Provider       string     `json:"provider"`
	PeerID         string     `json:"peer_id,omitempty"`
	OfferID        string     `json:"offer_id"`
	State          string     `json:"state"`
	Price          string     `json:"price"`
	Asset          string     `json:"asset"`
	SessionID      string     `json:"session_id,omitempty"`
	Settlement     string     `json:"settlement"`
	Drawdown       bool       `json:"drawdown,omitempty"`
	ResultSHA256   string     `json:"result_sha256,omitempty"`
	RedundantGroup string     `json:"redundant_group,omitempty"`
	TEEClaim       *advertTEE `json:"tee_attestation,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// sessionsMaxRows bounds the ledger listing — the purchase bound is
// higher but the operator surface stays a page, not a dump.
const sessionsMaxRows = 100

// listSessions returns the local purchase ledger newest-first (bounded),
// plus spend reporting against the buy_max_cost_* budgets. `all` lists
// terminal records too; default keeps live + recent-terminal rows.
func (pm *purchaseMgr) listSessions(all bool) ([]sessionRow, map[string]any) {
	pm.mu.Lock()
	var rows []sessionRow
	for _, p := range pm.byID {
		if !all && isTerminalPurchase(p.State) {
			continue
		}
		rows = append(rows, sessionRow{
			PurchaseID:     p.PurchaseID,
			Provider:       p.Provider,
			PeerID:         p.SellerPeerID,
			OfferID:        p.OfferID,
			State:          p.State,
			Price:          p.Price,
			Asset:          p.Asset,
			SessionID:      p.SessionID,
			Settlement:     p.Settlement,
			Drawdown:       p.Drawdown,
			ResultSHA256:   p.ResultSHA256,
			RedundantGroup: p.RedundantGroup,
			TEEClaim:       p.TEEClaim,
			Error:          p.Error,
			CreatedAt:      p.CreatedAt,
			UpdatedAt:      p.UpdatedAt,
		})
	}
	pm.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > sessionsMaxRows {
		rows = rows[:sessionsMaxRows]
	}

	// Spend reporting: committed 24h spend per asset vs the caps. The
	// per-task cap is a ceiling per purchase, the per-day cap is the
	// running budget — headroom is what a new buy could still spend.
	spend := map[string]any{}
	if mc := pm.cfg.Load(); mc != nil {
		spend["cap_per_task"] = mc.buyMaxCostPerTask
		spend["cap_per_day"] = mc.buyMaxCostPerDay
		assets := map[string]bool{}
		for _, r := range rows {
			if r.Asset != "" {
				assets[r.Asset] = true
			}
		}
		perAsset := map[string]any{}
		for a := range assets {
			spent := pm.dailySpend(a)
			entry := map[string]any{"spent_24h": spent.RatString()}
			if mc.buyMaxCostPerDay != "" {
				if capV, ok := new(big.Rat).SetString(mc.buyMaxCostPerDay); ok {
					headroom := new(big.Rat).Sub(capV, spent)
					if headroom.Sign() < 0 {
						headroom = new(big.Rat)
					}
					entry["headroom"] = headroom.RatString()
				}
			}
			perAsset[a] = entry
		}
		spend["assets"] = perAsset
	}
	return rows, spend
}
