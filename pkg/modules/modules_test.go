// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// registerTestSpec swaps in a synthetic catalog for the duration of a test.
func registerTestSpec(t *testing.T, specs ...ModuleSpec) {
	t.Helper()
	old := catalog
	catalog = specs
	t.Cleanup(func() { catalog = old })
}

func newTestManager(t *testing.T, cfg *config.Config) (*Manager, *config.Config, *string) {
	t.Helper()
	home := t.TempDir()
	var saved string
	mgr := NewManager(home, cfg, nil, func(c *config.Config) error {
		data, _ := json.Marshal(c.Modules)
		saved = string(data)
		return nil
	})
	return mgr, cfg, &saved
}

func testSpec(id string) ModuleSpec {
	return ModuleSpec{
		ID: id, Name: id, Kind: KindDaemon, License: "MIT",
		Install: InstallSpec{
			Method:        "github-release",
			Repo:          "acme/test",
			AssetTemplate: "test-{goos}-{goarch}-v{version}.tar.gz",
			Binary:        "testbin",
			Releases: []ReleasePin{
				{Version: "1.0.0", SHA256: map[string]string{Platform(): "deadbeef"}},
			},
		},
		ConfigFields: []ConfigField{
			{Key: "endpoint", Label: "Endpoint", Required: true},
			{Key: "token", Label: "Token", Secret: true},
			{Key: "port", Label: "Port", Default: "8545", Arg: "rpc-port"},
		},
	}
}

func TestCatalogLookup(t *testing.T) {
	spec, ok := Lookup("ethereum-rpc")
	if !ok {
		t.Fatal("ethereum-rpc not in catalog")
	}
	if spec.Kind != KindConfig {
		t.Fatalf("ethereum-rpc kind = %q, want config", spec.Kind)
	}
	if _, ok := Lookup("nonexistent"); ok {
		t.Fatal("Lookup returned unknown module")
	}
}

func TestPlatformAndReleaseResolution(t *testing.T) {
	spec := testSpec("x")
	spec.Platforms = []string{"linux/amd64"}
	if !spec.Supports("linux/amd64") {
		t.Fatal("Supports(linux/amd64) = false")
	}
	if spec.Supports(Platform()) && Platform() != "linux/amd64" {
		t.Fatalf("Supports(%s) = true, want false", Platform())
	}
	r, ok := spec.Release("v1.0.0")
	if !ok || r.Version != "1.0.0" {
		t.Fatalf("Release(v1.0.0) = %+v", r)
	}
	if _, ok := spec.Release("9.9.9"); ok {
		t.Fatal("Release(9.9.9) should fail")
	}
	if got := spec.Tag(r); got != "v1.0.0" {
		t.Fatalf("Tag = %q", got)
	}
	spec.Install.TagTemplate = "release-{version}-{build}"
	spec.Install.Releases[0].Build = "abc123"
	if got := spec.Tag(spec.Install.Releases[0]); got != "release-1.0.0-abc123" {
		t.Fatalf("Tag with build = %q", got)
	}
}

func TestStatusTransitions(t *testing.T) {
	registerTestSpec(t, testSpec("m1"))
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	info, err := mgr.Info("m1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != StatusMissing {
		t.Fatalf("status = %q, want missing", info.Status)
	}

	if err := mgr.markInstalled("m1", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	info, _ = mgr.Info("m1")
	if info.Status != StatusUnconfigured {
		t.Fatalf("status = %q, want unconfigured (required endpoint unset)", info.Status)
	}
	if len(info.MissingFields) != 1 || info.MissingFields[0] != "endpoint" {
		t.Fatalf("missing fields = %v", info.MissingFields)
	}

	if err := mgr.SetFields("m1", map[string]string{"endpoint": "http://x"}); err != nil {
		t.Fatal(err)
	}
	info, _ = mgr.Info("m1")
	if info.Status != StatusStopped {
		t.Fatalf("status = %q, want stopped (daemon kind, configured)", info.Status)
	}
}

func TestConfigModuleStatus(t *testing.T) {
	registerTestSpec(t, testSpec("m1")) // replaced below
	spec := testSpec("m2")
	spec.Kind = KindConfig
	spec.Install = InstallSpec{Method: "config"}
	registerTestSpec(t, spec)

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	info, _ := mgr.Info("m2")
	if info.Status != StatusUnconfigured {
		t.Fatalf("config module status = %q, want unconfigured", info.Status)
	}
	if err := mgr.SetFields("m2", map[string]string{"endpoint": "https://rpc.example"}); err != nil {
		t.Fatal(err)
	}
	info, _ = mgr.Info("m2")
	if info.Status != StatusConfigured {
		t.Fatalf("config module status = %q, want configured", info.Status)
	}
}

func TestUnsupportedPlatform(t *testing.T) {
	spec := testSpec("m3")
	spec.Platforms = []string{"plan9/mips"}
	registerTestSpec(t, spec)
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	info, _ := mgr.Info("m3")
	if info.Status != StatusUnsupported {
		t.Fatalf("status = %q, want unsupported", info.Status)
	}
	if err := mgr.Enable("m3", true); err == nil {
		t.Fatal("Enable on unsupported platform should fail")
	}
}

func TestSetFieldsAndSecretsRouting(t *testing.T) {
	registerTestSpec(t, testSpec("m1"))
	cfg := &config.Config{}
	mgr, _, saved := newTestManager(t, cfg)

	if err := mgr.SetFields("m1", map[string]string{"token": "x"}); err == nil {
		t.Fatal("SetFields accepted a secret field")
	}
	if err := mgr.SetSecrets("m1", map[string]string{"endpoint": "x"}); err == nil {
		t.Fatal("SetSecrets accepted a non-secret field")
	}
	if err := mgr.SetFields("m1", map[string]string{"nope": "x"}); err == nil {
		t.Fatal("SetFields accepted unknown field")
	}
	if err := mgr.SetSecrets("m1", map[string]string{"token": "s3cret"}); err != nil {
		t.Fatal(err)
	}
	// The SecureString must mask itself in JSON — plaintext never lands in
	// config.json representations.
	if *saved == "" || strings.Contains(*saved, "s3cret") {
		t.Fatalf("secret plaintext leaked into serialized config: %s", *saved)
	}
	mc := cfg.Modules["m1"]
	sec := mc.Secrets["token"]
	if sec.String() != "s3cret" {
		t.Fatalf("resolved secret = %q", sec.String())
	}
}

func TestEnableRequiresRequiredFields(t *testing.T) {
	registerTestSpec(t, testSpec("m1"))
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	if err := mgr.Enable("m1", true); err == nil {
		t.Fatal("Enable should fail while required endpoint is unset")
	}
	if err := mgr.SetFields("m1", map[string]string{"endpoint": "http://x"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Enable("m1", true); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !cfg.Modules["m1"].Enabled {
		t.Fatal("enabled flag not persisted to cfg.Modules")
	}
}

func TestValidateConfig(t *testing.T) {
	registerTestSpec(t, testSpec("m1"))

	cfg := &config.Config{Modules: config.ModulesConfig{
		"bogus": {Enabled: true},
	}}
	if err := ValidateConfig(cfg, nil); err == nil || !strings.Contains(err.Error(), "unknown module id") {
		t.Fatalf("expected unknown-id error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Fields: map[string]string{"token": "x"}},
	}}
	if err := ValidateConfig(cfg, nil); err == nil || !strings.Contains(err.Error(), "secret field") {
		t.Fatalf("expected secret-placement error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Enabled: true},
	}}
	if err := ValidateConfig(cfg, nil); err == nil || !strings.Contains(err.Error(), "required field") {
		t.Fatalf("expected required-field error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Enabled: true, Fields: map[string]string{"endpoint": "http://x"}},
	}}
	if err := ValidateConfig(cfg, nil); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// buildTarGz produces an in-memory .tar.gz with the given files.
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallReleaseVerifyAndExtract(t *testing.T) {
	spec := testSpec("m1")
	registerTestSpec(t, spec)

	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})
	sum := sha256.Sum256(asset)
	digest := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(asset)
	}))
	defer srv.Close()

	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	// Wrong digest → refuse to install.
	spec.Install.Releases[0].SHA256[Platform()] = strings.Repeat("0", 64)
	if err := mgr.Install(t.Context(), "m1", ""); err == nil ||
		!strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
	if mgr.installedVersion("m1") != "" {
		t.Fatal("module marked installed after checksum failure")
	}

	// Correct digest → installs.
	spec.Install.Releases[0].SHA256[Platform()] = digest
	if err := mgr.Install(t.Context(), "m1", "1.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := mgr.installedVersion("m1"); got != "1.0.0" {
		t.Fatalf("installed version = %q", got)
	}
	bin := mgr.binaryPath(spec)
	st, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("binary missing at %s: %v", bin, err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary not executable: mode %v", st.Mode())
	}
}

func TestInstallReleaseSHA512(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.Releases[0].SHA256 = nil
	spec.Install.Releases[0].SHA512 = map[string]string{Platform(): "deadbeef"}
	registerTestSpec(t, spec)

	asset := buildTarGz(t, map[string]string{"testbin": "#!/bin/sh\n"})
	sum := sha512.Sum512(asset)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(asset)
	}))
	defer srv.Close()
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)

	spec.Install.Releases[0].SHA512[Platform()] = strings.Repeat("0", 128)
	if err := mgr.Install(t.Context(), "m1", ""); err == nil ||
		!strings.Contains(err.Error(), "sha512 mismatch") {
		t.Fatalf("expected sha512 mismatch, got %v", err)
	}

	spec.Install.Releases[0].SHA512[Platform()] = hex.EncodeToString(sum[:])
	if err := mgr.Install(t.Context(), "m1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if got := mgr.installedVersion("m1"); got != "1.0.0" {
		t.Fatalf("installed version = %q", got)
	}
}

// TestInstallFlattensNestedBinary covers archives that wrap the binary in a
// subdirectory (nimbus ships build/nimbus_verified_proxy): the binary must
// land at the version-dir root where binaryPath looks.
func TestInstallFlattensNestedBinary(t *testing.T) {
	spec := testSpec("m1")
	registerTestSpec(t, spec)

	binName := "testbin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	asset := buildTarGz(t, map[string]string{
		"build/" + binName: "binary",
		"README.md":        "docs",
	})
	sum := sha256.Sum256(asset)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(asset)
	}))
	defer srv.Close()
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	spec.Install.Releases[0].SHA256[Platform()] = hex.EncodeToString(sum[:])
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	if err := mgr.Install(t.Context(), "m1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	bin := mgr.binaryPath(spec)
	if filepath.Base(bin) != binName {
		t.Fatalf("binaryPath base = %q, want %q", filepath.Base(bin), binName)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not at flattened path %s: %v", bin, err)
	}
	// The non-binary member keeps its archive path.
	if _, err := os.Stat(filepath.Join(mgr.Dir("m1"), "1.0.0", "README.md")); err != nil {
		t.Fatalf("README.md missing under version dir: %v", err)
	}
}

func TestAssetOSAlias(t *testing.T) {
	spec, ok := Lookup("nimbus-verified-proxy")
	if !ok {
		t.Skip("nimbus-verified-proxy not in catalog")
	}
	r, _ := spec.LatestRelease()
	asset := spec.Asset(r)
	if !strings.HasPrefix(asset, "nimbus_verified_proxy-") ||
		!strings.HasSuffix(asset, "-v0.4.1-ec214533.tar.gz") {
		t.Fatalf("unexpected asset name %q", asset)
	}
	wantOS := map[string]string{
		"linux": "linux", "windows": "windows", "darwin": "macos",
	}[runtime.GOOS]
	if wantOS != "" && !strings.Contains(asset, "-"+wantOS+"-") {
		t.Fatalf("asset %q lacks os label %q", asset, wantOS)
	}
}

func TestNimbusCatalogEntry(t *testing.T) {
	spec, ok := Lookup("nimbus-verified-proxy")
	if !ok {
		t.Fatal("nimbus-verified-proxy not in catalog")
	}
	if spec.Kind != KindDaemon {
		t.Fatalf("kind = %q, want daemon", spec.Kind)
	}
	r, ok := spec.LatestRelease()
	if !ok {
		t.Fatal("no pinned release")
	}
	// Every advertised platform must carry a digest.
	for _, p := range spec.Platforms {
		if d, algo := r.Digest(p); d == "" || algo != "sha512" {
			t.Fatalf("platform %s: digest=%q algo=%q", p, d, algo)
		}
	}
	for _, key := range []string{"execution_api_url", "trusted_block_root"} {
		f, ok := spec.Field(key)
		if !ok || !f.Required || f.Arg == "" {
			t.Fatalf("field %s missing/not required/no arg", key)
		}
	}
	// beacon_api_url is optional — p2p light-client sync is the default.
	if f, ok := spec.Field("beacon_api_url"); !ok || f.Required || f.Arg == "" {
		t.Fatal("beacon_api_url should exist as optional arg field")
	}
	if f, ok := spec.Field("p2p"); !ok || !f.Flag || f.Arg != "p2p" || f.Default != "true" {
		t.Fatalf("p2p flag field = %+v, want flag/default-true", f)
	}
	if spec.Health.Type != "jsonrpc" || spec.Health.Target != "{listen_url}" {
		t.Fatalf("health = %+v", spec.Health)
	}
}

func TestInstallRejectsMissingPlatformDigest(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.Releases[0].SHA256 = map[string]string{"other/os": "x"}
	registerTestSpec(t, spec)
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	if err := mgr.Install(t.Context(), "m1", ""); err == nil ||
		!strings.Contains(err.Error(), "no digest") {
		t.Fatalf("expected no-digest error, got %v", err)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "x"
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escape", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()

	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarGz(archive, t.TempDir(), ""); err == nil ||
		!strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line-%d", i))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := tailFile(path, 5)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(out, "\n")
	if len(got) != 5 || got[0] != "line-45" || got[4] != "line-49" {
		t.Fatalf("tail = %v", got)
	}
}

// TestHelperProcess is a fake module binary: prints to stdout/stderr, then
// sleeps until killed. Only runs when GO_MODULE_HELPER=1. (A bare select{}
// would trip the runtime deadlock detector — a timer keeps it alive.)
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_MODULE_HELPER") != "1" {
		return
	}
	fmt.Println("helper started")
	fmt.Fprintln(os.Stderr, "helper stderr")
	time.Sleep(24 * time.Hour)
}

func TestSupervisorStartStop(t *testing.T) {
	// detect-method daemon running the test binary itself.
	spec := ModuleSpec{
		ID: "helper", Name: "helper", Kind: KindDaemon,
		Install: InstallSpec{Method: "detect", Binary: os.Args[0]},
		Run: RunSpec{
			ArgsTemplate: []string{"-test.run=TestHelperProcess"},
			Env:          map[string]string{"GO_MODULE_HELPER": "1"},
		},
	}
	registerTestSpec(t, spec)

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	sup := NewSupervisor(mgr)
	defer sup.StopAll()

	if err := sup.Start("helper"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !sup.IsRunning("helper") {
		t.Fatal("IsRunning = false after Start")
	}
	// State file should carry the pid.
	st := mgr.loadState("helper")
	if st.PID == 0 {
		t.Fatal("state PID not recorded")
	}
	info, _ := mgr.Info("helper")
	if info.Status != StatusRunning {
		t.Fatalf("status = %q, want running", info.Status)
	}

	if err := sup.Stop("helper"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if sup.IsRunning("helper") {
		t.Fatal("IsRunning = true after Stop")
	}
	st = mgr.loadState("helper")
	if st.PID != 0 {
		t.Fatalf("state PID = %d after stop", st.PID)
	}
}

func TestSupervisorRestartOnExit(t *testing.T) {
	// Helper that exits immediately → supervisor restarts it (with backoff).
	spec := ModuleSpec{
		ID: "crasher", Name: "crasher", Kind: KindDaemon,
		Install: InstallSpec{Method: "detect", Binary: os.Args[0]},
		Run: RunSpec{
			// Runs the real test binary's -test.run filter against a test that
			// exits quickly (TestCatalogLookup), i.e. the process exits ~instantly.
			ArgsTemplate: []string{"-test.run=TestCatalogLookup"},
		},
	}
	registerTestSpec(t, spec)

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	sup := NewSupervisor(mgr)
	defer sup.StopAll()

	if err := sup.Start("crasher"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The first instance exits fast; wait for the monitor to record it.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := mgr.loadState("crasher")
		if st.Restarts > 0 {
			return // restart path exercised — module.crashed + relaunch queued
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("crash was not detected and restart not queued within 15s")
}

func TestSupervisorRejectsConfigKind(t *testing.T) {
	spec := testSpec("m2")
	spec.Kind = KindConfig
	spec.Install.Method = "config"
	registerTestSpec(t, spec)
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	sup := NewSupervisor(mgr)
	defer sup.StopAll()
	if err := sup.Start("m2"); err == nil {
		t.Fatal("Start on config-kind module should fail")
	}
}

func TestBuildCommandTemplating(t *testing.T) {
	spec := ModuleSpec{
		ID: "m1", Kind: KindDaemon,
		Install: InstallSpec{Method: "detect", Binary: os.Args[0]},
		Run: RunSpec{
			ArgsTemplate: []string{"--url={endpoint}", "--verbose", "{opt}"},
			Env:          map[string]string{"RPC": "{endpoint}"},
		},
		ConfigFields: []ConfigField{
			{Key: "endpoint", Label: "Endpoint", Required: true},
			{Key: "port", Label: "Port", Default: "8545", Arg: "rpc-port"},
			{Key: "opt", Label: "Opt"},
			{Key: "api_url", Label: "API URL", Arg: "api-url", Secret: true},
			{Key: "fast", Label: "Fast mode", Arg: "fast", Flag: true, Default: "true"},
			{Key: "slow", Label: "Slow mode", Arg: "slow", Flag: true, Default: "false"},
		},
	}
	registerTestSpec(t, spec)
	cfg := &config.Config{Modules: config.ModulesConfig{
		"m1": {
			Fields: map[string]string{"endpoint": "http://localhost:8545"},
			Secrets: map[string]config.SecureString{
				"api_url": *config.NewSecureString("https://key:secret@example.com"),
			},
		},
	}}
	mgr, _, _ := newTestManager(t, cfg)

	cmd, err := mgr.buildCommand(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	// Secret-valued args must reach argv (nimbus relies on this) but never
	// config.json — the Secrets map has no json tag.
	// Flag fields emit a bare --arg on truthy values and nothing on falsy.
	want := []string{
		"--rpc-port=8545", "--api-url=https://key:secret@example.com", "--fast",
		"--url=http://localhost:8545", "--verbose",
	}
	got := cmd.Args[1:]
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
	found := false
	for _, e := range cmd.Env {
		if e == "RPC=http://localhost:8545" {
			found = true
		}
	}
	if !found {
		t.Fatal("RPC env var not templated")
	}
}

// buildZip produces an in-memory .zip with the given files (mode-less —
// like a Windows-built archive).
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallZipVerifyAndExtract(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.AssetTemplate = "test-{goos}-{goarch}-v{version}.zip"
	registerTestSpec(t, spec)

	binName := "testbin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	// Wrap the binary in a subdirectory like kubo does (kubo/ipfs.exe).
	asset := buildZip(t, map[string]string{"pkg/" + binName: "binary", "pkg/README": "docs"})
	sum := sha256.Sum256(asset)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(asset)
	}))
	defer srv.Close()
	old := downloadBaseURL
	downloadBaseURL = srv.URL
	t.Cleanup(func() { downloadBaseURL = old })

	spec.Install.Releases[0].SHA256[Platform()] = hex.EncodeToString(sum[:])
	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	if err := mgr.Install(t.Context(), "m1", ""); err != nil {
		t.Fatalf("Install: %v", err)
	}
	bin := mgr.binaryPath(spec)
	if filepath.Base(bin) != binName {
		t.Fatalf("binary not flattened: %q", bin)
	}
	st, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("binary missing at %s: %v", bin, err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("binary not executable: mode %v", st.Mode())
	}
	// Non-binary members keep their archive path.
	if _, err := os.Stat(filepath.Join(mgr.Dir("m1"), "1.0.0", "pkg", "README")); err != nil {
		t.Fatalf("README missing under version dir: %v", err)
	}
	// The install-time digest record enables `module verify`.
	if _, err := mgr.Verify("m1"); err != nil {
		t.Fatalf("Verify after zip install: %v", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()

	archive := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZip(archive, t.TempDir(), ""); err == nil ||
		!strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("expected traversal rejection, got %v", err)
	}
}

func TestExtractZipRejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "link", Method: zip.Deflate}
	hdr.SetMode(0o777 | os.ModeSymlink)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("target"))
	_ = zw.Close()

	archive := filepath.Join(t.TempDir(), "ln.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZip(archive, t.TempDir(), ""); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestExtractZipRejectsMissingBinary(t *testing.T) {
	asset := buildZip(t, map[string]string{"docs/readme": "x"})
	archive := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(archive, asset, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZip(archive, t.TempDir(), "wantedbin"); err == nil ||
		!strings.Contains(err.Error(), "does not contain binary") {
		t.Fatalf("expected missing-binary error, got %v", err)
	}
}

func TestAssetTemplatesPerGOOS(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.AssetTemplates = map[string]string{
		runtime.GOOS: "override-{goos}-{goarch}-{version}.zip",
	}
	r := spec.Install.Releases[0]
	got := spec.Asset(r)
	want := "override-" + runtime.GOOS + "-" + runtime.GOARCH + "-1.0.0.zip"
	if got != want {
		t.Fatalf("Asset = %q, want %q", got, want)
	}
	// A map keyed for a different GOOS leaves the base template alone.
	spec.Install.AssetTemplates = map[string]string{"plan9": "x.zip"}
	if got := spec.Asset(r); !strings.HasSuffix(got, ".tar.gz") {
		t.Fatalf("non-matching override applied: %q", got)
	}
	// The kubo entry declares a .zip override for windows.
	kubo, ok := Lookup("ipfs-kubo")
	if !ok {
		t.Fatal("ipfs-kubo not in catalog")
	}
	if !strings.HasSuffix(kubo.Install.AssetTemplates["windows"], ".zip") {
		t.Fatalf("kubo windows asset template = %q, want .zip",
			kubo.Install.AssetTemplates["windows"])
	}
}

// TestModuleDirPlaceholder covers the {module_dir} reserved key: catalog
// defaults expand it at collection, templates can reference it, and a
// user-supplied field value cannot shadow it.
func TestModuleDirPlaceholder(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.Method = "detect"
	spec.Install.Binary = os.Args[0]
	spec.ConfigFields = append(spec.ConfigFields, ConfigField{
		Key: "data_dir", Label: "Data dir", Arg: "data-dir",
		Default: "{module_dir}/data",
	})
	registerTestSpec(t, spec)
	cfg := &config.Config{Modules: config.ModulesConfig{
		// Bypass SetFields (it would reject the unknown key) — prove the
		// reserved key wins even when smuggled in directly.
		"m1": {Fields: map[string]string{
			"endpoint":   "http://x",
			"module_dir": "/evil",
		}},
	}}
	mgr, _, _ := newTestManager(t, cfg)

	values := mgr.resolvedFields(spec, true)
	// Expansion is literal string substitution — no path cleaning, so a
	// catalog default like "{module_dir}/data" keeps its slash.
	wantData := mgr.Dir("m1") + "/data"
	if values["data_dir"] != wantData {
		t.Fatalf("data_dir = %q, want %q", values["data_dir"], wantData)
	}
	if values["module_dir"] != mgr.Dir("m1") {
		t.Fatalf("module_dir = %q, want %q", values["module_dir"], mgr.Dir("m1"))
	}
	// The resolved default flows into argv emission.
	cmd, err := mgr.buildCommand(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	want := "--data-dir=" + wantData
	found := false
	for _, a := range cmd.Args[1:] {
		if a == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("args %v missing %q", cmd.Args[1:], want)
	}
}

// TestSetupHelperProcess is the file-writing counterpart of
// TestHelperProcess: it appends its last argv element ("init"/"setup") to
// GO_MODULE_SETUP_LOG and, for the init tag, creates the marker file.
// Only runs when GO_MODULE_SETUP=1.
func TestSetupHelperProcess(t *testing.T) {
	if os.Getenv("GO_MODULE_SETUP") != "1" {
		return
	}
	tag := os.Args[len(os.Args)-1]
	f, err := os.OpenFile(os.Getenv("GO_MODULE_SETUP_LOG"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		fmt.Fprintln(f, tag)
		_ = f.Close()
	}
	if tag == "init" {
		if m := os.Getenv("GO_MODULE_SETUP_MARKER"); m != "" {
			_ = os.WriteFile(m, []byte("done"), 0o600)
		}
	}
}

func TestInitAndSetupCommands(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, "marker")
	logPath := filepath.Join(dir, "setup.log")
	spec := ModuleSpec{
		ID: "setupmod", Name: "setupmod", Kind: KindDaemon,
		Install: InstallSpec{Method: "detect", Binary: os.Args[0]},
		Run: RunSpec{
			Env: map[string]string{
				"GO_MODULE_HELPER":       "1",
				"GO_MODULE_SETUP":        "1",
				"GO_MODULE_SETUP_LOG":    logPath,
				"GO_MODULE_SETUP_MARKER": markerPath,
			},
			InitMarker: markerPath,
			InitArgs:   []string{"-test.run=TestSetupHelperProcess", "init"},
			SetupArgs: [][]string{
				{"-test.run=TestSetupHelperProcess", "setup"},
			},
			ArgsTemplate: []string{"-test.run=TestHelperProcess"},
		},
	}
	registerTestSpec(t, spec)

	cfg := &config.Config{}
	mgr, _, _ := newTestManager(t, cfg)
	sup := NewSupervisor(mgr)
	defer sup.StopAll()

	startStop := func() {
		if err := sup.Start("setupmod"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := sup.Stop("setupmod"); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	startStop()
	startStop()

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("setup log missing: %v", err)
	}
	got := strings.Fields(string(data))
	// init once (marker persists), setup once per launch (two launches).
	want := []string{"init", "setup", "setup"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("setup log = %v, want %v", got, want)
	}
}

func TestHeliosCatalogEntry(t *testing.T) {
	spec, ok := Lookup("helios")
	if !ok {
		t.Fatal("helios not in catalog")
	}
	if spec.Kind != KindDaemon {
		t.Fatalf("kind = %q, want daemon", spec.Kind)
	}
	r, ok := spec.LatestRelease()
	if !ok {
		t.Fatal("no pinned release")
	}
	for _, p := range spec.Platforms {
		if d, algo := r.Digest(p); d == "" || algo != "sha256" {
			t.Fatalf("platform %s: digest=%q algo=%q", p, d, algo)
		}
	}
	for _, p := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		if !spec.Supports(p) {
			t.Fatalf("helios should support %s", p)
		}
	}
	if spec.Supports("windows/amd64") {
		t.Fatal("helios must not advertise windows — upstream ships none")
	}
	// The `ethereum` subcommand must lead argv — all flags ride env vars.
	if len(spec.Run.ArgsTemplate) != 1 || spec.Run.ArgsTemplate[0] != "ethereum" {
		t.Fatalf("ArgsTemplate = %v, want [ethereum]", spec.Run.ArgsTemplate)
	}
	for _, key := range []string{"execution_api_url", "checkpoint"} {
		f, ok := spec.Field(key)
		if !ok || !f.Required || f.Env == "" {
			t.Fatalf("field %s missing/not required/no env", key)
		}
	}
	if f, ok := spec.Field("data_dir"); !ok || f.Default != "{module_dir}/data" {
		t.Fatalf("data_dir = %+v", f)
	}
	if spec.Health.Type != "jsonrpc" {
		t.Fatalf("health = %+v", spec.Health)
	}
}

func TestKuboCatalogEntry(t *testing.T) {
	spec, ok := Lookup("ipfs-kubo")
	if !ok {
		t.Fatal("ipfs-kubo not in catalog")
	}
	if spec.Kind != KindDaemon {
		t.Fatalf("kind = %q, want daemon", spec.Kind)
	}
	r, ok := spec.LatestRelease()
	if !ok {
		t.Fatal("no pinned release")
	}
	for _, p := range spec.Platforms {
		if d, algo := r.Digest(p); d == "" || algo != "sha512" {
			t.Fatalf("platform %s: digest=%q algo=%q", p, d, algo)
		}
	}
	if got := spec.Install.AssetTemplates["windows"]; !strings.HasSuffix(got, ".zip") {
		t.Fatalf("windows asset template = %q, want .zip", got)
	}
	if spec.Run.InitMarker == "" || len(spec.Run.InitArgs) == 0 || len(spec.Run.SetupArgs) == 0 {
		t.Fatal("kubo needs init marker + init/setup args for repo init and port config")
	}
	if f, ok := spec.Field("repo_dir"); !ok || f.Default != "{module_dir}/repo" || f.Env != "IPFS_PATH" {
		t.Fatalf("repo_dir field = %+v", f)
	}
	if spec.Health.Type != "tcp" || spec.Health.Target != "127.0.0.1:{api_port}" {
		t.Fatalf("health = %+v", spec.Health)
	}
}

func TestCatalogVersionBump(t *testing.T) {
	data, err := MarshalCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var env CatalogEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.CatalogVersion != 2 {
		t.Fatalf("MarshalCatalog emitted catalog_version %d, want 2", env.CatalogVersion)
	}
	// v1 catalogs still parse (additive schema), v3 refuses.
	if _, err := parseCatalogEnvelope(
		[]byte(`{"catalog_version":1,"modules":[]}`)); err != nil {
		t.Fatalf("v1 catalog refused: %v", err)
	}
	if _, err := parseCatalogEnvelope(data); err != nil {
		t.Fatalf("v2 catalog refused: %v", err)
	}
	if _, err := parseCatalogEnvelope(
		[]byte(`{"catalog_version":3,"modules":[]}`)); err == nil {
		t.Fatal("v3 catalog accepted")
	}
}
