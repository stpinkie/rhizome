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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
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
	Flag     bool   `json:"flag,omitempty"`     // with Arg: emit bare --<arg> when value is truthy (true/1/yes/on)
	Secret   bool   `json:"secret,omitempty"`   // store under secrets, mask in logs/UI
	Required bool   `json:"required,omitempty"` // required to run/enable the module
	Default  string `json:"default,omitempty"`  // applied when unset
}

// ReleasePin pins one upstream release: the exact version/build pair used in
// the asset name plus per-platform digests. Upstream asset names and digest
// algorithms change shape per project, so each release is pinned explicitly
// rather than fetched from a checksums file at install time. A platform must
// appear in exactly one digest map — SHA512 exists because upstreams like
// nimbus-eth1 publish sha512, not sha256.
type ReleasePin struct {
	Version string            `json:"version"`
	Build   string            `json:"build,omitempty"`  // extra asset-name component (e.g. commit hash)
	SHA256  map[string]string `json:"sha256,omitempty"` // "goos/goarch" → lowercase hex digest
	SHA512  map[string]string `json:"sha512,omitempty"` // "goos/goarch" → lowercase hex digest
}

// Digest returns the pinned digest for a platform and its algorithm name
// ("sha256"/"sha512"), or ("", "") when the platform is not pinned.
func (r ReleasePin) Digest(platform string) (digest, algo string) {
	if d := r.SHA256[platform]; d != "" {
		return d, "sha256"
	}
	if d := r.SHA512[platform]; d != "" {
		return d, "sha512"
	}
	return "", ""
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
	// OSAliases maps GOOS names to the upstream asset naming when they
	// differ — e.g. {"darwin": "macos"} for projects that label macOS
	// assets "macos".
	OSAliases map[string]string `json:"os_aliases,omitempty"`
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

// isTruthy reports whether a flag-typed field value means "on".
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
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
	goos := runtime.GOOS
	if alias, ok := s.Install.OSAliases[goos]; ok {
		goos = alias
	}
	return expand(s.Install.AssetTemplate, map[string]string{
		"goos":    goos,
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
		ID: "nimbus-verified-proxy", Name: "Nimbus Verified Proxy",
		Kind:    KindDaemon,
		License: "MIT OR Apache-2.0",
		Description: "Verified Ethereum JSON-RPC: a consensus light client " +
			"(status-im/nimbus-eth1) that serves execution API responses " +
			"verified against beacon-chain proofs. You supply an untrusted " +
			"execution endpoint and a trusted block root; light-client data " +
			"syncs over the beacon P2P network by default.",
		// Upstream ships linux amd64/arm64, windows amd64, macos arm64.
		Platforms: []string{"linux/amd64", "linux/arm64", "windows/amd64", "darwin/arm64"},
		Install: InstallSpec{
			Method:        "github-release",
			Repo:          "status-im/nimbus-eth1",
			TagTemplate:   "v{version}",
			AssetTemplate: "nimbus_verified_proxy-{goos}-{goarch}-v{version}-{build}.tar.gz",
			OSAliases:     map[string]string{"darwin": "macos"},
			Binary:        "nimbus_verified_proxy",
			Releases: []ReleasePin{
				{
					Version: "0.4.1",
					Build:   "ec214533",
					SHA512: map[string]string{
						"linux/amd64":   "0d9589645e5946370341e7084fd9c4b2e177ed42f4f1e74d171ee5370f5b22a2ba83db3130f5a3c1d56f6cfc116e94f3cd161bdb5a5d0251e27b605890225125",
						"linux/arm64":   "a769c2c54d0fdc74db7a55a9d80783187c913fad3cd1590d826be7191abdc0c131f0344e506b788ad682d2c1e701445464c43ff0d0113d1600cf42e519c74708",
						"windows/amd64": "28ab4e0dc1e7c01e467c34fec390ebaeb1bcb049f20aa343cc15ea4a17234e8efdd3e7f927aa3ac50c890a387401dd7b9bda08e2e8cfb81359c2a7ee1f1b72a2",
						"darwin/arm64":  "2d044a5163570e638c4c4eee94e771e5f2d75462748a4f7110857bfe6285408f082306939190325dec5abf813f711376b9e000cd4e04d68f5435d3f9cddb9bad",
					},
				},
			},
		},
		Run: RunSpec{Workdir: "."},
		Health: HealthSpec{
			Type:   "jsonrpc",
			Target: "{listen_url}",
			Method: "eth_chainId",
		},
		ConfigFields: []ConfigField{
			{
				Key:      "execution_api_url",
				Label:    "Execution API URL (untrusted EL RPC; may embed an API key)",
				Arg:      "execution-api-url",
				Secret:   true,
				Required: true,
			},
			{
				Key:     "p2p",
				Label:   "Sync light-client data over the beacon P2P network (no beacon endpoint needed)",
				Arg:     "p2p",
				Flag:    true,
				Default: "true",
			},
			{
				Key:    "beacon_api_url",
				Label:  "Beacon REST API URL (optional supplement to p2p; required when p2p=false)",
				Arg:    "beacon-api-url",
				Secret: true,
			},
			{
				Key:      "trusted_block_root",
				Label:    "Trusted finalized block root (0x…)",
				Arg:      "trusted-block-root",
				Required: true,
			},
			{
				Key:     "network",
				Label:   "Network (mainnet, sepolia, hoodi…)",
				Arg:     "network",
				Default: "mainnet",
			},
			{
				Key:     "listen_url",
				Label:   "Listen URL for the verified RPC",
				Arg:     "listen-url",
				Default: "http://127.0.0.1:8545",
			},
			{
				Key:     "p2p_tcp_port",
				Label:   "P2P TCP port",
				Arg:     "p2p-tcp-port",
				Default: "9000",
			},
			{
				Key:     "p2p_udp_port",
				Label:   "P2P UDP port (discovery)",
				Arg:     "p2p-udp-port",
				Default: "9000",
			},
			{
				Key:     "p2p_max_peers",
				Label:   "P2P target peer count",
				Arg:     "p2p-max-peers",
				Default: "160",
			},
		},
		Notes: "Syncs the consensus light client over the beacon P2P network by " +
			"default — set p2p=false to use only a beacon REST endpoint. " +
			"Requires a recent trusted_block_root — fetch one from a beacon API " +
			"at /eth/v1/beacon/headers/finalized. eth_syncing reports progress.",
	},
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

// ── Signed catalog (Track 70) ────────────────────────────────────────────

// releasePubKeyB64 is the baked-in Ed25519 public key (base64) that signs
// release catalogs. The matching private key lives only in the
// MODULE_CATALOG_SIGNING_KEY GitHub secret — see
// docs/operations/module-catalog-signing.md. It is a variable so tests can
// substitute their own keypair.
var releasePubKeyB64 = "Ww3Kz/J38L0ColSCrcOjq6I/3WCzsvZMvecGyyrDQzI="

// catalogVersionSupported is the highest remote-catalog schema version this
// binary understands. Catalogs carrying a newer version are refused so an
// old binary never silently misreads a newer wire shape.
const catalogVersionSupported = 1

// CatalogEnvelope is the signed remote-catalog wire format served at
// <module_index.url>/catalog.json.
type CatalogEnvelope struct {
	CatalogVersion int          `json:"catalog_version"`
	GeneratedAt    string       `json:"generated_at,omitempty"`
	Modules        []ModuleSpec `json:"modules"`
}

// MarshalCatalog renders the catalog in its canonical signed form — the
// exact bytes release signing covers (json.MarshalIndent is deterministic:
// struct field order plus sorted map keys).
func MarshalCatalog() ([]byte, error) {
	return json.MarshalIndent(CatalogEnvelope{
		CatalogVersion: catalogVersionSupported,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Modules:        catalog,
	}, "", "  ")
}

// catalogPubKey decodes the baked-in release public key.
func catalogPubKey() (ed25519.PublicKey, error) {
	if releasePubKeyB64 == "" {
		return nil, errors.New("no module catalog signing key baked into this build")
	}
	raw, err := base64.StdEncoding.DecodeString(releasePubKeyB64)
	if err != nil {
		return nil, fmt.Errorf("catalog pubkey: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("catalog pubkey: %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// VerifyCatalogSignature checks sigB64 (base64 Ed25519 signature) over the
// exact catalog bytes as published.
func VerifyCatalogSignature(data []byte, sigB64 string) error {
	pub, err := catalogPubKey()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("catalog signature: %w", err)
	}
	if !ed25519.Verify(pub, data, sig) {
		return errors.New("catalog signature does not verify")
	}
	return nil
}

// GenerateCatalogKeypair creates a fresh Ed25519 catalog signing keypair.
// Returns (base64 public key, base64 seed). Backs `rhizome module
// catalog-keygen`; the seed belongs in the MODULE_CATALOG_SIGNING_KEY
// GitHub secret, the pubkey in releasePubKeyB64 above.
func GenerateCatalogKeypair() (pubB64, seedB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub),
		base64.StdEncoding.EncodeToString(priv.Seed()), nil
}

// SignCatalog signs catalog bytes with a base64-encoded Ed25519 seed and
// returns the base64 signature. Used by `rhizome module catalog --sign`
// during release, and by tests.
func SignCatalog(data []byte, seedB64 string) (string, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(seedB64))
	if err != nil {
		return "", fmt.Errorf("catalog signing seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("catalog signing seed: %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// parseCatalogEnvelope unmarshals and sanity-checks signed catalog bytes.
func parseCatalogEnvelope(data []byte) ([]ModuleSpec, error) {
	var env CatalogEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("catalog JSON: %w", err)
	}
	if env.CatalogVersion < 1 || env.CatalogVersion > catalogVersionSupported {
		return nil, fmt.Errorf("unsupported catalog_version %d (this build understands ≤ %d)",
			env.CatalogVersion, catalogVersionSupported)
	}
	seen := map[string]bool{}
	for i, spec := range env.Modules {
		if spec.ID == "" || spec.Name == "" {
			return nil, fmt.Errorf("catalog entry %d: id/name required", i)
		}
		switch spec.Kind {
		case KindDaemon, KindOnDemand, KindConfig:
		default:
			return nil, fmt.Errorf("catalog entry %q: unknown kind %q", spec.ID, spec.Kind)
		}
		if seen[spec.ID] {
			return nil, fmt.Errorf("catalog lists %q twice", spec.ID)
		}
		seen[spec.ID] = true
	}
	return env.Modules, nil
}
