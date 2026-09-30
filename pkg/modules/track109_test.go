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
	"encoding/hex"
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

// writeTarGz builds a tar.gz fixture from name→body entries plus optional
// link entries (typeflag "symlink"/"link" carrying Linkname).
func writeTarGz(t *testing.T, regular map[string]string, links map[string][2]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range regular {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for name, ln := range links {
		typeflag := byte(tar.TypeSymlink)
		if ln[1] == "hard" {
			typeflag = tar.TypeLink
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o777, Typeflag: typeflag, Linkname: ln[0],
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	path := filepath.Join(t.TempDir(), "m.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()
	path := filepath.Join(t.TempDir(), "m.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBinaryPathLayoutPreserving is the headline behavior: with binary_path
// set, the archive keeps its nested layout — the binary is NOT flattened to
// the version dir root, so co-packaged shared libraries resolve.
func TestBinaryPathLayoutPreserving(t *testing.T) {
	dest := t.TempDir()
	archive := writeTarGz(t, map[string]string{
		"llama-b9/llama-server":    "BIN",
		"llama-b9/libllama.so":     "LIB",
		"llama-b9/libggml.so":      "LIB2",
		"llama-b9/sub/nested.tool": "TOOL",
	}, nil)

	bin, err := extractTarGz(archive, dest, "llama-server", "llama-b9/llama-server")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := filepath.Join(dest, "llama-b9", "llama-server")
	if bin != want {
		t.Fatalf("binary path = %q, want %q", bin, want)
	}
	// Nothing lands at the root — layout is preserved.
	for _, rel := range []string{
		"llama-b9/libllama.so", "llama-b9/libggml.so", "llama-b9/sub/nested.tool",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("member %q not extracted: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "llama-server")); !os.IsNotExist(err) {
		t.Fatal("binary was flattened to the dir root — layout not preserved")
	}
}

// TestExtractRejectsLinks refuses symlink and hardlink tar members outright —
// a link member followed by a regular member would let content escape dest.
func TestExtractRejectsLinks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links map[string][2]string
		want  string
	}{
		{"symlink", map[string][2]string{"evil": {"target", "sym"}}, "link member"},
		{"hardlink", map[string][2]string{"evil": {"target", "hard"}}, "link member"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeTarGz(t, map[string]string{"ok.txt": "x"}, tc.links)
			if _, err := extractTarGz(archive, t.TempDir(), "", ""); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s rejection, got %v", tc.name, err)
			}
		})
	}
}

// TestBinaryPathZipKeepsLayout exercises the same path through the zip
// extractor — the flat windows llama.cpp archive keeps everything at root.
func TestBinaryPathZipKeepsLayout(t *testing.T) {
	dest := t.TempDir()
	archive := writeZip(t, map[string]string{
		"llama-server.exe": "BIN",
		"ggml.dll":         "DLL",
	})
	bin, err := extractZip(archive, dest, "llama-server", "llama-server")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if bin != filepath.Join(dest, "llama-server.exe") {
		t.Fatalf("binary = %q", bin)
	}
	if _, err := os.Stat(filepath.Join(dest, "ggml.dll")); err != nil {
		t.Fatalf("dll not extracted: %v", err)
	}
}

// TestArchAliasAssetResolution pins amd64→x64 expansion in Asset().
func TestArchAliasAssetResolution(t *testing.T) {
	spec := testSpec("x")
	spec.Install.AssetTemplate = "llama-{version}-bin-win-cpu-{goarch}.zip"
	spec.Install.ArchAliases = map[string]string{"amd64": "x64"}
	r := spec.Install.Releases[0]
	r.Version = "b9999"
	got := spec.Asset(r)
	switch runtime.GOARCH {
	case "amd64":
		if got != "llama-b9999-bin-win-cpu-x64.zip" {
			t.Fatalf("asset = %q, want x64 alias", got)
		}
	case "arm64":
		if got != "llama-b9999-bin-win-cpu-arm64.zip" {
			t.Fatalf("asset = %q, want arm64 passthrough", got)
		}
	}
}

// TestBinaryRelPathPerGOOS checks the per-GOOS override + {version} expansion:
// linux/darwin nest under llama-{version}/, windows is flat.
func TestBinaryRelPathPerGOOS(t *testing.T) {
	spec := testSpec("x")
	spec.Install.BinaryPath = "llama-{version}/llama-server"
	spec.Install.BinaryPaths = map[string]string{"windows": "llama-server"}
	r := ReleasePin{Version: "b9999"}
	got := spec.BinaryRelPath(r)
	if runtime.GOOS == "windows" {
		if got != "llama-server" {
			t.Fatalf("binaryRelPath = %q, want flat override", got)
		}
	} else if got != "llama-b9999/llama-server" {
		t.Fatalf("binaryRelPath = %q, want nested {version}", got)
	}
}

// TestRunFetches covers the declared-download path: digest-pinned file lands
// at dest, a .digest sidecar makes repeat runs skip the wire, a bad digest
// refuses, and required/optional empty-URL semantics hold.
func TestRunFetches(t *testing.T) {
	payload := []byte("model-weights")
	sum := sha256.Sum256(payload)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	spec := testSpec("m1")
	spec.Install.Fetch = []FetchSpec{
		{URL: "{model_url}", SHA256: "{model_sha256}", Dest: "{module_dir}/models/m.gguf", Required: true},
	}
	registerTestSpec(t, spec)
	dest := filepath.Join(t.TempDir(), "mod")

	mgr := NewManager(dest, &config.Config{Modules: config.ModulesConfig{
		"m1": {
			Secrets: map[string]config.SecureString{
				"model_url": *config.NewSecureString(srv.URL + "/m.gguf"),
			},
			Fields: map[string]string{"model_sha256": hex.EncodeToString(sum[:])},
		},
	}}, nil, func(*config.Config) error { return nil })

	if err := mgr.runFetches(t.Context(), spec); err != nil {
		t.Fatalf("runFetches: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(mgr.Dir("m1"), "models", "m.gguf"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("fetched content = %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(mgr.Dir("m1"), "models", "m.gguf"+fetchDigestSuffix)); err != nil {
		t.Fatalf("digest sidecar missing: %v", err)
	}
	// Second run must skip the wire entirely (sidecar digest matches).
	if err := mgr.runFetches(t.Context(), spec); err != nil {
		t.Fatalf("second runFetches: %v", err)
	}
	if hits != 1 {
		t.Fatalf("fetch hits = %d, want 1 (sidecar skip)", hits)
	}

	// Wrong pinned digest refuses before rename.
	mc := mgr.moduleConfig("m1")
	mc.Fields["model_sha256"] = strings.Repeat("0", 64)
	mgr.cfg.Modules["m1"] = mc
	_ = os.Remove(filepath.Join(mgr.Dir("m1"), "models", "m.gguf"+fetchDigestSuffix))
	if err := mgr.runFetches(t.Context(), spec); err == nil ||
		!strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected digest refusal, got %v", err)
	}

	// URL present without sha256 is a hard error.
	spec.Install.Fetch[0].SHA256 = ""
	if err := mgr.runFetches(t.Context(), spec); err == nil ||
		!strings.Contains(err.Error(), "no sha256") {
		t.Fatalf("expected missing-sha256 refusal, got %v", err)
	}
}

// TestRunFetchesEmptyURL pins required-vs-optional empty-URL semantics.
func TestRunFetchesEmptyURL(t *testing.T) {
	spec := testSpec("m1")
	spec.Install.Fetch = []FetchSpec{
		{URL: "{model_url}", SHA256: "{model_sha256}", Dest: "{module_dir}/m.gguf"},
		{URL: "{model_url}", SHA256: "{model_sha256}", Dest: "{module_dir}/m2.gguf", Required: true},
	}
	registerTestSpec(t, spec)
	mgr := NewManager(t.TempDir(), &config.Config{}, nil, func(*config.Config) error { return nil })

	// Optional (index 0) skips silently; required (index 1) errors.
	if err := mgr.runFetches(t.Context(), spec); err == nil ||
		!strings.Contains(err.Error(), "resolved empty") {
		t.Fatalf("expected required-fetch error, got %v", err)
	}
	spec.Install.Fetch = spec.Install.Fetch[:1]
	if err := mgr.runFetches(t.Context(), spec); err != nil {
		t.Fatalf("optional empty-url fetch should skip, got %v", err)
	}
}

// TestOnDemandLaunchOnce proves KindOnDemand starts through the supervisor
// but never restarts — exit is final.
func TestOnDemandLaunchOnce(t *testing.T) {
	spec := ModuleSpec{
		ID: "oneshot", Name: "oneshot", Kind: KindOnDemand,
		Install: InstallSpec{Method: "detect", Binary: os.Args[0]},
		Run: RunSpec{
			ArgsTemplate: []string{"-test.run=TestCatalogLookup"},
		},
	}
	registerTestSpec(t, spec)
	mgr, _, _ := newTestManager(t, &config.Config{})
	sup := NewSupervisor(mgr)
	defer sup.StopAll()

	if err := sup.Start("oneshot"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st := mgr.loadState("oneshot")
		if st.LastExit != "" && !sup.IsRunning("oneshot") {
			if st.Restarts != 0 {
				t.Fatalf("restarts = %d, want 0 (no daemon supervision)", st.Restarts)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("ondemand module did not reach a final exit")
}

// TestLlamaCppCatalogEntry pins the real upstream naming — asset templates,
// arch/OS aliasing, binary layout, required fetch fields, and health probe.
func TestLlamaCppCatalogEntry(t *testing.T) {
	spec, ok := Lookup("llama-cpp")
	if !ok {
		t.Fatal("llama-cpp not in catalog")
	}
	if spec.Kind != KindOnDemand {
		t.Fatalf("kind = %q, want ondemand", spec.Kind)
	}
	r, ok := spec.LatestRelease()
	if !ok {
		t.Fatal("no pinned release")
	}
	if spec.Tag(r) != r.Version { // upstream tags are bare "bNNNNN"
		t.Fatalf("tag = %q, want bare %q", spec.Tag(r), r.Version)
	}
	for _, p := range spec.Platforms {
		if d, _ := r.Digest(p); d == "" {
			t.Fatalf("platform %s missing digest", p)
		}
	}
	asset := spec.Asset(r)
	if !strings.HasPrefix(asset, "llama-b") || !strings.Contains(asset, "-bin-") {
		t.Fatalf("asset %q does not match upstream naming", asset)
	}
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(asset, "win-cpu-") || !strings.HasSuffix(asset, ".zip") {
			t.Fatalf("windows asset = %q", asset)
		}
	case "darwin":
		if !strings.Contains(asset, "macos-") || !strings.HasSuffix(asset, ".tar.gz") {
			t.Fatalf("darwin asset = %q", asset)
		}
	case "linux":
		if !strings.Contains(asset, "ubuntu-") || !strings.HasSuffix(asset, ".tar.gz") {
			t.Fatalf("linux asset = %q", asset)
		}
	}
	if runtime.GOARCH == "amd64" && !strings.HasSuffix(
		strings.TrimSuffix(strings.TrimSuffix(asset, ".zip"), ".tar.gz"), "-x64") {
		t.Fatalf("amd64 should alias to x64 in %q", asset)
	}
	if len(spec.Install.Fetch) != 1 || !spec.Install.Fetch[0].Required {
		t.Fatalf("fetch = %+v, want one required weights fetch", spec.Install.Fetch)
	}
	for _, key := range []string{"model_url", "model_sha256"} {
		if f, ok := spec.Field(key); !ok || !f.Required {
			t.Fatalf("field %s missing/not required", key)
		}
	}
	if f, ok := spec.Field("extra_args"); !ok || !f.Split || f.Arg != "" {
		t.Fatalf("extra_args field = %+v, want split/no-arg", f)
	}
	if spec.Health.Type != "http" || spec.Health.Method != "/health" {
		t.Fatalf("health = %+v", spec.Health)
	}

	// The vulkan twin carries the same contract for GPU hosts.
	vspec, ok := Lookup("llama-cpp-vulkan")
	if !ok {
		t.Fatal("llama-cpp-vulkan not in catalog")
	}
	if vspec.Kind != KindOnDemand || len(vspec.Install.Fetch) != 1 {
		t.Fatalf("vulkan spec malformed: %+v", vspec)
	}
	if f, ok := vspec.Field("gpu_layers"); !ok || f.Default != "99" {
		t.Fatalf("vulkan gpu_layers = %+v, want default 99", f)
	}
	if catalogVersionRequired([]ModuleSpec{spec, vspec}) != 5 {
		t.Fatal("llama.cpp entries must emit catalog schema v5")
	}
}

// TestSplitFieldArgv proves the free-form extra_args escape hatch appends
// whitespace-split elements verbatim (no -- prefix).
func TestSplitFieldArgv(t *testing.T) {
	spec := testSpec("m1")
	spec.ConfigFields = append(spec.ConfigFields,
		ConfigField{Key: "extra_args", Split: true})
	spec.Install.Method = "detect"
	spec.Install.Binary = os.Args[0]
	registerTestSpec(t, spec)

	cfg := &config.Config{Modules: config.ModulesConfig{
		"m1": {Fields: map[string]string{
			"endpoint":   "http://x",
			"extra_args": "--jinja --top-k 40",
		}},
	}}
	mgr, _, _ := newTestManager(t, cfg)
	cmd, err := mgr.buildCommand(t.Context(), spec)
	if err != nil {
		t.Fatalf("buildCommand: %v", err)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--jinja", "--top-k 40"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "extra_args") {
		t.Fatalf("extra_args leaked as a flag name in %q", joined)
	}
}
