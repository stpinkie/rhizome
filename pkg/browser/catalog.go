// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package browser implements the pluggable browser-automation backend system.
// A single driver (the agent-browser CLI) drives local Chromium, system
// Chrome/Edge, remote CDP endpoints, and cloud provider sessions; Cloudflare
// is served by a stateless REST client. See docs/guides/browser-automation.md.
package browser

// BackendKind classifies how a backend produces a controllable browser.
type BackendKind string

const (
	// KindLocalCLI launches its own browser via the agent-browser CLI.
	KindLocalCLI BackendKind = "local-cli"
	// KindSpawnCDP spawns a local browser process and yields a CDP endpoint.
	KindSpawnCDP BackendKind = "spawn-cdp"
	// KindCustom uses a user-supplied CDP endpoint URL.
	KindCustom BackendKind = "custom"
	// KindNativeCDP is a CDP endpoint driven by the built-in Go client
	// (pkg/browser/cdp) — no agent-browser CLI required. Supports
	// authenticated ws/wss endpoints via headers (e.g. Cloudflare).
	KindNativeCDP BackendKind = "native-cdp"
	// KindProviderEnv uses an agent-browser provider plugin (-p <name>) with
	// credentials passed via environment variables.
	KindProviderEnv BackendKind = "provider-env"
	// KindCloudSession creates a remote session via a provider REST API and
	// yields a CDP WebSocket URL; sessions must be released/stopped.
	KindCloudSession BackendKind = "cloud-session"
	// KindCloudREST is a stateless REST backend (Cloudflare Browser Run) with
	// no interactive session — snapshot/screenshot/content only.
	KindCloudREST BackendKind = "cloud-rest"
)

// Capability is a browser action a backend may support.
type Capability string

const (
	CapOpen       Capability = "open"
	CapSnapshot   Capability = "snapshot"
	CapClick      Capability = "click"
	CapFill       Capability = "fill"
	CapScreenshot Capability = "screenshot"
	CapEval       Capability = "eval"
	CapWait       Capability = "wait"
)

// AuthField describes one credential/setting a backend accepts. Secret fields
// are stored as SecureString and masked in logs/UI.
type AuthField struct {
	Key      string `json:"key"`      // BrowserBackendConfig field / env mapping key
	Label    string `json:"label"`    // UI label
	Env      string `json:"env"`      // env var name used when passing to the driver
	Secret   bool   `json:"secret"`   // mask in UI/logs
	Required bool   `json:"required"` // required to use the backend
}

// InstallSpec describes how a backend is installed/activated.
type InstallSpec struct {
	// Method: "npm" (managed install), "detect" (find existing binary), or
	// "config" (no install — just credentials/endpoint).
	Method string `json:"method"`
	// Binary is the executable checked on PATH for "detect"/"npm" methods.
	Binary string `json:"binary,omitempty"`
	// Hint is shown in the UI/docs when Method is "detect" or install fails.
	Hint string `json:"hint,omitempty"`
}

// BackendSpec is the static catalog entry for one browser backend.
type BackendSpec struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Kind     BackendKind  `json:"kind"`
	License  string       `json:"license"`
	Status   string       `json:"status"` // "working" | "byo" | "rest-only"
	Caps     []Capability `json:"capabilities"`
	Auth     []AuthField  `json:"auth"`
	Install  InstallSpec  `json:"install"`
	DiskMB   int64        `json:"disk_estimate_mb"`
	Notes    string       `json:"notes"`
	Provider string       `json:"provider,omitempty"` // agent-browser -p name (provider-env kind)
}

var allCaps = []Capability{
	CapOpen, CapSnapshot, CapClick, CapFill, CapScreenshot, CapEval, CapWait,
}

// Catalog returns the static list of supported browser backends.
func Catalog() []BackendSpec {
	return []BackendSpec{
		{
			ID: "agent-browser", Name: "agent-browser (local Chromium)", Kind: KindLocalCLI,
			License: "Apache-2.0", Status: "working", Caps: allCaps,
			Install: InstallSpec{
				Method: "npm", Binary: "agent-browser",
				Hint: "Requires Node.js. Installs agent-browser and downloads Chromium.",
			},
			DiskMB: 400,
			Notes:  "Default backend. Runs headless Chromium locally via the agent-browser CLI.",
		},
		{
			ID: "system-chrome", Name: "System Chrome/Edge", Kind: KindSpawnCDP,
			License: "proprietary-binary", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "executable_path", Label: "Browser executable path (optional)"},
			},
			Install: InstallSpec{
				Method: "detect",
				Hint:   "Uses your installed Chrome or Edge — no download needed. Still requires agent-browser to drive it.",
			},
			Notes: "Launches your installed browser with a dedicated profile and remote debugging.",
		},
		{
			ID:      "clawbrowser",
			Name:    "ClawBrowser",
			Kind:    KindSpawnCDP,
			License: "MIT",
			Status:  "byo",
			Caps:    allCaps,
			Auth: []AuthField{
				{
					Key:      "api_key",
					Label:    "ClawBrowser API key",
					Env:      "CLAWBROWSER_API_KEY",
					Secret:   true,
					Required: true,
				},
				{Key: "session_name", Label: "Session/profile name"},
			},
			Install: InstallSpec{
				Method: "detect", Binary: "clawctl",
				Hint: "Bring-your-own install: run `clawctl install` first (see clawbrowser.dev).",
			},
			Notes: "Anti-detect Chromium with engine-level fingerprint patches. Requires an app.clawbrowser.ai API key.",
		},
		{
			ID: "custom-cdp", Name: "Custom CDP endpoint", Kind: KindCustom,
			License: "n/a", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "endpoint_url", Label: "CDP endpoint URL (ws://, wss:// or http://)", Required: true},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Any CDP endpoint: remote Chrome, self-hosted browserless, SSH tunnel, etc.",
		},
		{
			ID:      "rhizome-cdp",
			Name:    "Native CDP (built-in Go client)",
			Kind:    KindNativeCDP,
			License: "n/a",
			Status:  "working",
			Caps:    allCaps,
			Auth: []AuthField{
				{Key: "endpoint_url", Label: "CDP endpoint URL (ws://, wss:// or http://)", Required: true},
				{
					Key:    "api_key",
					Label:  "Auth token (sent as Authorization: Bearer on the WebSocket handshake — e.g. Cloudflare)",
					Secret: true,
				},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Drives any CDP endpoint natively — no Node.js/agent-browser. Authenticated wss:// endpoints (e.g. Cloudflare Browser Rendering connect) are supported via api_key.",
		},
		{
			ID: "provider", Name: "agent-browser provider plugin", Kind: KindProviderEnv,
			License: "n/a", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "provider", Label: "Provider name (browseruse, agentcore, ios, ...)", Required: true},
				{
					Key:   "env",
					Label: "Env vars as a JSON object — provider credentials go here (e.g. {\"BROWSERUSE_API_KEY\": \"...\"}); values under *KEY*/*TOKEN*/*SECRET*-looking names are masked in logs",
				},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Generic passthrough to agent-browser's -p provider plugins, including future ones.",
		},
		{
			ID: "browserbase", Name: "Browserbase", Kind: KindProviderEnv,
			License: "proprietary", Status: "working", Caps: allCaps,
			Provider: "browserbase",
			Auth: []AuthField{
				{Key: "api_key", Label: "API key", Env: "BROWSERBASE_API_KEY", Secret: true, Required: true},
				{Key: "project_id", Label: "Project ID", Env: "BROWSERBASE_PROJECT_ID", Required: true},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Managed cloud browsers. Free tier available.",
		},
		{
			ID: "browserless", Name: "Browserless", Kind: KindProviderEnv,
			License: "SSPL-1.0 (self-host) / commercial (cloud)", Status: "working", Caps: allCaps,
			Provider: "browserless",
			Auth: []AuthField{
				{Key: "api_key", Label: "API token", Env: "BROWSERLESS_API_KEY", Secret: true, Required: true},
				{Key: "base_url", Label: "API URL (self-host override)", Env: "BROWSERLESS_API_URL"},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Cloud or self-hosted. BROWSERLESS_STEALTH defaults on. Set base_url for self-host.",
		},
		{
			ID: "kernel", Name: "Kernel", Kind: KindProviderEnv,
			License: "proprietary", Status: "working", Caps: allCaps,
			Provider: "kernel",
			Auth: []AuthField{
				{Key: "api_key", Label: "API key", Env: "KERNEL_API_KEY", Secret: true, Required: true},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Cloud browsers for agents. Set env KERNEL_STEALTH=true for stealth mode.",
		},
		{
			ID: "steel", Name: "Steel", Kind: KindCloudSession,
			License: "Apache-2.0 (core)", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "api_key", Label: "API key", Env: "STEEL_API_KEY", Secret: true},
				{Key: "base_url", Label: "Base URL (self-host)", Env: "STEEL_BASE_URL"},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Self-hostable (docker run steel) or cloud. Sessions are released on browser_close.",
		},
		{
			ID: "hyperbrowser", Name: "Hyperbrowser", Kind: KindCloudSession,
			License: "proprietary", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "api_key", Label: "API key", Env: "HYPERBROWSER_API_KEY", Secret: true, Required: true},
				{Key: "base_url", Label: "Base URL"},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Stealth-focused cloud browsers. Sessions are stopped on browser_close.",
		},
		{
			ID: "tinyfish", Name: "TinyFish", Kind: KindCloudSession,
			License: "proprietary", Status: "working", Caps: allCaps,
			Auth: []AuthField{
				{Key: "api_key", Label: "API key", Env: "TINYFISH_API_KEY", Secret: true, Required: true},
				{Key: "base_url", Label: "Base URL"},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Agent-focused cloud browsers. Sessions cap at ~60 min and auto-close on inactivity.",
		},
		{
			ID: "cloudflare", Name: "Cloudflare Browser Rendering", Kind: KindCloudREST,
			License: "proprietary", Status: "rest-only",
			Caps: []Capability{CapOpen, CapSnapshot, CapScreenshot},
			Auth: []AuthField{
				{Key: "api_key", Label: "API token (Browser Rendering - Edit)", Secret: true, Required: true},
				{Key: "account_id", Label: "Account ID", Required: true},
			},
			Install: InstallSpec{Method: "config"},
			Notes:   "Stateless REST: snapshot/screenshot/content only — click/fill/eval are not supported.",
		},
	}
}

// Lookup returns the catalog entry for a backend id.
func Lookup(id string) (BackendSpec, bool) {
	for _, spec := range Catalog() {
		if spec.ID == id {
			return spec, true
		}
	}
	return BackendSpec{}, false
}

// HasCapability reports whether the backend supports the given capability.
func (s BackendSpec) HasCapability(c Capability) bool {
	for _, cap := range s.Caps {
		if cap == c {
			return true
		}
	}
	return false
}
