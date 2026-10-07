// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

const (
	// purchaseDirName holds one JSON record per purchase under the module
	// dir — the buyer-side mirror of receipts/.
	purchaseDirName    = "purchases"
	purchaseMaxRecords = 256
	// reviewTTL bounds how long a review-required purchase waits for
	// confirm_review_id — the token is in-memory only (a module restart
	// honestly refuses stale confirmations).
	reviewTTL     = 10 * time.Minute
	reviewMaxLive = 64
)

// Purchase lifecycle states — a purchase mirrors the escrow session.
const (
	purchasePendingReview = "pending_review"   // export review outstanding
	purchaseQueued        = "queued"           // approved, orchestration running
	purchaseEscrowOpening = "escrow_opening"   // open txs awaiting approval/confirm
	purchaseSession       = "session"          // ACP session live
	purchaseAwaitReceipt  = "awaiting_receipt" // prompt returned, fetching receipt
	purchaseAwaitRelease  = "awaiting_release" // verified; auto-release disabled
	purchaseDisputable    = "disputable"       // verify failed — lock or withdraw
	purchaseCompleted     = "completed"        // released
	purchaseDisputed      = "disputed"         // lock() submitted
	purchaseResolved      = "resolved"         // arbiter resolve() split the escrow
	purchaseRefunded      = "refunded"         // withdraw() after termination
	purchaseFailed        = "failed"           // pre-session failure
)

// purchaseTerms are the on-chain terms the buyer committed — the escrow
// clone's client/token/amount/termination facts.
type purchaseTerms struct {
	Amount          string `json:"amount"`             // base units, decimal (draw amount under drawdown)
	Token           string `json:"token"`              // ERC-20 address
	ChainID         int64  `json:"chain_id"`           // rail chain
	TerminationTime int64  `json:"termination_time"`   // unix; dispute horizon
	Drawdown        bool   `json:"drawdown,omitempty"` // session is a shared funded budget
}

// purchase is the durable buyer-side record of one market buy.
type purchase struct {
	V             int           `json:"v"`
	PurchaseID    string        `json:"purchase_id"`
	Provider      string        `json:"provider"`              // as requested — peer id or /p2p/ multiaddr
	DialAddr      string        `json:"dial_addr,omitempty"`   // resolved dial (index multiaddr, wss:// endpoint, or Provider)
	DialTLSFP     string        `json:"dial_tls_fp,omitempty"` // pinned cert sha256 for wss dials (TOFU)
	SellerPeerID  string        `json:"seller_peer_id"`
	Seller        string        `json:"seller"` // advert payout.address — escrow provider()
	Buyer         string        `json:"buyer"`  // escrow client() — purchaser's EVM address
	OfferID       string        `json:"offer_id"`
	Price         string        `json:"price"` // offer per_task, decimal offer-asset units
	Asset         string        `json:"asset"` // offer price_sheet.asset label
	Task          string        `json:"task"`  // the exact text sent (post-redact)
	TaskHash      string        `json:"task_hash"`
	Attachments   []string      `json:"attachments,omitempty"` // exported resource_link URIs
	SessionID     string        `json:"session_id"`            // escrow clone address / graduated session key
	CorrelationID string        `json:"correlation_id"`
	Drawdown      bool          `json:"drawdown,omitempty"` // draws a shared funded session
	Terms         purchaseTerms `json:"terms"`
	State         string        `json:"state"`
	PendingIDs    []string      `json:"pending_ids,omitempty"`
	TxHashes      []string      `json:"tx_hashes,omitempty"`
	ResultSHA256  string        `json:"result_sha256,omitempty"`
	Receipt       *receipt      `json:"receipt,omitempty"`
	ReceiptOK     *bool         `json:"receipt_verified,omitempty"`
	Attestation   *attestation  `json:"attestation,omitempty"` // issued claim (Track 130)
	// TEEClaim snapshots the seller's advertised TEE posture at buy time
	// (self-attested — `market session` displays it as claimed, Track 131).
	TEEClaim *advertTEE `json:"tee_attestation,omitempty"`
	// RedundantGroup labels a batch of --redundant purchases for grouped
	// result comparison in `market sessions` (Track 133).
	RedundantGroup string    `json:"redundant_group,omitempty"`
	Error          string    `json:"error,omitempty"`
	ErrCode        string    `json:"error_code,omitempty"`
	Settlement     string    `json:"settlement"`            // "fixture" | "configured"
	WatchBlock     uint64    `json:"watch_block,omitempty"` // last scanned block (event watcher)
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// pendingReview binds a confirm_review_id to the prepared purchase — the
// token is the review itself: confirming replays the stored decision, so
// a confirm call can't substitute a different task.
type pendingReview struct {
	purchaseID string
	expiresAt  time.Time
}

// purchaseMgr owns the buyer side: purchases/ persistence, review tokens,
// and the async orchestration goroutines.
type purchaseMgr struct {
	moduleDir string
	home      string
	audit     *auditLogger

	cfg      atomic.Pointer[marketConfig]
	rail     atomic.Pointer[settlement.Rail]
	endpoint atomic.Pointer[web3.Endpoint] // resolved endpoint for configured rails

	// dial is the bridge-dial seam (tests inject net.Pipe plumbing);
	// dialSecure is the wss/TOFU path for advert-advertised endpoints;
	// spawn runs the orchestration goroutine body.
	dial       func(peer, protocol string) (net.Conn, error)
	dialSecure func(endpoint, fingerprint string) (io.ReadWriteCloser, error)
	spawn      func(context.Context, *purchaseMgr, *purchase)
	nowFn      func() time.Time

	// drawdown is the buyer-side session ledger (Track 128) — always
	// non-nil, inert unless escrow_settlement=drawdown.
	drawdown *drawdownLedger

	// ident is the buyer's node key — attestation minting (Track 130)
	// signs with it; nil refuses rather than minting unattributed claims.
	ident atomic.Pointer[identity.Derived]

	mu      sync.Mutex
	byID    map[string]*purchase
	reviews map[string]*pendingReview
}

func newPurchaseMgr(moduleDir, home string, audit *auditLogger) *purchaseMgr {
	pm := &purchaseMgr{
		moduleDir:  moduleDir,
		home:       home,
		audit:      audit,
		dial:       dialPeer,
		dialSecure: dialWSS,
		nowFn:      time.Now,
		byID:       map[string]*purchase{},
		reviews:    map[string]*pendingReview{},
		drawdown:   openDrawdownLedger(moduleDir),
	}
	pm.spawn = runPurchase
	pm.loadAll()
	return pm
}

// setConfig publishes the resolved config + rail + endpoint on each
// (re)load; endpoint is nil under the fixture posture.
func (pm *purchaseMgr) setConfig(
	mc *marketConfig, rail settlement.Rail, ep *web3.Endpoint,
) {
	pm.cfg.Store(mc)
	pm.rail.Store(&rail)
	if ep != nil {
		epc := *ep
		pm.endpoint.Store(&epc)
	} else {
		pm.endpoint.Store(nil)
	}
}

func (pm *purchaseMgr) purchaseDir() string {
	return filepath.Join(pm.moduleDir, purchaseDirName)
}

// loadAll restores persisted purchases at boot — buys in progress when the
// module restarts keep their recorded state; the orchestration does not
// resume mid-flight (the audit + record carry what happened).
func (pm *purchaseMgr) loadAll() {
	dir := pm.purchaseDir()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := readBounded(filepath.Join(dir, e.Name()), 256<<10)
		if err != nil {
			continue
		}
		var p purchase
		if json.Unmarshal(data, &p) != nil || p.PurchaseID == "" {
			continue
		}
		pc := p
		pm.byID[pc.PurchaseID] = &pc
	}
	pm.mu.Lock()
	pm.boundLocked()
	pm.mu.Unlock()
}

func (pm *purchaseMgr) save(p *purchase) error {
	snap := pm.snapshot(p) // marshal a stable copy — other goroutines may mutate
	data, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pm.purchaseDir(), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(
		filepath.Join(pm.purchaseDir(), sanitizeSessionID(p.PurchaseID)+".json"), data)
}

// mutate applies fn to p under pm.mu and persists — for field updates
// that aren't state transitions (result hash, receipt).
func (pm *purchaseMgr) mutate(p *purchase, fn func(*purchase)) {
	pm.mu.Lock()
	fn(p)
	p.UpdatedAt = pm.nowFn().UTC()
	pm.mu.Unlock()
	if err := pm.save(p); err != nil && pm.audit != nil {
		pm.audit.log("market.purchase.persist_failed", map[string]any{
			"purchase_id": p.PurchaseID, "error": err.Error(),
		})
	}
}

// transition mutates the purchase state + timestamps, persists, audits —
// persist failures are audited but never fatal to the in-memory state.
func (pm *purchaseMgr) transition(p *purchase, state, errCode, errMsg string) {
	pm.mu.Lock()
	p.State = state
	p.ErrCode = errCode
	p.Error = errMsg
	p.UpdatedAt = pm.nowFn().UTC()
	pm.mu.Unlock()
	if err := pm.save(p); err != nil && pm.audit != nil {
		pm.audit.log("market.purchase.persist_failed", map[string]any{
			"purchase_id": p.PurchaseID, "error": err.Error(),
		})
	}
	if pm.audit != nil {
		pm.audit.log("market.purchase.state", map[string]any{
			"purchase_id": p.PurchaseID, "state": state,
			"session_id": p.SessionID, "error": errMsg,
		})
	}
	pm.reportOutcome(p)
}

// purchaseOutcome maps a terminal purchase state to the peer_score outcome
// label reported to the daemon — "" means no report (non-terminal).
func purchaseOutcome(state string) string {
	switch state {
	case purchaseCompleted:
		return "completed"
	case purchaseFailed:
		return "failed"
	case purchaseDisputed:
		return "disputed"
	case purchaseResolved:
		return "resolved"
	case purchaseRefunded:
		return "refunded"
	default:
		return ""
	}
}

// reportOutcome records the seller's market outcome in the mesh peer-score
// store via the peer_score bridge action. Best-effort and async — a
// settlement report never blocks or fails a purchase transition.
func (pm *purchaseMgr) reportOutcome(p *purchase) {
	outcome := purchaseOutcome(p.State)
	if outcome == "" || p.SellerPeerID == "" {
		return
	}
	vh := valueHash(p.Terms.Amount, p.Terms.Token, p.TaskHash)
	go func(peer, sid string) {
		if err := reportOutcome(peer, "market_buy", outcome, sid, vh); err != nil && pm.audit != nil {
			pm.audit.log("market.peer_score.failed", map[string]any{
				"purchase_id": p.PurchaseID, "peer": peer,
				"outcome": outcome, "error": err.Error(),
			})
		}
	}(p.SellerPeerID, p.SessionID)
}

// valueHash commits to a session's settled value: sha256 over
// amount|token|taskHash — enough to verify the ref later without the
// payment graph (amounts) ever landing in the peer-score file.
func valueHash(amount, token, taskHash string) string {
	sum := sha256.Sum256([]byte(amount + "|" + token + "|" + taskHash))
	return hex.EncodeToString(sum[:])
}

func (pm *purchaseMgr) lookup(id string) *purchase {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.byID[id]
}

// snapshot returns a copy for the API surface — the orchestration
// goroutine mutates purchases under pm.mu; handlers must not marshal
// the live struct mid-transition.
func (pm *purchaseMgr) snapshot(p *purchase) purchase {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return *p
}

// lookupAny resolves either a purchase id or an escrow/session id — the
// dispute/refund/release verbs take whichever the operator has.
func (pm *purchaseMgr) lookupAny(id string) *purchase {
	if p := pm.lookup(id); p != nil {
		return p
	}
	if l := pm.bySession(id); len(l) > 0 {
		return l[0]
	}
	return nil
}

// bySession finds purchases whose escrow/session id matches (session_id
// is the escrow clone address — usually unique; slice for test doubles).
func (pm *purchaseMgr) bySession(sessionID string) []*purchase {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	var out []*purchase
	for _, p := range pm.byID {
		if p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out
}

func (pm *purchaseMgr) register(p *purchase) {
	pm.mu.Lock()
	pm.byID[p.PurchaseID] = p
	pm.boundLocked()
	pm.mu.Unlock()
}

// boundLocked drops the oldest terminal-state records past the cap (the
// persisted files go with them — the records are small and re-creatable
// from the escrow/receipt chain if ever needed).
func (pm *purchaseMgr) boundLocked() {
	if len(pm.byID) <= purchaseMaxRecords {
		return
	}
	var terms []string
	for id, p := range pm.byID {
		if isTerminalPurchase(p.State) {
			terms = append(terms, id)
		}
	}
	sort.Slice(terms, func(i, j int) bool {
		return pm.byID[terms[i]].UpdatedAt.Before(pm.byID[terms[j]].UpdatedAt)
	})
	for len(pm.byID) > purchaseMaxRecords && len(terms) > 0 {
		victim := terms[0]
		terms = terms[1:]
		delete(pm.byID, victim)
		_ = os.Remove(filepath.Join(
			pm.purchaseDir(), sanitizeSessionID(victim)+".json"))
	}
}

func isTerminalPurchase(state string) bool {
	switch state {
	case purchaseCompleted, purchaseDisputed, purchaseResolved,
		purchaseRefunded, purchaseFailed:
		return true
	}
	return false
}

// randomHex returns n random bytes as lowercase hex — purchase and
// correlation ids (unpredictable, unguessable is not required; unique is).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable for id purposes — but a
		// colliding id is worse than none, so fall back to time+seq.
		return fmt.Sprintf("%x%x", time.Now().UnixNano(), n)
	}
	return hex.EncodeToString(b)
}

// mintReview stores a review token for a pending purchase.
func (pm *purchaseMgr) mintReview(p *purchase) string {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	now := pm.nowFn()
	for id, r := range pm.reviews {
		if now.After(r.expiresAt) {
			delete(pm.reviews, id)
		}
	}
	if len(pm.reviews) >= reviewMaxLive {
		return ""
	}
	id := randomHex(8)
	pm.reviews[id] = &pendingReview{
		purchaseID: p.PurchaseID, expiresAt: now.Add(reviewTTL),
	}
	return id
}

// confirmReview resolves a review token to its purchase; expired/absent
// tokens refuse honestly.
func (pm *purchaseMgr) confirmReview(reviewID string) (*purchase, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	r, ok := pm.reviews[reviewID]
	if !ok {
		return nil, fmt.Errorf("unknown or expired review id %q", reviewID)
	}
	if pm.nowFn().After(r.expiresAt) {
		delete(pm.reviews, reviewID)
		return nil, fmt.Errorf("review %q expired — resubmit the buy", reviewID)
	}
	p := pm.byID[r.purchaseID]
	if p == nil {
		return nil, fmt.Errorf("review %q has no purchase", reviewID)
	}
	if p.State != purchasePendingReview {
		return nil, fmt.Errorf("purchase %s is %s, not pending review", p.PurchaseID, p.State)
	}
	delete(pm.reviews, reviewID)
	return p, nil
}

// recordPending surfaces a queued approval id on the purchase — the
// operator sees exactly which web3-pending.json entry gates the buy.
func (pm *purchaseMgr) recordPending(p *purchase, id string) {
	pm.mu.Lock()
	p.PendingIDs = append(p.PendingIDs, id)
	p.UpdatedAt = pm.nowFn().UTC()
	pm.mu.Unlock()
	_ = pm.save(p)
}

func (pm *purchaseMgr) recordTx(p *purchase, hash string) {
	pm.mu.Lock()
	p.TxHashes = append(p.TxHashes, hash)
	p.UpdatedAt = pm.nowFn().UTC()
	pm.mu.Unlock()
	_ = pm.save(p)
}
