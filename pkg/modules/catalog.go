// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package modules implements the companion-module system: catalog-driven
// sidecar binaries that extend Rhizome without growing the base binary.
// Modules install under <RHIZOME_HOME>/modules/<id>/ and are either
// supervised daemons, on-demand tools, or config-only endpoint descriptors.
// The design generalizes the pkg/browser backend catalog + install pattern.
package modules

import (
	"fmt"
	"runtime"
	"strings"
)

// Kind describes a module's process lifecycle.
type Kind string

const (
	// KindDaemon is a supervised long-running process (e.g. a node) started
	// with the daemon and restarted on exit.
	KindDaemon Kind = "daemon"
	// KindOnDemand is a binary spawned per use (e.g. an agent process); it is
	// installed and health-checked but not supervised.
	KindOnDemand Kind = "ondemand"
	// KindConfig has no process at all — it surfaces a configured endpoint
	// (e.g. a remote RPC URL) to other parts of the system.
	KindConfig Kind = "config"
)

// ConfigField describes one setting a module accepts. It mirrors the
// browser AuthField shape: secrets are stored as SecureString values under
// modules.<id>.secrets and masked in logs/UI; everything else lives under
// modules.<id>.fields.
type ConfigField struct {
	Key      string `json:"key"`                // modules.<id>.fields/secrets key
	Label    string `json:"label"`              // UI label
	Env      string `json:"env,omitempty"`      // env var passed to the module process
	Arg      string `json:"arg,omitempty"`      // CLI flag passed as --<arg>=<value>
	Secret   bool   `json:"secret,omitempty"`   // store under secrets, mask in logs/UI
	Required bool   `json:"required,omitempty"` // required to run/enable the module
	Default  string `json:"default,omitempty"`  // applied when unset
}

// ReleasePin pins one upstream release: the exact version/build pair used in
// the asset name plus a per-platform sha256. Upstream asset names and
// digests change shape per project, so each release is pinned explicitly
// rather than fetched from a checksums file at install time.
type ReleasePin struct {
	Version string            `json:"version"`
	Build   string            `json:"build,omitempty"` // extra asset-name component (e.g. commit hash)
	SHA256  map[string]string `json:"sha256"`          // "goos/goarch" → lowercase hex digest
}

// InstallSpec describes how a module binary is obtained.
type InstallSpec struct {
	// Method: "github-release" (pinned download), "npm" (managed package),
	// "detect" (find an existing binary on PATH), or "config" (no binary).
	Method string `json:"method"`
	// Repo is the "owner/name" GitHub repository for github-release.
	Repo string `json:"repo,omitempty"`
	// TagTemplate builds the release tag; placeholders: {version}, {build}.
	// Defaults to "v{version}".
	TagTemplate string `json:"tag_template,omitempty"`
	// AssetTemplate builds the asset filename; placeholders: {goos},
	// {goarch}, {version}, {build}.
	AssetTemplate string `json:"asset_template,omitempty"`
	// Releases pins installable versions (newest last). Empty means "no
	// pinned releases yet" — the module cannot be installed.
	Releases []ReleasePin `json:"releases,omitempty"`
	// Binary is the executable name inside the archive / on PATH.
	Binary string `json:"binary,omitempty"`
	// NpmPackage is the package name for the "npm" method (defaults to Binary).
	NpmPackage string `json:"npm_package,omitempty"`
	// Hint is shown when Method is "detect" or an install fails.
	Hint string `json:"hint,omitempty"`
}

// RunSpec describes how a daemon/on-demand module is launched.
type RunSpec struct {
	// ArgsTemplate is the argv template after the binary; "{key}" is
	// replaced by the resolved field/secret value (empty → arg dropped).
	ArgsTemplate []string `json:"args_template,omitempty"`
	// Env maps extra environment variables passed to the process; values
	// support "{key}" substitution like ArgsTemplate.
	Env map[string]string `json:"env,omitempty"`
	// Workdir is the process working directory relative to the module dir
	// ("." means the module dir itself).
	Workdir string `json:"workdir,omitempty"`
}

// HealthSpec describes how a running module's health is probed.
type HealthSpec struct {
	// Type: "tcp" (connect succeeds), "http" (GET 2xx), "jsonrpc" (POST
	// Method, any JSON-RPC response).
	Type string `json:"type"`
	// Target supports "{key}" substitution — e.g. "127.0.0.1:{rpc_port}" for
	// tcp, or a URL for http/jsonrpc.
	Target string `json:"target"`
	// Method is the JSON-RPC method name (jsonrpc) or URL path (http).
	Method string `json:"method,omitempty"`
}

// ModuleSpec is the static catalog entry for one companion module.
type ModuleSpec struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	License      string        `json:"license"`
	Kind         Kind          `json:"kind"`
	Platforms    []string      `json:"platforms,omitempty"` // "goos/goarch"; empty = all
	Install      InstallSpec   `json:"install"`
	Run          RunSpec       `json:"run,omitempty"`
	Health       HealthSpec    `json:"health,omitempty"`
	ConfigFields []ConfigField `json:"config_fields,omitempty"`
	Notes        string        `json:"notes,omitempty"`
}

// Platform returns the current runtime platform in "goos/goarch" form.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// Supports reports whether the module ships for the given "goos/goarch"
// platform. An empty Platforms list means every platform is supported.
func (s ModuleSpec) Supports(platform string) bool {
	if len(s.Platforms) == 0 {
		return true
	}
	for _, p := range s.Platforms {
		if p == platform {
			return true
		}
	}
	return false
}

// Field returns the catalog field with the given key.
func (s ModuleSpec) Field(key string) (ConfigField, bool) {
	for _, f := range s.ConfigFields {
		if f.Key == key {
			return f, true
		}
	}
	return ConfigField{}, false
}

// LatestRelease returns the newest pinned release, if any.
func (s ModuleSpec) LatestRelease() (ReleasePin, bool) {
	if len(s.Install.Releases) == 0 {
		return ReleasePin{}, false
	}
	return s.Install.Releases[len(s.Install.Releases)-1], true
}

// Release returns the pin for an exact version ("" or "latest" → newest).
func (s ModuleSpec) Release(version string) (ReleasePin, bool) {
	if version == "" || version == "latest" {
		return s.LatestRelease()
	}
	version = strings.TrimPrefix(version, "v")
	for _, r := range s.Install.Releases {
		if r.Version == version {
			return r, true
		}
	}
	return ReleasePin{}, false
}

// expand substitutes "{key}" placeholders in tmpl.
func expand(tmpl string, values map[string]string) string {
	return strings.NewReplacer(pairs(values)...).Replace(tmpl)
}

func pairs(values map[string]string) []string {
	out := make([]string, 0, len(values)*2)
	for k, v := range values {
		out = append(out, "{"+k+"}", v)
	}
	return out
}

// Tag resolves the release tag for a pin.
func (s ModuleSpec) Tag(r ReleasePin) string {
	tmpl := s.Install.TagTemplate
	if tmpl == "" {
		tmpl = "v{version}"
	}
	return expand(tmpl, map[string]string{"version": r.Version, "build": r.Build})
}

// Asset resolves the asset filename for a pin and platform.
func (s ModuleSpec) Asset(r ReleasePin) string {
	return expand(s.Install.AssetTemplate, map[string]string{
		"goos":    runtime.GOOS,
		"goarch":  runtime.GOARCH,
		"version": r.Version,
		"build":   r.Build,
	})
}

// downloadBaseURL is the release-host base. It is a variable so tests can
// point installs at an httptest server; production keeps the https default.
var downloadBaseURL = "https://github.com"

// DownloadURL builds the GitHub release asset URL for a pin.
func (s ModuleSpec) DownloadURL(r ReleasePin) string {
	return fmt.Sprintf(
		"%s/%s/releases/download/%s/%s",
		downloadBaseURL, s.Install.Repo, s.Tag(r), s.Asset(r),
	)
}

// catalog is the static list of companion modules. It is a package variable
// (not a literal inside Catalog) so tests can register synthetic modules.
var catalog = []ModuleSpec{
	{
		ID: "ethereum-rpc", Name: "Ethereum RPC (remote endpoint)",
		Kind:    KindConfig,
		License: "n/a",
		Description: "Exposes a remote Ethereum JSON-RPC endpoint without " +
			"running a node — the zero-binary answer for any platform.",
		Install: InstallSpec{Method: "config"},
		ConfigFields: []ConfigField{
			{
				Key:      "endpoint_url",
				Label:    "Endpoint URL (https://…)",
				Required: true,
			},
			{
				Key:    "api_key",
				Label:  "API key (optional; sent as Bearer token)",
				Secret: true,
			},
		},
		Notes: "Config-only: pair with the network page or your own scripts.",
	},
}

// Catalog returns the static list of companion modules.
func Catalog() []ModuleSpec {
	return catalog
}

// Lookup returns the catalog entry for a module id.
func Lookup(id string) (ModuleSpec, bool) {
	for _, spec := range catalog {
		if spec.ID == id {
			return spec, true
		}
	}
	return ModuleSpec{}, false
}
