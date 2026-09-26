// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/stpinkie/rhizome/pkg/logger"
)

const (
	// advertFile is the per-module capability advert a serving module
	// publishes under its module dir. The daemon merges it into the signed
	// mesh capability manifest as Capability.ModuleAdverts[id].
	advertFile = "advert.json"
	// advertMaxBytes bounds advert.json reads; oversized or unreadable
	// adverts are dropped, never fatal — advert serving must not block
	// manifest signing.
	advertMaxBytes = 16 << 10
)

// ModuleAdverts returns capability adverts for catalog-known modules that
// are enabled and have a truthy serve_enabled field — the explicit admin
// opt-in that lets module-authored JSON ride inside the signed mesh
// manifest (and broadcast to every connected peer). Iterating the catalog
// spec set keeps ids bounded and matches bridge/supervisor enumeration.
//
// Each advert must be a regular file containing ≤16 KB of valid JSON; any
// failure drops that advert (debug-logged) rather than erroring out.
func (m *Manager) ModuleAdverts() map[string]json.RawMessage {
	var out map[string]json.RawMessage
	for _, spec := range m.specs() {
		if !m.moduleConfig(spec.ID).Enabled {
			continue
		}
		if !isTruthy(m.resolvedFields(spec, false)["serve_enabled"]) {
			continue
		}
		if adv, ok := m.readAdvert(spec.ID); ok {
			if out == nil {
				out = map[string]json.RawMessage{}
			}
			out[spec.ID] = adv
		}
	}
	return out
}

// readAdvert reads and validates one module's advert.json. Returns the raw
// JSON and true on success; false (and a debug log) on any failure.
func (m *Manager) readAdvert(id string) (json.RawMessage, bool) {
	path := filepath.Join(m.Dir(id), advertFile)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false // absent is the common case — not worth logging
	}
	if !info.Mode().IsRegular() {
		logger.WarnCF("modules", "module advert is not a regular file; dropping", map[string]any{
			"module": id, "path": path,
		})
		return nil, false
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is under the managed module dir.
	if err != nil {
		logger.DebugCF("modules", "module advert unreadable; dropping", map[string]any{
			"module": id, "error": err.Error(),
		})
		return nil, false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, advertMaxBytes+1))
	if err != nil {
		logger.DebugCF("modules", "module advert read failed; dropping", map[string]any{
			"module": id, "error": err.Error(),
		})
		return nil, false
	}
	if len(data) > advertMaxBytes {
		logger.WarnCF("modules", "module advert exceeds 16 KiB; dropping", map[string]any{
			"module": id, "bytes": len(data),
		})
		return nil, false
	}
	if !json.Valid(data) {
		logger.WarnCF("modules", "module advert is not valid JSON; dropping", map[string]any{
			"module": id,
		})
		return nil, false
	}
	return json.RawMessage(data), true
}
