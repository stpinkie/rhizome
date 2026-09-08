package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/browser"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// isMaskedBrowserKey reports whether a submitted api_key value is a masked
// placeholder rather than a real credential: empty, the UI's "***" sentinel,
// or the SecureString "[NOT_HERE]" JSON marker.
func isMaskedBrowserKey(v string) bool {
	return v == "" || v == "***" || v == "[NOT_HERE]"
}

// registerBrowserRoutes binds browser-automation admin endpoints.
func (h *Handler) registerBrowserRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/browser", h.handleGetBrowserConfig)
	mux.HandleFunc("PUT /api/browser", h.handlePutBrowserConfig)
	mux.HandleFunc("GET /api/browser/backends", h.handleBrowserBackends)
	mux.HandleFunc("POST /api/browser/install", h.handleBrowserInstall)
	mux.HandleFunc("POST /api/browser/uninstall", h.handleBrowserUninstall)
	mux.HandleFunc("GET /api/browser/diskspace", h.handleBrowserDiskSpace)
}

// backendStatus is one catalog entry enriched with runtime state.
type backendStatus struct {
	browser.BackendSpec
	State        string `json:"state"`      // installed|missing|configured|unconfigured
	Configured   bool   `json:"configured"` // has an entry in tools.browser.backends
	IsDefault    bool   `json:"is_default"`
	DriverReady  *bool  `json:"driver_ready,omitempty"` // agent-browser CLI on PATH (all non-REST backends need it)
	Version      string `json:"version,omitempty"`
	FreeSpaceOK  *bool  `json:"free_space_ok,omitempty"` // for installs with a disk estimate
	FreeSpaceMsg string `json:"free_space_msg,omitempty"`
}

// handleGetBrowserConfig returns the current tools.browser configuration.
// SecureString fields serialize as the "***" sentinel — never plaintext.
//
//	GET /api/browser
func (h *Handler) handleGetBrowserConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg.Tools.Browser); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handlePutBrowserConfig updates the tools.browser configuration section.
// Fields absent from the request body keep their stored values — the web
// console only sends the fields it manages, so a save must not silently drop
// session_timeout or private_host_whitelist set via config.json.
//
//	PUT /api/browser
func (h *Handler) handlePutBrowserConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Detect which top-level keys were sent so omitted fields can be
	// preserved rather than zeroed.
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(body, &sent); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	var browserCfg config.BrowserToolsConfig
	if err := json.Unmarshal(body, &browserCfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if browserCfg.DefaultBackend != "" {
		if _, ok := browser.Lookup(browserCfg.DefaultBackend); !ok {
			http.Error(w,
				fmt.Sprintf("unknown default_backend %q", browserCfg.DefaultBackend),
				http.StatusBadRequest)
			return
		}
	}
	for id := range browserCfg.Backends {
		if _, ok := browser.Lookup(id); !ok {
			http.Error(w, fmt.Sprintf("unknown backend %q in backends", id), http.StatusBadRequest)
			return
		}
	}
	if st := strings.TrimSpace(browserCfg.SessionTimeout); st != "" {
		if d, err := time.ParseDuration(st); err != nil || d <= 0 {
			http.Error(w,
				fmt.Sprintf("invalid session_timeout %q (want a positive duration like \"10m\")", st),
				http.StatusBadRequest)
			return
		}
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()

	// LoadConfig already merges .security.yml, so cfg carries the resolved
	// stored secrets.
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Preserve fields the request did not include.
	existing := cfg.Tools.Browser
	if _, ok := sent["enabled"]; !ok {
		browserCfg.Enabled = existing.Enabled
	}
	if _, ok := sent["default_backend"]; !ok {
		browserCfg.DefaultBackend = existing.DefaultBackend
	}
	if _, ok := sent["session_timeout"]; !ok {
		browserCfg.SessionTimeout = existing.SessionTimeout
	}
	if _, ok := sent["private_host_whitelist"]; !ok {
		browserCfg.PrivateHostWhitelist = existing.PrivateHostWhitelist
	}
	if _, ok := sent["backends"]; !ok {
		browserCfg.Backends = existing.Backends
	}

	// Preserve stored API keys: the UI echoes the masked sentinel ("***") or
	// the SecureString "[NOT_HERE]" JSON marker for unchanged secret fields —
	// SecureString.UnmarshalJSON drops the latter, leaving APIKey empty — so
	// restore the previously stored key whenever the request did not carry a
	// real value. (This also means a key cannot be cleared via the UI.)
	savedBackends := existing.Backends
	cfg.Tools.Browser = browserCfg
	for id, bc := range cfg.Tools.Browser.Backends {
		if isMaskedBrowserKey(bc.APIKey.String()) {
			if old, ok := savedBackends[id]; ok {
				bc.APIKey = old.APIKey
				cfg.Tools.Browser.Backends[id] = bc
			}
		}
	}

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	logger.Infof("browser configuration updated")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleBrowserBackends returns the backend catalog enriched with install/
// configuration status for the current config.
//
//	GET /api/browser/backends
func (h *Handler) handleBrowserBackends(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}

	// Every backend except the stateless Cloudflare REST one is driven by
	// the agent-browser CLI — report its presence so the UI can flag
	// backends that look ready but would fail at first use.
	driverReady := browser.DriverInstalled()

	out := make([]backendStatus, 0)
	for _, spec := range browser.Catalog() {
		bcfg, configured := cfg.Tools.Browser.Backends[spec.ID]
		bs := backendStatus{
			BackendSpec: spec,
			Configured:  configured,
			IsDefault:   cfg.Tools.Browser.DefaultBackend == spec.ID,
		}
		bs.State = browser.Status(spec, bcfg)
		if browser.NeedsDriver(spec) {
			bs.DriverReady = &driverReady
		}
		if spec.Install.Binary != "" {
			bs.Version = browser.Version(spec.Install.Binary)
		}
		if spec.DiskMB > 0 && spec.Install.Method == "npm" && bs.State != "installed" {
			if free, ferr := browser.FreeBytes(browser.InstallVolumeDir()); ferr == nil {
				ok := free >= uint64(spec.DiskMB)*1024*1024
				bs.FreeSpaceOK = &ok
				if !ok {
					bs.FreeSpaceMsg = fmt.Sprintf(
						"Only %.1f GB free — %s needs ~%d MB. Consider a cloud backend instead.",
						float64(free)/(1<<30), spec.ID, spec.DiskMB)
				}
			}
		}
		out = append(out, bs)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"enabled":         cfg.Tools.Browser.Enabled,
		"default_backend": cfg.Tools.Browser.DefaultBackend,
		"backends":        out,
	}); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// handleBrowserDiskSpace reports free space on the volume where managed
// installs land (the OS user cache dir, not necessarily RHIZOME_HOME).
//
//	GET /api/browser/diskspace
func (h *Handler) handleBrowserDiskSpace(w http.ResponseWriter, r *http.Request) {
	free, err := browser.FreeBytes(browser.InstallVolumeDir())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to check disk space: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"free_bytes": free, "free_gb": float64(free) / (1 << 30)})
}

// handleBrowserInstall installs a backend (agent-browser via npm) with a
// disk-space gate for browser downloads.
//
//	POST /api/browser/install?backend=<id>
func (h *Handler) handleBrowserInstall(w http.ResponseWriter, r *http.Request) {
	spec, ok := browser.Lookup(r.URL.Query().Get("backend"))
	if !ok {
		http.Error(w, "unknown backend", http.StatusBadRequest)
		return
	}
	if spec.DiskMB > 0 && spec.Install.Method == "npm" {
		if free, ferr := browser.FreeBytes(browser.InstallVolumeDir()); ferr == nil &&
			free < uint64(spec.DiskMB)*1024*1024 {
			http.Error(w, fmt.Sprintf(
				"not enough disk space: %.1f GB free, %s needs ~%d MB",
				float64(free)/(1<<30), spec.ID, spec.DiskMB), http.StatusInsufficientStorage)
			return
		}
	}
	msg, err := browser.Install(r.Context(), spec, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": msg})
}

// handleBrowserUninstall removes a managed backend install.
//
//	POST /api/browser/uninstall?backend=<id>
func (h *Handler) handleBrowserUninstall(w http.ResponseWriter, r *http.Request) {
	spec, ok := browser.Lookup(r.URL.Query().Get("backend"))
	if !ok {
		http.Error(w, "unknown backend", http.StatusBadRequest)
		return
	}
	msg, err := browser.Uninstall(r.Context(), spec, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": msg})
}
