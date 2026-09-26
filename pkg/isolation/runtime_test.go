package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
)

func TestResolveInstanceRoot_UsesRhizomeHome(t *testing.T) {
	t.Setenv(config.EnvHome, filepath.FromSlash("/custom/rhizome/home"))
	root, err := ResolveInstanceRoot()
	if err != nil {
		t.Fatalf("ResolveInstanceRoot() error = %v", err)
	}
	want := filepath.FromSlash("/custom/rhizome/home")
	if root != want {
		t.Fatalf("ResolveInstanceRoot() = %q, want %q", root, want)
	}
}

func TestPrepareInstanceRoot_CreatesDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instance")
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot() error = %v", err)
	}
	for _, dir := range InstanceDirs(root) {
		if info, err := os.Stat(dir); err != nil {
			t.Fatalf("os.Stat(%q): %v", dir, err)
		} else if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir)
		}
	}
}

func TestInstanceDirs_UsesInstanceWorkspaceNotGlobalState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instance")
	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	cfg.Agents.Defaults.Workspace = filepath.Join(t.TempDir(), "external-workspace")
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	dirs := InstanceDirs(root)
	wantWorkspace := filepath.Join(root, pkg.WorkspaceName)
	found := false
	for _, dir := range dirs {
		if dir == wantWorkspace {
			found = true
		}
		if dir == cfg.WorkspacePath() {
			t.Fatalf("InstanceDirs() should not depend on process-wide workspace state: %q", dir)
		}
	}
	if !found {
		t.Fatalf("InstanceDirs() missing instance workspace dir %q", wantWorkspace)
	}
}

func TestIsSupportedOn(t *testing.T) {
	tests := []struct {
		goos string
		want bool
	}{
		{goos: "linux", want: true},
		{goos: "windows", want: true},
		{goos: "darwin", want: true},
		{goos: "freebsd", want: false},
	}
	for _, tt := range tests {
		if got := isSupportedOn(tt.goos); got != tt.want {
			t.Fatalf("isSupportedOn(%q) = %v, want %v", tt.goos, got, tt.want)
		}
	}
}

func TestValidateExposePaths(t *testing.T) {
	src, dst, extra := "/src", "/dst", "/extra"
	if runtime.GOOS == "windows" {
		src, dst, extra = `C:\src`, `C:\dst`, `C:\extra`
	}

	err := ValidateExposePaths([]config.ExposePath{{Source: src, Target: dst, Mode: "ro"}})
	if err != nil {
		t.Fatalf("ValidateExposePaths() error = %v", err)
	}

	err = ValidateExposePaths([]config.ExposePath{{Source: src, Target: dst, Mode: "bad"}})
	if err == nil {
		t.Fatal("ValidateExposePaths() expected invalid mode error")
	}

	err = ValidateExposePaths(
		[]config.ExposePath{
			{Source: src, Target: dst, Mode: "ro"},
			{Source: extra, Target: dst, Mode: "rw"},
		},
	)
	if err == nil {
		t.Fatal("ValidateExposePaths() expected duplicate target error")
	}
}

func TestMergeExposePaths_OverrideByTarget(t *testing.T) {
	srcA, srcB, dst := "/src-a", "/src-b", "/dst"
	if runtime.GOOS == "windows" {
		srcA, srcB, dst = `C:\src-a`, `C:\src-b`, `C:\dst`
	}
	merged := MergeExposePaths(
		[]config.ExposePath{{Source: srcA, Target: dst, Mode: "ro"}},
		[]config.ExposePath{{Source: srcB, Target: dst, Mode: "rw"}},
	)
	if len(merged) != 1 {
		t.Fatalf("MergeExposePaths len = %d, want 1", len(merged))
	}
	if got := merged[0]; got.Source != srcB || got.Target != dst || got.Mode != "rw" {
		t.Fatalf("merged[0] = %+v, want source=%s target=%s mode=rw", got, srcB, dst)
	}
}

func TestBuildLinuxMountPlan(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only default mount set")
	}
	plan := BuildLinuxMountPlan("/rootdir", []config.ExposePath{{Source: "/src", Target: "/dst", Mode: "ro"}})
	if len(plan) == 0 {
		t.Fatal("BuildLinuxMountPlan returned empty plan")
	}
	foundRoot := false
	foundOverride := false
	for _, rule := range plan {
		if rule.Source == "/rootdir" && rule.Target == "/rootdir" && rule.Mode == "rw" {
			foundRoot = true
		}
		if rule.Source == "/src" && rule.Target == "/dst" && rule.Mode == "ro" {
			foundOverride = true
		}
	}
	if !foundRoot {
		t.Fatal("BuildLinuxMountPlan missing root mapping")
	}
	if !foundOverride {
		t.Fatal("BuildLinuxMountPlan missing override mapping")
	}
}

func TestBuildWindowsAccessRules(t *testing.T) {
	rules := BuildWindowsAccessRules(
		`C:\rhizome`,
		[]config.ExposePath{{Source: `D:\data`, Target: `C:\mapped`, Mode: "ro"}},
	)
	if len(rules) == 0 {
		t.Fatal("BuildWindowsAccessRules returned empty rules")
	}
	foundRoot := false
	foundOverride := false
	for _, rule := range rules {
		if rule.Path == `C:\rhizome` && rule.Mode == "rw" {
			foundRoot = true
		}
		if rule.Path == `D:\data` && rule.Mode == "ro" {
			foundOverride = true
		}
	}
	if !foundRoot {
		t.Fatal("BuildWindowsAccessRules missing root rule")
	}
	if !foundOverride {
		t.Fatal("BuildWindowsAccessRules missing override rule")
	}
}

func TestValidateWindowsExposePaths(t *testing.T) {
	if err := validateWindowsExposePaths(nil); err != nil {
		t.Fatalf("validateWindowsExposePaths(nil) error = %v", err)
	}
	err := validateWindowsExposePaths([]config.ExposePath{{Source: `D:\data`, Target: `D:\data`, Mode: "ro"}})
	if err == nil {
		t.Fatal("validateWindowsExposePaths() expected error for expose_paths")
	}
}

func TestDefaultLinuxSystemExposePaths(t *testing.T) {
	paths := defaultLinuxSystemExposePaths()
	needed := map[string]bool{}
	for _, path := range []string{"/etc/hosts", "/etc/nsswitch.conf", "/etc/ssl", "/usr/share/zoneinfo", "/etc/localtime"} {
		if _, err := os.Stat(path); err == nil {
			needed[path] = false
		}
	}
	for _, item := range paths {
		if _, ok := needed[item.Source]; ok {
			needed[item.Source] = true
		}
	}
	for path, found := range needed {
		if !found {
			t.Fatalf("defaultLinuxSystemExposePaths missing %s", path)
		}
	}
}

func TestExistingExposePaths_SkipsMissingPaths(t *testing.T) {
	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	filtered := existingExposePaths([]config.ExposePath{
		{Source: existing, Target: existing, Mode: "ro"},
		{Source: filepath.Join(t.TempDir(), "missing"), Target: "/missing", Mode: "ro"},
	})
	if len(filtered) != 1 {
		t.Fatalf("existingExposePaths() len = %d, want 1", len(filtered))
	}
	if got := filtered[0]; got.Source != existing {
		t.Fatalf("existingExposePaths()[0] = %+v, want source=%q", got, existing)
	}
}

func TestPrepareCommand_AppliesUserEnv(t *testing.T) {
	if !isSupportedOn(runtime.GOOS) {
		t.Skipf("isolation not supported on %s", runtime.GOOS)
	}
	t.Setenv(config.EnvHome, filepath.Join(t.TempDir(), "home"))
	if runtime.GOOS == "linux" {
		binDir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		fakeBwrap := filepath.Join(binDir, "bwrap")
		if err := os.WriteFile(fakeBwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })
	cmd := exec.Command("sh", "-c", "true")
	if err := PrepareCommand(cmd); err != nil {
		t.Fatalf("PrepareCommand() error = %v", err)
	}
	hasHome := false
	for _, env := range cmd.Env {
		if len(env) > 5 && env[:5] == "HOME=" {
			hasHome = true
			break
		}
	}
	if runtime.GOOS != "windows" && !hasHome {
		t.Fatal("PrepareCommand() did not inject HOME")
	}
}

func TestOptionsFromConfig(t *testing.T) {
	iso := config.IsolationConfig{
		Enabled:     true,
		Backend:     "bwrap",
		ExposePaths: []config.ExposePath{{Source: "/a", Target: "/b", Mode: "ro"}},
	}
	opts := OptionsFromConfig(iso)
	if !opts.Enabled || opts.Backend != "bwrap" {
		t.Fatalf("OptionsFromConfig() = %+v", opts)
	}
	if len(opts.ExposePaths) != 1 || opts.ExposePaths[0].Source != "/a" {
		t.Fatalf("OptionsFromConfig() ExposePaths = %+v", opts.ExposePaths)
	}
	if opts.NetMode != "" || opts.Root != "" {
		t.Fatalf("OptionsFromConfig() must not invent Root/NetMode: %+v", opts)
	}
}

func TestOptionsResolveRoot(t *testing.T) {
	t.Setenv(config.EnvHome, filepath.Join(t.TempDir(), "home"))
	got, err := (Options{}).resolveRoot()
	if err != nil {
		t.Fatalf("resolveRoot() error = %v", err)
	}
	if want := filepath.Clean(config.GetHome()); got != want {
		t.Fatalf("resolveRoot() = %q, want %q", got, want)
	}

	scratch := filepath.Join(t.TempDir(), "scratch")
	got, err = (Options{Root: scratch}).resolveRoot()
	if err != nil {
		t.Fatalf("resolveRoot() error = %v", err)
	}
	if got != scratch {
		t.Fatalf("resolveRoot() = %q, want scratch %q", got, scratch)
	}

	if _, err := (Options{Root: "."}).resolveRoot(); err == nil {
		t.Fatal("resolveRoot(.) should error")
	}
}

func TestPreflightWith_InvalidNetMode(t *testing.T) {
	opts := Options{Enabled: true, Root: t.TempDir(), NetMode: "bogus"}
	if err := PreflightWith(opts); err == nil ||
		!strings.Contains(err.Error(), "net mode") {
		t.Fatalf("PreflightWith() expected invalid net mode error: %v", err)
	}
	// Disabled isolation ignores the knob — nothing is enforced either way.
	opts.Enabled = false
	if err := PreflightWith(opts); err != nil {
		t.Fatalf("PreflightWith(disabled) error = %v", err)
	}
}

func TestPrepareCommandWith_DisabledLeavesCommand(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", "0")
	}
	if err := PrepareCommandWith(cmd, Options{Enabled: false}); err != nil {
		t.Fatalf("PrepareCommandWith(disabled) error = %v", err)
	}
	if cmd.Env != nil {
		t.Fatalf("disabled isolation must not touch cmd.Env: %v", cmd.Env)
	}
}

func TestPrepareCommandWith_ScratchRootUserEnv(t *testing.T) {
	if !isSupportedOn(runtime.GOOS) {
		t.Skipf("isolation not supported on %s", runtime.GOOS)
	}
	if runtime.GOOS == "linux" {
		binDir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		fakeBwrap := filepath.Join(binDir, "bwrap")
		if err := os.WriteFile(fakeBwrap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	scratch := filepath.Join(t.TempDir(), "acp-sandbox", "peer")
	cmd := exec.Command("sh", "-c", "true")
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", "0")
	}
	opts := Options{Enabled: true, Root: scratch}
	if err := PrepareCommandWith(cmd, opts); err != nil {
		t.Fatalf("PrepareCommandWith() error = %v", err)
	}
	wantHome := filepath.Join(scratch, "runtime-user-env", "home")
	hasHome := false
	for _, env := range cmd.Env {
		if env == "HOME="+wantHome {
			hasHome = true
			break
		}
	}
	if !hasHome {
		t.Fatalf("PrepareCommandWith() must redirect HOME into the scratch root %q; env=%v",
			wantHome, cmd.Env)
	}
}
