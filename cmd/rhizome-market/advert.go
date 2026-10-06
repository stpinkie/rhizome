// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/isolation"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// advertMaxBytes matches pkg/modules' read-side bound — the daemon drops
// oversized adverts, so we refuse to write past it rather than emit one
// that can never be picked up.
const (
	advertMaxBytes = 16 << 10
	// advertTTL is how long a written advert stays meaningful; ~2× the
	// daemon's 5-minute announce cadence so a consumer never sees a
	// stale-but-current advert as fresh.
	advertTTL = 10 * time.Minute
	// advertRefresh is the write/refresh tick — cheap enough to catch
	// config changes quickly, far above the announce cadence.
	advertRefresh = 30 * time.Second
)

// advert is the module-authored capability advert merged into the signed
// mesh manifest as Capability.ModuleAdverts["rhizome-market"]. Content is
// deliberately public: offers, payout, runtime posture — anything in here
// broadcasts to every connected peer, so secrets are never rendered.
type advert struct {
	V             int      `json:"v"`
	Module        string   `json:"module"`
	ModuleVersion string   `json:"module_version"`
	GeneratedAt   string   `json:"generated_at"`
	ExpiresAt     string   `json:"expires_at"`
	PeerID        string   `json:"peer_id,omitempty"`
	Protocols     []string `json:"protocols"`
	// Endpoints lists non-mesh transports (wss://…) buyers can dial
	// directly; TLSFingerprint pins the serving cert's sha256 for TOFU.
	Endpoints        []string      `json:"endpoints,omitempty"`
	TLSFingerprint   string        `json:"tls_fingerprint,omitempty"`
	ServeEnabled     bool          `json:"serve_enabled"`
	Runtime          string        `json:"runtime"`
	RuntimeAvailable bool          `json:"runtime_available"`
	Payout           *advertPayout `json:"payout,omitempty"`
	Offers           []offer       `json:"offers,omitempty"`
	Escrow           *advertEscrow `json:"escrow,omitempty"`
	// Attestations carries the newest-N buyer-signed completion claims
	// the seller registered — additive evidence, not a ledger (the
	// honest-limits contract in docs/guides/market.md).
	Attestations []attestation `json:"attestations,omitempty"`
}

type advertPayout struct {
	ChainID int64  `json:"chain_id"`
	Address string `json:"address"`
	Asset   string `json:"asset"`
}

type advertEscrow struct {
	Posture           string `json:"posture"` // "fixture" | "configured"
	ChainID           uint64 `json:"chain_id,omitempty"`
	Contract          string `json:"contract,omitempty"`
	Token             string `json:"token,omitempty"`
	Arbiter           string `json:"arbiter,omitempty"`
	DisputeWindowSecs int64  `json:"dispute_window_secs,omitempty"`
}

// advertWriter renders + writes advert.json on config change and interval.
type advertWriter struct {
	moduleDir string
	version   string
	peerID    string
	audit     *auditLogger
	https     *httpsServer // non-nil while the wss listener is up

	mu      sync.Mutex
	lastRaw []byte // last rendered advert for change-detection
	lastExp time.Time
	attest  *attestationStore // nil → no attestation field emitted
}

func newAdvertWriter(
	moduleDir, version, peerID string, audit *auditLogger, https *httpsServer,
	attest *attestationStore,
) *advertWriter {
	return &advertWriter{
		moduleDir: moduleDir, version: version, peerID: peerID,
		audit: audit, https: https, attest: attest,
	}
}

// render builds the advert for the current config. Serving requires
// advertable() — the advert must not claim a service that can't run.
// Returns (nil, reason) when the advert should not be written.
func (w *advertWriter) render(mc *marketConfig) ([]byte, string) {
	if mc.serveEnabled && !mc.advertable() {
		switch {
		case len(mc.errs) > 0:
			return nil, mc.errs[0]
		case mc.payoutAddress == "":
			return nil, "serve_enabled requires payout_address"
		default:
			return nil, "serve_enabled requires at least one offer"
		}
	}
	a := advert{
		V:                1,
		Module:           moduleID,
		ModuleVersion:    w.version,
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:        time.Now().UTC().Add(advertTTL).Format(time.RFC3339),
		PeerID:           w.peerID,
		Protocols:        []string{"/rhizome/acp/1.0.0"},
		ServeEnabled:     mc.serveEnabled,
		Runtime:          mc.runtime,
		RuntimeAvailable: runtimeAvailable(mc.runtime),
	}
	// HTTPS endpoint advert: emitted only while the listener is actually
	// up AND serving is enabled — a configured-but-unstarted endpoint
	// (or one whose sessions the gate would refuse) must never be claimed.
	if w.https != nil && mc.serveEnabled {
		if ep := w.https.advertiseEndpoint(); ep != "" {
			a.Endpoints = []string{ep}
			a.TLSFingerprint = w.https.fingerprint
		}
	}
	if mc.serveEnabled {
		a.Payout = &advertPayout{
			ChainID: mc.payoutChainID, Address: mc.payoutAddress, Asset: mc.payoutAsset,
		}
		a.Offers = mc.offers
	}
	if mc.rail != nil {
		a.Escrow = &advertEscrow{
			Posture:           "configured",
			ChainID:           mc.rail.ChainID,
			Contract:          mc.rail.Factory,
			Token:             mc.rail.Token,
			Arbiter:           mc.rail.Arbiter,
			DisputeWindowSecs: mc.rail.DisputeWindowSecs,
		}
	} else {
		a.Escrow = &advertEscrow{Posture: "fixture"}
	}
	// Newest-N attestations, trimmed until the advert fits — the bound
	// wins over the count; dropping oldest evidence is the honest move
	// (withholding is the documented failure mode anyway).
	if mc.serveEnabled && w.attest != nil {
		for n := attestationMaxLive; ; n /= 2 {
			a.Attestations = w.attest.latest(n)
			data, err := json.Marshal(a)
			if err != nil {
				return nil, "advert marshal: " + err.Error()
			}
			if len(data) <= advertMaxBytes {
				return data, ""
			}
			if n == 0 {
				break
			}
		}
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, "advert marshal: " + err.Error()
	}
	if len(data) > advertMaxBytes {
		return nil, fmt.Sprintf("advert exceeds %d bytes (%d)", advertMaxBytes, len(data))
	}
	return data, ""
}

// refresh writes the advert when the rendered bytes changed or the
// expiry approaches — called on config reload and the refresh tick.
func (w *advertWriter) refresh(mc *marketConfig) {
	data, reason := w.render(mc)
	w.mu.Lock()
	defer w.mu.Unlock()
	path := filepath.Join(w.moduleDir, advertFile)
	if data == nil {
		if w.lastRaw != nil {
			_ = os.Remove(path)
			w.lastRaw = nil
		}
		if reason != "" {
			logger.WarnCF("market", "advert not written", map[string]any{"reason": reason})
		}
		return
	}
	fresh := time.Until(w.lastExp) > advertTTL/2
	if fresh && string(w.lastRaw) == string(data) {
		return // nothing changed, expiry still comfortably out
	}
	if err := writeFileAtomic(path, data); err != nil {
		logger.WarnCF("market", "advert write failed", map[string]any{"error": err.Error()})
		return
	}
	w.lastRaw = data
	w.lastExp = time.Now().Add(advertTTL)
	w.audit.log("market.advert.write", map[string]any{
		"bytes": len(data), "offers": len(mc.offers), "serve_enabled": mc.serveEnabled,
	})
}

// remove deletes the advert — a stopped module must not keep claiming
// serving to peers re-reading the file at the next announce.
func (w *advertWriter) remove() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = os.Remove(filepath.Join(w.moduleDir, advertFile))
	w.lastRaw = nil
}

// runtimeAvailable reports whether the configured serving runtime can
// actually launch — advert honesty: `runtime` is the operator's claim,
// `runtime_available` is what this host can do. sandbox → pkg/isolation
// preflight; container → docker|podman on PATH.
func runtimeAvailable(rt string) bool {
	switch rt {
	case "sandbox":
		return isolation.PreflightWith(isolation.Options{
			Enabled: true, Backend: "auto",
		}) == nil
	case "container":
		for _, eng := range []string{"docker", "podman"} {
			if _, err := exec.LookPath(eng); err == nil {
				return true
			}
		}
		return false
	}
	return false
}
