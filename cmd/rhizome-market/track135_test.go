package main

import (
	"context"
	"encoding/json"
	"testing"
)

// Track 135 — market dashboard API depth: /v1/sessions carries the
// buy ledger (133), live sell sessions, and dispute-state purchases so
// the web panel renders the whole market in one call.

func TestTrack135_SellSessionsView(t *testing.T) {
	f := newGateFixture(t, 4)
	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	rows := f.mgr.sellSessions()
	if len(rows) != 1 {
		t.Fatalf("sellSessions = %d, want 1", len(rows))
	}
	v := rows[0]
	if v.SessionID != s.ID || v.EscrowID != s.EscrowID {
		t.Fatalf("ids = %s/%s", v.SessionID, v.EscrowID)
	}
	if v.Peer != "12D3peer-buyer" || v.OfferID != "offer-1" {
		t.Fatalf("peer/offer = %s/%s", v.Peer, v.OfferID)
	}
	if v.State != sessionOpen {
		t.Fatalf("state = %s", v.State)
	}
	if v.TermsHash != sessionTermsHash(s) {
		t.Fatalf("terms_hash = %s, want %s", v.TermsHash, sessionTermsHash(s))
	}
	if v.Amount != "5000000" || v.Token != fixtureToken {
		t.Fatalf("terms = %s %s", v.Amount, v.Token)
	}
	if v.Drawdown {
		t.Fatal("per-task session reported as drawdown")
	}

	// Terminal sessions unregister — the live view drops them.
	s.Peer = ""
	f.mgr.finish(s, sessionFailed, "done")
	if rows := f.mgr.sellSessions(); len(rows) != 0 {
		t.Fatalf("sellSessions after finish = %d", len(rows))
	}
}

func TestTrack135_DisputeRows(t *testing.T) {
	dir := t.TempDir()
	pm := newPurchaseMgr(dir, "", newAuditLogger(""))
	for _, p := range []*purchase{
		{
			PurchaseID: "p-disputed", Provider: "prov", State: purchaseDisputed,
			SessionID: "0xdead", Price: "5", Asset: "USDC",
		},
		{
			PurchaseID: "p-resolved", Provider: "prov", State: purchaseResolved,
			SessionID: "0xbeef", Price: "5", Asset: "USDC",
		},
		{
			PurchaseID: "p-clean", Provider: "prov2", State: purchaseSession,
			Price: "5", Asset: "USDC",
		},
	} {
		pm.byID[p.PurchaseID] = p
	}

	d := pm.disputeRows()
	if len(d) != 2 {
		t.Fatalf("disputeRows = %d, want 2: %+v", len(d), d)
	}
	seen := map[string]bool{}
	for _, r := range d {
		seen[r.PurchaseID] = true
	}
	if !seen["p-disputed"] || !seen["p-resolved"] {
		t.Fatalf("dispute set = %v", seen)
	}
	if seen["p-clean"] {
		t.Fatal("non-dispute purchase leaked into disputes")
	}
}

func TestTrack135_SessionsResponseShape(t *testing.T) {
	f := newGateFixture(t, 4)
	s, err := gateSessionOpen(
		context.Background(), f.mgr, "12D3peer-buyer", 1, f.presentation(nil))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}

	dir := t.TempDir()
	pm := newPurchaseMgr(dir, "", newAuditLogger(""))
	pm.byID["p-disputed"] = &purchase{
		PurchaseID: "p-disputed", Provider: "prov",
		State: purchaseDisputed, SessionID: "0xdead",
		Price: "5", Asset: "USDC",
	}
	buyRows, spend := pm.listSessions(true)

	// The handler's response keys — sessions/sell_sessions/disputes/spend.
	body, err := json.Marshal(map[string]any{
		"sessions":      buyRows,
		"sell_sessions": f.mgr.sellSessions(),
		"disputes":      pm.disputeRows(),
		"spend":         spend,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var resp struct {
		Sessions     []sessionRow      `json:"sessions"`
		SellSessions []sellSessionView `json:"sell_sessions"`
		Disputes     []sessionRow      `json:"disputes"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("shape: %v", err)
	}
	if len(resp.SellSessions) != 1 || resp.SellSessions[0].SessionID != s.ID {
		t.Fatalf("sell_sessions = %+v", resp.SellSessions)
	}
	if len(resp.Disputes) != 1 || resp.Disputes[0].PurchaseID != "p-disputed" {
		t.Fatalf("disputes = %+v", resp.Disputes)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %+v", resp.Sessions)
	}

	s.Peer = ""
	f.mgr.finish(s, sessionFailed, "done")
}
