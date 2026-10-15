package mesh

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// The web3 close-out driver (Track 143): payer-side state machine over
// the in-flight journal, and the payee-side offer verification. The
// handshake stays on /rhizome/econ/1.0.0 — the backend field inside the
// signed offer selects the two-phase path:
//
//	phase 1  offer{backend:web3, payment{...}}      → payee matches
//	         receivables, acks awaiting_tx          → payer pays
//	payment  journal-first, then pending-queue or   → tx hash
//	         wallet-signed broadcast
//	phase 2  offer{same doc, +tx_hash}              → payee verifies
//	         on-chain, settles, acks                → payer settles
//
// The journal is written BEFORE any payment submission so a restart
// resumes the exact pending entry or signed transaction — never a
// second transfer.

// Web3SettleResult is what EconomySettleWeb3 reports to the gateway and
// CLI — a durable state, not just a momentary outcome.
type Web3SettleResult struct {
	Status    string            `json:"status"` // journal state or "confirming"
	PeerID    string            `json:"peer_id"`
	Unit      string            `json:"unit"`
	SettleID  string            `json:"settle_id,omitempty"`
	PendingID string            `json:"pending_id,omitempty"`
	TxHash    string            `json:"tx_hash,omitempty"`
	Offer     *econ.SettleOffer `json:"offer,omitempty"`
	Entries   []econ.Entry      `json:"entries,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// settlePhase2MaxAttempts bounds how many times a broadcast-state record
// retries phase-2 delivery before stalling for operator attention.
const settlePhase2MaxAttempts = 100

// SetEconSettler injects the close-out rail (pkg/rhizome/econweb3) when
// mesh.economy.settle_backend=web3. Must be called before Start; nil
// leaves the web3 backend inert — web3 offers get a clean reject and
// EconomySettleWeb3 an explanatory error.
func (m *Mesh) SetEconSettler(s econ.Settler) {
	m.econSettler = s
}

// peerAdvertisesBackend checks the stored advert's settle_backends —
// the fail-closed signal that the peer understands the close-out rail
// (absent on pre-Track-143 builds, which must never see a web3 offer).
func peerAdvertisesBackend(adv *config.EconAdvert, backend string) bool {
	for _, b := range adv.SettleBackends {
		if b == backend {
			return true
		}
	}
	return false
}

// EconomySettleWeb3 runs the payer side of web3 close-out: build and
// sign a payment-committed offer, run phase 1, then submit the transfer
// through the configured signer — approval queue (async, durable) or
// wallet (synchronous broadcast + phase 2). A live journal record for
// the same (peer, unit) is re-driven instead of double-paying.
func (m *Mesh) EconomySettleWeb3(ctx context.Context, peerID, unit string) (*Web3SettleResult, error) {
	if m == nil || m.econLedger == nil {
		return nil, fmt.Errorf("economy ledger unavailable")
	}
	if !m.cfg.Economy.Enabled {
		return nil, fmt.Errorf("economy is not enabled (mesh.economy.enabled)")
	}
	if m.econSettler == nil {
		return nil, fmt.Errorf(
			"web3 close-out is not configured (mesh.economy.settle_backend=web3 plus tools.web3 endpoint)")
	}
	if m.inflight == nil {
		return nil, fmt.Errorf("settle in-flight journal unavailable")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return nil, fmt.Errorf("invalid peer id %q", peerID)
	}
	unit, err = m.resolveSettleUnit(peerID, unit)
	if err != nil {
		return nil, err
	}

	// A live record for this (peer, unit) already owns the balance —
	// resume it rather than queue a second payment.
	if rec, err := m.inflightRecord(peerID, unit); err == nil && rec != nil {
		return m.resumeInflight(ctx, rec, true)
	}

	adv := m.peerEconAdvert(pid)
	if adv == nil || adv.Payout == nil ||
		adv.Payout.Address == "" || adv.Payout.ChainID == "" || adv.Payout.Asset == "" {
		return nil, fmt.Errorf(
			"peer %s does not advertise a web3 payout address — use --backend ledger", peerID)
	}
	if !peerAdvertisesBackend(adv, "web3") {
		return nil, fmt.Errorf(
			"peer %s does not advertise web3 close-out support — use --backend ledger", peerID)
	}
	from, err := m.econSettler.PayerAddress()
	if err != nil {
		return nil, err
	}
	payment := &econ.SettlePayment{
		ChainID: adv.Payout.ChainID,
		Address: adv.Payout.Address,
		Asset:   adv.Payout.Asset,
		From:    from,
	}
	offer, err := econ.BuildOffer(
		m.selfID(), peerID, unit, m.econLedger.Entries(), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if offer == nil {
		return nil, fmt.Errorf("no accrued payable balance for peer %s", peerID)
	}
	offer.Backend = "web3"
	offer.Payment = payment
	if err := offer.Sign(m.id.PrivateKey); err != nil {
		return nil, err
	}
	rec, owned, err := m.inflight.Acquire(peerID, unit, offer, payment)
	if err != nil {
		return nil, fmt.Errorf("settle journal: %w", err)
	}
	if !owned {
		return m.resumeInflight(ctx, rec, true)
	}
	m.auditMesh(pid, "econ.settle.web3", "", offer.ID(), "offered", time.Now(), "")
	return m.resumeInflight(ctx, rec, true)
}

// inflightRecord returns the live journal record for (peer, unit)
// issued by this node — records from another issuer sharing the home
// (or an attacker-dropped file) are never driven.
func (m *Mesh) inflightRecord(peerID, unit string) (*econ.InflightRecord, error) {
	recs, err := m.inflight.Live()
	if err != nil {
		return nil, err
	}
	key := peerID + "|" + unit
	for _, r := range recs {
		if r.Key == key && r.Offer != nil && r.Offer.Issuer == m.selfID() {
			return r, nil
		}
	}
	return nil, nil
}

// resolveSettleUnit picks the unit to settle — the existing multi-unit
// guard from EconomySettle, factored out for the web3 path.
func (m *Mesh) resolveSettleUnit(peerID, unit string) (string, error) {
	if unit != "" {
		return unit, nil
	}
	units := map[string]bool{}
	for _, e := range m.econLedger.Entries() {
		if e.PeerID == peerID && e.Direction == econ.DirectionPayable &&
			e.State == econ.StateAccrued {
			units[e.Unit] = true
		}
	}
	switch {
	case len(units) > 1:
		list := make([]string, 0, len(units))
		for u := range units {
			list = append(list, u)
		}
		return "", fmt.Errorf(
			"peer %s owes in multiple units %v — settle one unit at a time (--unit)", peerID, list)
	case len(units) == 1:
		for u := range units {
			return u, nil
		}
	}
	return "", fmt.Errorf("no accrued payable balance for peer %s", peerID)
}

// resumeInflight re-drives a live record through its state machine.
// Synchronous steps (offer send, wallet sign, phase-2 ack) run inline;
// human-wait states (awaiting_approval) report their durable status.
func (m *Mesh) resumeInflight(
	ctx context.Context, rec *econ.InflightRecord, report bool,
) (*Web3SettleResult, error) {
	if !m.lockSettleDrive(rec.Key) {
		return resultFor(rec, "settlement drive already in progress"), nil
	}
	defer m.unlockSettleDrive(rec.Key)
	return m.driveInflight(ctx, rec, report)
}

// driveInflight advances the record until it reaches a wait state or a
// terminal state. report=false (the background watcher) swallows
// per-step errors into the journal rather than returning them.
func (m *Mesh) driveInflight(
	ctx context.Context, rec *econ.InflightRecord, report bool,
) (*Web3SettleResult, error) {
	for step := 0; step < 8; step++ {
		advanced, res, err := m.stepInflight(ctx, rec)
		if err != nil {
			if report {
				return nil, err
			}
			return resultFor(rec, ""), nil
		}
		if res != nil {
			return res, nil
		}
		if !advanced {
			return resultFor(rec, ""), nil
		}
		// Reload the record — each step persists a new state. A reload
		// hiccup is non-fatal: the next step re-reads the journal anyway.
		if fresh, ferr := m.inflightRecord(rec.PeerID, rec.Unit); ferr == nil && fresh != nil {
			rec = fresh
		}
	}
	return resultFor(rec, "settlement exceeded step budget — will resume"), nil
}

// stepInflight performs one state transition. advanced=true means the
// record moved and another step should run immediately; res != nil
// means the drive is complete for now.
func (m *Mesh) stepInflight(
	ctx context.Context, rec *econ.InflightRecord,
) (advanced bool, res *Web3SettleResult, err error) {
	// Never drive a record this node didn't issue — a foreign entry
	// sharing the journal file or a tampered write (the watcher applies
	// the same rule in settleDriveStep). The offer is what the signed
	// payment commitment anchors to: nil or another issuer's means there
	// is nothing legitimate to pay.
	if rec.Offer == nil || rec.Offer.Issuer != m.selfID() {
		return false, resultFor(rec,
			"in-flight record was not issued by this node — inspect economy-settle-inflight.json"), nil
	}
	fail := func(state, msg string, cause error) (bool, *Web3SettleResult, error) {
		// Update by journal key, not the record's peer/unit fields —
		// those fields are exactly what the tamper checks below
		// distrust, and a desynced record must still be markable.
		_ = m.inflight.UpdateByKey(rec.Key, func(r *econ.InflightRecord) {
			r.State = state
			r.Error = msg
		})
		// The journal record was persisted by Update; mirror the
		// transition onto the caller-side snapshot so resultFor reports
		// the terminal state, not the pre-step one.
		rec.State, rec.Error = state, msg
		// A rejected offer is a divergence — pending until reconciled.
		// Failed/stalled are local or transport faults, not peer verdicts.
		if state == econ.SettleRejected {
			m.recordEconOutcome(rec.PeerID, OpEconSettle, OutcomeDisputed,
				offerIDFor(rec.Offer), "")
		}
		m.settleEvent(rec, state, map[string]any{"error": msg})
		return false, resultFor(rec, ""), cause
	}
	// The journal keys records peerID|unit — a record whose fields
	// disagree with its key is a tampered/corrupt write: transitions
	// derive the key from the fields, so it could never mutate while
	// staying live — an endless re-drive that also burned signer and
	// RPC calls on every watcher tick. Fail it out terminally instead.
	if rec.Key != rec.PeerID+"|"+rec.Unit {
		return fail(econ.SettleFailed,
			"journal key does not match record peer/unit — economy-settle-inflight.json is tampered; refusing to drive", nil)
	}
	// The issuer string alone is forgeable — a tampered write just names
	// this node. Before any state reaches the signer or the queue, the
	// journaled offer must verify against our own key (it was signed at
	// build), and the stored payment must equal the offer's
	// signature-bound payment — a captured record with a rewritten
	// destination still fails here.
	if m.id == nil || m.id.Libp2pPubKey == nil {
		return fail(econ.SettleFailed, "no identity key to verify the journaled offer", nil)
	}
	if verr := rec.Offer.Verify(m.id.Libp2pPubKey); verr != nil {
		return fail(econ.SettleFailed,
			"journaled offer fails signature verification — economy-settle-inflight.json is tampered; refusing to drive", nil)
	}
	if rec.Offer.Payment == nil || rec.Payment == nil || *rec.Payment != *rec.Offer.Payment {
		return fail(econ.SettleFailed,
			"journaled payment diverges from the signed offer — economy-settle-inflight.json is tampered; refusing to drive", nil)
	}
	pid, err := peer.Decode(rec.PeerID)
	if err != nil {
		// Acquire only journals peer ids this node already decoded —
		// an undecodable id on a live record is a tampered write too.
		return fail(econ.SettleFailed,
			"journal record peer id does not decode — economy-settle-inflight.json is tampered; refusing to drive", err)
	}

	switch rec.State {
	case econ.SettlePaying:
		// Phase 1 acknowledged; the payment step comes next. Signer mode
		// decides whether the transfer goes to the human queue or signs
		// straight out.
		if m.econSettler == nil {
			return fail(econ.SettleFailed, "settle backend no longer configured", nil)
		}
		if m.econSettler.SignerMode() == "wallet" {
			raw, hash, serr := m.econSettler.SignRaw(ctx, rec.Payment, rec.Offer.Total)
			if serr != nil {
				return fail(econ.SettleFailed, serr.Error(), serr)
			}
			if err := m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.State = econ.SettleBroadcast
				r.TxHash = hash
				r.SignedRaw = "0x" + hex.EncodeToString(raw)
			}); err != nil {
				return false, nil, err
			}
			return true, nil, nil
		}
		// Approval mode: mint the queue id into the journal BEFORE
		// submitting so crash-resume knows whether the submit landed.
		pendingID := rec.PendingID
		if pendingID == "" {
			if pendingID, err = m.inflight.MintPendingID(); err != nil {
				return false, nil, err
			}
			if err := m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.PendingID = pendingID
			}); err != nil {
				return false, nil, err
			}
		}
		if err := m.econSettler.Queue(ctx, rec.Payment, rec.Offer.Total, pendingID); err != nil {
			if econ.IsQueueEntryExists(err) {
				// Submit landed before a crash — adopt the entry.
				return true, nil, m.transitionInflight(rec, econ.SettleAwaitingApproval)
			}
			return fail(econ.SettleFailed, err.Error(), err)
		}
		return true, nil, m.transitionInflight(rec, econ.SettleAwaitingApproval)

	case "offered":
		// Phase-1 delivery (or re-delivery after a crash — the payee's
		// phase-1 handler is stateless, so a repeat offer is safe).
		resp, cerr := m.econCall(ctx, pid, econRequest{Op: "settle_offer", Offer: rec.Offer})
		if cerr != nil {
			_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.Attempts++
			})
			if rec.Attempts+1 >= settlePhase2MaxAttempts {
				return fail(econ.SettleStalled, "phase-1 delivery retries exhausted: "+cerr.Error(), cerr)
			}
			return false, resultFor(rec, ""), cerr
		}
		switch {
		case resp.OK && resp.Kind == "settle_ack" && resp.AwaitingTx:
			return true, nil, m.transitionInflight(rec, econ.SettlePaying)
		case resp.Kind == "settle_reject":
			return fail(econ.SettleRejected,
				"peer rejected settle offer: "+resp.Reason,
				fmt.Errorf("settle rejected (econ:diverged): %s", resp.Reason))
		default:
			// An ack without awaiting_tx means the peer treated the
			// web3 offer as a ledger handshake — refuse to let that
			// count as payment.
			return fail(econ.SettleRejected,
				"peer acknowledged without awaiting payment — use --backend ledger",
				fmt.Errorf("peer acknowledged without awaiting payment: %s", resp.Error))
		}

	case econ.SettleAwaitingApproval:
		if m.econSettler == nil {
			return fail(econ.SettleFailed, "settle backend no longer configured", nil)
		}
		hash, done, err := m.econSettler.QueuedTx(rec.PendingID)
		if err != nil && !done {
			// Transient queue read failure — retry, but bound it like
			// the other transport states so a dead store stalls out.
			_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.Attempts++
			})
			if rec.Attempts+1 >= settlePhase2MaxAttempts {
				return fail(econ.SettleStalled, "queue lookup retries exhausted: "+err.Error(), err)
			}
			return false, resultFor(rec, ""), err
		}
		if !done {
			return false, resultFor(rec,
				"awaiting human approval — rhizome web3 approve "+rec.PendingID), nil
		}
		if err != nil {
			return fail(econ.SettleFailed, err.Error(), err)
		}
		if err := m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
			r.State = econ.SettleBroadcast
			r.TxHash = hash
		}); err != nil {
			return false, nil, err
		}
		return true, nil, nil

	case econ.SettleBroadcast:
		// Wallet mode may have crashed between sign and send — the
		// journaled raw re-broadcasts (already-known txs return their
		// hash), then phase 2 delivers the proof.
		if m.econSettler == nil {
			return fail(econ.SettleFailed, "settle backend no longer configured", nil)
		}
		if rec.SignedRaw != "" {
			raw, derr := hex.DecodeString(strings.TrimPrefix(rec.SignedRaw, "0x"))
			if derr != nil {
				return fail(econ.SettleFailed, "journaled raw tx is not hex: "+derr.Error(), derr)
			}
			if _, berr := m.econSettler.Broadcast(ctx, raw); berr != nil {
				_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
					r.Attempts++
				})
				if rec.Attempts+1 >= settlePhase2MaxAttempts {
					return fail(econ.SettleStalled, "broadcast retries exhausted: "+berr.Error(), berr)
				}
				return false, resultFor(rec, ""), nil
			}
		}
		offer2, err := econ.Phase2Offer(rec.Offer, rec.TxHash)
		if err != nil {
			return false, nil, err
		}
		if err := offer2.Sign(m.id.PrivateKey); err != nil {
			return false, nil, err
		}
		resp, cerr := m.econCall(ctx, pid, econRequest{Op: "settle_offer", Offer: offer2})
		if cerr != nil {
			// Transport-level failure — the payee may simply be down.
			_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.Attempts++
			})
			if rec.Attempts+1 >= settlePhase2MaxAttempts {
				return fail(econ.SettleStalled, "phase-2 delivery retries exhausted: "+cerr.Error(), cerr)
			}
			return false, resultFor(rec, ""), nil
		}
		switch {
		case resp.OK && resp.Kind == "settle_ack" && resp.Accepted:
			if resp.SettleID != offer2.ID() {
				return fail(econ.SettleStalled,
					fmt.Sprintf("phase-2 ack settle_id mismatch: got %s want %s",
						resp.SettleID, offer2.ID()),
					fmt.Errorf("phase-2 ack settle_id mismatch"))
			}
			settled, serr := m.settlePayables(rec, offer2)
			if serr != nil {
				return false, nil, serr
			}
			_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.State = econ.SettleSettled
				r.SettleID = offer2.ID()
			})
			m.settleEvent(rec, "settled", map[string]any{
				"settle_id": offer2.ID(), "entry_ids": entryIDs(settled),
			})
			m.recordEconOutcome(rec.PeerID, OpEconSettle, OutcomeCompleted, offer2.ID(),
				econValueHash(offer2.Total, rec.Unit, offer2.ID()))
			m.auditMeshFields(pid, "econ.settle.web3", "", offer2.ID(), "ok", time.Now(), "",
				map[string]any{
					"unit": rec.Unit, "amount": offer2.Total, "tx_hash": rec.TxHash,
					"entry_ids": entryIDs(settled), "outcome": OutcomeCompleted,
				})
			return false, &Web3SettleResult{
				Status: econ.SettleSettled, PeerID: rec.PeerID, Unit: rec.Unit,
				SettleID: offer2.ID(), TxHash: rec.TxHash,
				Offer: offer2, Entries: settled,
			}, nil
		case resp.Kind == "settle_reject":
			return fail(econ.SettleRejected,
				"peer rejected payment proof: "+resp.Reason,
				fmt.Errorf("payment rejected by peer: %s", resp.Reason))
		default:
			// Verification pending (tx not yet mined) or payee-side
			// transient error — the payment is durable, retry phase 2.
			_ = m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
				r.Attempts++
			})
			if rec.Attempts+1 >= settlePhase2MaxAttempts {
				return fail(econ.SettleStalled,
					"phase-2 verification retries exhausted: "+resp.Error,
					fmt.Errorf("phase-2 verification retries exhausted: %s", resp.Error))
			}
			return false, resultFor(rec,
				"payment broadcast, awaiting peer verification (tx "+rec.TxHash+")"), nil
		}
	}
	return false, resultFor(rec, ""), nil
}

func (m *Mesh) transitionInflight(rec *econ.InflightRecord, state string) error {
	return m.inflight.Update(rec.PeerID, rec.Unit, func(r *econ.InflightRecord) {
		r.State = state
	})
}

// settlePayables marks the payer-side payable entries settled under the
// phase-2 settle id, keyed by the broadcast tx hash.
func (m *Mesh) settlePayables(rec *econ.InflightRecord, offer2 *econ.SettleOffer) ([]econ.Entry, error) {
	settled := make([]econ.Entry, 0, len(rec.Offer.Entries))
	for _, e := range m.econLedger.Entries() {
		if e.PeerID != rec.PeerID || e.Unit != rec.Unit ||
			e.Direction != econ.DirectionPayable || e.State != econ.StateAccrued ||
			e.TS.After(rec.Offer.Cutoff) {
			continue
		}
		r, err := m.econLedger.Transition(e.EntryID, econ.StateSettled, offer2.ID(), rec.TxHash, "")
		if err != nil {
			return settled, err
		}
		settled = append(settled, *r)
	}
	return settled, nil
}

func (m *Mesh) settleEvent(rec *econ.InflightRecord, stage string, extra map[string]any) {
	attrs := map[string]any{
		"peer_id": rec.PeerID,
		"unit":    rec.Unit,
		"backend": "web3",
		"stage":   stage,
	}
	if rec.TxHash != "" {
		attrs["tx_hash"] = rec.TxHash
	}
	if rec.PendingID != "" {
		attrs["pending_id"] = rec.PendingID
	}
	for k, v := range extra {
		attrs[k] = v
	}
	kind := runtimeevents.KindMeshEconSettle
	if stage == econ.SettleRejected || stage == econ.SettleFailed || stage == econ.SettleStalled {
		kind = runtimeevents.KindMeshError
	}
	m.publishMeshEvent(kind, attrs)
}

// resultFor renders a record's durable state for the caller.
func resultFor(rec *econ.InflightRecord, note string) *Web3SettleResult {
	res := &Web3SettleResult{
		Status:    rec.State,
		PeerID:    rec.PeerID,
		Unit:      rec.Unit,
		PendingID: rec.PendingID,
		TxHash:    rec.TxHash,
		SettleID:  rec.SettleID,
		Offer:     rec.Offer,
	}
	if res.Status == econ.SettleBroadcast {
		res.Status = "confirming"
	}
	if note != "" {
		res.Note = note
	} else if rec.Error != "" {
		res.Note = rec.Error
	}
	return res
}

// --- drive locking ------------------------------------------------------

func (m *Mesh) lockSettleDrive(key string) bool {
	m.settleDriveMu.Lock()
	defer m.settleDriveMu.Unlock()
	if m.settleDrive[key] {
		return false
	}
	m.settleDrive[key] = true
	return true
}

func (m *Mesh) unlockSettleDrive(key string) {
	m.settleDriveMu.Lock()
	delete(m.settleDrive, key)
	m.settleDriveMu.Unlock()
}

// settleWatchLoop drives live in-flight records until the mesh stops —
// approval resolution, broadcast confirmation, and phase-2 delivery all
// converge without the CLI staying attached.
func (m *Mesh) settleWatchLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.settleDriveStep(ctx)
		}
	}
}

// settleDriveStep drives each live journal record one pass — the
// watcher's unit of work and the test seam for deterministic resumes.
// Without a settler the records wait — an operator wiring the backend
// later resumes them, and a misconfiguration never burns a retry slot.
func (m *Mesh) settleDriveStep(ctx context.Context) {
	if m.econSettler == nil || m.inflight == nil {
		return
	}
	recs, err := m.inflight.Live()
	if err != nil || len(recs) == 0 {
		return
	}
	for _, rec := range recs {
		if rec.Offer == nil || rec.Offer.Issuer != m.selfID() {
			continue // foreign record sharing the journal file
		}
		if !m.lockSettleDrive(rec.Key) {
			continue
		}
		_, _ = m.driveInflight(ctx, rec, false)
		m.unlockSettleDrive(rec.Key)
	}
}

// handleSettleOfferWeb3 is the payee side of web3 close-out. Phase-1
// offers (no tx_hash) are matched against receivables and acknowledged
// with awaiting_tx — entries stay accrued until payment verifies.
// Phase-2 offers (tx_hash set) re-verify the payment commitment against
// this node's own advertised payout, re-match receivables, verify the
// transfer on-chain, then settle — a retryable outcome returns a
// transport error so the payer retries; a hard mismatch is a
// settle_reject divergence.
func (m *Mesh) handleSettleOfferWeb3(from peer.ID, offer *econ.SettleOffer, started time.Time) econResponse {
	reject := func(reason string) econResponse {
		m.recordEconOutcome(from.String(), OpEconSettle, OutcomeDisputed, offerIDFor(offer), "")
		m.auditMesh(from, "econ.settle_offer", "", "", "econ:diverged", started, reason)
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "econ.settle_offer",
			"error":   "econ:diverged: " + reason,
			"peer_id": from.String(),
		})
		return econResponse{OK: true, Kind: "settle_reject", Reason: reason}
	}
	if m.econSettler == nil {
		return reject("web3 close-out is not enabled on this node")
	}
	p := offer.Payment
	if p == nil {
		return reject("web3 offer missing payment commitment")
	}
	mine := m.cfg.Economy.Payout
	if mine == nil {
		return reject("no payout configured on this node")
	}
	if !strings.EqualFold(p.Address, mine.Address) ||
		p.ChainID != mine.ChainID || !strings.EqualFold(p.Asset, mine.Asset) {
		return reject("payment does not match this node's advertised payout")
	}
	if p.From == "" {
		return reject("web3 offer missing payment.from commitment")
	}
	matched, err := econ.MatchReceivables(m.econLedger.Entries(), offer)
	if err != nil {
		if offer.TxHash != "" {
			// Phase-2 redelivery after a lost ack: this node already
			// verified the transfer and settled these receivables under
			// the offer's tx — re-ack idempotently rather than rejecting
			// a settlement that committed. The payer's retry offer is a
			// new document (fresh nonce/ts), so the ack carries this
			// offer's id while our receivable rows keep the original
			// settle_id; both sides converge on settled state under the
			// same tx hash, which is the shared anchor.
			if already, aerr := econ.MatchSettledReceivables(
				m.econLedger.Entries(), offer); aerr == nil && already != nil {
				m.auditMeshFields(from, "econ.settle_offer", "", offer.ID(),
					"ok", started, "", map[string]any{
						"unit": offer.Unit, "amount": offer.Total,
						"tx_hash": offer.TxHash, "entry_ids": entryIDs(already),
						"outcome": OutcomeCompleted, "redelivery": true,
					})
				return econResponse{
					OK: true, Kind: "settle_ack", Accepted: true,
					SettleID: offer.ID(),
				}
			}
		}
		return reject(err.Error())
	}
	if offer.TxHash == "" {
		// Phase 1: receivables verified, payment terms acceptable —
		// hold accrued and await the transfer proof.
		m.auditMesh(from, "econ.settle_offer", "", offer.ID(), "awaiting_tx", started, "")
		return econResponse{
			OK: true, Kind: "settle_ack", Accepted: true,
			AwaitingTx: true, SettleID: offer.ID(),
		}
	}
	// A settlement tx is single-use. The matched receivables are fresh
	// (still accrued), so the hash must be too: a tx that already closed
	// any settled entry belongs to a different entry set — replaying it
	// here would mark new debt paid with no new payment. The legitimate
	// repeat — the identical entry set re-offered after a lost ack —
	// took the MatchSettledReceivables branch above.
	if econ.SettleTXConsumed(m.econLedger.Entries(), offer.TxHash) {
		return reject("payment tx already consumed by a prior settlement")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	verr := m.econSettler.Verify(ctx, p, offer.Total, offer.TxHash)
	switch {
	case verr == nil:
		// verified — settle below
	case econ.IsVerifyPending(verr):
		// Not yet visible/mined — a transport-shaped error so the payer
		// retries phase 2 rather than recording a divergence.
		return econResponse{OK: false, Error: verr.Error()}
	default:
		return reject("payment verification failed: " + verr.Error())
	}
	settleID := offer.ID()
	settled := make([]econ.Entry, 0, len(matched))
	for _, e := range matched {
		rec, err := m.econLedger.Transition(e.EntryID, econ.StateSettled, settleID, offer.TxHash, "")
		if err != nil {
			return econResponse{OK: false, Error: err.Error()}
		}
		settled = append(settled, *rec)
	}
	m.recordEconOutcome(from.String(), OpEconSettle, OutcomeCompleted, settleID,
		econValueHash(offer.Total, offer.Unit, settleID))
	m.auditMeshFields(from, "econ.settle_offer", "", settleID, "ok", started, "",
		map[string]any{
			"unit": offer.Unit, "amount": offer.Total, "tx_hash": offer.TxHash,
			"entry_ids": entryIDs(settled), "outcome": OutcomeCompleted,
		})
	m.publishMeshEvent(runtimeevents.KindMeshEconSettle, map[string]any{
		"peer_id":   from.String(),
		"unit":      offer.Unit,
		"entry_ids": entryIDs(settled),
		"settle_id": settleID,
		"mark_only": false,
		"direction": "receivable",
		"total":     offer.Total,
		"backend":   "web3",
		"tx_hash":   offer.TxHash,
	})
	return econResponse{OK: true, Kind: "settle_ack", Accepted: true, SettleID: settleID}
}
