# Rhizome as an MCP Server

`rhizome mcp serve` runs Rhizome in the opposite direction of
`tools.mcp.servers`: instead of Rhizome consuming MCP servers, it *becomes*
one over stdio, exposing the operator's own tool registry to any MCP client
(Claude Desktop, Cursor, another agent process).

This is a daemonless command — the serving process is the MCP server
process itself, spawned by the client. It reads `config.json` like every
other Rhizome command, so the workspace, tool sandboxing, and guard
patterns all apply exactly as they do to the agent.

## Enabling

```json
"tools": {
  "mcp_server": {
    "enabled": true,
    "allow": ["list_dir", "read_file", "web_fetch"]
  }
}
```

- `enabled` — master switch; the command exits with an explanatory error
  when unset.
- `allow` — exact tool names from the local registry to expose. **Empty
  means deny-all**: the server starts, answers `tools/list` with an empty
  set, and refuses every `tools/call`. There is intentionally no
  allow-all mode.

## Client wiring

The client launches the binary directly over stdio. Claude Desktop-style
config:

```json
"mcpServers": {
  "rhizome": {
    "command": "rhizome",
    "args": ["serve"]
  }
}
```

Point `RHIZOME_CONFIG` at a non-default config file if the server should
use one; diagnostics go to `RHIZOME_LOG_FILE` or stderr — stdout is
reserved for the MCP protocol stream and stays clean.

## What clients see

`tools/list` advertises exactly the `allow` entries (unknown or
unregistrable names are skipped). `tools/call` on anything not listed
returns an in-band error — this is a policy response, not a protocol
failure.

The serve registry deliberately **excludes agent-loop tools** —
`delegate`, `acp_run`, `spawn`, the swarm verbs, `message`, `send_*` — so a
client can never reach orchestration surfaces through this channel.
Filesystem tools keep their normal `restrict_to_workspace` posture and
deny-pattern screening; `exec` keeps `tools.exec` deny/allow patterns.

## Trust posture

`mcp serve` is a *server* — the client is the trusted party driving it.
Gate exposure by what you put in `allow`, not by assuming the client is
benign: any tool reachable here is reachable to whatever MCP client (and
whatever model behind it) the operator wires in. Keep the allowlist
minimal; prefer read tools (`list_dir`, `read_file`, `web_fetch`) over
write/exec tools.

Related: `docs/reference/mcp-cli.md` covers the `rhizome mcp` management
commands (the client side — configuring servers Rhizome consumes).
