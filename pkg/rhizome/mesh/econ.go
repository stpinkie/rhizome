package mesh

import (
	"crypto/ed25519"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
)

// This file carries the paired-settlement operator verbs (Track 141): the
// CLI's daemon-path mutations and the gateway /network/economy handler
// both land here, so daemonless writes and daemon writes take the same
// transition, audit, and event path.

// EconomyLedger exposes the bilateral ledger for read views and gateway
// handlers. Nil-safe — returns nil when the ledger failed to open at
// NewMesh time (the mesh still serves; economy simply does not book).
func (m *Mesh) EconomyLedger() *econ.Ledger {
	if m == nil {
		return nil
	}
	return m.econLedger
}

// EconomyDispute moves every accrued entry matching taskID to disputed
// with the operator's reason attached. Entries in either direction are
// disputable — a caller contests a payable, a callee declines a receivable
// it does not intend to collect (resolve --drop then writes it off).
func (m *Mesh) EconomyDispute(taskID, reason string) ([]econ.Entry, error) {
	if m == nil || m.econLedger == nil {
		return nil, fmt.Errorf("economy ledger unavailable")
	}
	out, err := econ.DisputeTask(m.econLedger, taskID, reason)
	if err != nil {
		return nil, err
	}
	m.publishMeshEvent(runtimeevents.KindMeshEconDispute, map[string]any{
		"task_id":   taskID,
		"entry_ids": entryIDs(out),
		"peer_id":   out[0].PeerID,
		"reason":    reason,
	})
	return out, nil
}

// EconomyResolve closes a dispute: credit re-accrues the entries (the
// charge stands), drop writes them off permanently. Written-off entries
// stay in the file — the trail is append-only — they just stop counting.
func (m *Mesh) EconomyResolve(taskID string, drop bool) ([]econ.Entry, error) {
	if m == nil || m.econLedger == nil {
		return nil, fmt.Errorf("economy ledger unavailable")
	}
	action := "credit"
	if drop {
		action = "drop"
	}
	out, err := econ.ResolveTask(m.econLedger, taskID, drop)
	if err != nil {
		return nil, err
	}
	m.publishMeshEvent(runtimeevents.KindMeshEconResolve, map[string]any{
		"task_id":   taskID,
		"entry_ids": entryIDs(out),
		"peer_id":   out[0].PeerID,
		"action":    action,
	})
	return out, nil
}

// EconomyMarkSettled writes the --mark-only settle marker: the node's
// accrued payable entries for peerID transition to settled under a signed
// local offer id. The payee is not contacted — the marker attests the
// operator settled out of band, and the documented divergence risk stands
// (the peer's receivable side still shows accrued until it settles on its
// own or the Track 142 handshake lands). Returns the attestation offer
// plus the settled records.
func (m *Mesh) EconomyMarkSettled(peerID string, unit string) (*econ.SettleOffer, []econ.Entry, error) {
	if m == nil || m.econLedger == nil {
		return nil, nil, fmt.Errorf("economy ledger unavailable")
	}
	if _, err := peer.Decode(peerID); err != nil {
		return nil, nil, fmt.Errorf("invalid peer id %q", peerID)
	}
	var priv ed25519.PrivateKey
	if m.id != nil {
		priv = m.id.PrivateKey
	}
	offer, out, err := econ.MarkSettled(m.econLedger, m.selfID(), peerID, unit, priv)
	if err != nil {
		return nil, nil, err
	}
	m.publishMeshEvent(runtimeevents.KindMeshEconSettle, map[string]any{
		"peer_id":   peerID,
		"unit":      unit,
		"entry_ids": entryIDs(out),
		"settle_id": offer.ID(),
		"mark_only": true,
	})
	return offer, out, nil
}

// selfID returns the local peer id string ("" when the node is down).
func (m *Mesh) selfID() string {
	if m.node != nil {
		return m.node.PeerID()
	}
	if m.id != nil {
		return m.id.PeerID
	}
	return ""
}

func entryIDs(entries []econ.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.EntryID)
	}
	return out
}
