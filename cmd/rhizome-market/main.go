// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Command rhizome-market is the market companion module: a daemon-kind
// process supervised by pkg/modules that serves the loopback API behind
// `rhizome market`, bridges /rhizome/acp/1.0.0 streams, and writes
// advert.json for the signed mesh manifest. Track 100 is the skeleton —
// config, listeners, auth, advert, audit are real; session/buy business
// logic lands in Tracks 102/103.
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

	peerID := loadPeerID(p.home)
	token := newTokenProvider(p.moduleDir)
	if token.token() == "" {
		logger.WarnCF("market", "no bridge token yet — /v1/* will 503 until the daemon mints one", nil)
	}

	api, err := startAPI(p.moduleDir, token, audit, config.FormatVersion())
	if err != nil {
		return err
	}
	defer api.Close()
	logger.InfoCF("market", "api listening", map[string]any{"addr": api.ln.Addr().String()})

	agent := &marketAgent{}
	connCap := mc.maxSessions * 4
	if connCap < 32 {
		connCap = 32
	}
	bridge, err := startBridge(p.moduleDir, token, agent, connCap, audit)
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
					audit.log("market.config.reload", map[string]any{"errors": len(cur.errs)})
				}
			}
			aw.refresh(cur)
		}
	}
}

// loadPeerID best-effort loads the node identity for advert peer_id —
// the module is non-interactive, so an encrypted identity without a
// keyring/env passphrase just omits the field rather than prompting.
func loadPeerID(home string) string {
	dir := filepath.Join(home, "identity")
	d, _, err := identity.Load(dir)
	if err == nil {
		return d.PeerID
	}
	if !errors.Is(err, identity.ErrIdentityEncrypted) {
		logger.WarnCF("market", "identity load failed; advert omits peer_id",
			map[string]any{"error": err.Error()})
		return ""
	}
	if d, _, err = identity.LoadWithProvider(dir, &identity.KeyringProvider{}); err == nil {
		return d.PeerID
	}
	if pp := os.Getenv("RHIZOME_IDENTITY_PASSPHRASE"); pp != "" {
		if d, _, err = identity.LoadWithProvider(dir, &identity.ScryptProvider{Passphrase: pp}); err == nil {
			return d.PeerID
		}
	}
	logger.WarnCF("market", "identity encrypted; advert omits peer_id", nil)
	return ""
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
