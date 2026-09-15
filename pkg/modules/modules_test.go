// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"archive/tar"
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
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "unknown module id") {
		t.Fatalf("expected unknown-id error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Fields: map[string]string{"token": "x"}},
	}}
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "secret field") {
		t.Fatalf("expected secret-placement error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Enabled: true},
	}}
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "required field") {
		t.Fatalf("expected required-field error, got %v", err)
	}

	cfg = &config.Config{Modules: config.ModulesConfig{
		"m1": {Enabled: true, Fields: map[string]string{"endpoint": "http://x"}},
	}}
	if err := ValidateConfig(cfg); err != nil {
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
	for _, key := range []string{"execution_api_url", "beacon_api_url", "trusted_block_root"} {
		f, ok := spec.Field(key)
		if !ok || !f.Required || f.Arg == "" {
			t.Fatalf("field %s missing/not required/no arg", key)
		}
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
	if err := extractTarGz(archive, t.TempDir(), ""); err == nil ||
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
	want := []string{
		"--rpc-port=8545", "--api-url=https://key:secret@example.com",
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
