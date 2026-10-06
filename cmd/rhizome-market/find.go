// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/marketindex"
	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/sigverify"
)

const (
	// indexCacheTTL is how long a fresh fetch serves before refetching.
	indexCacheTTL  = time.Hour
	indexCacheFile = "index-cache.json"
	// indexFetchTimeout bounds both index fetches (doc + .sig).
	indexFetchTimeout = 15 * time.Second
)

// indexCache persists the last verified index for stale-serve and seq
// regression checks.
type indexCache struct {
	FetchedAt time.Time          `json:"fetched_at"`
	Index     *marketindex.Index `json:"index"`
}

// Local aliases keep call sites terse; the schema itself lives in
// pkg/marketindex so the publish pipeline validates identically.
type (
	marketIndex   = marketindex.Index
	indexProvider = marketindex.IndexProvider
)

// findRow is one result in a /v1/find response — projected offer terms
// with the provenance (index vs direct-peer journal) and trust flags the
// operator needs to make a buy decision honestly.
type findRow struct {
	PeerID    string `json:"peer_id"`
	Multiaddr string `json:"multiaddr,omitempty"`
	OfferID   string `json:"offer_id"`
	PerTask   string `json:"per_task"`
	Asset     string `json:"asset"`
	ChainID   int64  `json:"chain_id"`

	Runtime          string `json:"runtime,omitempty"`
	RuntimeAvailable bool   `json:"runtime_available"`
	EscrowPosture    string `json:"escrow_posture,omitempty"`
	Payout           string `json:"payout,omitempty"`
	AdvertExpiresAt  string `json:"advert_expires_at,omitempty"`

	Source       string   `json:"source"` // index | peers
	Trusted      bool     `json:"trusted"`
	Stale        bool     `json:"stale,omitempty"`   // index cache older than TTL
	Expired      bool     `json:"expired,omitempty"` // advert past its expires_at
	Attestations []string `json:"attestations,omitempty"`
}

// runFind executes /v1/find: index query when market_index_url is set,
// the peer-advert journal otherwise. The query matches offer ids and
// agent bindings (substring, case-insensitive).
func (pm *purchaseMgr) runFind(ctx context.Context, query string) (any, error) {
	mc := pm.cfg.Load()
	if mc == nil {
		return nil, buyErr("not_ready", "module config not loaded")
	}
	q := strings.ToLower(strings.TrimSpace(query))
	match := func(o *offer) bool {
		if q == "" {
			return true
		}
		return strings.Contains(strings.ToLower(o.ID), q) ||
			strings.Contains(strings.ToLower(o.AgentBinding), q)
	}
	now := pm.nowFn()

	if mc.indexEnabled && mc.indexURL != "" {
		idx, stale, err := pm.fetchIndex(ctx)
		if err != nil {
			return nil, buyErr("index_unavailable", "%s", err)
		}
		var rows []findRow
		for i := range idx.Providers {
			pr := &idx.Providers[i]
			var a advert
			if json.Unmarshal(pr.Advert, &a) != nil {
				continue
			}
			pid := a.PeerID
			if pid == "" {
				pid = pr.PeerID
			}
			addr := pr.Multiaddr
			if addrs := pr.AddrsOrMultiaddr(); len(addrs) > 0 {
				addr = addrs[0]
			}
			for j := range a.Offers {
				o := &a.Offers[j]
				if !match(o) {
					continue
				}
				row := pm.projectRow(&a, o, pid, addr, "index", false, stale, now)
				row.Attestations = pr.AttestationKinds()
				rows = append(rows, row)
			}
		}
		return map[string]any{
			"source": "index", "index_url": mc.indexURL,
			"stale": stale, "providers": rows,
		}, nil
	}

	// Direct-peer path: the daemon-journaled capability adverts.
	rows, err := peeradverts.Load(pm.home)
	if err != nil {
		return nil, buyErr("peer_adverts", "peer advert journal: %s", err)
	}
	var out []findRow
	for _, r := range rows {
		raw, ok := r.Adverts[moduleID]
		if !ok {
			continue
		}
		var a advert
		if json.Unmarshal(raw, &a) != nil {
			continue
		}
		expired := r.Expired(now)
		if advExp, perr := time.Parse(time.RFC3339, a.ExpiresAt); perr == nil && now.After(advExp) {
			expired = true
		}
		for i := range a.Offers {
			o := &a.Offers[i]
			if !match(o) {
				continue
			}
			out = append(out, pm.projectRow(&a, o, r.PeerID, "", "peers", r.Trusted, expired, now))
		}
	}
	return map[string]any{
		"source": "peers", "providers": out,
	}, nil
}

// projectRow flattens one advert+offer pair into a find row.
func (pm *purchaseMgr) projectRow(
	a *advert, o *offer, peerID, multiaddr, source string,
	trusted, expired bool, _ time.Time,
) findRow {
	row := findRow{
		PeerID:           peerID,
		Multiaddr:        multiaddr,
		OfferID:          o.ID,
		PerTask:          o.PriceSheet.PerTask,
		Asset:            o.PriceSheet.Asset,
		ChainID:          o.PriceSheet.ChainID,
		Runtime:          a.Runtime,
		RuntimeAvailable: a.RuntimeAvailable,
		AdvertExpiresAt:  a.ExpiresAt,
		Source:           source,
		Trusted:          trusted,
	}
	if expired {
		row.Expired = true
	}
	if a.Payout != nil {
		row.Payout = a.Payout.Address
	}
	if a.Escrow != nil {
		row.EscrowPosture = a.Escrow.Posture
	}
	return row
}

// fetchIndex retrieves + verifies <index_url>/index.json{,.sig}, using the
// 1 h cache and serving stale on fetch/verify failure. A fetched index
// whose seq regresses below the cached seq is a rollback — refused outright
// (tamper evidence); an index past its own expires_at is served stale.
func (pm *purchaseMgr) fetchIndex(
	ctx context.Context,
) (*marketindex.Index, bool, error) {
	mc := pm.cfg.Load()
	if mc == nil || mc.indexURL == "" {
		return nil, false, buyErr("index_disabled", "market_index_url unset")
	}
	cachePath := filepath.Join(pm.moduleDir, indexCacheFile)
	cached := readIndexCache(cachePath)

	// Fresh cache hit: under TTL and not self-expired → serve without a
	// network round-trip.
	if cached != nil && pm.nowFn().Sub(cached.FetchedAt) < indexCacheTTL &&
		!cached.Index.Expired(pm.nowFn()) {
		return cached.Index, false, nil
	}

	idx, err := pm.fetchIndexFresh(ctx, mc.indexURL, mc.indexPubKey)
	if err != nil {
		if cached != nil {
			pm.audit.log("market.index.stale_serve", map[string]any{
				"error": err.Error(), "age_s": pm.nowFn().Sub(cached.FetchedAt).Seconds(),
			})
			return cached.Index, true, nil
		}
		return nil, false, err
	}
	// Rollback guard: a freshly verified index must never carry a seq below
	// the last cached seq — a regression means the history was rewritten.
	if cached != nil && idx.Seq < cached.Index.Seq {
		pm.audit.log("market.index.seq_regressed", map[string]any{
			"fetched_seq": idx.Seq, "cached_seq": cached.Index.Seq,
		})
		return cached.Index, true, nil
	}
	if err := writeIndexCache(cachePath, idx, pm.nowFn()); err != nil {
		pm.audit.log("market.index.cache_write_failed", map[string]any{"error": err.Error()})
	}
	// Self-expired index serves stale — usable, but every row flags it.
	return idx, idx.Expired(pm.nowFn()), nil
}

// fetchIndexFresh downloads and verifies the index document + signature.
// The envelope's optional signer field must equal the key that verified
// the signature — an index declaring a different signer than the one that
// signed it is a curator-confusion attempt.
func (pm *purchaseMgr) fetchIndexFresh(
	ctx context.Context, baseURL, pubkeyB64 string,
) (*marketindex.Index, error) {
	base := strings.TrimRight(baseURL, "/")
	ctx, cancel := context.WithTimeout(ctx, indexFetchTimeout)
	defer cancel()
	doc, err := httpGet(ctx, base+"/index.json", marketindex.MaxBytes)
	if err != nil {
		return nil, fmt.Errorf("index.json fetch: %w", err)
	}
	sigB64, err := httpGet(ctx, base+"/index.json.sig", 4<<10)
	if err != nil {
		return nil, fmt.Errorf("index.json.sig fetch: %w", err)
	}
	effectiveKey := strings.TrimSpace(pubkeyB64)
	if effectiveKey == "" {
		effectiveKey = sigverify.ReleasePubKeyB64
	}
	if err := verifyIndexSig(doc, strings.TrimSpace(string(sigB64)), pubkeyB64); err != nil {
		return nil, fmt.Errorf("index signature: %w", err)
	}
	idx, err := marketindex.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("index schema: %w", err)
	}
	if idx.Signer != "" && idx.Signer != effectiveKey {
		return nil, fmt.Errorf(
			"index signer %q does not match the verifying key", idx.Signer)
	}
	return idx, nil
}

// verifyIndexSig checks the detached Ed25519 signature: the operator's
// market_index_pubkey when set, else the baked first-party release key
// (the curator-index path Track 105/v0.16.0 formalizes).
func verifyIndexSig(doc []byte, sigB64, pubkeyB64 string) error {
	if pubkeyB64 == "" {
		return sigverify.VerifyReleaseSignature(doc, sigB64)
	}
	raw, err := base64.StdEncoding.DecodeString(pubkeyB64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("market_index_pubkey is not a base64 Ed25519 key")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signature is not base64")
	}
	if !ed25519.Verify(ed25519.PublicKey(raw), doc, sig) {
		return fmt.Errorf("signature does not verify")
	}
	return nil
}

// httpGet performs a bounded GET with the module's caller identity.
func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "rhizome-market/"+config.FormatVersion())
	hc := &http.Client{Timeout: indexFetchTimeout}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit+1))
}

func readIndexCache(path string) *indexCache {
	data, err := readBounded(path, marketindex.MaxBytes+4<<10)
	if err != nil {
		return nil
	}
	var c indexCache
	if json.Unmarshal(data, &c) != nil || c.Index == nil {
		return nil
	}
	return &c
}

func writeIndexCache(path string, idx *marketindex.Index, at time.Time) error {
	data, err := json.Marshal(indexCache{FetchedAt: at, Index: idx})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}
