// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// BrowserHome returns the directory for Rhizome-managed browser state
// (profiles, session data). Managed binaries are never downloaded here —
// installs use the backend's native package manager (npm) or are BYO.
func BrowserHome() string {
	return filepath.Join(config.GetHome(), "browser")
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// Status reports the install status of a backend without side effects.
// Returns one of: "installed", "missing", "configured", "unconfigured".
func Status(spec BackendSpec, cfg config.BrowserBackendConfig) string {
	switch spec.Install.Method {
	case "npm":
		if _, err := exec.LookPath(spec.Install.Binary); err != nil {
			return "missing"
		}
		return "installed"
	case "detect":
		switch spec.ID {
		case "system-chrome":
			if _, err := findSystemBrowser(cfg.ExecutablePath); err != nil {
				return "missing"
			}
			return "installed"
		default:
			if _, err := exec.LookPath(spec.Install.Binary); err != nil {
				return "missing"
			}
			return "installed"
		}
	case "config":
		for _, f := range spec.Auth {
			if !f.Required {
				continue
			}
			var v string
			switch f.Key {
			case "api_key":
				v = cfg.APIKey.String()
			case "account_id":
				v = cfg.AccountID
			case "project_id":
				v = cfg.ProjectID
			case "endpoint_url":
				v = cfg.EndpointURL
			case "provider":
				v = cfg.Provider
			case "base_url":
				v = cfg.BaseURL
			}
			if strings.TrimSpace(v) == "" {
				return "unconfigured"
			}
		}
		return "configured"
	default:
		return "unknown"
	}
}

// Install performs a managed install. Only agent-browser is managed (npm);
// detect/config backends return a guidance message instead.
func Install(ctx context.Context, spec BackendSpec, run Runner) (string, error) {
	if run == nil {
		run = ExecRunner{}
	}
	switch spec.Install.Method {
	case "npm":
		if _, err := exec.LookPath("npm"); err != nil {
			return "", fmt.Errorf("npm not found — install Node.js first, then retry")
		}
		nctx, ncancel := context.WithTimeout(ctx, 10*time.Minute)
		_, stderr, nerr := run.Run(nctx, nil, "npm", "install", "-g", "agent-browser")
		ncancel()
		if nerr != nil {
			return "", fmt.Errorf("npm install agent-browser failed: %s", strings.TrimSpace(stderr))
		}
		// Download the bundled Chromium.
		cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if _, stderr, err := run.Run(cctx, nil, "agent-browser", "install"); err != nil {
			return "", fmt.Errorf("agent-browser install (browser download) failed: %s",
				strings.TrimSpace(stderr))
		}
		return "agent-browser installed", nil
	case "detect":
		return fmt.Sprintf(
			"No managed install for %s. %s", spec.ID, spec.Install.Hint), nil
	case "config":
		return fmt.Sprintf(
			"%s needs no install — configure its credentials below.", spec.ID), nil
	default:
		return "", fmt.Errorf("unknown install method %q", spec.Install.Method)
	}
}

// Uninstall removes a managed install. Only agent-browser is managed.
func Uninstall(ctx context.Context, spec BackendSpec, run Runner) (string, error) {
	if run == nil {
		run = ExecRunner{}
	}
	switch spec.Install.Method {
	case "npm":
		if _, err := exec.LookPath("npm"); err != nil {
			return "", fmt.Errorf("npm not found")
		}
		if _, stderr, err := run.Run(ctx, nil,
			"npm", "uninstall", "-g", "agent-browser"); err != nil {
			return "", fmt.Errorf("npm uninstall failed: %s", strings.TrimSpace(stderr))
		}
		return "agent-browser uninstalled (browser cache may remain under your profile)", nil
	case "detect":
		return fmt.Sprintf(
			"%s is a bring-your-own install — remove it with the vendor's tooling.", spec.ID), nil
	default:
		return fmt.Sprintf("%s is config-only — nothing to uninstall.", spec.ID), nil
	}
}

// versionEntry caches a binary's --version result; the admin backend list
// calls Version for every catalog entry on each page load, and spawning a
// process per entry would stall the response.
type versionEntry struct {
	value string
	at    time.Time
}

var versionCache sync.Map // binary -> versionEntry

const versionCacheTTL = time.Minute

// Version returns the installed version of a managed binary, or "".
// Results (including misses) are cached for a minute.
func Version(binary string) string {
	if e, ok := versionCache.Load(binary); ok {
		if ent, ok := e.(versionEntry); ok && time.Since(ent.at) < versionCacheTTL {
			return ent.value
		}
	}

	v := ""
	if _, err := exec.LookPath(binary); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, _, err := ExecRunner{}.Run(ctx, nil, binary, "--version")
		cancel()
		if err == nil {
			v = versionRe.FindString(strings.TrimSpace(out))
		}
	}
	versionCache.Store(binary, versionEntry{value: v, at: time.Now()})
	return v
}

// EnsureDir creates the browser home directory.
func EnsureDir() error {
	return os.MkdirAll(BrowserHome(), 0o700)
}

// DriverInstalled reports whether the agent-browser driver binary is on PATH.
// Every non-REST backend needs it to drive sessions — even backends whose own
// install state is "installed"/"configured" (system-chrome, custom-cdp,
// provider-env, cloud-session).
func DriverInstalled() bool {
	_, err := exec.LookPath(agentBrowserBin)
	return err == nil
}

// NeedsDriver reports whether a backend kind is driven through the
// agent-browser CLI (everything except the stateless Cloudflare REST API).
func NeedsDriver(spec BackendSpec) bool {
	return spec.Kind != KindCloudREST
}

// InstallVolumeDir returns the directory whose free space actually bounds a
// managed install: the OS user cache dir, where `agent-browser install`
// downloads Chromium, falling back to the user home and then RHIZOME_HOME.
// Checking RHIZOME_HOME alone can probe the wrong volume when it is on a
// different drive than the user profile.
func InstallVolumeDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return d
	}
	if d, err := os.UserHomeDir(); err == nil && d != "" {
		return d
	}
	return config.GetHome()
}
