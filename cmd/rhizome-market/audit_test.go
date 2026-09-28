// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudit_AppendAndShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), auditFile)
	l := newAuditLogger(path)
	l.log("market.api.request", map[string]any{"verb": "/v1/find", "status": 501})
	l.log("market.stream.accept", map[string]any{"peer": "p1", "protocol": "/rhizome/acp/1.0.0"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatal(err)
	}
	if e["event"] != "market.api.request" || e["ts"] == "" {
		t.Fatalf("entry = %v", e)
	}
	if e["verb"] != "/v1/find" {
		t.Fatalf("attrs missing: %v", e)
	}
}

func TestAudit_Rotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), auditFile)
	l := &auditLogger{path: path, maxSize: 256, keep: 2}
	for i := 0; i < 40; i++ {
		l.log("market.config.reload", map[string]any{"n": i, "pad": strings.Repeat("x", 20)})
	}
	// File rotated: .1 exists, main file bounded.
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("no rotation generation")
	}
	info, _ := os.Stat(path)
	if info.Size() > 256+512 { // cap + one entry slack
		t.Fatalf("audit over cap: %d", info.Size())
	}
	// Third generation gets dropped at keep=2.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatal("keep bound violated")
	}
}

func TestAudit_NilSafe(t *testing.T) {
	var l *auditLogger
	l.log("market.x", nil) // must not panic
	newAuditLogger("").log("market.x", nil)
}
