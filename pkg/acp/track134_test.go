// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package acp

// Track 134 — sell-side egress verification: the bound-spawn path must
// demonstrably land the no-egress posture on the spawned agent.
// sandbox → isolation.Options.NetModeNone reaches the launcher;
// container → --network none argv (covered by bound_test.go +
// TestContainerRunArgs); exec → refused rather than silently dropping
// the flag.

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/isolation"
)

func TestSpawnBound_NoEgressRefusesExec(t *testing.T) {
	cfg := &config.Config{}
	src := BoundSource{
		ID:  "paid",
		ACP: &config.ACPAgentConfig{Command: "sh"},
	}
	// Default runtime resolves to exec — asking for enforced no-egress
	// must fail closed, not spawn an unwrapped child.
	_, err := SpawnBound(context.Background(), cfg, src, BoundSpawn{
		Runtime: "exec", Handler: testHandler{}, NoEgress: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no-egress") {
		t.Fatalf("NoEgress+exec = %v, want refusal", err)
	}
	// Empty runtime resolves through the same default → still refused.
	_, err = SpawnBound(context.Background(), cfg, src, BoundSpawn{
		Handler: testHandler{}, NoEgress: true,
	})
	if err == nil || !strings.Contains(err.Error(), "no-egress") {
		t.Fatalf("NoEgress+default(exec) = %v, want refusal", err)
	}
}

// TestSpawnSandboxIO_NetModeNoneLands is the Track 134 fixture: the
// sandbox profile the market asks for must demonstrably reach the
// isolation launcher — capture the Options at the StartWith seam.
func TestSpawnSandboxIO_NetModeNoneLands(t *testing.T) {
	orig := isolationStartWith
	t.Cleanup(func() { isolationStartWith = orig })

	var got isolation.Options
	started := false
	isolationStartWith = func(cmd *exec.Cmd, opts isolation.Options) error {
		got = opts
		// Start the real child so cmd.Process is populated — the
		// isolation wrapper would own this on a real spawn.
		started = true
		return cmd.Start()
	}

	cfg := &config.Config{}
	binding := &config.ACPAgentConfig{Command: helperBinary()}
	root := filepath.Join(t.TempDir(), "scratch")
	pio, err := spawnSandboxIO(cfg, "paid", binding, root, true)
	if err != nil {
		t.Fatalf("spawnSandboxIO: %v", err)
	}
	defer pio.kill()

	if !started {
		t.Fatal("isolationStartWith seam never invoked")
	}
	if got.NetMode != isolation.NetModeNone {
		t.Fatalf("NetMode = %q, want none — no-egress did not land", got.NetMode)
	}
	if got.Root != root {
		t.Fatalf("Root = %q, want %q", got.Root, root)
	}

	// noEgress=false keeps the config posture (empty here → inherit).
	got = isolation.Options{}
	pio, err = spawnSandboxIO(cfg, "free", binding,
		filepath.Join(t.TempDir(), "scratch"), false)
	if err != nil {
		t.Fatalf("spawnSandboxIO free: %v", err)
	}
	defer pio.kill()
	if got.NetMode == isolation.NetModeNone {
		t.Fatal("unforced spawn got NetModeNone — flag leaked")
	}
}

func helperBinary() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}
