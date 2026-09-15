// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/events"
)

// Status is the resolved lifecycle state shown to users.
type Status string

const (
	StatusUnsupported  Status = "unsupported"  // no release for this platform
	StatusMissing      Status = "missing"      // installable, not installed
	StatusUnconfigured Status = "unconfigured" // required config fields unset
	StatusInstalled    Status = "installed"    // on-disk, not runnable yet
	StatusConfigured   Status = "configured"   // ready to run (no process)
	StatusStopped      Status = "stopped"      // installed+configured, not running
	StatusRunning      Status = "running"
	StatusUnhealthy    Status = "unhealthy" // running but health check fails
)

// Info is the catalog + config + state view of one module.
type Info struct {
	Spec          ModuleSpec        `json:"spec"`
	Status        Status            `json:"status"`
	Enabled       bool              `json:"enabled"`
	InstalledPath string            `json:"installed_path,omitempty"`
	Version       string            `json:"version,omitempty"`
	PID           int               `json:"pid,omitempty"`
	StartedAt     time.Time         `json:"started_at,omitempty"`
	Restarts      int               `json:"restarts,omitempty"`
	LastExit      string            `json:"last_exit,omitempty"`
	MissingFields []string          `json:"missing_fields,omitempty"`
	Healthy       *bool             `json:"healthy,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"`
	SecretKeys    []string          `json:"secret_keys,omitempty"`
}

// Manager owns module lifecycle: catalog lookups, config, install state,
// and (when running under the daemon) process supervision. It is safe to
// construct in contexts without a daemon — the web backend and CLI use it
// for read/config/install operations and only proxy lifecycle actions.
type Manager struct {
	root   string // <RHIZOME_HOME>/modules
	cfg    *config.Config
	bus    events.Bus
	client *http.Client
	sup    *Supervisor // nil outside the daemon
	saveFn func(*config.Config) error
}

// NewManager builds a module manager rooted at <home>/modules. bus may be
// nil outside the daemon (no events emitted). saveFn persists config changes
// made by Enable/SetFields/SetSecrets — typically
// func(c) { return config.SaveConfig(path, c) }; when nil, config-mutating
// operations return an error (read/install operations still work).
func NewManager(home string, cfg *config.Config, bus events.Bus, saveFn func(*config.Config) error) *Manager {
	return &Manager{
		root:   filepath.Join(home, "modules"),
		cfg:    cfg,
		bus:    bus,
		client: &http.Client{Timeout: 5 * time.Minute},
		saveFn: saveFn,
	}
}

// save persists the config through the caller-provided saveFn.
func (m *Manager) save() error {
	if m.saveFn == nil {
		return errors.New("module manager was built without config persistence")
	}
	return m.saveFn(m.cfg)
}

// SetSupervisor attaches the supervisor (daemon only) after construction.
func (m *Manager) SetSupervisor(sup *Supervisor) { m.sup = sup }

// Supervisor returns the attached supervisor, or nil.
func (m *Manager) Supervisor() *Supervisor { return m.sup }

func (m *Manager) publish(kind string, attrs map[string]any) {
	if m.bus == nil {
		return
	}
	m.bus.PublishNonBlocking(events.Event{
		Kind:     events.Kind(kind),
		Source:   events.Source{Component: "modules"},
		Severity: events.SeverityInfo,
		Attrs:    attrs,
	})
}

// moduleConfig returns a copy of the config entry for a module id.
func (m *Manager) moduleConfig(id string) config.ModuleConfig {
	if m.cfg.Modules == nil {
		return config.ModuleConfig{}
	}
	return m.cfg.Modules[id]
}

// resolvedFields returns the effective field values for a module: catalog
// defaults overridden by configured fields. Secret values are NOT included;
// pass includeSecrets=true to merge them (process launch, health checks
// needing credentials).
func (m *Manager) resolvedFields(spec ModuleSpec, includeSecrets bool) map[string]string {
	out := make(map[string]string, len(spec.ConfigFields))
	for _, f := range spec.ConfigFields {
		if f.Default != "" {
			out[f.Key] = f.Default
		}
	}
	mc := m.moduleConfig(spec.ID)
	for k, v := range mc.Fields {
		out[k] = v
	}
	if includeSecrets {
		for k, v := range mc.Secrets {
			out[k] = v.String()
		}
	}
	return out
}

// missingRequired returns catalog fields required but unset.
func (m *Manager) missingRequired(spec ModuleSpec) []string {
	values := m.resolvedFields(spec, true)
	var missing []string
	for _, f := range spec.ConfigFields {
		if f.Required && values[f.Key] == "" {
			missing = append(missing, f.Key)
		}
	}
	return missing
}

// binaryPath returns the installed binary path for a module.
func (m *Manager) binaryPath(spec ModuleSpec) string {
	name := spec.Install.Binary
	if name == "" {
		name = spec.ID
	}
	dir := filepath.Join(m.Dir(spec.ID), m.installedVersion(spec.ID))
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		// Archives normally ship the .exe; a bare-name fallback keeps
		// extensionless members (and tests) working.
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return filepath.Join(dir, name)
		}
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// Info resolves the full status for one module.
func (m *Manager) Info(id string) (Info, error) {
	spec, ok := Lookup(id)
	if !ok {
		return Info{}, fmt.Errorf("unknown module %q", id)
	}
	mc := m.moduleConfig(id)
	st := m.loadState(id)
	info := Info{
		Spec:    spec,
		Enabled: mc.Enabled,
		Version: m.installedVersion(id),
		Fields:  mc.Fields,
	}
	if info.Version != "" {
		info.InstalledPath = filepath.Join(m.Dir(id), info.Version)
	}
	for k := range mc.Secrets {
		info.SecretKeys = append(info.SecretKeys, k)
	}
	sort.Strings(info.SecretKeys)
	info.PID, info.StartedAt, info.Restarts, info.LastExit =
		st.PID, st.StartedAt, st.Restarts, st.LastExit

	missing := m.missingRequired(spec)
	switch {
	case !spec.Supports(Platform()):
		info.Status = StatusUnsupported
	case spec.Install.Method == "config":
		if len(missing) > 0 {
			info.Status, info.MissingFields = StatusUnconfigured, missing
		} else {
			info.Status = StatusConfigured
		}
	case spec.Install.Method == "detect":
		// detect is "installed" iff the binary resolves on PATH.
		if _, err := m.detectBinary(spec); err != nil {
			info.Status = StatusMissing
		} else if len(missing) > 0 {
			info.Status, info.MissingFields = StatusUnconfigured, missing
		} else {
			info.Status = StatusInstalled
		}
	case info.Version == "":
		info.Status = StatusMissing
	case len(missing) > 0:
		info.Status, info.MissingFields = StatusUnconfigured, missing
	default:
		info.Status = StatusInstalled
	}

	// Running state overrides the install-level status for process kinds.
	if spec.Kind != KindConfig && m.sup != nil && m.sup.IsRunning(id) {
		info.Status = StatusRunning
		if h, ok := m.HealthCheck(context.Background(), id); ok {
			info.Healthy = &h
			if !h {
				info.Status = StatusUnhealthy
			}
		}
	} else if spec.Kind != KindConfig && info.Status == StatusInstalled {
		// An installed+configured process module that isn't running is "stopped".
		info.Status = StatusStopped
	}
	return info, nil
}

// List returns Info for every catalog module.
func (m *Manager) List() ([]Info, error) {
	var out []Info
	for _, spec := range Catalog() {
		info, err := m.Info(spec.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

// Enable flips modules.<id>.enabled and persists config.
func (m *Manager) Enable(id string, enabled bool) error {
	spec, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	if enabled {
		if !spec.Supports(Platform()) {
			return fmt.Errorf("module %q is not supported on %s", id, Platform())
		}
		if missing := m.missingRequired(spec); len(missing) > 0 {
			return fmt.Errorf("module %q missing required fields: %v", id, missing)
		}
	}
	if m.cfg.Modules == nil {
		m.cfg.Modules = config.ModulesConfig{}
	}
	mc := m.cfg.Modules[id]
	mc.Enabled = enabled
	m.cfg.Modules[id] = mc
	if err := m.save(); err != nil {
		return err
	}
	m.publish(map[bool]string{true: "module.enabled", false: "module.disabled"}[enabled],
		map[string]any{"module": id})
	return nil
}

// SetFields writes non-secret field values for a module and persists config.
// Unknown or secret-marked keys are rejected — secrets go through SetSecrets.
func (m *Manager) SetFields(id string, fields map[string]string) error {
	spec, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	for k := range fields {
		f, ok := spec.Field(k)
		if !ok {
			return fmt.Errorf("module %q: unknown field %q", id, k)
		}
		if f.Secret {
			return fmt.Errorf("module %q: field %q is a secret — use secrets", id, k)
		}
	}
	if m.cfg.Modules == nil {
		m.cfg.Modules = config.ModulesConfig{}
	}
	mc := m.cfg.Modules[id]
	if mc.Fields == nil {
		mc.Fields = map[string]string{}
	}
	for k, v := range fields {
		if v == "" {
			delete(mc.Fields, k)
		} else {
			mc.Fields[k] = v
		}
	}
	m.cfg.Modules[id] = mc
	return m.save()
}

// SetSecrets writes secret field values for a module; they persist to
// .security.yml via the SecureString pattern, never to config.json.
func (m *Manager) SetSecrets(id string, secrets map[string]string) error {
	spec, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	for k := range secrets {
		f, ok := spec.Field(k)
		if !ok {
			return fmt.Errorf("module %q: unknown field %q", id, k)
		}
		if !f.Secret {
			return fmt.Errorf("module %q: field %q is not a secret — use fields", id, k)
		}
	}
	if m.cfg.Modules == nil {
		m.cfg.Modules = config.ModulesConfig{}
	}
	mc := m.cfg.Modules[id]
	if mc.Secrets == nil {
		mc.Secrets = map[string]config.SecureString{}
	}
	for k, v := range secrets {
		if v == "" {
			delete(mc.Secrets, k)
		} else {
			s := config.SecureString{}
			if err := s.UnmarshalText([]byte(v)); err != nil {
				return fmt.Errorf("module %q: secret %q: %w", id, k, err)
			}
			mc.Secrets[k] = s
		}
	}
	m.cfg.Modules[id] = mc
	return m.save()
}

// Install resolves a release, downloads, verifies, and extracts the module.
// version "" means "latest pinned".
func (m *Manager) Install(ctx context.Context, id, version string) error {
	spec, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	switch spec.Install.Method {
	case "config":
		return fmt.Errorf("module %q is config-only — nothing to install", id)
	case "detect":
		path, err := m.detectBinary(spec)
		if err != nil {
			return err
		}
		return m.markInstalled(id, "detected:"+path)
	}
	if !spec.Supports(Platform()) {
		return fmt.Errorf("module %q is not supported on %s", id, Platform())
	}
	if err := m.installRelease(ctx, spec, version); err != nil {
		return err
	}
	m.publish("module.installed", map[string]any{"module": id, "version": m.installedVersion(id)})
	return nil
}

// Uninstall removes the module directory. The caller must stop the module
// first — Uninstall refuses while the supervisor reports it running.
func (m *Manager) Uninstall(id string) error {
	if _, ok := Lookup(id); !ok {
		return fmt.Errorf("unknown module %q", id)
	}
	if m.sup != nil && m.sup.IsRunning(id) {
		return fmt.Errorf("module %q is running — stop it first", id)
	}
	if err := os.RemoveAll(m.Dir(id)); err != nil {
		return err
	}
	m.publish("module.uninstalled", map[string]any{"module": id})
	return nil
}

// markInstalled writes the "current" version marker.
func (m *Manager) markInstalled(id, version string) error {
	if err := os.MkdirAll(m.Dir(id), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.Dir(id), "current"), []byte(version+"\n"), 0o600)
}

var errNoDaemon = errors.New("module lifecycle requires a running daemon")

// Start launches a daemon module via the supervisor.
func (m *Manager) Start(id string) error {
	if m.sup == nil {
		return errNoDaemon
	}
	return m.sup.Start(id)
}

// Stop stops a running module via the supervisor.
func (m *Manager) Stop(id string) error {
	if m.sup == nil {
		return errNoDaemon
	}
	return m.sup.Stop(id)
}

// Restart restarts a daemon module via the supervisor.
func (m *Manager) Restart(id string) error {
	if m.sup == nil {
		return errNoDaemon
	}
	return m.sup.Restart(id)
}

// Logs returns the tail of the module's stdout/stderr logs.
func (m *Manager) Logs(id string, tail int) (stdout, stderr string, err error) {
	if tail <= 0 || tail > 2000 {
		tail = 200
	}
	read := func(name string) (string, error) {
		data, err := tailFile(filepath.Join(m.Dir(id), "logs", name), tail)
		if os.IsNotExist(err) {
			return "", nil
		}
		return data, err
	}
	stdout, err = read("stdout.log")
	if err != nil {
		return "", "", err
	}
	stderr, err = read("stderr.log")
	return stdout, stderr, err
}
