// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Command rhizome-market is the market companion module: a daemon-kind
// process supervised by pkg/modules that serves the loopback API behind
// `rhizome market`, bridges /rhizome/acp/1.0.0 streams, runs escrow-gated
// sell-side sessions with signed receipts, and writes advert.json for the
// signed mesh manifest. Buy-side verbs land in Track 103.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println("rhizome-market " + config.FormatVersion())
		return
	}
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "rhizome-market: "+err.Error())
		os.Exit(1)
	}
}

// run resolves paths, loads config, starts both listeners, and serves until
// SIGINT/SIGTERM (or parent-context death, which the supervisor enforces).
// Non-fatal config problems keep the process alive and visible in
// /v1/health — only unusable listeners/dirs are fatal.
func run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	p, err := resolvePaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.moduleDir, 0o700); err != nil {
		return fmt.Errorf("module dir: %w", err)
	}
	audit := newAuditLogger(filepath.Join(p.moduleDir, auditFile))
	audit.log("market.start", map[string]any{"version": config.FormatVersion()})
	defer audit.log("market.stop", nil)

	cfg, err := config.LoadConfig(p.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger.SetLevelFromString(cfg.Gateway.LogLevel)
	mc := loadMarketConfig(cfg, p.moduleDir)
	for _, e := range mc.errs {
		logger.WarnCF("market", "config problem", map[string]any{"error": e})
	}

	ident := loadIdentity(p.home)
	peerID := ""
	if ident != nil {
		peerID = ident.PeerID
	}
	token := newTokenProvider(p.moduleDir)
	if token.token() == "" {
		logger.WarnCF("market", "no bridge token yet — /v1/* will 503 until the daemon mints one", nil)
	}

	mgr := newSessionMgr(p.moduleDir, audit)
	if ident != nil {
		mgr.ident.Store(ident)
	}
	mgr.setConfig(mc, cfg, agentBindingsFromConfig(cfg), assembleRail(ctx, cfg, mc))
	go mgr.runReaper(ctx)

	api, err := startAPI(p.moduleDir, token, mgr, audit, config.FormatVersion())
	if err != nil {
		return err
	}
	defer api.Close()
	logger.InfoCF("market", "api listening", map[string]any{"addr": api.ln.Addr().String()})

	connCap := mc.maxSessions * 4
	if connCap < 32 {
		connCap = 32
	}
	bridge, err := startBridge(p.moduleDir, token, mgr, connCap, audit)
	if err != nil {
		return err
	}
	defer bridge.Close()
	logger.InfoCF("market", "bridge accept listener up", map[string]any{
		"addr": bridge.ln.Addr().String(),
	})
	api.bridge = func() bridgeStatus {
		return bridgeStatus{Listener: true, Outbound: outboundReady()}
	}
	api.setConfig(mc)

	aw := newAdvertWriter(p.moduleDir, config.FormatVersion(), peerID, audit)
	aw.refresh(mc)
	defer aw.remove()

	// Watch config + offers file for changes; refresh the advert on the
	// same tick. Mtime polling is cheap and needs no fsnotify dependency.
	secPath := filepath.Join(filepath.Dir(p.configPath), config.SecurityConfigFile)
	watched := []string{p.configPath, secPath, filepath.Join(p.moduleDir, offersFile)}
	lastMod := mtimeOf(watched)
	ticker := time.NewTicker(advertRefresh)
	defer ticker.Stop()
	cur := mc

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			mods := mtimeOf(watched)
			if mods != lastMod {
				lastMod = mods
				if cfg2, err := config.LoadConfig(p.configPath); err != nil {
					logger.WarnCF("market", "config reload failed", map[string]any{"error": err.Error()})
					audit.log("market.config.error", map[string]any{"error": err.Error()})
				} else {
					cur = loadMarketConfig(cfg2, p.moduleDir)
					api.setConfig(cur)
					mgr.setConfig(
						cur, cfg2, agentBindingsFromConfig(cfg2),
						assembleRail(ctx, cfg2, cur))
					audit.log("market.config.reload", map[string]any{"errors": len(cur.errs)})
				}
			}
			aw.refresh(cur)
		}
	}
}

// loadIdentity best-effort loads the node identity — advert peer_id and
// receipt signing both come from it. The module is non-interactive, so an
// encrypted identity without a keyring/env passphrase degrades honestly:
// adverts omit peer_id, receipts mint unsigned.
func loadIdentity(home string) *identity.Derived {
	dir := filepath.Join(home, "identity")
	d, _, err := identity.Load(dir)
	if err == nil {
		return d
	}
	if !errors.Is(err, identity.ErrIdentityEncrypted) {
		logger.WarnCF("market", "identity load failed; unsigned receipts, no advert peer_id",
			map[string]any{"error": err.Error()})
		return nil
	}
	if d, _, err = identity.LoadWithProvider(dir, &identity.KeyringProvider{}); err == nil {
		return d
	}
	if pp := os.Getenv("RHIZOME_IDENTITY_PASSPHRASE"); pp != "" {
		if d, _, err = identity.LoadWithProvider(dir, &identity.ScryptProvider{Passphrase: pp}); err == nil {
			return d
		}
	}
	logger.WarnCF("market", "identity encrypted; unsigned receipts, no advert peer_id", nil)
	return nil
}

// assembleRail builds the settlement rail for the resolved config:
//   - escrow_contract unset → MockRail fixture (the module only verifies
//     locks presented against escrows its own callers opened — honest
//     posture for tests and dev loops, never fakes a chain read);
//   - configured → RPCRail over the web3-resolved endpoint. VerifyLock is
//     pure eth_call, so the DirectSender needs no key — payout_address
//     stamps the tx identity for send verbs (claims) when used.
//
// A configured-but-unresolvable rail returns nil — sessions then refuse
// with rail_unavailable rather than silently falling back to fixture.
func assembleRail(ctx context.Context, cfg *config.Config, mc *marketConfig) settlement.Rail {
	if mc == nil || mc.rail == nil {
		return settlement.NewMockRail(settlement.RailConfig{})
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ep, err := web3.NewProvider(cfg).Resolve(rctx)
	if err != nil {
		logger.WarnCF("market", "escrow endpoint unresolved; sessions refuse until it resolves",
			map[string]any{"error": err.Error()})
		return nil
	}
	snd := settlement.NewDirectSender(
		web3.NewClient(ep.URL, ep.APIKey, nil), mc.payoutAddress)
	rail, err := settlement.NewRPCRail(*mc.rail, snd)
	if err != nil {
		logger.WarnCF("market", "escrow rail invalid", map[string]any{"error": err.Error()})
		return nil
	}
	logger.InfoCF("market", "settlement rail configured", map[string]any{
		"chain_id": mc.rail.ChainID, "source": string(ep.Source),
	})
	return rail
}

// mtimeOf returns a fingerprint string of the given files' mtimes — a
// change in any file produces a different fingerprint.
func mtimeOf(files []string) string {
	var s string
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			s += f + "@" + info.ModTime().UTC().Format(time.RFC3339Nano) + ";"
		}
	}
	return s
}
