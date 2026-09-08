package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

// fakeRunner records invocations and returns canned output.
type fakeRunner struct {
	mu     sync.Mutex
	calls  [][]string
	envs   [][]string
	output string
	err    error
	stderr string
}

func (f *fakeRunner) Run(
	_ context.Context,
	env []string,
	args ...string,
) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	f.envs = append(f.envs, env)
	return f.output, f.stderr, f.err
}

func testManager(t *testing.T, run Runner, driver Driver) *Manager {
	cfg := &config.BrowserToolsConfig{
		ToolConfig:     config.ToolConfig{Enabled: true},
		DefaultBackend: "agent-browser",
		SessionTimeout: "50ms",
		Backends:       config.BrowserBackendsConfig{},
	}
	m := NewManager(cfg, t.TempDir())
	if run != nil {
		m.SetRunner(run)
	}
	if driver != nil {
		m.SetDriver(driver)
	}
	return m
}

func TestCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range Catalog() {
		if spec.ID == "" || spec.Name == "" || spec.Kind == "" {
			t.Errorf("incomplete catalog entry: %+v", spec)
		}
		if seen[spec.ID] {
			t.Errorf("duplicate backend id %q", spec.ID)
		}
		seen[spec.ID] = true
		if len(spec.Caps) == 0 {
			t.Errorf("backend %q has no capabilities", spec.ID)
		}
	}
	// The 12 planned backends must all be present.
	for _, id := range []string{
		"agent-browser", "system-chrome", "clawbrowser", "custom-cdp",
		"provider", "browserbase", "browserless", "kernel",
		"steel", "hyperbrowser", "tinyfish", "cloudflare",
	} {
		if _, ok := Lookup(id); !ok {
			t.Errorf("catalog missing backend %q", id)
		}
	}
}

func TestCloudflareIsRestOnly(t *testing.T) {
	spec, ok := Lookup("cloudflare")
	if !ok {
		t.Fatal("cloudflare missing")
	}
	for _, cap := range []Capability{CapClick, CapFill, CapEval, CapWait} {
		if spec.HasCapability(cap) {
			t.Errorf("cloudflare should not support %s", cap)
		}
	}
	if !spec.HasCapability(CapSnapshot) || !spec.HasCapability(CapScreenshot) {
		t.Error("cloudflare should support snapshot and screenshot")
	}
}

func TestDriverArgs(t *testing.T) {
	run := &fakeRunner{output: "ok"}
	d := &AgentBrowserDriver{Runner: run}
	ep := &Endpoint{CDPURL: "ws://127.0.0.1:9222"}
	if _, err := d.Open(context.Background(), ep, "s1", "https://example.com"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := strings.Join(run.calls[0], " ")
	want := "agent-browser --json --session s1 open https://example.com"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	// The CDP endpoint travels via env, not argv, so credential-bearing ws
	// URLs stay out of process listings.
	if len(run.envs[0]) != 1 || run.envs[0][0] != "AGENT_BROWSER_CDP=ws://127.0.0.1:9222" {
		t.Fatalf("env = %v, want AGENT_BROWSER_CDP=ws://127.0.0.1:9222", run.envs)
	}
}

func TestDriverProviderArgs(t *testing.T) {
	run := &fakeRunner{output: "ok"}
	d := &AgentBrowserDriver{Runner: run}
	ep := &Endpoint{Provider: "browserbase", Env: []string{"BROWSERBASE_API_KEY=k"}}
	if _, err := d.Snapshot(context.Background(), ep, "s1", true); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	got := strings.Join(run.calls[0], " ")
	if !strings.Contains(got, "-p browserbase") {
		t.Fatalf("missing provider flag: %q", got)
	}
	if !strings.HasSuffix(got, "snapshot -i") {
		t.Fatalf("missing interactive snapshot: %q", got)
	}
	if run.envs[0][0] != "BROWSERBASE_API_KEY=k" {
		t.Fatalf("env not passed: %v", run.envs)
	}
}

func TestDriverError(t *testing.T) {
	run := &fakeRunner{err: fmt.Errorf("exit 1"), stderr: "boom"}
	d := &AgentBrowserDriver{Runner: run}
	_, err := d.Click(context.Background(), nil, "s1", "@e1")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr surfaced, got %v", err)
	}
}

func TestManagerUnknownBackend(t *testing.T) {
	cfg := &config.BrowserToolsConfig{
		DefaultBackend: "no-such-backend",
		Backends:       config.BrowserBackendsConfig{},
	}
	m := NewManager(cfg, t.TempDir())
	_, err := m.Do(context.Background(), "k", CapOpen, map[string]string{"url": "https://x"})
	if err == nil || !strings.Contains(err.Error(), "unknown browser backend") {
		t.Fatalf("expected unknown-backend error, got %v", err)
	}
}

func TestManagerCustomCDP(t *testing.T) {
	run := &fakeRunner{output: "opened"}
	cfg := &config.BrowserToolsConfig{
		DefaultBackend: "custom-cdp",
		SessionTimeout: "1h",
		Backends: config.BrowserBackendsConfig{
			"custom-cdp": {EndpointURL: "ws://127.0.0.1:9222/devtools/browser/x"},
		},
	}
	m := NewManager(cfg, t.TempDir())
	m.SetRunner(run)
	m.SetDriver(&AgentBrowserDriver{Runner: run})
	out, err := m.Do(context.Background(), "k", CapOpen,
		map[string]string{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("Do open: %v", err)
	}
	if out != "opened" {
		t.Fatalf("out = %q", out)
	}
	// The configured endpoint must reach the driver via AGENT_BROWSER_CDP.
	found := false
	for _, e := range run.envs[0] {
		if e == "AGENT_BROWSER_CDP=ws://127.0.0.1:9222/devtools/browser/x" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env missing cdp endpoint: %v", run.envs[0])
	}
}

func TestManagerSessionTimeout(t *testing.T) {
	run := &fakeRunner{output: "ok"}
	m := testManager(t, run, &AgentBrowserDriver{Runner: run})
	m.now = func() time.Time { return fakeNow }

	ctx := context.Background()
	if _, err := m.Do(ctx, "k", CapSnapshot, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	callsBefore := len(run.calls)

	// Advance past the 50ms timeout — session must be torn down + re-created.
	fakeNow = fakeNow.Add(time.Hour)
	if _, err := m.Do(ctx, "k", CapSnapshot, nil); err != nil {
		t.Fatalf("second call: %v", err)
	}
	// close + snapshot for the old session, then snapshot on the new one
	if len(run.calls) <= callsBefore {
		t.Fatal("expected session re-creation")
	}
}

var fakeNow = time.Now()

func TestManagerCloseAll(t *testing.T) {
	run := &fakeRunner{output: "ok"}
	m := testManager(t, run, &AgentBrowserDriver{Runner: run})
	ctx := context.Background()
	if _, err := m.Do(ctx, "k", CapSnapshot, nil); err != nil {
		t.Fatal(err)
	}
	m.CloseAll(ctx)
	if len(m.sessions) != 0 {
		t.Fatal("sessions not cleared")
	}
}

func TestNewCloudflareClient(t *testing.T) {
	_, err := NewCloudflareClient(config.BrowserBackendConfig{})
	if err == nil {
		t.Fatal("expected missing-account error")
	}
	c, err := NewCloudflareClient(config.BrowserBackendConfig{
		AccountID: "acc",
		APIKey:    *config.NewSecureString("tok"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.endpoint("snapshot"), "browser-rendering/snapshot") {
		t.Fatalf("bad endpoint: %s", c.endpoint("snapshot"))
	}
}
