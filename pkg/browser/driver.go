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
	"strings"
	"time"
)

// Runner executes an external command. Injectable for tests.
type Runner interface {
	Run(ctx context.Context, env []string, args ...string) (stdout, stderr string, err error)
}

// ExecRunner is the production Runner backed by os/exec.
type ExecRunner struct{}

// Run executes args[0] with the given args and environment.
func (ExecRunner) Run(ctx context.Context, env []string, args ...string) (string, string, error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("no command specified")
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// Endpoint describes how to reach a browser for a session: either a CDP URL
// passed via the AGENT_BROWSER_CDP env var, or an agent-browser provider name
// passed via -p with credentials in Env. Cleanup releases remote resources
// (cloud sessions, spawned processes) and may be nil.
type Endpoint struct {
	CDPURL   string
	Provider string
	Env      []string
	Cleanup  func(context.Context) error
}

// Driver executes browser actions against a session endpoint.
type Driver interface {
	Open(ctx context.Context, ep *Endpoint, session, url string) (string, error)
	Snapshot(ctx context.Context, ep *Endpoint, session string, interactive bool) (string, error)
	Click(ctx context.Context, ep *Endpoint, session, ref string) (string, error)
	Fill(ctx context.Context, ep *Endpoint, session, ref, text string) (string, error)
	Screenshot(ctx context.Context, ep *Endpoint, session, outPath string) (string, error)
	Eval(ctx context.Context, ep *Endpoint, session, js string) (string, error)
	Wait(ctx context.Context, ep *Endpoint, session, target string) (string, error)
	Close(ctx context.Context, ep *Endpoint, session string) error
}

const (
	defaultDriverTimeout = 2 * time.Minute
	agentBrowserBin      = "agent-browser"
)

// AgentBrowserDriver drives browsers through the agent-browser CLI:
// local Chromium by default, remote CDP endpoints via AGENT_BROWSER_CDP, and
// cloud providers via -p <provider> + environment variables.
type AgentBrowserDriver struct {
	// Bin overrides the agent-binary name/path (default "agent-browser").
	Bin string
	// Runner executes the CLI (default ExecRunner).
	Runner Runner
	// Timeout bounds a single CLI invocation (default 2m).
	Timeout time.Duration
}

func (d *AgentBrowserDriver) runner() Runner {
	if d.Runner != nil {
		return d.Runner
	}
	return ExecRunner{}
}

func (d *AgentBrowserDriver) bin() string {
	if strings.TrimSpace(d.Bin) != "" {
		return d.Bin
	}
	return agentBrowserBin
}

func (d *AgentBrowserDriver) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return defaultDriverTimeout
}

// globalArgs builds the flags shared by every invocation. The CDP endpoint
// is deliberately not an argv flag: it can embed credentials (e.g. steel's
// wss://…?apiKey=…), so it travels via AGENT_BROWSER_CDP instead, which does
// not show up in process listings.
func (d *AgentBrowserDriver) globalArgs(ep *Endpoint, session string) []string {
	args := []string{"--json"}
	if session != "" {
		args = append(args, "--session", session)
	}
	if ep != nil && ep.Provider != "" {
		args = append(args, "-p", ep.Provider)
	}
	return args
}

func (d *AgentBrowserDriver) run(
	ctx context.Context,
	ep *Endpoint,
	session string,
	args ...string,
) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()

	full := append([]string{d.bin()}, d.globalArgs(ep, session)...)
	full = append(full, args...)

	var env []string
	if ep != nil {
		env = append([]string{}, ep.Env...)
		if ep.CDPURL != "" {
			env = append(env, "AGENT_BROWSER_CDP="+ep.CDPURL)
		}
	}
	stdout, stderr, err := d.runner().Run(cctx, env, full...)
	if err != nil {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("agent-browser: %s", msg)
	}
	return strings.TrimSpace(stdout), nil
}

// Open navigates the session browser to url.
func (d *AgentBrowserDriver) Open(
	ctx context.Context, ep *Endpoint, session, url string,
) (string, error) {
	return d.run(ctx, ep, session, "open", url)
}

// Snapshot returns the page snapshot; interactive limits it to actionable
// elements with @eN refs.
func (d *AgentBrowserDriver) Snapshot(
	ctx context.Context, ep *Endpoint, session string, interactive bool,
) (string, error) {
	args := []string{"snapshot"}
	if interactive {
		args = append(args, "-i")
	}
	return d.run(ctx, ep, session, args...)
}

// Click clicks the element identified by ref (e.g. @e1 from a snapshot).
func (d *AgentBrowserDriver) Click(
	ctx context.Context, ep *Endpoint, session, ref string,
) (string, error) {
	return d.run(ctx, ep, session, "click", ref)
}

// Fill types text into the element identified by ref.
func (d *AgentBrowserDriver) Fill(
	ctx context.Context, ep *Endpoint, session, ref, text string,
) (string, error) {
	return d.run(ctx, ep, session, "fill", ref, text)
}

// Screenshot captures the page to outPath.
func (d *AgentBrowserDriver) Screenshot(
	ctx context.Context, ep *Endpoint, session, outPath string,
) (string, error) {
	return d.run(ctx, ep, session, "screenshot", outPath)
}

// Eval evaluates a JavaScript expression in the page.
func (d *AgentBrowserDriver) Eval(
	ctx context.Context, ep *Endpoint, session, js string,
) (string, error) {
	return d.run(ctx, ep, session, "eval", js)
}

// Wait waits for a selector, ref, or time duration.
func (d *AgentBrowserDriver) Wait(
	ctx context.Context, ep *Endpoint, session, target string,
) (string, error) {
	return d.run(ctx, ep, session, "wait", target)
}

// Close closes the session browser.
func (d *AgentBrowserDriver) Close(ctx context.Context, ep *Endpoint, session string) error {
	_, err := d.run(ctx, ep, session, "close")
	return err
}
