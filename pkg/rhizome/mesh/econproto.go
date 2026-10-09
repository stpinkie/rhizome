package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

// EconProtocolID is the libp2p protocol for paired-settlement between
// trusted peers (Track 142): a signed settle_offer both ledgers converge
// on, plus a read-only ledger_query for divergence reconciliation.
const EconProtocolID = protocol.ID("/rhizome/econ/1.0.0")

const (
	econFrameRequest  = byte(1)
	econFrameResponse = byte(2)
)

// econRequest is the signed control envelope — the same nonce/timestamp/
// signature posture as the skill protocol.
type econRequest struct {
	Op string `json:"op"` // "settle_offer" | "ledger_query"

	// settle_offer carries the signed econ.SettleOffer document.
	Offer *econ.SettleOffer `json:"offer,omitempty"`

	// ledger_query params.
	Unit  string `json:"unit,omitempty"`
	Since int64  `json:"since,omitempty"` // unix seconds; 0 = all

	Nonce     string `json:"nonce,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// econResponse answers the request. Kind distinguishes settle_ack /
// settle_reject / ledger_view.
type econResponse struct {
	OK    bool   `json:"ok"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error,omitempty"`

	// settle_ack payload.
	SettleID string `json:"settle_id,omitempty"`
	Accepted bool   `json:"accepted,omitempty"`

	// settle_reject payload (also carried in Error).
	Reason string `json:"reason,omitempty"`

	// ledger_view payload.
	EntriesDigest string             `json:"entries_digest,omitempty"`
	Balance       []econ.UnitBalance `json:"balance,omitempty"`

	Signature []byte `json:"signature,omitempty"`
}

// startEcon registers the economy protocol handler. Called from Start; the
// handler additionally gates on mesh.economy.enabled per request.
func (m *Mesh) startEcon() {
	m.host.SetStreamHandler(EconProtocolID, m.handleEconStream)
}

// econCall sends a signed economy request and returns the signed response.
func (m *Mesh) econCall(ctx context.Context, pid peer.ID, req econRequest) (econResponse, error) {
	if !m.isTrusted(pid) {
		return econResponse{}, fmt.Errorf("peer %s is not trusted", pid)
	}
	req.Nonce = newTaskNonce()
	req.Timestamp = time.Now().Unix()
	req.Signature = nil
	payload, err := json.Marshal(req)
	if err != nil {
		return econResponse{}, fmt.Errorf("encode economy request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)

	s, err := p2putil.OpenProtocolStream(ctx, m.host, pid, EconProtocolID, 15*time.Second)
	if err != nil {
		return econResponse{}, fmt.Errorf("peer %s does not support %s: %w", pid, EconProtocolID, err)
	}
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer func() { _ = rc.Close() }()

	data, err := json.Marshal(req)
	if err != nil {
		return econResponse{}, fmt.Errorf("encode economy request: %w", err)
	}
	if err = rc.WriteFrame(econFrameRequest, data); err != nil {
		return econResponse{}, fmt.Errorf("write economy request: %w", err)
	}
	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != econFrameResponse {
		return econResponse{}, fmt.Errorf("read economy response: %w", err)
	}
	var resp econResponse
	if err = json.Unmarshal(raw, &resp); err != nil {
		return econResponse{}, fmt.Errorf("decode economy response: %w", err)
	}
	sig := resp.Signature
	resp.Signature = nil
	payload, err = json.Marshal(resp)
	if err != nil {
		return econResponse{}, fmt.Errorf("encode economy response: %w", err)
	}
	pub := m.host.Peerstore().PubKey(pid)
	if pub == nil {
		return econResponse{}, fmt.Errorf("no public key for peer %s", pid)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil || !ok {
		return econResponse{}, fmt.Errorf("invalid economy response signature")
	}
	return resp, nil
}

// handleEconStream services one inbound economy request.
func (m *Mesh) handleEconStream(s network.Stream) {
	serveSignedStream(m, s, econFrameRequest, econFrameResponse,
		m.handleEconRequest, func(r *econResponse) *[]byte { return &r.Signature })
}

// handleEconRequest authorizes and dispatches an economy request.
func (m *Mesh) handleEconRequest(from peer.ID, req econRequest) econResponse {
	started := time.Now()
	reject := func(msg string) econResponse {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "econ." + req.Op,
			"error":   msg,
			"peer_id": from.String(),
		})
		m.auditMesh(from, "econ."+req.Op, "", "", "rejected", started, msg)
		return econResponse{OK: false, Error: msg}
	}

	if !m.isTrusted(from) {
		return reject("peer is not trusted")
	}
	if !m.cfg.Economy.Enabled {
		return reject("economy is not enabled")
	}
	if err := m.replay.check(from, req.Nonce, req.Timestamp); err != nil {
		return reject(fmt.Sprintf("replay check failed: %v", err))
	}
	if !m.allowRate(from) {
		return reject("rate_limited: economy request rate exceeded")
	}

	sig := req.Signature
	req.Signature = nil
	payload, err := json.Marshal(req)
	if err != nil {
		return reject("encode request")
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return reject("no public key for peer")
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil || !ok {
		return reject("invalid signature")
	}

	switch req.Op {
	case "settle_offer":
		return m.handleSettleOffer(from, req.Offer, started)
	case "ledger_query":
		return m.handleLedgerQuery(from, req, started)
	default:
		return reject(fmt.Sprintf("unknown op %q", req.Op))
	}
}

// handleSettleOffer verifies an inbound settle_offer: signature, identity
// binding (issuer == stream peer, peer_id == us), offer-level replay
// freshness, then the receivable match. On match, the receivable rows
// transition to settled under the offer's settle_id and the payer gets
// settle_ack; on mismatch, settle_reject + an econ:diverged audit.
func (m *Mesh) handleSettleOffer(from peer.ID, offer *econ.SettleOffer, started time.Time) econResponse {
	reject := func(reason string) econResponse {
		m.auditMesh(from, "econ.settle_offer", "", "", "econ:diverged", started, reason)
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "econ.settle_offer",
			"error":   "econ:diverged: " + reason,
			"peer_id": from.String(),
		})
		return econResponse{OK: true, Kind: "settle_reject", Reason: reason}
	}
	if offer == nil {
		return reject("missing offer")
	}
	if offer.Protocol != "econ/1.0.0" {
		return reject(fmt.Sprintf("unsupported protocol %q", offer.Protocol))
	}
	if offer.Issuer != from.String() {
		return reject("offer issuer does not match stream peer")
	}
	if offer.PeerID != m.selfID() {
		return reject("offer is not addressed to this peer")
	}
	if m.econLedger == nil {
		return reject("economy ledger unavailable")
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return reject("no public key for peer")
	}
	if err := offer.Verify(pub); err != nil {
		return reject(err.Error())
	}
	// Offer-level freshness: the nonce/ts are inside the signature, so a
	// captured offer cannot be re-timestamped. Reuse the mesh replay
	// window against the offer's own clock.
	if skew := time.Since(offer.TS); skew < -5*time.Minute || skew > 10*time.Minute {
		return reject("offer timestamp outside replay window")
	}

	matched, err := econ.MatchReceivables(m.econLedger.Entries(), offer)
	if err != nil {
		return reject(err.Error())
	}
	settleID := offer.ID()
	settled := make([]econ.Entry, 0, len(matched))
	for _, e := range matched {
		rec, err := m.econLedger.Transition(e.EntryID, econ.StateSettled, settleID, econ.HandshakeSettleTX, "")
		if err != nil {
			return reject(err.Error())
		}
		settled = append(settled, *rec)
	}
	m.auditMesh(from, "econ.settle_offer", "", settleID, "ok", started, "")
	m.publishMeshEvent(runtimeevents.KindMeshEconSettle, map[string]any{
		"peer_id":   from.String(),
		"unit":      offer.Unit,
		"entry_ids": entryIDs(settled),
		"settle_id": settleID,
		"mark_only": false,
		"direction": "receivable",
		"total":     offer.Total,
	})
	return econResponse{OK: true, Kind: "settle_ack", Accepted: true, SettleID: settleID}
}

// handleLedgerQuery answers a read-only ledger view: the entries the peer
// is a party to (either direction), filtered by unit/since, as a digest +
// per-unit balance summary for divergence reconciliation.
func (m *Mesh) handleLedgerQuery(from peer.ID, req econRequest, started time.Time) econResponse {
	if m.econLedger == nil {
		return econResponse{OK: false, Error: "economy ledger unavailable"}
	}
	var since time.Time
	if req.Since != 0 {
		since = time.Unix(req.Since, 0)
	}
	var view []econ.Entry
	for _, e := range m.econLedger.Entries() {
		if e.PeerID != from.String() {
			continue
		}
		if req.Unit != "" && e.Unit != req.Unit {
			continue
		}
		if !since.IsZero() && e.TS.Before(since) {
			continue
		}
		view = append(view, e)
	}
	balance := m.econLedger.Balance(from.String())
	if req.Unit != "" {
		kept := balance[:0]
		for _, b := range balance {
			if b.Unit == req.Unit {
				kept = append(kept, b)
			}
		}
		balance = kept
	}
	m.auditMesh(from, "econ.ledger_query", "", "", "ok", started, "")
	return econResponse{
		OK:            true,
		Kind:          "ledger_view",
		EntriesDigest: econ.EntriesDigest(view),
		Balance:       balance,
	}
}

// EconomyLedgerQuery asks a trusted peer for its ledger view of this
// node — entries digest + per-unit balances — so an operator can compare
// the payee's receivable side against the local payable side after a
// settle_reject (econ:diverged).
func (m *Mesh) EconomyLedgerQuery(ctx context.Context, peerID, unit string, since time.Time) (econResponse, error) {
	if m == nil || m.econLedger == nil {
		return econResponse{}, fmt.Errorf("economy ledger unavailable")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return econResponse{}, fmt.Errorf("invalid peer id %q", peerID)
	}
	var sinceUnix int64
	if !since.IsZero() {
		sinceUnix = since.Unix()
	}
	return m.econCall(ctx, pid, econRequest{Op: "ledger_query", Unit: unit, Since: sinceUnix})
}

// EconomySettle runs the payer side of the handshake: build a signed offer
// over the accrued payable entries for peerID/unit, deliver it, and on
// settle_ack transition the payables to settled under the shared
// settle_id. A settle_reject surfaces as an error carrying the peer's
// divergence reason — reconcile via `economy ledger` / ledger_query.
func (m *Mesh) EconomySettle(ctx context.Context, peerID, unit string) (*econ.SettleOffer, []econ.Entry, error) {
	if m == nil || m.econLedger == nil {
		return nil, nil, fmt.Errorf("economy ledger unavailable")
	}
	if !m.cfg.Economy.Enabled {
		return nil, nil, fmt.Errorf("economy is not enabled (mesh.economy.enabled)")
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid peer id %q", peerID)
	}
	started := time.Now()

	entries := m.econLedger.Entries()
	if unit == "" {
		units := map[string]bool{}
		for _, e := range entries {
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
			return nil, nil, fmt.Errorf(
				"peer %s owes in multiple units %v — settle one unit at a time (--unit)", peerID, list)
		case len(units) == 1:
			for u := range units {
				unit = u
			}
		}
	}
	offer, err := econ.BuildOffer(m.selfID(), peerID, unit, entries, time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	if offer == nil {
		return nil, nil, fmt.Errorf("no accrued payable balance for peer %s", peerID)
	}
	if err := offer.Sign(m.id.PrivateKey); err != nil {
		return nil, nil, err
	}

	resp, err := m.econCall(ctx, pid, econRequest{Op: "settle_offer", Offer: offer})
	if err != nil {
		m.auditMesh(pid, "econ.settle_offer.req", "", "", "error", started, err.Error())
		return nil, nil, err
	}
	switch {
	case resp.OK && resp.Kind == "settle_ack" && resp.Accepted:
		if resp.SettleID != offer.ID() {
			return nil, nil, fmt.Errorf("ack settle_id mismatch: got %s want %s", resp.SettleID, offer.ID())
		}
	case resp.Kind == "settle_reject":
		m.auditMesh(pid, "econ.settle_offer.req", "", offer.ID(), "econ:diverged", started, resp.Reason)
		return nil, nil, fmt.Errorf("settle rejected (econ:diverged): %s", resp.Reason)
	default:
		return nil, nil, fmt.Errorf("settle offer rejected: %s", resp.Error)
	}

	settleID := offer.ID()
	settled := make([]econ.Entry, 0, len(offer.Entries))
	for _, e := range entries {
		if e.PeerID != peerID || e.Unit != offer.Unit ||
			e.Direction != econ.DirectionPayable || e.State != econ.StateAccrued ||
			e.TS.After(offer.Cutoff) {
			continue
		}
		rec, err := m.econLedger.Transition(e.EntryID, econ.StateSettled, settleID, econ.HandshakeSettleTX, "")
		if err != nil {
			return offer, settled, err
		}
		settled = append(settled, *rec)
	}
	m.auditMesh(pid, "econ.settle_offer.req", "", settleID, "ok", started, "")
	m.publishMeshEvent(runtimeevents.KindMeshEconSettle, map[string]any{
		"peer_id":   peerID,
		"unit":      offer.Unit,
		"entry_ids": entryIDs(settled),
		"settle_id": settleID,
		"mark_only": false,
		"direction": "payable",
		"total":     offer.Total,
	})
	return offer, settled, nil
}
