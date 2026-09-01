//go:build darwin

package isolation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

var (
	darwinPendingResources sync.Map
)

type darwinProcessResources struct {
	profilePath string
}

// applyPlatformIsolation rewrites the command to run under sandbox-exec with a
// generated Seatbelt profile. It keeps the command line semantics by prepending
// sandbox-exec and a profile file to the original executable.
func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	if !isolation.Enabled || cmd == nil {
		return nil
	}

	backend := isolation.Backend
	if backend == "" || backend == "auto" {
		backend = "sandbox-exec"
	}
	if backend == "none" {
		return nil
	}
	if backend != "sandbox-exec" {
		return fmt.Errorf("unsupported macOS isolation backend %q", backend)
	}

	sandboxExecPath, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return fmt.Errorf(
			"macOS isolation requires sandbox-exec and does not fall back: %w; install the Xcode Command Line Tools, or disable isolation by setting isolation.enabled=false",
			err,
		)
	}

	originalPath := cmd.Path
	originalArgs := append([]string{}, cmd.Args...)
	if originalPath == "" && len(originalArgs) > 0 {
		originalPath = originalArgs[0]
	}
	if originalPath == "" {
		return fmt.Errorf("macOS isolation: command has no executable")
	}

	execDir := cmd.Dir
	if execDir == "" {
		var err error
		execDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("macOS isolation: get working dir: %w", err)
		}
	}
	execDir, err = filepath.Abs(execDir)
	if err != nil {
		return fmt.Errorf("macOS isolation: resolve working dir: %w", err)
	}

	resolvedPath, err := resolveDarwinCommandPath(originalPath, execDir)
	if err != nil {
		return err
	}

	rules := BuildDarwinAccessRules(root, isolation.ExposePaths)

	// Ensure the binary and its directory are reachable. macOS sandbox-exec
	// does not support source->target remapping, so we use the real host path.
	rules = ensureDarwinAccessRule(rules, resolvedPath, "ro")
	rules = ensureDarwinAccessRule(rules, filepath.Dir(resolvedPath), "ro")
	if execDir != "" {
		rules = ensureDarwinAccessRule(rules, execDir, "rw")
	}

	// Resolve symlinks so the real path is also allowed.
	if resolved, resolveErr := filepath.EvalSymlinks(resolvedPath); resolveErr == nil && resolved != resolvedPath {
		rules = ensureDarwinAccessRule(rules, resolved, "ro")
		rules = ensureDarwinAccessRule(rules, filepath.Dir(resolved), "ro")
	}
	if execDir != "" {
		if resolved, resolveErr := filepath.EvalSymlinks(execDir); resolveErr == nil && resolved != execDir {
			rules = ensureDarwinAccessRule(rules, resolved, "rw")
		}
	}

	userEnv := ResolveUserEnv(root)

	profile, err := buildDarwinSandboxProfile(rules, root, userEnv)
	if err != nil {
		return fmt.Errorf("macOS isolation: build sandbox profile: %w", err)
	}

	profilePath, err := writeDarwinSandboxProfile(root, profile)
	if err != nil {
		return fmt.Errorf("macOS isolation: write sandbox profile: %w", err)
	}

	darwinPendingResources.Store(cmd, darwinProcessResources{profilePath: profilePath})

	logger.InfoCF("isolation", "macOS isolation process constraints",
		map[string]any{
			"root":        root,
			"command":     resolvedPath,
			"profile":     profilePath,
			"rules_count": len(rules),
			"note":        "macOS uses sandbox-exec; expose_paths target mapping is not supported, only the source path is allowed",
		})

	cmd.Path = sandboxExecPath
	cmd.Args = append([]string{
		"sandbox-exec",
		"-f",
		profilePath,
		resolvedPath,
	}, originalArgs[1:]...)
	return nil
}

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	cleanupDarwinPendingResources(cmd)
	return nil
}

func cleanupPendingPlatformResources(cmd *exec.Cmd) {
	cleanupDarwinPendingResources(cmd)
}

func cleanupDarwinPendingResources(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	resAny, ok := darwinPendingResources.LoadAndDelete(cmd)
	if !ok {
		return
	}
	res, ok := resAny.(darwinProcessResources)
	if !ok || res.profilePath == "" {
		return
	}
	_ = os.Remove(res.profilePath)
}

func buildDarwinSandboxProfile(rules []AccessRule, root string, userEnv UserEnv) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(allow process-fork)\n")
	b.WriteString("(allow signal)\n")
	b.WriteString("(allow network-outbound)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow system-info)\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow pseudo-tty)\n")
	b.WriteString("(allow file-ioctl (subpath \"/dev\"))\n")
	b.WriteString("(allow file-read-metadata (subpath \"/\"))\n\n")

	// The instance root is read-only by default; the runtime user
	// directories below it are read-write.
	b.WriteString("; instance root\n")
	b.WriteString(fmt.Sprintf("(allow file-read* file-read-metadata (subpath %q))\n", root))

	if userEnv.Home != "" {
		b.WriteString("; redirected user directories\n")
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.Home))
	}
	if userEnv.Tmp != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.Tmp))
	}
	if userEnv.Config != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.Config))
	}
	if userEnv.Cache != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.Cache))
	}
	if userEnv.State != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.State))
	}
	if userEnv.Library != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.Library))
	}
	if userEnv.LibraryApplicationSupport != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.LibraryApplicationSupport))
	}
	if userEnv.LibraryCaches != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.LibraryCaches))
	}
	if userEnv.LibraryLogs != "" {
		b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", userEnv.LibraryLogs))
	}

	b.WriteString("\n; configured and implicit access rules\n")
	for _, rule := range rules {
		if rule.Path == "" {
			continue
		}
		if rule.Mode == "rw" {
			b.WriteString(fmt.Sprintf("(allow file-read* file-write* file-read-metadata (subpath %q))\n", rule.Path))
		} else {
			b.WriteString(fmt.Sprintf("(allow file-read* file-read-metadata (subpath %q))\n", rule.Path))
		}
	}

	return b.String(), nil
}

func writeDarwinSandboxProfile(root, profile string) (string, error) {
	cacheDir := filepath.Join(root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(cacheDir, "rhizome-isolation-*.sb")
	if err != nil {
		return "", err
	}
	profilePath := f.Name()
	if _, err := f.WriteString(profile); err != nil {
		_ = f.Close()
		_ = os.Remove(profilePath)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(profilePath)
		return "", err
	}
	return profilePath, nil
}

func resolveDarwinCommandPath(originalPath, execDir string) (string, error) {
	if filepath.IsAbs(originalPath) || !isRelativeDarwinCommandPath(originalPath) {
		return filepath.Clean(originalPath), nil
	}
	base := execDir
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current working dir: %w", err)
		}
	}
	return filepath.Clean(filepath.Join(base, originalPath)), nil
}

func isRelativeDarwinCommandPath(path string) bool {
	return !filepath.IsAbs(path) && strings.ContainsRune(path, filepath.Separator)
}

func ensureDarwinAccessRule(rules []AccessRule, path, mode string) []AccessRule {
	if path == "" {
		return rules
	}
	clean := filepath.Clean(path)
	for i, r := range rules {
		if filepath.Clean(r.Path) == clean {
			if mode == "rw" {
				rules[i].Mode = "rw"
			}
			return rules
		}
	}
	return append(rules, AccessRule{Path: clean, Mode: mode})
}
