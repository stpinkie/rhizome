// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package marketindex holds the curated provider index schema shared by the
// rhizome-market module's find path (client side) and the
// stpinkie/rhizome-market-index repo's publish pipeline (validate + sign
// before commit — scripts/market-index.go). The index is a repo, not a
// service: index.json plus a detached Ed25519 index.json.sig over the exact
// bytes served.
package marketindex

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// MaxBytes bounds one index.json fetch — provider lists are small by
	// design.
	MaxBytes = 1 << 20
	// MaxEntries bounds the provider list; a curated index grows by
	// admission, not crawl.
	MaxEntries = 500
	// MaxEntryBytes bounds one provider entry after marshal — dominated by
	// the embedded advert (itself bounded at advertMaxBytes in the module).
	MaxEntryBytes = 32 << 10
	// MaxAddrs bounds contact hints per provider.
	MaxAddrs = 8
	// MaxAttestations bounds the opaque attestation list per provider.
	MaxAttestations = 16
)

// Index is the index.json envelope. seq is a monotonic publication counter
// (rollback guard); expires_at makes the whole document self-staling —
// clients serve it marked stale rather than fresh-verified.
type Index struct {
	V         int             `json:"v"`
	UpdatedAt string          `json:"updated_at"`
	Signer    string          `json:"signer,omitempty"`     // base64 curator pubkey; must equal the verifying key
	Seq       int64           `json:"seq,omitempty"`        // monotonic; regression vs cache refuses
	ExpiresAt string          `json:"expires_at,omitempty"` // RFC3339; past → stale-serve
	Providers []IndexProvider `json:"providers"`
}

// IndexProvider is one index row: a peer plus its market advert (the same
// signed advert shape the mesh manifest carries) and curator-level hints.
// addrs supplements multiaddr; attestations are curator-signed claims
// surfaced opaquely for operator judgment (Track 130 defines the portable
// attestation signature shape).
type IndexProvider struct {
	PeerID       string            `json:"peer_id"`
	Multiaddr    string            `json:"multiaddr,omitempty"`
	Addrs        []string          `json:"addrs,omitempty"`
	Advert       json.RawMessage   `json:"advert"`
	Attestations []json.RawMessage `json:"attestations,omitempty"`
}

// AttestationLabel renders an attestation entry for display — kind@issuer
// when both fields decode, else the raw JSON truncated.
type AttestationLabel struct {
	Kind   string `json:"kind"`
	Issuer string `json:"issuer,omitempty"`
}

// Parse decodes index.json bytes and validates the schema + bounds. It does
// NOT check the signature — callers verify the detached .sig over doc first
// (a parse failure and a signature failure are both fatal; order between
// them doesn't matter for trust, but signing-side callers validate before
// signing).
func Parse(doc []byte) (*Index, error) {
	if len(doc) > MaxBytes {
		return nil, fmt.Errorf("index exceeds %d bytes", MaxBytes)
	}
	var idx Index
	if err := json.Unmarshal(doc, &idx); err != nil {
		return nil, fmt.Errorf("index parse: %w", err)
	}
	if idx.V != 1 {
		return nil, fmt.Errorf("index v%d unsupported (want 1)", idx.V)
	}
	if err := idx.Validate(); err != nil {
		return nil, err
	}
	return &idx, nil
}

// Validate enforces the schema invariants Publish CI and clients both rely
// on. Called by Parse on load; also called standalone by the publish
// pipeline after mutating seq/expires_at.
func (idx *Index) Validate() error {
	if idx.V != 1 {
		return fmt.Errorf("index v%d unsupported (want 1)", idx.V)
	}
	if idx.UpdatedAt == "" {
		return fmt.Errorf("index missing updated_at")
	}
	if _, err := time.Parse(time.RFC3339, idx.UpdatedAt); err != nil {
		return fmt.Errorf("index updated_at not RFC3339: %q", idx.UpdatedAt)
	}
	if idx.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, idx.ExpiresAt); err != nil {
			return fmt.Errorf("index expires_at not RFC3339: %q", idx.ExpiresAt)
		}
	}
	if len(idx.Providers) > MaxEntries {
		return fmt.Errorf("index has %d providers (max %d)", len(idx.Providers), MaxEntries)
	}
	seen := make(map[string]bool, len(idx.Providers))
	for i := range idx.Providers {
		p := &idx.Providers[i]
		if _, err := peer.Decode(p.PeerID); err != nil {
			return fmt.Errorf("providers[%d]: bad peer_id: %w", i, err)
		}
		if seen[p.PeerID] {
			return fmt.Errorf("providers[%d]: duplicate peer_id %s", i, p.PeerID)
		}
		seen[p.PeerID] = true
		if len(p.Advert) == 0 {
			return fmt.Errorf("providers[%d]: missing advert", i)
		}
		if len(p.Advert) > MaxEntryBytes {
			return fmt.Errorf("providers[%d]: advert exceeds %d bytes", i, MaxEntryBytes)
		}
		if len(p.Addrs) > MaxAddrs {
			return fmt.Errorf("providers[%d]: %d addrs (max %d)", i, len(p.Addrs), MaxAddrs)
		}
		if len(p.Attestations) > MaxAttestations {
			return fmt.Errorf(
				"providers[%d]: %d attestations (max %d)", i, len(p.Attestations), MaxAttestations)
		}
	}
	return nil
}

// Expired reports whether the index's self-declared expires_at has passed.
// An unexpired-or-absent expiry returns false; an unparseable expiry is a
// schema violation caught by Validate, treated as expired-defensively here.
func (idx *Index) Expired(now time.Time) bool {
	if idx.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, idx.ExpiresAt)
	if err != nil {
		return true
	}
	return now.After(exp)
}

// AddrsOrMultiaddr returns the provider's contact hints — the addrs list
// when present, else the legacy single multiaddr field.
func (p *IndexProvider) AddrsOrMultiaddr() []string {
	if len(p.Addrs) > 0 {
		return p.Addrs
	}
	if p.Multiaddr != "" {
		return []string{p.Multiaddr}
	}
	return nil
}

// AttestationKinds renders each attestation as "kind@issuer" (kind alone
// when no issuer) for display surfaces.
func (p *IndexProvider) AttestationKinds() []string {
	out := make([]string, 0, len(p.Attestations))
	for _, raw := range p.Attestations {
		var a AttestationLabel
		if err := json.Unmarshal(raw, &a); err != nil || a.Kind == "" {
			out = append(out, "unknown")
			continue
		}
		if a.Issuer != "" {
			out = append(out, a.Kind+"@"+a.Issuer)
		} else {
			out = append(out, a.Kind)
		}
	}
	return out
}
