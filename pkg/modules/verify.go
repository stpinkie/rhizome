// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// VerifyResult reports the outcome of re-hashing an installed module binary
// against the digest recorded at install time.
type VerifyResult struct {
	Module    string `json:"module"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	Algorithm string `json:"algorithm"`
	Want      string `json:"want"`
	Got       string `json:"got"`
	Match     bool   `json:"match"`
}

// Verify re-hashes the installed binary for a module against the sha256
// recorded at install time (drift detection). The chain of trust: the
// catalog's artifact digest pin is checked at install, and the digest of
// the binary that trusted archive produced is recorded next to it —
// Verify compares the on-disk bytes to that record. Only managed installs
// (github-release) are verifiable — config-kind modules carry no binary and
// detect-method installs point at a PATH binary outside our control.
func (m *Manager) Verify(id string) (VerifyResult, error) {
	spec, _, ok := m.lookupSpec(id)
	if !ok {
		return VerifyResult{}, fmt.Errorf("unknown module %q", id)
	}
	res := VerifyResult{Module: id, Algorithm: "sha256"}
	switch spec.Install.Method {
	case "config":
		return res, fmt.Errorf("module %q is config-only — nothing installed to verify", id)
	case "detect":
		return res, fmt.Errorf("module %q is a detect-method install — no catalog pin applies", id)
	}
	version := m.installedVersion(id)
	if version == "" || strings.HasPrefix(version, "detected:") {
		return res, fmt.Errorf("module %q is not installed (or was detected, not installed)", id)
	}
	res.Version = version

	digestPath := filepath.Join(m.Dir(id), version, binaryDigestFile)
	//nolint:gosec // G304: path is the managed install location for a catalog module.
	digestData, err := os.ReadFile(digestPath)
	if err != nil {
		return res, fmt.Errorf(
			"module %q v%s has no install-time digest record — reinstall to enable verification", id, version)
	}
	res.Want = strings.TrimSpace(string(digestData))

	path := m.binaryPath(spec)
	res.Path = path
	//nolint:gosec // G304: path is the managed install location for a catalog module.
	f, err := os.Open(path)
	if err != nil {
		return res, fmt.Errorf("module %q: open %s: %w", id, path, err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return res, fmt.Errorf("module %q: hash %s: %w", id, path, err)
	}
	res.Got = hex.EncodeToString(sum.Sum(nil))
	res.Match = strings.EqualFold(res.Got, res.Want)

	m.publish(map[bool]string{true: "module.verify.ok", false: "module.verify.failed"}[res.Match],
		map[string]any{"module": id, "version": res.Version})
	if !res.Match {
		return res, fmt.Errorf(
			"module %q v%s: sha256 mismatch — installed binary digest %s, install-time record %s",
			id, res.Version, res.Got, res.Want)
	}
	return res, nil
}
