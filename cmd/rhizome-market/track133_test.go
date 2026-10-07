// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 133: buy-side depth — redundant fan-out (`market buy
// --redundant N`) and the local purchase ledger (`market sessions`).

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// journalSellerID writes an advert for an arbitrary peer id into the
// buyer's journal — the multi-provider variant of journalSeller.
func (f *buyFixture) journalSellerID(t *testing.T, peerID string) {
	t.Helper()
	advJSON, err := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: peerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Payout:    &advertPayout{Address: testSeller, Asset: "USDC", ChainID: 11155111},
		Offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet: priceSheet{
				PerTask: "5", Asset: "USDC", ChainID: 11155111,
			},
		}},
		Escrow: &advertEscrow{Posture: "fixture"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := peeradverts.Record(f.home, peerID, true,
		map[string]json.RawMessage{moduleID: advJSON}); err != nil {
		t.Fatalf("journal: %v", err)
	}
}

func TestTrack133_RedundantGuards(t *testing.T) {
	f := newBuyFixture(t, nil)

	if _, _, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 1, Query: "offer", Task: "t",
	}); err == nil {
		t.Fatal("n=1 must refuse")
	}
	if _, _, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: redundantMaxN + 1, Query: "offer", Task: "t",
	}); err == nil {
		t.Fatal("over-max n must refuse")
	}
	if _, _, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 2, Query: "offer",
	}); err == nil {
		t.Fatal("empty task must refuse")
	}
	if _, _, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 2, Query: "nothing-here", Task: "t",
	}); err == nil {
		t.Fatal("no candidates must fail")
	}
}

// noSpawn parks the orchestration goroutine — the tests exercise the
// fan-out bookkeeping, not the async lifecycle (which races TempDir
// cleanup).
func noSpawn(pm *purchaseMgr) {
	pm.spawn = func(context.Context, *purchaseMgr, *purchase) {}
}

func TestTrack133_RedundantFanOut(t *testing.T) {
	f := newBuyFixture(t, nil)
	noSpawn(f.pm)
	other := testutil.NewIdentity(t).PeerID
	f.journalSeller(t)
	f.journalSellerID(t, other)

	group, branches, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 4, Query: "offer", Task: "summarize the timeline",
	})
	if err != nil {
		t.Fatalf("beginRedundant: %v", err)
	}
	if group == "" || len(branches) != 2 {
		t.Fatalf("group=%q branches=%v", group, branches)
	}
	// Both branches bought (fixture dial serves any peer); each carries
	// the shared group id for `market sessions` comparison.
	for _, b := range branches {
		if b.Error != "" || b.PurchaseID == "" {
			t.Fatalf("branch failed: %+v", b)
		}
		if b.Purchase == nil || b.Purchase.RedundantGroup != group {
			t.Fatalf("branch group: %+v", b.Purchase)
		}
	}
	// Self-exclusion: our own peer id never becomes a branch.
	if id := f.pm.ident.Load(); id != nil {
		for _, b := range branches {
			if b.PeerID == id.PeerID {
				t.Fatal("self became a redundant branch")
			}
		}
	}
}

func TestTrack133_RedundantPartialFailure(t *testing.T) {
	f := newBuyFixture(t, nil)
	noSpawn(f.pm)
	f.journalSeller(t)
	// A second peer with an unbuyable advert — its branch records the
	// refusal while the good peer still buys.
	badID := testutil.NewIdentity(t).PeerID
	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: badID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		// No payout — buy() refuses provider_unsettled.
		Offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet:   priceSheet{PerTask: "5", Asset: "USDC"},
		}},
	})
	if err := peeradverts.Record(f.home, badID, true,
		map[string]json.RawMessage{moduleID: advJSON}); err != nil {
		t.Fatal(err)
	}

	_, branches, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 2, Query: "offer", Task: "t",
	})
	if err != nil {
		t.Fatalf("partial failure should not fail the batch: %v", err)
	}
	var ok, failed int
	for _, b := range branches {
		if b.Error != "" {
			failed++
		} else {
			ok++
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("branches ok=%d failed=%d: %+v", ok, failed, branches)
	}
}

func TestTrack133_SessionsLedger(t *testing.T) {
	f := newBuyFixture(t, func(mc *marketConfig) {
		mc.buyMaxCostPerTask = "10"
		mc.buyMaxCostPerDay = "20"
	})
	noSpawn(f.pm)
	f.journalSeller(t)
	f.journalSellerID(t, testutil.NewIdentity(t).PeerID)

	if _, _, err := f.pm.beginRedundant(context.Background(), buyRequest{
		Redundant: 2, Query: "offer", Task: "t",
	}); err != nil {
		t.Fatal(err)
	}
	// Terminal record — exercises the --all gate.
	done := &purchase{
		PurchaseID: "pur-old", State: purchaseCompleted,
		Price: "1", Asset: "USDC", OfferID: "offer-1",
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	}
	f.pm.register(done)

	rows, _ := f.pm.listSessions(false)
	if len(rows) != 2 {
		t.Fatalf("live rows = %d, want 2 (terminal filtered)", len(rows))
	}
	rows, spend := f.pm.listSessions(true)
	if len(rows) != 3 {
		t.Fatalf("all rows = %d, want 3", len(rows))
	}
	// Newest-first ordering.
	if !rows[0].CreatedAt.After(rows[len(rows)-1].CreatedAt) {
		t.Fatal("rows not newest-first")
	}
	// Spend report: caps + per-asset committed spend + headroom.
	if spend["cap_per_day"] != "20" || spend["cap_per_task"] != "10" {
		t.Fatalf("spend caps: %v", spend)
	}
	assets, _ := spend["assets"].(map[string]any)
	usdc, _ := assets["USDC"].(map[string]any)
	if usdc == nil || usdc["spent_24h"] == nil || usdc["headroom"] == nil {
		t.Fatalf("spend assets: %v", assets)
	}
	// Grouped rows carry the batch id for comparison.
	var grouped int
	for _, r := range rows {
		if r.RedundantGroup != "" {
			grouped++
		}
	}
	if grouped != 2 {
		t.Fatalf("grouped rows = %d, want 2", grouped)
	}
}
