// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"
)

// market-audit.jsonl posture mirrors pkg/rhizome/mesh's auditLogger:
// append-only JSONL, rotated at a size cap across a fixed number of
// generations, failures silent — the audit trail must never break serving.
const (
	auditMaxBytes  = 10 * 1024 * 1024
	auditKeepFiles = 3
)

type auditLogger struct {
	path    string
	maxSize int64
	keep    int
	mu      sync.Mutex
}

func newAuditLogger(path string) *auditLogger {
	return &auditLogger{path: path, maxSize: auditMaxBytes, keep: auditKeepFiles}
}

// log appends one entry; "ts" and "event" are always present. Attrs must
// never carry secrets, tokens, or task bodies — callers log
// status/shape/identifiers only.
func (l *auditLogger) log(event string, attrs map[string]any) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if info, err := os.Stat(l.path); err == nil && info.Size() > l.maxSize {
		l.rotateLocked()
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	entry := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339),
		"event": event,
	}
	for k, v := range attrs {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
}

// rotateLocked shifts .N-1 → .N downward; os.Rename on Windows fails when
// the destination exists, so remove each target first.
func (l *auditLogger) rotateLocked() {
	last := l.path + "." + strconv.Itoa(l.keep)
	_ = os.Remove(last)
	for i := l.keep - 1; i >= 1; i-- {
		old := l.path + "." + strconv.Itoa(i)
		newer := l.path + "." + strconv.Itoa(i+1)
		_ = os.Remove(newer)
		_ = os.Rename(old, newer)
	}
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
}
