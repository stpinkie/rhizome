// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package agent

import (
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

func TestBuildMCPServeToolRegistry(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()

	reg, err := BuildMCPServeToolRegistry(cfg)
	if err != nil {
		t.Fatalf("BuildMCPServeToolRegistry: %v", err)
	}

	// Standalone tools register when enabled.
	for _, name := range []string{"read_file", "list_dir", "exec", "write_file"} {
		if cfg.Tools.IsToolEnabled(name) && !reg.HasRegistered(name) {
			t.Errorf("enabled standalone tool %q not registered", name)
		}
	}

	// Agent-loop-dependent tools are never registered — the mcp_server
	// allowlist cannot name them.
	for _, name := range []string{"delegate", "acp_run", "message", "send_file", "spawn"} {
		if reg.HasRegistered(name) {
			t.Errorf("agent-loop tool %q must not be in the serve registry", name)
		}
	}
}
