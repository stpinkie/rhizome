---
name: stealth-browser
description: "Stealth browser automation via a Patchright-based MCP server. Use when browser automation on a site blocks or detects normal automation (bot detection, CAPTCHAs, fingerprinting). Requires the 'patchright' MCP server to be configured."
metadata: {"rhizome":{"emoji":"🥷","requires":{"mcp_servers":["patchright"]},"install":[{"id":"npm-patchright","kind":"npm","package":"@browserbasehq/mcp-patchright","global":true,"bins":["mcp-patchright"],"label":"Install mcp-patchright (npm)"}]}}
---

# Stealth Browser

Use a **Patchright**-based MCP browser for sites that detect normal automation.
Patchright patches Playwright's client-side behavior (bot fingerprints), which
is why this path uses MCP instead of the plain-CDP `browser_*` tools — the
stealth lives in the client driver, not the browser binary.

## Setup

Configure the MCP server in `config.json`:

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "patchright": {
          "command": "npx",
          "args": ["-y", "@browserbasehq/mcp-patchright"],
          "env": {}
        }
      }
    }
  }
}
```

Restart the gateway after changing MCP config. The server exposes tools like
`browser_navigate`, `browser_click`, `browser_type`, `browser_take_screenshot`
via Rhizome's MCP bridge.

## When to use this vs `browser_*` tools

- `browser_*` (agent-browser / CDP): fast, lightweight, works for most sites.
- Stealth MCP browser: use when pages block headless browsers, show CAPTCHAs,
  or gate content behind bot detection.

## Notes

- Respect site terms of service; stealth is for legitimate automation that the
  default fingerprint unnecessarily blocks.
- MCP tool calls are still subject to Rhizome's tool allowlist.
- The MCP server runs a real Chromium via Patchright — it needs a desktop
  environment or `xvfb` on headless Linux.
