# Browser Automation

Rhizome's browser tools (`browser_*`) let the agent open pages, inspect the DOM,
fill forms, click elements, take screenshots, and run JavaScript — through a
pluggable backend that can be a local browser, a custom CDP endpoint, or a cloud
browser provider.

> **Off by default.** Enable with `tools.browser.enabled: true`. All `browser_*`
> tools respect the normal tool allowlist and per-agent restrictions.

## How it works

- One **driver**: the [agent-browser](https://github.com/vercel-labs/agent-browser)
  CLI drives local Chromium, remote CDP endpoints, and cloud providers.
- One **stateless REST backend**: Cloudflare Browser Rendering (snapshot,
  screenshot, content only — no click/fill/eval).
- Sessions are per-conversation and close on `browser_close`, the
  `session_timeout` (default `10m`), or agent shutdown. Cloud sessions are
  explicitly released/stopped to avoid runaway billing.

## Quick start

```powershell
# Requires Node.js
npm i -g agent-browser
agent-browser install   # downloads Chromium (~400 MB)
```

```json
{
  "tools": {
    "browser": { "enabled": true, "default_backend": "agent-browser" }
  }
}
```

Or use the **Browser** page in the web console (`/browser`) to install, pick the
default backend, and enter credentials.

## Agent tools

| Tool | Purpose |
| --- | --- |
| `browser_open` | Navigate to a URL (http/https only; private hosts blocked) |
| `browser_snapshot` | Page snapshot; `interactive=true` (default) yields `@eN` element refs |
| `browser_click` | Click an element by `@eN` ref |
| `browser_fill` | Fill an input by `@eN` ref |
| `browser_screenshot` | PNG into `workspace/browser/screenshots/` |
| `browser_eval` | Evaluate a JS expression in the page |
| `browser_wait` | Wait for a selector, ref, or duration |
| `browser_close` | Close the session and release remote resources |

Workflow: `browser_open` → `browser_snapshot` → interact via `@eN` refs →
re-snapshot after navigation (refs are invalidated).

## Backends

| Backend | Kind | Notes |
| --- | --- | --- |
| `agent-browser` | local | Bundled Chromium via npm install (default) |
| `system-chrome` | local | Your installed Chrome/Edge, dedicated profile — no download |
| `clawbrowser` | local (BYO) | Anti-detect Chromium; needs `clawctl` + API key |
| `custom-cdp` | remote | Any `ws://`/`http://` CDP endpoint |
| `provider` | cloud | Generic `agent-browser -p <name>` passthrough |
| `browserbase` | cloud | `api_key` + `project_id` |
| `browserless` | cloud/self-host | `api_key`; `base_url` for self-host |
| `kernel` | cloud | `api_key`; set `env.KERNEL_STEALTH=true` for stealth |
| `steel` | cloud/self-host | `api_key` optional for self-host |
| `hyperbrowser` | cloud | `api_key` |
| `tinyfish` | cloud | `api_key` |
| `cloudflare` | REST | `account_id` + `api_key`; snapshot/screenshot only |

### Configuration

```json
{
  "tools": {
    "browser": {
      "enabled": true,
      "default_backend": "browserbase",
      "session_timeout": "10m",
      "private_host_whitelist": [],
      "backends": {
        "browserbase": {
          "api_key": "YOUR_BROWSERBASE_API_KEY",
          "project_id": "YOUR_PROJECT_ID"
        }
      }
    }
  }
}
```

- API keys are `SecureString` — they can live in `.security.yml` and are masked
  in logs and tool output. Put credentials in `api_key`, not in a backend's
  `env` map: `env` values are stored in plain `config.json` (values under
  secret-looking names like `*_API_KEY`/`*_TOKEN` are still masked in logs and
  tool output, but they are not relocated to `.security.yml`).
- `private_host_whitelist` lists hostnames/IPs/CIDRs the SSRF guard allows for
  `browser_open` (same mechanism as `tools.web.private_host_whitelist`).

### Cloudflare Browser Rendering

REST-only in v0.7.1: `browser_open` registers the target URL, then
`browser_snapshot`/`browser_screenshot` call the stateless API. Interactive
ops (`click`, `fill`, `eval`, `wait`) return a clear unsupported-backend error.
A native CDP client (for authenticated WebSocket connections) is planned for
v0.8.0.

### Stealth browsing

For anti-detect automation, use the **stealth-browser** workspace skill, which
routes through a Patchright-based MCP server instead of the CDP driver —
stealth lives in the client, so plain-CDP access to a patched browser loses it.

## Security notes

- `browser_open` rejects `localhost`, private IPs, link-local and metadata
  addresses unless whitelisted — both for literal hosts and after DNS
  resolution, so a public-looking hostname cannot resolve to a private or
  cloud-metadata address. The optional `url` on `browser_snapshot` and
  `browser_screenshot` is checked the same way. This guards the *initial* URL;
  once a page is loaded, in-page redirects and subresource fetches follow
  normal browser rules and are not intercepted.
- `browser_eval` executes arbitrary JavaScript in the page — treat like `exec`.
- Local CDP endpoints (`system-chrome`, `custom-cdp` on loopback) listen on
  `127.0.0.1` for the session's lifetime; any local process can attach. CDP
  endpoints are passed to the driver via `AGENT_BROWSER_CDP` rather than argv
  so credential-bearing ws URLs don't appear in process listings — they are
  still visible to same-user processes via the environment.
- Cloud sessions bill while open; `session_timeout` is enforced by a
  background reaper (idle sessions are stopped even without further tool
  calls) and `browser_close` releases immediately. Provider endpoint URLs
  containing tokens are masked in logs.
- Screenshots and artifacts are constrained to the agent workspace.

## Deferred / dropped

- **LightPanda** — no Windows binary, WIP CDP, no real renderer, AGPL.
- **CamouFox direct** — Playwright/Juggler protocol, not CDP (would need a
  Python bridge).
- **Donut** — CDP is Pro-gated; requires a running desktop app.
