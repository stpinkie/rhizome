// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package browsertools provides the browser_* agent tools backed by
// pkg/browser: pluggable local/remote/CDP/cloud backends driven through the
// agent-browser CLI or a stateless REST backend (Cloudflare).
package browsertools

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"time"

	"github.com/stpinkie/rhizome/pkg/browser"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/guard"
	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// Tool is a single browser_* tool bound to a shared session Manager.
type Tool struct {
	mgr       *browser.Manager
	name      string
	desc      string
	action    browser.Capability // empty for browser_close
	params    map[string]any
	whitelist *utils.PrivateHostWhitelist
	needsURL  bool
	prepare   func(*browser.Manager, map[string]any) map[string]string
}

func (t *Tool) Name() string               { return t.name }
func (t *Tool) Description() string        { return t.desc }
func (t *Tool) Parameters() map[string]any { return t.params }

func strArg(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

func boolArg(args map[string]any, key string) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return false
}

func (t *Tool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	sessionKey := tools.ToolSessionKey(ctx)
	if sessionKey == "" {
		sessionKey = "default"
	}

	// browser_close is session teardown, not a capability.
	if t.action == "" {
		if err := t.mgr.Close(ctx, sessionKey); err != nil {
			return tools.ErrorResult(fmt.Sprintf("browser_close failed: %v", err))
		}
		return tools.SilentResult("browser session closed")
	}

	// Every tool that accepts a url argument (browser_open requires it;
	// browser_snapshot/browser_screenshot accept one for stateless REST
	// backends) is subject to the same SSRF policy.
	raw := strArg(args, "url")
	if t.needsURL && raw == "" {
		return tools.ErrorResult("url is required")
	}
	if raw != "" {
		if err := t.checkURL(ctx, raw); err != nil {
			return tools.ErrorResult(err.Error())
		}
	}

	// browser_eval executes JavaScript in the page — enforce (not just warn
	// on) prompt-injection phrases, mirroring the exec tool's command screen.
	if t.action == browser.CapEval {
		if match, found := guard.ContainsPromptInjection(strArg(args, "js")); found {
			return tools.ErrorResult(fmt.Sprintf(
				"browser_eval blocked by safety guard (prompt-injection pattern: %s)", match))
		}
	}

	mapped := map[string]string{}
	for k, v := range args {
		switch x := v.(type) {
		case string:
			mapped[k] = x
		case bool:
			if x {
				mapped[k] = "true"
			}
		}
	}
	if t.prepare != nil {
		for k, v := range t.prepare(t.mgr, args) {
			mapped[k] = v
		}
	}

	out, err := t.mgr.Do(ctx, sessionKey, t.action, mapped)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s failed: %v", t.name, err))
	}
	return tools.SilentResult(out)
}

// lookupIPAddr resolves a hostname for the SSRF check. A var so tests can
// stub DNS without depending on a live resolver.
var lookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// checkURL enforces scheme + SSRF policy on user-supplied URLs: literal
// private hosts are rejected outright, and hostnames are resolved so a
// public-looking name cannot point at a private, link-local, or
// cloud-metadata address. Note this guards the *initial* URL only — a real
// browser follows redirects and fetches subresources under its own control.
func (t *Tool) checkURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are allowed")
	}
	if u.Host == "" {
		return fmt.Errorf("missing host in URL")
	}
	host := u.Hostname()
	if utils.IsObviousPrivateHost(host, t.whitelist, nil) {
		return fmt.Errorf(
			"navigating to private or local network hosts is not allowed " +
				"(set tools.browser.private_host_whitelist to allow specific hosts)")
	}

	// Resolve hostnames: the browser will do its own DNS, so reject upfront
	// any name that resolves to a private or restricted address (DNS
	// rebinding, intranet names). Literal IPs were handled above.
	if ip := net.ParseIP(host); ip == nil {
		addrs, err := lookupIPAddr(ctx, host)
		if err != nil {
			return fmt.Errorf("could not resolve host %q: %v", host, err)
		}
		for _, a := range addrs {
			if utils.IsPrivateOrRestrictedIP(a.IP) && !t.whitelist.Contains(a.IP) {
				return fmt.Errorf(
					"host %q resolves to a private or restricted address (%s); "+
						"set tools.browser.private_host_whitelist to allow it",
					host, a.IP)
			}
		}
	}
	return nil
}

// screenshotPath returns a workspace-scoped path for screenshot output.
func screenshotPath(mgr *browser.Manager, args map[string]any) string {
	if p := strArg(args, "path"); p != "" {
		// Constrain to workspace: clean any traversal and keep it relative.
		clean := filepath.Clean(string(filepath.Separator) + p)
		return filepath.Join(mgr.Workspace(), "browser", clean)
	}
	return filepath.Join(
		mgr.Workspace(), "browser", "screenshots",
		fmt.Sprintf("screenshot-%d.png", time.Now().Unix()))
}

// Tools constructs the full browser_* tool set bound to mgr. cfg supplies the
// private-host whitelist for SSRF policy on browser_open.
func Tools(mgr *browser.Manager, cfg *config.BrowserToolsConfig) []tools.Tool {
	var whitelist *utils.PrivateHostWhitelist
	if cfg != nil && len(cfg.PrivateHostWhitelist) > 0 {
		if wl, err := utils.NewPrivateHostWhitelist([]string(cfg.PrivateHostWhitelist)); err == nil {
			whitelist = wl
		}
	}

	strProp := func(desc string) map[string]any {
		return map[string]any{"type": "string", "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		m := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}

	open := &Tool{
		mgr: mgr, name: "browser_open", action: browser.CapOpen, needsURL: true,
		whitelist: whitelist,
		desc: "Open a URL in the agent's browser session. The session persists across browser_* calls " +
			"until browser_close or the session timeout. Use browser_snapshot afterwards to see page content.",
		params: obj(map[string]any{"url": strProp("http/https URL to open")}, "url"),
	}

	snapshot := &Tool{
		mgr: mgr, name: "browser_snapshot", action: browser.CapSnapshot,
		whitelist: whitelist,
		desc: "Capture a snapshot of the current page. With interactive=true (default) returns only " +
			"actionable elements with @eN refs usable by browser_click and browser_fill. " +
			"Re-snapshot after navigation or DOM changes — refs are invalidated.",
		params: obj(map[string]any{
			"interactive": map[string]any{
				"type":        "boolean",
				"description": "Only include interactive elements with @eN refs (default true)",
			},
			"url": strProp("Optional URL to snapshot (stateless backends)"),
		}),
		prepare: func(_ *browser.Manager, args map[string]any) map[string]string {
			m := map[string]string{}
			if boolArg(args, "interactive") || args["interactive"] == nil {
				m["interactive"] = "true"
			}
			return m
		},
	}

	click := &Tool{
		mgr: mgr, name: "browser_click", action: browser.CapClick,
		desc: "Click an element by its @eN ref from the latest browser_snapshot.",
		params: obj(map[string]any{
			"ref": strProp("Element ref, e.g. @e3"),
		}, "ref"),
	}

	fill := &Tool{
		mgr: mgr, name: "browser_fill", action: browser.CapFill,
		desc: "Fill a form input by its @eN ref from the latest browser_snapshot.",
		params: obj(map[string]any{
			"ref":  strProp("Element ref, e.g. @e5"),
			"text": strProp("Text to type into the field"),
		}, "ref", "text"),
	}

	screenshot := &Tool{
		mgr: mgr, name: "browser_screenshot", action: browser.CapScreenshot,
		desc: "Capture a PNG screenshot of the current page into the workspace " +
			"(browser/screenshots/ by default). Optional path is relative to workspace/browser/.",
		params: obj(map[string]any{
			"path": strProp("Optional output path for the PNG, relative to workspace/browser/"),
			"url":  strProp("Optional URL to capture (stateless backends)"),
		}),
		prepare: func(mgr *browser.Manager, args map[string]any) map[string]string {
			return map[string]string{"path": screenshotPath(mgr, args)}
		},
	}

	eval := &Tool{
		mgr: mgr, name: "browser_eval", action: browser.CapEval,
		desc: "Evaluate a JavaScript expression in the current page. Returns the result.",
		params: obj(map[string]any{
			"js": strProp("JavaScript expression to evaluate"),
		}, "js"),
	}

	wait := &Tool{
		mgr: mgr, name: "browser_wait", action: browser.CapWait,
		desc: "Wait for a CSS selector, @eN ref, or a duration in the current page.",
		params: obj(map[string]any{
			"target": strProp("CSS selector, @eN ref, or duration like 2000ms"),
		}, "target"),
	}

	closeTool := &Tool{
		mgr: mgr, name: "browser_close",
		desc:   "Close the browser session and release remote resources (cloud sessions are released/stopped).",
		params: obj(map[string]any{}),
	}

	return []tools.Tool{open, snapshot, click, fill, screenshot, eval, wait, closeTool}
}

// Register registers all browser_* tools into reg.
func Register(reg *tools.ToolRegistry, mgr *browser.Manager, cfg *config.BrowserToolsConfig) {
	if reg == nil || mgr == nil {
		return
	}
	for _, t := range Tools(mgr, cfg) {
		reg.Register(t)
	}
}
