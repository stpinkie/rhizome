// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// stubDHTFind installs a canned dhtFind for the test's duration.
func stubDHTFind(t *testing.T, peers []bridgePeer, err error) {
	t.Helper()
	orig := dhtFind
	dhtFind = func(string) ([]bridgePeer, error) { return peers, err }
	t.Cleanup(func() { dhtFind = orig })
}

// TestFind_DHTUnvettedRows: DHT-discovered peers emit rows marked
// source=dht + unvetted — journal detail surfaces when held, and the
// reachability-only row still lands for an empty query.
func TestFind_DHTUnvettedRows(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t) // journaled advert gives the DHT peer offer detail
	f.mc.dhtEnabled = true

	ghostID := testutil.NewIdentity(t).PeerID
	stubDHTFind(t, []bridgePeer{
		{ID: f.sellerID, Addrs: []string{"/ip4/10.0.0.1/tcp/4001"}},
		{ID: ghostID, Addrs: []string{"/ip4/10.0.0.2/tcp/4001"}},
	}, nil)

	out, err := f.pm.runFind(context.Background(), "")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	m, _ := out.(map[string]any)
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	// Row 1: seller with journaled offer detail, unvetted.
	var sellerRow, ghostRow *findRow
	for i := range rows {
		switch rows[i].PeerID {
		case f.sellerID:
			sellerRow = &rows[i]
		case ghostID:
			ghostRow = &rows[i]
		}
	}
	if sellerRow == nil || sellerRow.Source != "dht" || !sellerRow.Unvetted {
		t.Fatalf("seller row = %+v", sellerRow)
	}
	if sellerRow.OfferID != "offer-1" || sellerRow.PerTask != "5" {
		t.Fatalf("seller row lost journal detail: %+v", sellerRow)
	}
	// Row 2: ghost — no advert on file, reachability only.
	if ghostRow == nil || ghostRow.Source != "dht" || !ghostRow.Unvetted {
		t.Fatalf("ghost row = %+v", ghostRow)
	}
	if ghostRow.OfferID != "" || ghostRow.Multiaddr != "/ip4/10.0.0.2/tcp/4001" {
		t.Fatalf("ghost row = %+v", ghostRow)
	}
}

// TestFind_DHTIndexPrecedence: a peer listed in the curated index is not
// repeated by the DHT tier — and doesn't get its rows flagged unvetted.
func TestFind_DHTIndexPrecedence(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.mc.dhtEnabled = true

	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: f.sellerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: "2099-01-01T00:00:00Z",
		Offers: []offer{{
			ID:           "offer-1",
			AgentBinding: "agent-1",
			PriceSheet:   priceSheet{PerTask: "5", Asset: "USDC", ChainID: 11155111},
		}},
	})
	idx := &marketIndex{
		V: 1, Seq: 1,
		UpdatedAt: "2099-01-01T00:00:00Z",
		Providers: []indexProvider{{
			PeerID: f.sellerID, Advert: advJSON,
		}},
	}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey

	// The DHT also sees the seller — the index tier must dedup it.
	stubDHTFind(t, []bridgePeer{{ID: f.sellerID}}, nil)

	out, err := f.pm.runFind(context.Background(), "")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	m, _ := out.(map[string]any)
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want 1 (index row only)", rows)
	}
	if rows[0].Source != "index" || rows[0].Unvetted {
		t.Fatalf("row = %+v, want index-listed & vetted", rows[0])
	}
}

// TestFind_DHTErrorDegrades: a DHT failure doesn't lose the journal tier
// or error the whole find — discovery must degrade cleanly.
func TestFind_DHTErrorDegrades(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.journalSeller(t)
	f.mc.dhtEnabled = true
	stubDHTFind(t, nil, fmt.Errorf("dht: no peers in table"))

	out, err := f.pm.runFind(context.Background(), "")
	if err != nil {
		t.Fatalf("find with broken DHT: %v", err)
	}
	m, _ := out.(map[string]any)
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 1 || rows[0].Source != "peers" || rows[0].Unvetted {
		t.Fatalf("rows = %v, want 1 journaled vetted row", rows)
	}
}

// TestFind_DHTQueryFilter: a non-empty query suppresses reachability
// rows — an addrs-only peer can't claim to match an offer it never
// described.
func TestFind_DHTQueryFilter(t *testing.T) {
	f := newBuyFixture(t, nil)
	f.mc.dhtEnabled = true
	ghostID := testutil.NewIdentity(t).PeerID
	stubDHTFind(t, []bridgePeer{{ID: ghostID}}, nil)

	out, err := f.pm.runFind(context.Background(), "offer-that-exists-nowhere")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	m, _ := out.(map[string]any)
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 0 {
		t.Fatalf("query-matched rows = %v, want none", rows)
	}
}

// TestConfig_MarketDHT: market_dht parses as a bool field, defaulting
// off — the unvetted tier is strictly opt-in.
func TestConfig_MarketDHT(t *testing.T) {
	f := newBuyFixture(t, nil)
	if f.mc.dhtEnabled {
		t.Fatal("market_dht default should be off")
	}
	mc := loadMarketConfig(cfgWith(t,
		map[string]string{"market_dht": "true"}, nil), t.TempDir())
	if !mc.dhtEnabled {
		t.Fatal("market_dht=true not parsed")
	}
	mc = loadMarketConfig(cfgWith(t, nil, nil), t.TempDir())
	if mc.dhtEnabled {
		t.Fatal("market_dht unset should be off")
	}
}
