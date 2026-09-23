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
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/sigverify"
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
	// Signature optionally declares an upstream cryptographic signature for
	// the release artifact (catalog schema v3). The pinned digest remains
	// the mandatory floor — a signature adds provenance, not a replacement
	// for integrity. Absent is fine; declared-but-unverifiable is fatal.
	Signature *ReleaseSignature `json:"signature,omitempty"`
}

// ReleaseSignature declares a detached upstream signature for a release
// artifact. Key is inline key material pinned inside the (itself signed or
// embedded) catalog — that is what anchors the check: a remotely fetched
// key would inherit only TLS trust and could be swapped alongside the
// artifact it claims to verify.
type ReleaseSignature struct {
	// Kind selects the verifier: "minisign" (.minisig files),
	// "cosign-blob" (raw blob signature + PEM public key), or "gpg"
	// (detached OpenPGP signature + armored public key).
	Kind string `json:"kind"`
	// URL is the signature artifact location; the same placeholders as
	// asset templates apply ({version}, {build}, {goos}, {goarch}) plus
	// {asset} for the resolved asset name and {tag} for the release tag.
	URL string `json:"url"`
	// Key is the verification key material, per kind:
	//   minisign:    the .pub file contents or its base64 line
	//   cosign-blob: PEM public key (ECDSA or Ed25519)
	//   gpg:         ASCII-armored (or binary) OpenPGP public key
	Key string `json:"key"`
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
	// AssetTemplates overrides AssetTemplate per GOOS (keyed by the runtime
	// GOOS name) — e.g. {"windows": "kubo_v{version}_{goos}-{goarch}.zip"}
	// for projects that ship a different archive format on one OS.
	AssetTemplates map[string]string `json:"asset_templates,omitempty"`
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
	// InitMarker is a path template; when the expanded path does not exist,
	// InitArgs run once before the main command (first-run repo/config
	// creation — e.g. `ipfs init`). InitMarker absent → InitArgs never run.
	InitMarker string `json:"init_marker,omitempty"`
	// InitArgs is the argv template run once when InitMarker is absent —
	// the same binary with these args. Values support {key} substitution.
	InitArgs []string `json:"init_args,omitempty"`
	// SetupArgs are argv templates run before EVERY launch — idempotent
	// config writes that track field changes across restarts (e.g. `ipfs
	// config Addresses.API …`).
	SetupArgs [][]string `json:"setup_args,omitempty"`
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
	tmpl := s.Install.AssetTemplate
	if t := s.Install.AssetTemplates[goos]; t != "" {
		tmpl = t
	}
	if alias, ok := s.Install.OSAliases[goos]; ok {
		goos = alias
	}
	return expand(tmpl, map[string]string{
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

// SignatureURL expands the declared signature URL template for a pin. It
// applies the same GOOS aliasing as Asset so {goos} resolves identically,
// plus {asset} and {tag} for publishers that name signatures after the
// artifact (e.g. "kubo_v{version}_{goos}-{goarch}.tar.gz.minisig" or
// "{asset}.minisig").
func (s ModuleSpec) SignatureURL(r ReleasePin) string {
	if r.Signature == nil {
		return ""
	}
	goos := runtime.GOOS
	if alias, ok := s.Install.OSAliases[goos]; ok {
		goos = alias
	}
	return expand(r.Signature.URL, map[string]string{
		"version": r.Version,
		"build":   r.Build,
		"goos":    goos,
		"goarch":  runtime.GOARCH,
		"asset":   s.Asset(r),
		"tag":     s.Tag(r),
	})
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
	{
		ID: "helios", Name: "Helios (Ethereum light client)",
		Kind:    KindDaemon,
		License: "MIT",
		Description: "Helios (a16z) — a fast Ethereum light client serving a " +
			"local JSON-RPC endpoint synced from beacon-chain data. " +
			"Complements nimbus-verified-proxy: linux/darwin only (no " +
			"Windows build), and it covers darwin/amd64 which nimbus lacks.",
		// Upstream ships linux amd64/arm64 and darwin amd64/arm64
		// (helios_{goos}_{goarch}.tar.gz, tag "0.11.1" — no v prefix).
		Platforms: []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"},
		Install: InstallSpec{
			Method:        "github-release",
			Repo:          "a16z/helios",
			TagTemplate:   "{version}",
			AssetTemplate: "helios_{goos}_{goarch}.tar.gz",
			Binary:        "helios",
			Releases: []ReleasePin{
				{
					Version: "0.11.1",
					SHA256: map[string]string{
						"linux/amd64":  "339bf4ce73073c53790e41e3217b6d91f0e5d8571132b9e88689997613162ddb",
						"linux/arm64":  "20132e1f772af246eac3885bcba3b54c21a98ac24027a5853eca2fb0edc5dab6",
						"darwin/amd64": "825ba2e16b82fa0b2f38d56717692e1828bbe852067954286ec673faee104651",
						"darwin/arm64": "fc88981c7fe12e1010115b7bfe61909cfae36f65177325562c57daa0eb30ae8c",
					},
				},
			},
		},
		// `helios ethereum <flags>` — the subcommand must lead argv, so all
		// settings ride env vars (clap env attrs) instead of field args.
		Run: RunSpec{Workdir: ".", ArgsTemplate: []string{"ethereum"}},
		Health: HealthSpec{
			Type:   "jsonrpc",
			Target: "http://{rpc_bind_ip}:{rpc_port}",
			Method: "eth_chainId",
		},
		ConfigFields: []ConfigField{
			{
				Key:      "execution_api_url",
				Label:    "Execution RPC URL (untrusted EL endpoint; may embed an API key)",
				Env:      "EXECUTION_RPC",
				Secret:   true,
				Required: true,
			},
			{
				Key:      "checkpoint",
				Label:    "Trusted weak-subjectivity checkpoint (0x… beacon block root)",
				Env:      "CHECKPOINT",
				Required: true,
			},
			{
				Key:    "consensus_rpc_url",
				Label:  "Consensus RPC URL (optional; default https://www.lightclientdata.org)",
				Env:    "CONSENSUS_RPC",
				Secret: true,
			},
			{
				Key:    "fallback",
				Label:  "Checkpoint-sync fallback URL (optional; used when the checkpoint goes stale)",
				Env:    "FALLBACK",
				Secret: true,
			},
			{
				Key:     "network",
				Label:   "Network (mainnet, sepolia, hoodi…)",
				Env:     "NETWORK",
				Default: "mainnet",
			},
			{
				Key:     "rpc_bind_ip",
				Label:   "JSON-RPC bind address",
				Env:     "RPC_BIND_IP",
				Default: "127.0.0.1",
			},
			{
				Key:     "rpc_port",
				Label:   "JSON-RPC port (8546 avoids colliding with nimbus's 8545 default)",
				Env:     "RPC_PORT",
				Default: "8546",
			},
			{
				Key:     "data_dir",
				Label:   "Helios data directory (checkpoint DB)",
				Env:     "DATA_DIR",
				Default: "{module_dir}/data",
			},
		},
		Notes: "Requires a recent checkpoint — fetch a finalized beacon block " +
			"root from a beacon API at /eth/v1/beacon/headers/finalized (same " +
			"source as nimbus's trusted_block_root). consensus_rpc_url is " +
			"optional (upstream default: lightclientdata.org). Default port " +
			"8546 leaves 8545 free for nimbus; set 8545 only when nimbus " +
			"isn't installed.",
	},
	{
		ID: "ipfs-kubo", Name: "IPFS Kubo",
		Kind:    KindDaemon,
		License: "MIT OR Apache-2.0",
		Description: "Kubo IPFS node — pinning, gateway, and DHT/libp2p " +
			"participation. The repo lives under the module dir; api/swarm/" +
			"gateway addresses are written into the repo config before each " +
			"start (kubo has no address flags — they're repo-config only).",
		// Upstream ships kubo_v{version}_{goos}-{goarch}.tar.gz for unix and
		// .zip for windows; archives wrap contents in a kubo/ dir.
		Platforms: []string{
			"linux/amd64", "linux/arm64",
			"darwin/amd64", "darwin/arm64",
			"windows/amd64", "windows/arm64",
		},
		Install: InstallSpec{
			Method:        "github-release",
			Repo:          "ipfs/kubo",
			TagTemplate:   "v{version}",
			AssetTemplate: "kubo_v{version}_{goos}-{goarch}.tar.gz",
			AssetTemplates: map[string]string{
				"windows": "kubo_v{version}_{goos}-{goarch}.zip",
			},
			Binary: "ipfs",
			Releases: []ReleasePin{
				{
					Version: "0.43.1",
					SHA512: map[string]string{
						"linux/amd64":   "ff53b2428794fc8cca39505d28c15b4cceaef4b90a09284f291f611fcdc8c06399690575fc89cbd4d85ef71f3652581296c6f2e710386f887c8edf36b6e89d71",
						"linux/arm64":   "70f082584651ef78fb5b07448be53bb0adf141aadcb7eea15ffa59bd9ca78fed46781c2cf5915b3528c591052f97486067fedb0a7a1415abd5bfd44942921501",
						"darwin/amd64":  "efe0c1561595fbf7b5bdc3fc651863697c2bf388eed5f8c94c163f60d9575201808538abba84f652718f015ad1af6e3c01ced1321ddb2082f934ba8952e4a4ab",
						"darwin/arm64":  "bc282d781d856736051ce6d216ba47d52bec703f1a54ab5fa20fba26a92e4af3e8fb131e220cdf01f20205f1effb1a5b7bedbdeff15e8edbbe8e5b01923a7cbb",
						"windows/amd64": "e611cecef25cddb10b7f907145383608d9b31219ad7120041aeacfe2313882da1944f2897fda29bc2d959dd357e4950089ec8f2cbd52b29f2fc472bcae4356d0",
						"windows/arm64": "a6f2578ad3386a567c437eeebac1646293276b5fb44a26a33195f4310ed7b06cab9dfe0e02e17f33ce8ba1beb4f1ccef45375a861c8cc60192f46da7e0c02166",
					},
				},
			},
		},
		Run: RunSpec{
			Workdir:    ".",
			InitMarker: "{repo_dir}/config",
			InitArgs:   []string{"init", "--profile={profile}"},
			SetupArgs: [][]string{
				{"config", "Addresses.API", "/ip4/127.0.0.1/tcp/{api_port}"},
				{"config", "Addresses.Gateway", "/ip4/127.0.0.1/tcp/{gateway_port}"},
				{
					"config", "--json", "Addresses.Swarm",
					`["/ip4/0.0.0.0/tcp/{swarm_port}","/ip6/::/tcp/{swarm_port}",` +
						`"/ip4/0.0.0.0/udp/{swarm_port}/quic-v1","/ip6/::/udp/{swarm_port}/quic-v1",` +
						`"/ip4/0.0.0.0/udp/{swarm_port}/quic-v1/webtransport",` +
						`"/ip6/::/udp/{swarm_port}/quic-v1/webtransport"]`,
				},
			},
			ArgsTemplate: []string{"daemon", "--routing={routing}"},
		},
		Health: HealthSpec{Type: "tcp", Target: "127.0.0.1:{api_port}"},
		ConfigFields: []ConfigField{
			{
				Key:     "repo_dir",
				Label:   "IPFS repo directory (IPFS_PATH)",
				Env:     "IPFS_PATH",
				Default: "{module_dir}/repo",
			},
			{
				Key:     "profile",
				Label:   "Init profile (default-networking, server, lowpower, …)",
				Default: "default-networking",
			},
			{
				Key:     "api_port",
				Label:   "API port (loopback only)",
				Default: "5001",
			},
			{
				Key:     "gateway_port",
				Label:   "Gateway port (loopback only)",
				Default: "8080",
			},
			{
				Key:     "swarm_port",
				Label:   "Swarm listen port (tcp + quic-v1 + webtransport)",
				Default: "4001",
			},
			{
				Key:     "routing",
				Label:   "Routing mode (dht, dhtclient, dhtserver, none)",
				Default: "dht",
			},
		},
		Notes: "First start runs `ipfs init --profile=<profile>` (repo stays " +
			"under the module dir — uninstall removes it). profile applies at " +
			"init only; use `server` on public-internet hosts, `lowpower` on " +
			"constrained ones. Port fields re-apply to the repo config on " +
			"every start. API/gateway bind loopback; swarm listens on all " +
			"interfaces for DHT participation.",
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
//
// The Ed25519 release-signing trust root lives in pkg/sigverify
// (sigverify.ReleasePubKeyB64) — it is shared with the curated skills
// index. The matching private key lives only in the
// MODULE_CATALOG_SIGNING_KEY GitHub secret — see
// docs/operations/module-catalog-signing.md.

// catalogVersionSupported is the highest remote-catalog schema version this
// binary understands. Catalogs carrying a newer version are refused so an
// old binary never silently misreads a newer wire shape. v2 adds the
// RunSpec init_marker/init_args/setup_args and InstallSpec asset_templates
// fields — a v1 binary would silently drop them and misrun modules that
// depend on them, so catalogs that use them must declare version 2.
// v3 adds ReleasePin.signature — a binary that cannot verify a declared
// signature must not silently skip it, so catalogs declaring one must
// declare version 3.
const catalogVersionSupported = 3

// catalogVersionRequired returns the lowest schema version that covers the
// given module set — MarshalCatalog emits it rather than always emitting
// the newest version, so older binaries keep working until a catalog
// actually uses a field they cannot honor.
func catalogVersionRequired(specs []ModuleSpec) int {
	v := 1
	for _, spec := range specs {
		if len(spec.Install.AssetTemplates) > 0 || spec.Run.InitMarker != "" ||
			len(spec.Run.InitArgs) > 0 || len(spec.Run.SetupArgs) > 0 {
			v = 2
		}
		for _, r := range spec.Install.Releases {
			if r.Signature != nil {
				return 3
			}
		}
	}
	return v
}

// CatalogEnvelope is the signed remote-catalog wire format served at
// <module_index.url>/catalog.json.
type CatalogEnvelope struct {
	CatalogVersion int          `json:"catalog_version"`
	GeneratedAt    string       `json:"generated_at,omitempty"`
	Modules        []ModuleSpec `json:"modules"`
}

// MarshalCatalog renders the catalog in its canonical signed form — the
// exact bytes release signing covers (json.MarshalIndent is deterministic:
// struct field order plus sorted map keys). The emitted catalog_version is
// the lowest that covers the content (see catalogVersionRequired).
func MarshalCatalog() ([]byte, error) {
	return json.MarshalIndent(CatalogEnvelope{
		CatalogVersion: catalogVersionRequired(catalog),
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Modules:        catalog,
	}, "", "  ")
}

// VerifyCatalogSignature checks sigB64 (base64 Ed25519 signature) over the
// exact catalog bytes as published.
func VerifyCatalogSignature(data []byte, sigB64 string) error {
	return sigverify.VerifyReleaseSignature(data, sigB64)
}

// GenerateCatalogKeypair creates a fresh Ed25519 catalog signing keypair.
// Returns (base64 public key, base64 seed). Backs `rhizome module
// catalog-keygen`; the seed belongs in the MODULE_CATALOG_SIGNING_KEY
// GitHub secret, the pubkey in pkg/sigverify ReleasePubKeyB64.
func GenerateCatalogKeypair() (pubB64, seedB64 string, err error) {
	return sigverify.GenerateKeypair()
}

// SignCatalog signs catalog bytes with a base64-encoded Ed25519 seed and
// returns the base64 signature. Used by `rhizome module catalog --sign`
// during release, and by tests.
func SignCatalog(data []byte, seedB64 string) (string, error) {
	return sigverify.SignRelease(data, seedB64)
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
		for _, r := range spec.Install.Releases {
			if err := validateReleaseSignature(spec.ID, r.Signature); err != nil {
				return nil, err
			}
		}
		if seen[spec.ID] {
			return nil, fmt.Errorf("catalog lists %q twice", spec.ID)
		}
		seen[spec.ID] = true
	}
	return env.Modules, nil
}

// validateReleaseSignature rejects malformed signature declarations: a
// present-but-broken declaration must fail closed at the schema gate
// rather than at install time.
func validateReleaseSignature(id string, sig *ReleaseSignature) error {
	if sig == nil {
		return nil
	}
	switch sig.Kind {
	case "minisign", "cosign-blob", "gpg":
	default:
		return fmt.Errorf("catalog entry %q: unsupported signature kind %q", id, sig.Kind)
	}
	if sig.URL == "" {
		return fmt.Errorf("catalog entry %q: signature missing url", id)
	}
	if sig.Key == "" {
		return fmt.Errorf("catalog entry %q: signature missing key", id)
	}
	return nil
}
