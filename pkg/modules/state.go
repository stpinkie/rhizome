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
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// ModuleState is the on-disk runtime record for one module, stored at
// <modules-root>/<id>/module-state.json. It lets `module status` report
// useful information while the daemon (and its supervisor) is down.
type ModuleState struct {
	Version   string    `json:"version,omitempty"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Restarts  int       `json:"restarts,omitempty"`
	LastExit  string    `json:"last_exit,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Dir returns the module's root directory under the modules root.
func (m *Manager) Dir(id string) string {
	return filepath.Join(m.root, id)
}

// statePath returns the state file location for a module.
func (m *Manager) statePath(id string) string {
	return filepath.Join(m.Dir(id), "module-state.json")
}

// loadState reads the persisted state for a module. Missing files or
// malformed content yield a zero state — status must never fail on state.
func (m *Manager) loadState(id string) ModuleState {
	var st ModuleState
	data, err := os.ReadFile(m.statePath(id))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

// saveState persists the module state atomically.
func (m *Manager) saveState(id string, st ModuleState) error {
	st.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Dir(id), 0o700); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(m.statePath(id), data, 0o600)
}

// installedVersion returns the installed version marker, or "" when the
// module is not installed via a managed method.
func (m *Manager) installedVersion(id string) string {
	data, err := os.ReadFile(filepath.Join(m.Dir(id), "current"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
