// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// mutableIndexServer serves a signed index whose doc can be swapped mid-
// test under the same curator key (for rollback/expiry scenarios).
func mutableIndexServer(t *testing.T, idx *marketIndex) (set func(*marketIndex), url, pubkeyB64 string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var doc atomic.Value
	marshal := func(i *marketIndex) []byte {
		d, err := json.Marshal(i)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	doc.Store(marshal(idx))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_, _ = w.Write(doc.Load().([]byte))
		case "/index.json.sig":
			sig := ed25519.Sign(priv, doc.Load().([]byte))
			_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return func(i *marketIndex) { doc.Store(marshal(i)) },
		srv.URL, base64.StdEncoding.EncodeToString(pub)
}

// TestFind_IndexSeqRollbackRefused: a freshly fetched index whose seq is
// below the cached seq is a rollback — the older-seq signed doc loses to
// the cache (marked stale).
func TestFind_IndexSeqRollbackRefused(t *testing.T) {
	f := newBuyFixture(t, nil)
	sellerID := testutil.NewIdentity(t)
	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: sellerID.PeerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})

	// Seed cache at seq 5.
	high := &marketIndex{
		V: 1, Seq: 5,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: []indexProvider{{PeerID: sellerID.PeerID, Advert: advJSON}},
	}
	setDoc, url, pubkey := mutableIndexServer(t, high)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey
	if _, _, err := f.pm.fetchIndex(context.Background()); err != nil {
		t.Fatalf("seed fetch: %v", err)
	}

	// Expire the cache TTL so the next call refetches, then serve seq 3.
	cachePath := filepath.Join(f.buyerDir, indexCacheFile)
	c := readIndexCache(cachePath)
	c.FetchedAt = time.Now().Add(-2 * indexCacheTTL)
	data, _ := json.Marshal(c)
	_ = writeFileAtomic(cachePath, data)

	setDoc(&marketIndex{
		V: 1, Seq: 3,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: []indexProvider{},
	})
	got, stale, err := f.pm.fetchIndex(context.Background())
	if err != nil {
		t.Fatalf("rollback fetch: %v", err)
	}
	if !stale {
		t.Fatal("seq regression should serve cached index as stale")
	}
	if got.Seq != 5 {
		t.Fatalf("served seq=%d, want cached 5", got.Seq)
	}
}

// TestFind_IndexExpiryMarksStale: an index past its own expires_at serves
// but marks stale.
func TestFind_IndexExpiryMarksStale(t *testing.T) {
	f := newBuyFixture(t, nil)
	idx := &marketIndex{
		V: 1, Seq: 1,
		UpdatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		ExpiresAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
		Providers: []indexProvider{},
	}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey
	got, stale, err := f.pm.fetchIndex(context.Background())
	if err != nil {
		t.Fatalf("expired fetch: %v", err)
	}
	if !stale || got == nil {
		t.Fatalf("expired index should serve stale, got idx=%v stale=%v", got, stale)
	}
}

// TestFind_IndexSignerMismatch: an envelope whose signer differs from the
// verifying key is refused — curator-confusion guard.
func TestFind_IndexSignerMismatch(t *testing.T) {
	f := newBuyFixture(t, nil)
	idx := &marketIndex{
		V: 1, Seq: 1, Signer: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: []indexProvider{},
	}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey
	if _, err := f.pm.fetchIndexFresh(
		context.Background(), url, pubkey); err == nil {
		t.Fatal("signer mismatch accepted")
	}
}

// TestFind_IndexAddrsAndAttestations surface on find rows.
func TestFind_IndexAddrsAndAttestations(t *testing.T) {
	f := newBuyFixture(t, nil)
	sellerID := testutil.NewIdentity(t)
	advJSON, _ := json.Marshal(advert{
		V: 1, Module: "rhizome-market", PeerID: sellerID.PeerID,
		Runtime: "exec", RuntimeAvailable: true,
		ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		Offers: []offer{{
			ID:           "offer-a",
			AgentBinding: "agent-1",
			PriceSheet:   priceSheet{PerTask: "1", Asset: "USDC", ChainID: 11155111},
		}},
	})
	idx := &marketIndex{
		V: 1, Seq: 1,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Providers: []indexProvider{{
			PeerID: sellerID.PeerID,
			Addrs:  []string{"/dns4/seller.example/tcp/443"},
			Advert: advJSON,
			Attestations: []json.RawMessage{
				json.RawMessage(`{"kind":"runtime","issuer":"curator"}`),
			},
		}},
	}
	url, pubkey := signedIndexServer(t, idx)
	f.mc.indexEnabled = true
	f.mc.indexURL = url
	f.mc.indexPubKey = pubkey

	out, err := f.pm.runFind(context.Background(), "offer-a")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	m, _ := out.(map[string]any)
	rows, _ := m["providers"].([]findRow)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0].Multiaddr != "/dns4/seller.example/tcp/443" {
		t.Fatalf("row multiaddr = %q", rows[0].Multiaddr)
	}
	if len(rows[0].Attestations) != 1 || rows[0].Attestations[0] != "runtime@curator" {
		t.Fatalf("row attestations = %v", rows[0].Attestations)
	}
}
