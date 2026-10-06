// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/redact"
	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// buyRequest is the POST /v1/buy body — `market buy <provider> <offer>
// <task> [--max-cost]`, or `confirm_review_id` to proceed past a pending
// export review. Attachments are https URI references (ACP resource_link
// blocks — never file contents); they require export_allow_attachments.
type buyRequest struct {
	Provider        string   `json:"provider"`
	Offer           string   `json:"offer"`
	Task            string   `json:"task"`
	Attachments     []string `json:"attachments,omitempty"`
	MaxCost         string   `json:"max_cost,omitempty"`
	ConfirmReviewID string   `json:"confirm_review_id,omitempty"`
}

// buyError is a buy-path refusal with a stable machine-readable code.
type buyError struct {
	code string
	msg  string
}

func (e *buyError) Error() string { return e.msg }
func buyErr(code, format string, args ...any) *buyError {
	return &buyError{code: code, msg: fmt.Sprintf(format, args...)}
}

// taskMaxBytes bounds the outbound task text — prompts ride inside the
// bridged ACP stream; absurd sizes are refused before hashing.
const taskMaxBytes = 64 << 10

// Attachment bounds: URI references only (http/https — a file:// or other
// scheme could smuggle a local-path read attempt into the seller's
// context), few and small.
const (
	attachmentMaxCount = 8
	attachmentMaxBytes = 2048
)

// validateAttachmentURIs enforces the URI-only, http(s)-only attachment
// policy — these travel as ACP resource_link references the seller's
// agent may dereference; the module never reads or sends their bytes.
func validateAttachmentURIs(attachments []string) error {
	if len(attachments) > attachmentMaxCount {
		return buyErr("attachments_denied",
			"%d attachments exceeds the %d limit", len(attachments), attachmentMaxCount)
	}
	for _, uri := range attachments {
		if len(uri) > attachmentMaxBytes {
			return buyErr(
				"attachments_denied",
				"attachment URI exceeds %d bytes",
				attachmentMaxBytes,
			)
		}
		u := strings.ToLower(strings.TrimSpace(uri))
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return buyErr("attachments_denied",
				"attachment %q is not an http(s) URI — only resource links are exported", uri)
		}
	}
	return nil
}

// begin validates a fresh buy or confirms a pending review. Confirmed
// purchases spawn the orchestration goroutine; pending-review purchases
// return a review preview instead.
func (pm *purchaseMgr) begin(
	ctx context.Context, req buyRequest,
) (*purchase, string, error) {
	if id := strings.TrimSpace(req.ConfirmReviewID); id != "" {
		p, err := pm.confirmReview(id)
		if err != nil {
			return nil, "", buyErr("review_expired", "%s", err)
		}
		pm.transition(p, purchaseQueued, "", "")
		go pm.spawn(context.Background(), pm, p)
		return p, "", nil
	}

	// Structural request validation comes before config/rail checks — a
	// malformed request is bad regardless of module readiness.
	if strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.Offer) == "" {
		return nil, "", buyErr("bad_request", "provider and offer are required")
	}
	if strings.TrimSpace(req.Task) == "" {
		return nil, "", buyErr("bad_request", "task is required")
	}
	if len(req.Task) > taskMaxBytes {
		return nil, "", buyErr("bad_request", "task exceeds %d bytes", taskMaxBytes)
	}
	mc := pm.cfg.Load()
	if mc == nil {
		return nil, "", buyErr("not_ready", "module config not loaded")
	}
	rail := pm.rail.Load()
	if rail == nil || *rail == nil {
		return nil, "", buyErr("rail_unavailable", "no settlement rail configured")
	}

	// Provider advert: peer journal (direct-peer discovery) or the index.
	adv, dialAddr, dialFP, err := pm.resolveProvider(ctx, req.Provider)
	if err != nil {
		return nil, "", err
	}
	var off *offer
	for i := range adv.Offers {
		if adv.Offers[i].ID == req.Offer {
			off = &adv.Offers[i]
			break
		}
	}
	if off == nil {
		return nil, "", buyErr("unknown_offer",
			"provider %s advertises no offer %q — run market find first", req.Provider, req.Offer)
	}
	if strings.TrimSpace(off.PriceSheet.PerTask) == "" {
		return nil, "", buyErr("offer_not_buyable",
			"offer %q has no price_sheet.per_task — only fixed-price offers are buyable in v1",
			off.ID)
	}
	if adv.Payout == nil || !web3.IsAddress(adv.Payout.Address) {
		return nil, "", buyErr("provider_unsettled",
			"provider %s advert no usable payout.address", req.Provider)
	}
	if adv.Escrow != nil && adv.Escrow.Posture == "configured" && mc.rail != nil &&
		!strings.EqualFold(adv.Escrow.Contract, mc.rail.Factory) {
		return nil, "", buyErr("terms_mismatch",
			"provider escrow factory %s differs from ours %s — cross-factory buys are refused",
			adv.Escrow.Contract, mc.rail.Factory)
	}
	if adv.Runtime != "" && !adv.RuntimeAvailable {
		return nil, "", buyErr("provider_unavailable",
			"provider %s advertises runtime %q but reports it unavailable",
			req.Provider, adv.Runtime)
	}

	// Export policy: task text plus, when enabled, URI-only resource links.
	// Attachment bytes never leave the module — links are references the
	// seller's agent may fetch, which is why the flag gates them.
	if len(req.Attachments) > 0 {
		if !mc.exportAllowAttachments {
			return nil, "", buyErr("attachments_denied",
				"export_allow_attachments is off — %d attachment(s) refused",
				len(req.Attachments))
		}
		if err := validateAttachmentURIs(req.Attachments); err != nil {
			return nil, "", err
		}
	}
	task := req.Task
	if mc.exportRedact {
		task = redact.Mask(task)
	}
	if err := pm.checkCaps(mc, off, req.MaxCost); err != nil {
		return nil, "", err
	}

	buyer, err := pm.buyerAddress(mc)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256([]byte(task))
	// Drawdown defers session assignment to runPurchase — the ledger
	// reuses a live funded session or mints one; per-task predicts now.
	corr, sessionID := "", ""
	drawdown := mc.drawdownMode()
	if !drawdown {
		corr = randomHex(16)
		var err error
		sessionID, err = pm.predictSessionID(ctx, *rail, corr)
		if err != nil {
			return nil, "", buyErr("predict_failed", "escrow address prediction: %s", err)
		}
	}
	window := int64(24 * 60 * 60)
	if mc.rail != nil && mc.rail.DisputeWindowSecs > 0 {
		window = mc.rail.DisputeWindowSecs
	}
	token := fixtureToken
	chainID := off.PriceSheet.ChainID
	if mc.rail != nil {
		token = mc.rail.Token
		chainID = int64(mc.rail.ChainID)
	}
	amount, err := priceBaseUnits(ctx, pm, off.PriceSheet.PerTask, *rail)
	if err != nil {
		return nil, "", buyErr("price_resolution", "%s", err)
	}

	p := &purchase{
		V:             1,
		PurchaseID:    randomHex(8),
		Provider:      req.Provider,
		DialAddr:      dialAddr,
		DialTLSFP:     dialFP,
		SellerPeerID:  adv.PeerID,
		Seller:        adv.Payout.Address,
		Buyer:         buyer,
		OfferID:       off.ID,
		Price:         off.PriceSheet.PerTask,
		Asset:         off.PriceSheet.Asset,
		Task:          task,
		TaskHash:      "0x" + hex.EncodeToString(sum[:]),
		Attachments:   req.Attachments,
		SessionID:     sessionID,
		CorrelationID: corr,
		Drawdown:      drawdown,
		Terms: purchaseTerms{
			Amount:          amount.String(),
			Token:           token,
			ChainID:         chainID,
			TerminationTime: pm.nowFn().Unix() + window,
			Drawdown:        drawdown,
		},
		State:      purchasePendingReview,
		Settlement: "fixture",
		CreatedAt:  pm.nowFn().UTC(),
		UpdatedAt:  pm.nowFn().UTC(),
	}
	if mc.rail != nil {
		p.Settlement = "configured"
	}
	pm.register(p)
	if err := pm.save(p); err != nil && pm.audit != nil {
		pm.audit.log("market.purchase.persist_failed", map[string]any{
			"purchase_id": p.PurchaseID, "error": err.Error(),
		})
	}

	if mc.exportRequireReview != "never" {
		reviewID := pm.mintReview(p)
		if reviewID == "" {
			pm.transition(p, purchaseFailed, "review_saturated",
				"too many pending reviews — confirm or let them expire")
			return p, "", buyErr("review_saturated",
				"review queue full (%d) — confirm or let pending reviews expire", reviewMaxLive)
		}
		return p, reviewID, nil
	}
	pm.transition(p, purchaseQueued, "", "")
	go pm.spawn(context.Background(), pm, p)
	return p, "", nil
}

// resolveProvider finds the provider's advert and the address to dial:
// connected-peer journal first (fresh, trust-flagged), index rows second.
// A /p2p/ multiaddr resolves to its peer id for journal matching; an
// index-sourced provider dials its advertised multiaddr (bare peer ids
// only reach already-connected peers through the bridge). When the advert
// carries endpoints + tls_fingerprint (Track 110), the first wss endpoint
// wins — a direct TLS dial works whether or not the mesh peer is
// connected. Returns the advert, dial address, and TLS fingerprint
// ("" for mesh dials).
func (pm *purchaseMgr) resolveProvider(
	ctx context.Context, provider string,
) (*advert, string, string, error) {
	pid := provider
	if i := strings.LastIndex(provider, "/p2p/"); i >= 0 {
		pid = provider[i+5:]
	}
	// wssEndpoint picks the first advertised wss endpoint when the advert
	// pins a fingerprint — without the pin there is nothing to TOFU
	// against, so endpoint-less adverts keep the mesh dial.
	wssEndpoint := func(a *advert) (string, string) {
		if a.TLSFingerprint == "" || len(a.Endpoints) == 0 {
			return "", ""
		}
		for _, ep := range a.Endpoints {
			if strings.HasPrefix(ep, "wss://") || strings.HasPrefix(ep, "https://") {
				return ep, a.TLSFingerprint
			}
		}
		return "", ""
	}
	rows, err := peeradverts.Load(pm.home)
	if err == nil {
		for _, r := range rows {
			if r.PeerID != pid {
				continue
			}
			raw, ok := r.Adverts[moduleID]
			if !ok {
				continue
			}
			var a advert
			if json.Unmarshal(raw, &a) == nil {
				if a.PeerID == "" {
					a.PeerID = r.PeerID
				}
				if ep, fp := wssEndpoint(&a); ep != "" {
					return &a, ep, fp, nil
				}
				return &a, provider, "", nil
			}
		}
	}
	if idx, _, ierr := pm.fetchIndex(ctx); ierr == nil && idx != nil {
		for _, pr := range idx.Providers {
			if pr.PeerID != pid {
				continue
			}
			var a advert
			if json.Unmarshal(pr.Advert, &a) == nil {
				if a.PeerID == "" {
					a.PeerID = pr.PeerID
				}
				if ep, fp := wssEndpoint(&a); ep != "" {
					return &a, ep, fp, nil
				}
				dial := provider
				if pr.Multiaddr != "" {
					dial = pr.Multiaddr
				}
				return &a, dial, "", nil
			}
		}
	}
	return nil, "", "", buyErr("provider_unknown",
		"no advert for provider %q — run market find to refresh peer/index discovery", provider)
}

// buyerAddress resolves the purchaser's EVM identity: the buyer_address
// field wins; otherwise the shared web3 wallet's default account. Wallet
// signer mode additionally requires the resolved key to be present in
// the store — a resolvable address without key material fails honestly
// at buy time rather than mid-purchase at the first send.
func (pm *purchaseMgr) buyerAddress(mc *marketConfig) (string, error) {
	ws := web3.OpenWalletStore(web3.WalletDir(pm.home))
	if mc.signerMode == "wallet" {
		cand := mc.buyerAddress
		if cand == "" {
			if def, err := ws.Default(); err == nil && web3.IsAddress(def) {
				cand = def
			}
		}
		if cand == "" || !ws.Has(cand) {
			return "", buyErr("no_buyer_key",
				"settlement_signer=wallet needs buyer_address present in the web3 "+
					"wallet store (or a default wallet) — `rhizome web3 wallet create`")
		}
		return cand, nil
	}
	if mc.buyerAddress != "" {
		return mc.buyerAddress, nil
	}
	if def, err := ws.Default(); err == nil && web3.IsAddress(def) {
		return def, nil
	}
	return "", buyErr("no_buyer_address",
		"no buyer_address field and no default web3 wallet — "+
			"set modules.rhizome-market.fields.buyer_address or "+
			"`rhizome web3 wallet create`")
}

// checkCaps enforces buy_max_cost_per_task (and per_day) against the
// offer price, plus the CLI --max-cost ceiling. All comparisons are exact
// big.Rat arithmetic on the decimal offer units — never float.
func (pm *purchaseMgr) checkCaps(mc *marketConfig, off *offer, maxCost string) error {
	price, ok := new(big.Rat).SetString(strings.TrimSpace(off.PriceSheet.PerTask))
	if !ok || price.Sign() <= 0 {
		return buyErr("bad_price", "offer %q per_task %q is not a positive decimal",
			off.ID, off.PriceSheet.PerTask)
	}
	if mc.buyMaxCostPerTask != "" {
		capV, _ := new(big.Rat).SetString(mc.buyMaxCostPerTask)
		if capV != nil && price.Cmp(capV) > 0 {
			return buyErr("cap_exceeded",
				"offer price %s exceeds buy_max_cost_per_task %s",
				off.PriceSheet.PerTask, mc.buyMaxCostPerTask)
		}
	}
	if mc := strings.TrimSpace(maxCost); mc != "" {
		capV, ok := new(big.Rat).SetString(mc)
		if !ok || capV.Sign() <= 0 {
			return buyErr("bad_max_cost", "--max-cost %q is not a positive decimal", maxCost)
		}
		if price.Cmp(capV) > 0 {
			return buyErr("cap_exceeded",
				"offer price %s exceeds --max-cost %s", off.PriceSheet.PerTask, maxCost)
		}
	}
	if mc.buyMaxCostPerDay != "" {
		capV, _ := new(big.Rat).SetString(mc.buyMaxCostPerDay)
		if capV == nil {
			return nil
		}
		spent := pm.dailySpend(off.PriceSheet.Asset)
		if new(big.Rat).Add(spent, price).Cmp(capV) > 0 {
			return buyErr("cap_exceeded",
				"daily spend cap %s would be exceeded (spent %s + %s)",
				mc.buyMaxCostPerDay, spent.RatString(), off.PriceSheet.PerTask)
		}
	}
	return nil
}

// dailySpend sums committed per-task prices (decimal offer units) for
// same-asset purchases in the last 24 h — fixture and configured rails
// share the cap semantics.
func (pm *purchaseMgr) dailySpend(asset string) *big.Rat {
	cutoff := pm.nowFn().Add(-24 * time.Hour)
	sum := new(big.Rat)
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for _, p := range pm.byID {
		if p.CreatedAt.Before(cutoff) || p.Asset != asset {
			continue
		}
		if p.State == purchasePendingReview || p.State == purchaseFailed {
			continue // unconfirmed/failed buys committed nothing
		}
		if r, ok := new(big.Rat).SetString(p.Price); ok {
			sum.Add(sum, r)
		}
	}
	return sum
}

// predictSessionID resolves the on-chain session id for a correlation id
// — the escrow clone address on Smart Invoice, the bytes32 session key
// on the graduated rail.
func (pm *purchaseMgr) predictSessionID(
	ctx context.Context, rail settlement.Rail, corr string,
) (string, error) {
	switch r := rail.(type) {
	case *settlement.RPCRail:
		return r.PredictEscrowAddr(ctx, corr)
	case *settlement.GraduatedRail:
		return r.SessionID(corr), nil
	case *settlement.MockRail:
		return r.PredictEscrowAddr(corr), nil
	default:
		return "", fmt.Errorf("rail %T cannot predict session ids", rail)
	}
}

// priceBaseUnits converts a decimal offer price into token base units.
func priceBaseUnits(
	ctx context.Context, pm *purchaseMgr, perTask string, rail settlement.Rail,
) (*big.Int, error) {
	price, ok := new(big.Rat).SetString(strings.TrimSpace(perTask))
	if !ok || price.Sign() <= 0 {
		return nil, fmt.Errorf("per_task %q is not a positive decimal", perTask)
	}
	decimals := uint8(6) // fixture posture: the module's own convention
	var decRail interface {
		TokenDecimals(ctx context.Context) (uint8, error)
	}
	switch r := rail.(type) {
	case *settlement.RPCRail:
		decRail = r
	case *settlement.GraduatedRail:
		decRail = r
	}
	if decRail != nil {
		d, err := decRail.TokenDecimals(ctx)
		if err != nil {
			return nil, fmt.Errorf("token decimals: %w", err)
		}
		decimals = d
	}
	mul := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	base := new(big.Rat).Mul(price, new(big.Rat).SetInt(mul))
	if !base.IsInt() {
		return nil, fmt.Errorf(
			"per_task %q is not expressible in base units at %d decimals", perTask, decimals)
	}
	return base.Num(), nil
}

// --- orchestration ------------------------------------------------------

// senderFor builds the per-purchase tx transport: nil on the fixture rail
// (MockRail needs no chain), the configured sender mode otherwise —
// "direct" for unlocked/dev endpoints, "approval" (default) queues into
// the shared web3-pending.json with per-purchase pending-id attribution.
func (pm *purchaseMgr) senderFor(p *purchase) settlement.Sender {
	mc := pm.cfg.Load()
	ep := pm.endpoint.Load()
	if mc == nil || mc.rail == nil || ep == nil {
		return nil
	}
	client := web3.NewClient(ep.URL, ep.APIKey, nil)
	switch mc.signerMode {
	case "direct":
		return settlement.NewDirectSender(client, p.Buyer)
	case "wallet":
		// Local-signing opt-in: the buyer's key must live in the shared
		// web3 wallet store — PrivateKey decrypts per send.
		return settlement.NewWalletSender(
			client, web3.NewStaticProvider(ep.URL, ep.APIKey),
			web3.OpenWalletStore(web3.WalletDir(pm.home)),
			p.Buyer, mc.rail.ChainID)
	}
	q := settlement.NewQueuedSender(pm.pendingStore(), client, p.Buyer, mc.rail.ChainID)
	q.SetOnSubmit(func(e *web3.PendingEntry) { pm.recordPending(p, e.ID) })
	q.SetSummary(func(to string, data []byte) string {
		return fmt.Sprintf("market buy %s (%s): contract call → %s",
			p.PurchaseID, p.SessionID, to)
	})
	return q
}

// pendingStore opens the shared approval queue — <home>/web3.
func (pm *purchaseMgr) pendingStore() *web3.PendingStore {
	return web3.OpenPendingStore(web3.WalletDir(pm.home))
}

// railFor returns the settlement rail with the per-purchase sender
// applied where the rail supports sender override (RPC + graduated).
// Returns nil when no rail is configured.
func (pm *purchaseMgr) railFor(p *purchase) settlement.Rail {
	base := pm.rail.Load()
	if base == nil || *base == nil {
		return nil
	}
	rail := *base
	if snd := pm.senderFor(p); snd != nil {
		switch r := rail.(type) {
		case *settlement.RPCRail:
			rail = r.WithSender(snd)
		case *settlement.GraduatedRail:
			rail = r.WithSender(snd)
		}
	}
	return rail
}

// runPurchase is the async buy orchestrator: escrow open → bridge dial →
// ACP session → receipt verify → release. Every failure mode leaves the
// purchase in an honest state — escrowed funds become disputable (locked
// until the window lapses, then refundable via withdraw), never silently
// "succeeded".
func runPurchase(ctx context.Context, pm *purchaseMgr, p *purchase) {
	mc := pm.cfg.Load()
	if mc == nil || pm.rail.Load() == nil {
		pm.transition(p, purchaseFailed, "rail_unavailable", "no settlement rail")
		return
	}
	rail := pm.railFor(p)
	if rail == nil {
		pm.transition(p, purchaseFailed, "rail_unavailable", "no settlement rail")
		return
	}

	// 1. Escrow open — the queued sender blocks each tx on approval.
	// Drawdown acquires a funded session instead: fresh sessions open
	// with the configured budget + drawdown flag, reused ones skip the
	// chain entirely.
	pm.transition(p, purchaseEscrowOpening, "", "")
	amount, _ := new(big.Int).SetString(p.Terms.Amount, 10)
	var taskHash [32]byte
	th, _ := hex.DecodeString(strings.TrimPrefix(p.TaskHash, "0x"))
	copy(taskHash[:], th)
	if p.Drawdown {
		ds, fresh, err := pm.drawdownAcquire(ctx, p, rail, amount, mc)
		if err != nil {
			pm.transition(p, purchaseFailed, "escrow_open", err.Error())
			return
		}
		if fresh {
			anchor := sha256.Sum256([]byte("drawdown:" + ds.CorrelationID))
			budget, _ := new(big.Int).SetString(ds.Budget, 10)
			tx, err := rail.Open(ctx, ds.CorrelationID, settlement.Terms{
				Buyer: p.Buyer, Seller: ds.Seller, Token: ds.Token,
				Amount: budget, TaskHash: anchor,
				TerminationTime: ds.Deadline, Drawdown: true,
			})
			if err != nil {
				pm.drawdown.rollbackDraw(ds.SessionID, p.PurchaseID)
				pm.drawdown.markClosed(ds.SessionID, "open_failed")
				pm.transition(p, purchaseFailed, "escrow_open", err.Error())
				return
			}
			if err := pm.drawdown.markOpened(ds.SessionID, tx); err != nil {
				pm.transition(p, purchaseFailed, "escrow_open", err.Error())
				return
			}
			pm.recordTx(p, tx)
		}
		pm.mutate(p, func(pp *purchase) {
			pp.SessionID = ds.SessionID
			pp.CorrelationID = ds.CorrelationID
			pp.Terms.TerminationTime = ds.Deadline
		})
		// Failures below return the reservation to headroom — but a
		// purchase parked in awaiting_release still owes its draw, and a
		// recorded tx makes rollback a no-op anyway.
		defer func() {
			if p.State == purchaseAwaitRelease || p.State == purchaseCompleted {
				return
			}
			pm.drawdown.rollbackDraw(ds.SessionID, p.PurchaseID)
		}()
	} else {
		tx, err := rail.Open(ctx, p.CorrelationID, settlement.Terms{
			Buyer: p.Buyer, Seller: p.Seller, Token: p.Terms.Token,
			Amount: amount, TaskHash: taskHash,
			TerminationTime: p.Terms.TerminationTime,
		})
		if err != nil {
			pm.transition(p, purchaseFailed, "escrow_open", err.Error())
			return
		}
		pm.recordTx(p, tx)
	}

	// 2. Dial + ACP session — wss endpoint (TOFU-pinned) when the advert
	// advertised one, the mesh bridge otherwise.
	dialAddr := p.DialAddr
	if dialAddr == "" {
		dialAddr = p.Provider
	}
	pm.transition(p, purchaseSession, "", "")
	conn, err := pm.dialProvider(dialAddr, p.DialTLSFP)
	if err != nil {
		// Escrow is open — funds are recoverable, not lost: disputable.
		pm.transition(p, purchaseDisputable, "dial_failed", err.Error())
		return
	}
	sessionCtx, cancel := context.WithTimeout(ctx, pm.sessionTimeout())
	res, err := runBuyerSession(sessionCtx, conn, p)
	cancel()
	_ = conn.Close()
	if err != nil {
		pm.transition(p, purchaseDisputable, "session_failed", err.Error())
		return
	}
	sum := sha256.Sum256([]byte(res.ResultText))
	pm.mutate(p, func(pp *purchase) {
		pp.ResultSHA256 = "0x" + hex.EncodeToString(sum[:])
	})

	// 3. Receipt fetch + verify.
	pm.transition(p, purchaseAwaitReceipt, "", "")
	conn, err = pm.dialProvider(dialAddr, p.DialTLSFP)
	if err != nil {
		pm.transition(p, purchaseDisputable, "receipt_dial", err.Error())
		return
	}
	rc, err := fetchReceipt(ctx, conn, p.SessionID, p.PurchaseID)
	_ = conn.Close()
	if err != nil {
		pm.transition(p, purchaseDisputable, "receipt_fetch", err.Error())
		return
	}
	ok, err := verifyPurchaseReceipt(p, rc)
	pm.mutate(p, func(pp *purchase) {
		pp.Receipt = rc
		pp.ReceiptOK = &ok
	})
	switch {
	case err != nil:
		pm.transition(p, purchaseDisputable, "receipt_invalid", err.Error())
		return
	case !ok:
		pm.transition(p, purchaseDisputable, "receipt_unverified",
			"receipt signature/result hash did not verify — funds stay locked")
		return
	}

	// 4. Release (auto by default; awaiting_release when opted out).
	if !mc.buyAutoRelease {
		pm.transition(p, purchaseAwaitRelease, "", "")
		return
	}
	pm.release(ctx, p, rail)
}

// dialProvider picks the transport: a wss/https dial address goes direct
// with the advert's TLS fingerprint (Track 110); anything else routes
// through the mesh bridge.
func (pm *purchaseMgr) dialProvider(addr, tlsFP string) (io.ReadWriteCloser, error) {
	if strings.HasPrefix(addr, "wss://") || strings.HasPrefix(addr, "https://") {
		return pm.dialSecure(addr, tlsFP)
	}
	return pm.dial(addr, acpMarketProtocol)
}

// release submits escrow.release() for a verified purchase — a full
// milestone release per-task, an amount-based partial draw under
// drawdown (paced by draw_interval and recorded in the session ledger).
func (pm *purchaseMgr) release(ctx context.Context, p *purchase, rail settlement.Rail) {
	if p.Drawdown {
		if err := pm.waitDrawSlot(ctx, p.SessionID); err != nil {
			pm.transition(p, purchaseDisputable, "release_failed", err.Error())
			return
		}
		amount, _ := new(big.Int).SetString(p.Terms.Amount, 10)
		tx, err := rail.ReleasePartial(ctx, p.SessionID, amount)
		if err != nil {
			pm.transition(p, purchaseDisputable, "release_failed", err.Error())
			return
		}
		if err := pm.drawdown.recordDraw(p.SessionID, p.PurchaseID, tx); err != nil {
			pm.audit.log("market.drawdown.record_failed", map[string]any{
				"session_id": p.SessionID, "purchase_id": p.PurchaseID,
				"error": err.Error(),
			})
		}
		pm.recordTx(p, tx)
		pm.transition(p, purchaseCompleted, "", "")
		return
	}
	tx, err := rail.Release(ctx, p.SessionID)
	if err != nil {
		pm.transition(p, purchaseDisputable, "release_failed", err.Error())
		return
	}
	pm.recordTx(p, tx)
	pm.transition(p, purchaseCompleted, "", "")
}

// verifyPurchaseReceipt checks the seller's signed receipt against the
// purchase: signature (seller peer pubkey), session binding, result hash,
// and the committed price.
func verifyPurchaseReceipt(p *purchase, rc *receipt) (bool, error) {
	if rc == nil {
		return false, fmt.Errorf("seller returned no receipt")
	}
	if !strings.EqualFold(rc.SessionID, p.SessionID) {
		return false, fmt.Errorf(
			"receipt session_id %s ≠ purchase session %s", rc.SessionID, p.SessionID)
	}
	if p.SellerPeerID != "" && rc.SellerPeerID != p.SellerPeerID {
		return false, fmt.Errorf(
			"receipt seller %s ≠ discovered provider %s", rc.SellerPeerID, p.SellerPeerID)
	}
	if rc.ResultSHA256 == "" || !strings.EqualFold(rc.ResultSHA256, p.ResultSHA256) {
		return false, fmt.Errorf(
			"receipt result_sha256 %s does not match the session output %s",
			rc.ResultSHA256, p.ResultSHA256)
	}
	if rc.OfferID != "" && rc.OfferID != p.OfferID {
		return false, fmt.Errorf("receipt offer %s ≠ purchased %s", rc.OfferID, p.OfferID)
	}
	if rc.Terms.Price != "" && rc.Terms.Price != p.Terms.Amount {
		return false, fmt.Errorf(
			"receipt price %s ≠ committed %s base units", rc.Terms.Price, p.Terms.Amount)
	}
	return VerifyReceipt(rc)
}

// sessionTimeout bounds the prompt wait — seller TTL plus slack for
// receipt minting.
func (pm *purchaseMgr) sessionTimeout() time.Duration {
	if mc := pm.cfg.Load(); mc != nil && mc.sessionTTL > 0 {
		return mc.sessionTTL + 5*time.Minute
	}
	return 35 * time.Minute
}

// dispute freezes the escrow via lock(details) — callable while the
// dispute window is live.
func (pm *purchaseMgr) dispute(ctx context.Context, id, reason string) (*purchase, error) {
	p := pm.lookupAny(id)
	if p == nil {
		return nil, buyErr("not_found", "no purchase %q", id)
	}
	switch p.State {
	case purchaseDisputable, purchaseAwaitRelease, purchaseSession,
		purchaseAwaitReceipt:
	default:
		return nil, buyErr("bad_state",
			"purchase %s is %s — dispute only while funds are locked and unresolved",
			id, p.State)
	}
	rail := pm.railFor(p)
	if rail == nil {
		return nil, buyErr("rail_unavailable", "no settlement rail")
	}
	dsum := sha256.Sum256([]byte("dispute:" + reason))
	var details [32]byte
	copy(details[:], dsum[:])
	tx, err := rail.Dispute(ctx, p.SessionID, details)
	if err != nil {
		return p, fmt.Errorf("dispute: %w", err)
	}
	pm.recordTx(p, tx)
	pm.transition(p, purchaseDisputed, "", reason)
	// A dispute locks the whole shared session on-chain — the ledger
	// stops serving draws on it immediately.
	if p.Drawdown {
		pm.drawdown.markClosed(p.SessionID, "disputed")
	}
	return p, nil
}

// refund calls client-side withdraw() — post-termination only.
func (pm *purchaseMgr) refund(ctx context.Context, id string) (*purchase, error) {
	p := pm.lookupAny(id)
	if p == nil {
		return nil, buyErr("not_found", "no purchase %q", id)
	}
	switch p.State {
	case purchaseDisputable, purchaseAwaitRelease, purchaseFailed:
	default:
		return nil, buyErr("bad_state",
			"purchase %s is %s — refund applies after the dispute window lapses",
			id, p.State)
	}
	rail := pm.railFor(p)
	if rail == nil {
		return nil, buyErr("rail_unavailable", "no settlement rail")
	}
	tx, err := rail.Withdraw(ctx, p.SessionID)
	if err != nil {
		return p, fmt.Errorf("withdraw: %w", err)
	}
	pm.recordTx(p, tx)
	pm.transition(p, purchaseRefunded, "", "")
	// Withdraw returns the shared session's remainder — the ledger's
	// close marker matches the on-chain end state.
	if p.Drawdown {
		pm.drawdown.markClosed(p.SessionID, "closed")
	}
	return p, nil
}

// releaseByID is the manual release path for buy_auto_release=false
// purchases — the operator signs off after inspecting the receipt.
func (pm *purchaseMgr) releaseByID(ctx context.Context, id string) (*purchase, error) {
	p := pm.lookupAny(id)
	if p == nil {
		return nil, buyErr("not_found", "no purchase %q", id)
	}
	if p.State != purchaseAwaitRelease {
		return nil, buyErr("bad_state",
			"purchase %s is %s — manual release applies to awaiting_release only",
			id, p.State)
	}
	rail := pm.railFor(p)
	if rail == nil {
		return nil, buyErr("rail_unavailable", "no settlement rail")
	}
	pm.release(ctx, p, rail)
	return p, nil
}
