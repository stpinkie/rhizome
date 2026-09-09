// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Resolver produces an Endpoint for a backend + its config.
type Resolver interface {
	Resolve(ctx context.Context, cfg config.BrowserBackendConfig) (*Endpoint, error)
}

// ResolverFor returns the resolver implementation for a backend spec.
// The returned resolver may share a Runner with the driver for spawn-based
// backends; pass nil to use the default exec runner.
func ResolverFor(spec BackendSpec, run Runner) (Resolver, error) {
	if run == nil {
		run = ExecRunner{}
	}
	switch spec.Kind {
	case KindLocalCLI, KindProviderEnv:
		return providerEnvResolver{spec: spec}, nil
	case KindSpawnCDP:
		switch spec.ID {
		case "clawbrowser":
			return clawResolver{runner: run}, nil
		default:
			return systemBrowserResolver{runner: run}, nil
		}
	case KindCustom:
		return customResolver{}, nil
	case KindNativeCDP:
		return nativeCDPResolver{}, nil
	case KindCloudSession:
		return &cloudSessionResolver{spec: spec}, nil
	case KindCloudREST:
		return nil, fmt.Errorf("backend %q is REST-only and has no session endpoint", spec.ID)
	default:
		return nil, fmt.Errorf("unknown backend kind %q", spec.Kind)
	}
}

// ---------------------------------------------------------------------------
// custom: user-supplied CDP endpoint
// ---------------------------------------------------------------------------

type customResolver struct{}

func (customResolver) Resolve(
	_ context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	raw := strings.TrimSpace(cfg.EndpointURL)
	if raw == "" {
		return nil, fmt.Errorf("endpoint_url is required for the custom-cdp backend")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("endpoint_url %q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return nil, fmt.Errorf("endpoint_url scheme %q is not supported (want ws/wss/http/https)", u.Scheme)
	}
	return &Endpoint{CDPURL: raw, Env: envList(BackendSpec{}, cfg)}, nil
}

// ---------------------------------------------------------------------------
// native-cdp: user-supplied CDP endpoint + optional bearer auth, driven by
// the built-in Go client instead of the agent-browser CLI
// ---------------------------------------------------------------------------

type nativeCDPResolver struct{}

func (nativeCDPResolver) Resolve(
	_ context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	raw := strings.TrimSpace(cfg.EndpointURL)
	if raw == "" {
		return nil, fmt.Errorf("endpoint_url is required for the rhizome-cdp backend")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("endpoint_url %q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return nil, fmt.Errorf("endpoint_url scheme %q is not supported (want ws/wss/http/https)", u.Scheme)
	}
	ep := &Endpoint{CDPURL: raw, Env: envList(BackendSpec{}, cfg)}
	if key := strings.TrimSpace(cfg.APIKey.String()); key != "" {
		ep.Headers = map[string]string{"Authorization": "Bearer " + key}
	}
	return ep, nil
}

// ---------------------------------------------------------------------------
// provider-env: agent-browser -p <provider> + env credentials
// ---------------------------------------------------------------------------

type providerEnvResolver struct {
	spec BackendSpec
}

func (r providerEnvResolver) Resolve(
	_ context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	provider := r.spec.Provider
	if provider == "" {
		provider = strings.TrimSpace(cfg.Provider)
	}
	if r.spec.Kind == KindProviderEnv && provider == "" {
		return nil, fmt.Errorf("provider name is required for backend %q", r.spec.ID)
	}
	return &Endpoint{Provider: provider, Env: envList(r.spec, cfg)}, nil
}

// envList converts backend config into KEY=VALUE pairs for the driver process.
// Fields declared with an Env name in the backend's Auth spec are exported
// under that name; cfg.Env entries pass through verbatim (and can override).
func envList(spec BackendSpec, cfg config.BrowserBackendConfig) []string {
	var env []string
	for _, f := range spec.Auth {
		if f.Env == "" {
			continue
		}
		var value string
		switch f.Key {
		case "api_key":
			value = cfg.APIKey.String()
		case "account_id":
			value = cfg.AccountID
		case "project_id":
			value = cfg.ProjectID
		case "base_url":
			value = cfg.BaseURL
		case "endpoint_url":
			value = cfg.EndpointURL
		case "session_name":
			value = cfg.SessionName
		}
		if v := strings.TrimSpace(value); v != "" {
			env = append(env, f.Env+"="+v)
		}
	}
	for k, v := range cfg.Env {
		if ek := strings.TrimSpace(k); ek != "" {
			env = append(env, ek+"="+v)
		}
	}
	return env
}

// ---------------------------------------------------------------------------
// spawn-cdp: launch a local browser process and expose a CDP endpoint
// ---------------------------------------------------------------------------

var cdpURLRe = regexp.MustCompile(`(?:https?|ws)://[^\s"']+`)

// systemBrowserResolver launches the user's installed Chrome/Edge/Chromium
// with a dedicated profile and --remote-debugging-port.
type systemBrowserResolver struct {
	runner Runner
}

// systemBrowserCandidates lists executable names/paths tried in order.
func systemBrowserCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			"chrome", "msedge", "chromium",
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"chrome", "chromium",
		}
	default:
		return []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "chrome",
		}
	}
}

func findSystemBrowser(override string) (string, error) {
	if p := strings.TrimSpace(override); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		if resolved, err := exec.LookPath(p); err == nil {
			return resolved, nil
		}
		return "", fmt.Errorf("browser executable not found: %s", p)
	}
	for _, candidate := range systemBrowserCandidates() {
		if strings.Contains(candidate, string(os.PathSeparator)) ||
			strings.ContainsAny(candidate, `/\`) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf(
		"no Chrome/Edge/Chromium installation found; set executable_path or install agent-browser instead")
}

// waitForDevToolsPort waits for Chrome to write the DevToolsActivePort file
// in the profile dir — created once the debug listener is bound — and returns
// the port from its first line. Passing --remote-debugging-port=0 lets Chrome
// pick the port itself, which avoids the bind race of reserving one first.
func waitForDevToolsPort(ctx context.Context, profileDir string, timeout time.Duration) (int, error) {
	portFile := filepath.Join(profileDir, "DevToolsActivePort")
	deadline := time.Now().Add(timeout)
	for {
		if raw, err := os.ReadFile(portFile); err == nil {
			if line, _, _ := strings.Cut(string(raw), "\n"); line != "" {
				if port, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && port > 0 {
					return port, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no DevToolsActivePort after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (r systemBrowserResolver) Resolve(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	binary, err := findSystemBrowser(cfg.ExecutablePath)
	if err != nil {
		return nil, err
	}

	profileDir := filepath.Join(BrowserHome(), "profiles", "system-chrome")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, fmt.Errorf("create browser profile dir: %w", err)
	}
	// The profile dir is persistent, so drop any stale port file first —
	// otherwise a leftover from a previous launch could race the new one.
	_ = os.Remove(filepath.Join(profileDir, "DevToolsActivePort"))

	proc := exec.Command(binary,
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--headless=new",
		"about:blank",
	)
	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("launch system browser: %w", err)
	}

	port, err := waitForDevToolsPort(ctx, profileDir, 15*time.Second)
	if err != nil {
		_ = proc.Process.Kill()
		return nil, fmt.Errorf("system browser did not report a debug port: %w", err)
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForCDP(ctx, endpoint, 15*time.Second); err != nil {
		_ = proc.Process.Kill()
		return nil, fmt.Errorf("system browser did not expose CDP on %s: %w", endpoint, err)
	}

	logger.InfoCF("browser", "System browser launched for CDP",
		map[string]any{"binary": binary, "endpoint": endpoint})

	return &Endpoint{
		CDPURL: endpoint,
		Cleanup: func(context.Context) error {
			if proc.Process != nil {
				_ = proc.Process.Kill()
			}
			return nil
		},
	}, nil
}

// waitForCDP polls the CDP HTTP endpoint until /json/version answers.
func waitForCDP(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, base+"/json/version", nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out after %s", timeout)
}

// clawResolver drives ClawBrowser sessions through the user's clawctl CLI.
// clawctl manages install/profiles itself; we only start sessions and read
// their CDP endpoint.
type clawResolver struct {
	runner Runner
}

func (r clawResolver) Resolve(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	if _, err := exec.LookPath("clawctl"); err != nil {
		return nil, fmt.Errorf(
			"clawctl not found on PATH — ClawBrowser is bring-your-own: run `clawctl install` first")
	}
	session := strings.TrimSpace(cfg.SessionName)
	if session == "" {
		session = "rhizome"
	}

	// Start a session; tolerate "already running" style failures by falling
	// through to the endpoint query.
	env := envList(BackendSpec{Auth: []AuthField{
		{Key: "api_key", Env: "CLAWBROWSER_API_KEY"},
	}}, cfg)

	stdout, stderr, err := r.runner.Run(ctx, env, "clawctl", "start", "--session", session, "--json")
	if err != nil {
		logger.WarnCF("browser", "clawctl start returned error (session may already exist)",
			map[string]any{"stderr": strings.TrimSpace(stderr)})
	}
	if u := cdpURLRe.FindString(stdout); u != "" {
		return &Endpoint{CDPURL: u, Env: env, Cleanup: clawCleanup(r.runner, session, env)}, nil
	}

	stdout, stderr, err = r.runner.Run(ctx, env, "clawctl", "endpoint", "--session", session, "--json")
	if err != nil {
		return nil, fmt.Errorf("clawctl endpoint failed: %s", strings.TrimSpace(stderr))
	}
	u := cdpURLRe.FindString(stdout)
	if u == "" {
		return nil, fmt.Errorf("could not determine ClawBrowser CDP endpoint")
	}
	return &Endpoint{CDPURL: u, Env: env, Cleanup: clawCleanup(r.runner, session, env)}, nil
}

func clawCleanup(
	runner Runner,
	session string,
	env []string,
) func(context.Context) error {
	return func(ctx context.Context) error {
		_, _, err := runner.Run(ctx, env, "clawctl", "stop", "--session", session)
		return err
	}
}

// ---------------------------------------------------------------------------
// cloud-session: provider REST API → CDP WebSocket URL
// ---------------------------------------------------------------------------

// sharedCloudClient is reused across cloud-session resolvers so release
// calls benefit from connection pooling.
var sharedCloudClient = &http.Client{Timeout: 30 * time.Second}

type cloudSessionResolver struct {
	spec BackendSpec
}

func (r *cloudSessionResolver) Resolve(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
) (*Endpoint, error) {
	key := strings.TrimSpace(cfg.APIKey.String())
	if key == "" && r.spec.ID != "steel" {
		return nil, fmt.Errorf("api_key is required for the %s backend", r.spec.ID)
	}

	switch r.spec.ID {
	case "steel":
		return r.resolveSteel(ctx, cfg, key)
	case "hyperbrowser":
		return r.resolveHyperbrowser(ctx, cfg, key)
	case "tinyfish":
		return r.resolveTinyFish(ctx, cfg, key)
	default:
		return nil, fmt.Errorf("no cloud-session resolver for backend %q", r.spec.ID)
	}
}

func defaultBase(cfg config.BrowserBackendConfig, fallback string) string {
	if b := strings.TrimSpace(cfg.BaseURL); b != "" {
		return strings.TrimRight(b, "/")
	}
	return fallback
}

func postJSON(
	ctx context.Context,
	client *http.Client,
	url string,
	headers map[string]string,
	body any,
) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s (status %d)", truncate(string(data), 200), resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid provider response: %w", err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (r *cloudSessionResolver) resolveSteel(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
	key string,
) (*Endpoint, error) {
	base := defaultBase(cfg, "https://api.steel.dev")
	headers := map[string]string{}
	if key != "" {
		headers["steel-api-key"] = key
	}
	out, err := postJSON(ctx, sharedCloudClient, base+"/v1/sessions", headers, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("steel session create: %w", err)
	}
	id := stringField(out, "id", "sessionId", "session_id")
	ws := stringField(out, "websocketUrl", "wsEndpoint", "cdpUrl")
	if ws == "" && id != "" && base == "https://api.steel.dev" {
		// The connect URL only exists for the hosted API; for self-hosted
		// bases we cannot derive it and must get wsEndpoint from the API.
		ws = "wss://connect.steel.dev?sessionId=" + url.QueryEscape(id)
		if key != "" {
			ws += "&apiKey=" + url.QueryEscape(key)
		}
	}
	if ws == "" {
		return nil, fmt.Errorf("steel session created but no WebSocket URL in response")
	}
	return &Endpoint{
		CDPURL: ws,
		Cleanup: func(cctx context.Context) error {
			if id == "" {
				return nil
			}
			_, err := postJSON(cctx, sharedCloudClient,
				fmt.Sprintf("%s/v1/sessions/%s/release", base, url.PathEscape(id)), headers, nil)
			return err
		},
	}, nil
}

func (r *cloudSessionResolver) resolveHyperbrowser(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
	key string,
) (*Endpoint, error) {
	base := defaultBase(cfg, "https://api.hyperbrowser.ai")
	headers := map[string]string{"x-api-key": key}
	out, err := postJSON(ctx, sharedCloudClient, base+"/api/session", headers, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("hyperbrowser session create: %w", err)
	}
	id := stringField(out, "id", "sessionId", "session_id")
	ws := stringField(out, "wsEndpoint", "websocketUrl", "cdpUrl")
	if ws == "" {
		return nil, fmt.Errorf("hyperbrowser session created but no wsEndpoint in response")
	}
	return &Endpoint{
		CDPURL: ws,
		Cleanup: func(cctx context.Context) error {
			if id == "" {
				return nil
			}
			_, err := postJSON(cctx, sharedCloudClient,
				fmt.Sprintf("%s/api/session/%s/stop", base, url.PathEscape(id)), headers, nil)
			return err
		},
	}, nil
}

func (r *cloudSessionResolver) resolveTinyFish(
	ctx context.Context,
	cfg config.BrowserBackendConfig,
	key string,
) (*Endpoint, error) {
	base := defaultBase(cfg, "https://agent.tinyfish.ai")
	headers := map[string]string{"X-Api-Key": key}
	out, err := postJSON(ctx, sharedCloudClient, base+"/v1/browser/sessions", headers, map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("tinyfish session create: %w", err)
	}
	id := stringField(out, "id", "sessionId", "session_id")
	ws := stringField(out, "cdp_url", "cdpUrl", "wsEndpoint", "websocketUrl")
	if ws == "" {
		return nil, fmt.Errorf("tinyfish session created but no cdp_url in response")
	}
	return &Endpoint{
		CDPURL: ws,
		Cleanup: func(cctx context.Context) error {
			if id == "" {
				return nil
			}
			req, err := http.NewRequestWithContext(cctx, http.MethodDelete,
				fmt.Sprintf("%s/v1/browser/sessions/%s", base, url.PathEscape(id)), nil)
			if err != nil {
				return err
			}
			req.Header.Set("X-Api-Key", key)
			resp, err := sharedCloudClient.Do(req)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			return resp.Body.Close()
		},
	}, nil
}
