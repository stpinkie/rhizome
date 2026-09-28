// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

func TestResolvePaths_Precedence(t *testing.T) {
	tmp := t.TempDir()
	modDir := filepath.Join(tmp, "modules", moduleID)
	home := filepath.Join(tmp, "home")

	t.Run("env module dir derives home", func(t *testing.T) {
		t.Setenv("RHIZOME_MODULE_DIR", modDir)
		t.Setenv("RHIZOME_HOME", "")
		t.Setenv(config.EnvConfig, "")
		p, err := resolvePaths()
		if err != nil {
			t.Fatal(err)
		}
		if p.moduleDir != modDir {
			t.Fatalf("moduleDir = %q", p.moduleDir)
		}
		if p.home != filepath.Dir(filepath.Dir(modDir)) {
			t.Fatalf("home = %q, want parent-of-parent of moduleDir", p.home)
		}
	})

	t.Run("RHIZOME_HOME wins over module dir derivation", func(t *testing.T) {
		t.Setenv("RHIZOME_MODULE_DIR", modDir)
		t.Setenv("RHIZOME_HOME", home)
		t.Setenv(config.EnvConfig, "")
		p, err := resolvePaths()
		if err != nil {
			t.Fatal(err)
		}
		if p.home != home {
			t.Fatalf("home = %q, want %q", p.home, home)
		}
	})

	t.Run("RHIZOME_CONFIG overrides config path", func(t *testing.T) {
		t.Setenv("RHIZOME_MODULE_DIR", modDir)
		t.Setenv("RHIZOME_HOME", "")
		cfgPath := filepath.Join(tmp, "elsewhere.json")
		t.Setenv(config.EnvConfig, cfgPath)
		p, err := resolvePaths()
		if err != nil {
			t.Fatal(err)
		}
		if p.configPath != cfgPath {
			t.Fatalf("configPath = %q, want %q", p.configPath, cfgPath)
		}
	})

	t.Run("default module dir under home", func(t *testing.T) {
		t.Setenv("RHIZOME_MODULE_DIR", "")
		t.Setenv("RHIZOME_HOME", home)
		t.Setenv(config.EnvConfig, "")
		p, err := resolvePaths()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, "modules", moduleID)
		if p.moduleDir != want {
			t.Fatalf("moduleDir = %q, want %q", p.moduleDir, want)
		}
	})
}

func TestTokenProvider(t *testing.T) {
	tmp := t.TempDir()

	t.Run("env token wins", func(t *testing.T) {
		t.Setenv("RHIZOME_BRIDGE_TOKEN", "envtok")
		tp := newTokenProvider(tmp)
		if got := tp.token(); got != "envtok" {
			t.Fatalf("token = %q", got)
		}
	})

	t.Run("file fallback + rotation re-read", func(t *testing.T) {
		t.Setenv("RHIZOME_BRIDGE_TOKEN", "")
		tp := newTokenProvider(tmp)
		if got := tp.token(); got != "" {
			t.Fatalf("missing token = %q", got)
		}
		path := filepath.Join(tmp, bridgeTokenFile)
		if err := os.WriteFile(path, []byte("tok1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tp.token(); got != "tok1" {
			t.Fatalf("token = %q", got)
		}
		// Rotation: a new file content is picked up on the next read.
		if err := os.WriteFile(path, []byte("tok2\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tp.token(); got != "tok2" {
			t.Fatalf("rotated token = %q", got)
		}
	})

	t.Run("oversize token file rejected", func(t *testing.T) {
		t.Setenv("RHIZOME_BRIDGE_TOKEN", "")
		tp := newTokenProvider(tmp)
		if err := os.WriteFile(
			filepath.Join(tmp, bridgeTokenFile),
			make([]byte, tokenMaxBytes+64), 0o600,
		); err != nil {
			t.Fatal(err)
		}
		if got := tp.token(); got != "" {
			t.Fatalf("oversize token = %q", got)
		}
	})
}
