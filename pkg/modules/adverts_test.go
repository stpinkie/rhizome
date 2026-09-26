// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
)

// advertFixture builds a Manager over a temp home and returns helpers to
// configure modules and drop advert.json files under their dirs.
func advertFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{Modules: config.ModulesConfig{}}
	return NewManager(home, cfg, nil, nil), home
}

func writeAdvert(t *testing.T, mgr *Manager, id, body string) {
	t.Helper()
	dir := mgr.Dir(id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, advertFile), []byte(body), 0o600))
}

func TestModuleAdvertsServingModuleIncluded(t *testing.T) {
	mgr, _ := advertFixture(t)
	mgr.cfg.Modules["helios"] = config.ModuleConfig{
		Enabled: true,
		Fields:  map[string]string{"serve_enabled": "true"},
	}
	writeAdvert(t, mgr, "helios", `{"offers":[],"serving":true}`)

	adverts := mgr.ModuleAdverts()
	require.Len(t, adverts, 1)
	assert.JSONEq(t, `{"offers":[],"serving":true}`, string(adverts["helios"]))
}

func TestModuleAdvertsGates(t *testing.T) {
	valid := `{"ok":true}`

	t.Run("disabled module excluded", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = config.ModuleConfig{
			Enabled: false,
			Fields:  map[string]string{"serve_enabled": "true"},
		}
		writeAdvert(t, mgr, "helios", valid)
		assert.Nil(t, mgr.ModuleAdverts())
	})

	t.Run("serve_enabled off excluded", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		for _, v := range []string{"false", "0", "no", ""} {
			mgr.cfg.Modules["helios"] = config.ModuleConfig{
				Enabled: true,
				Fields:  map[string]string{"serve_enabled": v},
			}
			writeAdvert(t, mgr, "helios", valid)
			assert.Nil(t, mgr.ModuleAdverts(), "serve_enabled=%q must not advertise", v)
		}
	})

	t.Run("serve_enabled truthy variants included", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		for _, v := range []string{"true", "1", "yes", "on"} {
			mgr.cfg.Modules["helios"] = config.ModuleConfig{
				Enabled: true,
				Fields:  map[string]string{"serve_enabled": v},
			}
			writeAdvert(t, mgr, "helios", valid)
			assert.Len(t, mgr.ModuleAdverts(), 1, "serve_enabled=%q must advertise", v)
		}
	})

	t.Run("missing advert excluded", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = config.ModuleConfig{
			Enabled: true,
			Fields:  map[string]string{"serve_enabled": "true"},
		}
		assert.Nil(t, mgr.ModuleAdverts())
	})

	t.Run("unknown catalog id excluded", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["not-in-catalog"] = config.ModuleConfig{
			Enabled: true,
			Fields:  map[string]string{"serve_enabled": "true"},
		}
		writeAdvert(t, mgr, "not-in-catalog", valid)
		assert.Nil(t, mgr.ModuleAdverts())
	})
}

func TestModuleAdvertsBadFilesDropped(t *testing.T) {
	cfgEntry := func() config.ModuleConfig {
		return config.ModuleConfig{
			Enabled: true,
			Fields:  map[string]string{"serve_enabled": "true"},
		}
	}

	t.Run("invalid JSON", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = cfgEntry()
		writeAdvert(t, mgr, "helios", `{"offers": [broken`)
		assert.Nil(t, mgr.ModuleAdverts())
	})

	t.Run("oversized", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = cfgEntry()
		writeAdvert(t, mgr, "helios", `{"pad":"`+strings.Repeat("x", advertMaxBytes)+`"}`)
		assert.Nil(t, mgr.ModuleAdverts())
	})

	t.Run("advert.json is a directory", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = cfgEntry()
		dir := mgr.Dir("helios")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, advertFile), 0o700))
		assert.Nil(t, mgr.ModuleAdverts())
	})

	t.Run("symlink rejected", func(t *testing.T) {
		mgr, _ := advertFixture(t)
		mgr.cfg.Modules["helios"] = cfgEntry()
		dir := mgr.Dir("helios")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		target := filepath.Join(t.TempDir(), "secret.json")
		require.NoError(t, os.WriteFile(target, []byte(`{"leak":true}`), 0o600))
		if err := os.Symlink(target, filepath.Join(dir, advertFile)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		assert.Nil(t, mgr.ModuleAdverts())
	})
}

func TestModuleAdvertsRawJSONShape(t *testing.T) {
	mgr, _ := advertFixture(t)
	mgr.cfg.Modules["helios"] = config.ModuleConfig{
		Enabled: true,
		Fields:  map[string]string{"serve_enabled": "true"},
	}
	// Whitespace/order are preserved verbatim — the signed manifest carries
	// the raw bytes, not a re-encode.
	writeAdvert(t, mgr, "helios", "{\n  \"a\": 1,\n  \"b\": [2, 3]\n}")
	adverts := mgr.ModuleAdverts()
	require.Len(t, adverts, 1)
	assert.Equal(t, json.RawMessage("{\n  \"a\": 1,\n  \"b\": [2, 3]\n}"), adverts["helios"])
}
