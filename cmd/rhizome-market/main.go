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
		logger.WarnCF(
			"market",
			"no bridge token yet — /v1/* will 503 until the daemon mints one",
			nil,
		)
	}

	mgr := newSessionMgr(p.moduleDir, audit)
	if ident != nil {
		mgr.ident.Store(ident)
	}
	rail, ep := assembleRail(ctx, cfg, mc, p.home)
	mgr.setConfig(mc, cfg, agentBindingsFromConfig(cfg), rail)
	pm := newPurchaseMgr(p.moduleDir, p.home, audit)
	pm.setConfig(mc, rail, ep)
	go mgr.runReaper(ctx)
	go pm.runWatcher(ctx)

	api, err := startAPI(p.moduleDir, token, mgr, pm, audit, config.FormatVersion())
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

	// Track 110: optional TLS/wss listener for non-mesh buyers — serves the
	// identical session_open → gate → session path over a websocket-adapted
	// byte stream. Bind failure is fatal like the other listeners: the
	// operator explicitly asked for this endpoint.
	var https *httpsServer
	if mc.httpsListen != "" {
		https, err = startHTTPS(mc, p.moduleDir, mgr, mgr.nextConnID, audit)
		if err != nil {
			return err
		}
		defer https.Close()
		logger.InfoCF("market", "https listener up", map[string]any{
			"addr": https.ln.Addr().String(), "tls_fingerprint": https.fingerprint,
		})
	}

	aw := newAdvertWriter(p.moduleDir, config.FormatVersion(), peerID, audit, https)
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
					logger.WarnCF(
						"market",
						"config reload failed",
						map[string]any{"error": err.Error()},
					)
					audit.log("market.config.error", map[string]any{"error": err.Error()})
				} else {
					cur = loadMarketConfig(cfg2, p.moduleDir)
					api.setConfig(cur)
					r2, ep2 := assembleRail(ctx, cfg2, cur, p.home)
					mgr.setConfig(cur, cfg2, agentBindingsFromConfig(cfg2), r2)
					pm.setConfig(cur, r2, ep2)
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
//   - configured → RPCRail over the web3-resolved endpoint. Reads are
//     eth_call; send verbs go through the configured signer —
//     settlement_signer=approval queues each tx into web3-pending.json
//     for human sign-off (the default), =direct talks to unlocked/dev
//     endpoints (anvil, FakeChain).
//
// The resolved endpoint is returned alongside so the buy path can build
// per-purchase senders (buyer `from` + pending-id attribution). A
// configured-but-unresolvable rail returns (nil, nil) — sessions refuse
// with rail_unavailable rather than silently falling back to fixture.
func assembleRail(
	ctx context.Context, cfg *config.Config, mc *marketConfig, home string,
) (settlement.Rail, *web3.Endpoint) {
	if mc == nil || mc.rail == nil {
		return settlement.NewMockRail(settlement.RailConfig{}), nil
	}
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ep, err := web3.NewProvider(cfg).Resolve(rctx)
	if err != nil {
		logger.WarnCF("market", "escrow endpoint unresolved; sessions refuse until it resolves",
			map[string]any{"error": err.Error()})
		return nil, nil
	}
	client := web3.NewClient(ep.URL, ep.APIKey, nil)

	// Chain pinning: the configured rail must match the endpoint's live
	// eth_chainId — a mis-pointed endpoint refuses the whole rail rather
	// than signing into the wrong network. Mainnet is refused unless the
	// operator opted in (escrow_allow_mainnet); the market ships
	// experimental and the Sepolia posture is the documented path.
	if mc.rail.ChainID == 1 && !mc.allowMainnet {
		logger.WarnCF(
			"market",
			"escrow_chain_id=1 refused — set escrow_allow_mainnet=true to opt in",
			nil,
		)
		return nil, nil
	}
	live, err := web3.ChainID(ctx, web3.NewStaticProvider(ep.URL, ep.APIKey))
	if err != nil {
		logger.WarnCF("market", "escrow chain check failed — rail unavailable",
			map[string]any{"error": err.Error()})
		return nil, nil
	}
	if live != mc.rail.ChainID {
		logger.WarnCF("market", "escrow chain pin mismatch — rail unavailable",
			map[string]any{"live": live, "pinned": mc.rail.ChainID})
		return nil, nil
	}

	var snd settlement.Sender
	switch mc.signerMode {
	case "direct":
		snd = settlement.NewDirectSender(client, mc.payoutAddress)
	case "wallet":
		// Local-signing path: keys from the shared web3 wallet store,
		// decrypted per send (keyring or RHIZOME_WALLET_PASSPHRASE).
		snd = settlement.NewWalletSender(
			client, web3.NewStaticProvider(ep.URL, ep.APIKey),
			web3.OpenWalletStore(web3.WalletDir(home)),
			mc.payoutAddress, mc.rail.ChainID)
	default: // approval
		snd = settlement.NewQueuedSender(
			web3.OpenPendingStore(web3.WalletDir(home)), client,
			mc.payoutAddress, mc.rail.ChainID)
	}
	rail, err := settlement.NewRPCRail(*mc.rail, snd)
	if err != nil {
		logger.WarnCF("market", "escrow rail invalid", map[string]any{"error": err.Error()})
		return nil, nil
	}
	logger.InfoCF("market", "settlement rail configured", map[string]any{
		"chain_id": mc.rail.ChainID, "source": string(ep.Source),
		"signer": mc.signerMode,
	})
	return rail, ep
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
