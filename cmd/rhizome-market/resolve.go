// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
)

// Files the module owns under its module dir. api.addr/bridge.addr are the
// published listener endpoints consumed by the daemon bridge and the
// `rhizome market` CLI; advert.json is merged into the signed mesh manifest
// by pkg/modules; market-audit.jsonl is the module-local audit trail.
const (
	moduleID        = "rhizome-market"
	apiAddrFile     = "api.addr"
	bridgeAddrFile  = "bridge.addr"
	bridgeTokenFile = "bridge-token"
	advertFile      = "advert.json"
	auditFile       = "market-audit.jsonl"
	offersFile      = "offers.json"

	// tokenMaxBytes bounds bridge-token reads; it is a hex string on disk.
	tokenMaxBytes = 1 << 10
)

// paths holds the resolved filesystem locations the module operates on.
type paths struct {
	home       string // RHIZOME_HOME
	moduleDir  string // <home>/modules/rhizome-market
	configPath string // config.json location
}

// resolvePaths derives home, the module dir, and the config path.
// Precedence:
//   - moduleDir: $RHIZOME_MODULE_DIR (supervisor-injected for
//     protocol-declaring modules) → <home>/modules/<id>
//   - home: $RHIZOME_HOME → moduleDir's parent's parent (the module dir is
//     <home>/modules/<id> by construction) → config.GetHome() default
//   - configPath: $RHIZOME_CONFIG → <home>/config.json
func resolvePaths() (paths, error) {
	home := os.Getenv("RHIZOME_HOME")
	moduleDir := os.Getenv("RHIZOME_MODULE_DIR")
	if home == "" && moduleDir != "" {
		home = filepath.Dir(filepath.Dir(moduleDir))
	}
	if home == "" {
		home = config.GetHome()
	}
	if home == "" {
		return paths{}, fmt.Errorf("cannot resolve RHIZOME_HOME")
	}
	if moduleDir == "" {
		moduleDir = filepath.Join(home, "modules", moduleID)
	}
	configPath := os.Getenv(config.EnvConfig)
	if configPath == "" {
		configPath = filepath.Join(home, "config.json")
	}
	return paths{home: home, moduleDir: moduleDir, configPath: configPath}, nil
}

// tokenProvider resolves the current bridge token: $RHIZOME_BRIDGE_TOKEN
// when the supervisor injected it, else <module_dir>/bridge-token re-read
// on each call so a token rotated by reinstall takes effect without a
// module restart.
type tokenProvider struct {
	envTok string
	path   string
}

func newTokenProvider(moduleDir string) *tokenProvider {
	return &tokenProvider{
		envTok: strings.TrimSpace(os.Getenv("RHIZOME_BRIDGE_TOKEN")),
		path:   filepath.Join(moduleDir, bridgeTokenFile),
	}
}

// token returns the current bearer, "" when neither source has one.
func (p *tokenProvider) token() string {
	if p.envTok != "" {
		return p.envTok
	}
	f, err := os.Open(p.path) //nolint:gosec // G304: path is the module's own dir.
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, tokenMaxBytes+1))
	if err != nil || len(data) > tokenMaxBytes {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// writeFileAtomic writes data to path via a sibling tmp file + rename,
// mode 0600 — the pkg/pid pidfile pattern, so readers never see a
// half-written api.addr/bridge.addr/advert.json.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
