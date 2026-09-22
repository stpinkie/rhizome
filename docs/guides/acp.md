# ACP — Agent Client Protocol

Rhizome speaks [ACP](https://agentclientprotocol.com) in both directions:

- **Server** — `rhizome acp` lets editors such as **Zed** and **JetBrains
  IDEs** drive your Rhizome agents directly, with streamed responses,
  tool-call progress, and permission prompts.
- **Client** — `agents.list[].acp` binds an external ACP agent
  (`gemini --acp`, `claude-code acp`, …) to a Rhizome agent id, so it can
  be delegated to like any local agent.

`rhizome acp` speaks newline-delimited JSON-RPC over **stdio**. The editor
spawns it as a subprocess; prompts run through the normal Rhizome agent
pipeline (routing, session history, tools, provider fallback) on an internal
`acp` channel.

## Setup

### Zed

Add to `~/.config/zed/settings.json` (or **Agent Panel → Settings → Agent
Servers**):

```json
{
  "agent_servers": {
    "Rhizome": {
      "command": {
        "path": "rhizome",
        "args": ["acp"]
      }
    }
  }
}
```

Pin a specific agent instead of the routing default:

```json
"args": ["acp", "--agent", "worker"]
```

### JetBrains IDEs

In the AI/agent settings, register a custom ACP agent with command
`rhizome acp` (the IDE manages the stdio transport the same way).

## Behaviour

- **Sessions** — `session/new` maps to a Rhizome session key
  `agent:<agent-id>:acp:<session-id>`, so history persists across prompts
  within the session. `session/load` resurrects a persisted session after
  a restart: session metadata lives in
  `<RHIZOME_HOME>/acp-sessions.json` (0600), prior turns replay as
  `session/update` chunks, and cached `allow_always`/`reject_always`
  permission decisions are restored. `--agent <id>` refuses loads bound
  to a different agent.
- **Streaming** — model streaming is force-enabled inside the `acp` process
  for every configured model. Providers without streaming support fall back
  to a normal response delivered as one chunk.
- **Tool calls** — tool execution appears as ACP `tool_call` /
  `tool_call_update` notifications, correlated by the provider's tool-call
  id. File-path arguments surface as clickable locations.
- **Permissions** — every tool call is bridged through
  `session/request_permission` unless configured otherwise.

## Tool permissions

`config.json`:

```json
{
  "acp": {
    "server": { "permission_policy": "prompt" }
  }
}
```

| Policy   | Behaviour                                                        |
| -------- | ---------------------------------------------------------------- |
| `prompt` | *(default)* ask the client per tool call; `allow_always`/`reject_always` answers are cached per tool for the session. |
| `allow`  | approve every tool call without prompting (same as a channel agent). |
| `deny`   | reject every tool call — the agent can read/answer but not act.   |

If the client disconnects or cancels a permission request, the tool call
fails closed (denied).

## Session MCP servers

`session/new` and `session/load` accept client-declared `mcpServers`.
Only **stdio** entries are honoured (http/sse/acp variants are refused);
each session gets its own MCP manager, at most 4 servers, and a 30 s
connect bound. Tools surface to the agent as
`mcp_acp-<session>-<server>_<tool>` and are scoped to the owning session —
other sessions (and other channels) neither see nor can execute them, and
everything is torn down on `session/close` or server shutdown.

Session-declared servers run **env-only**: the child receives a minimal
system base (PATH, HOME, temp dirs, …) plus exactly the `env` entries the
client declared — the daemon's environment is never inherited.

## Diagnostics

stdout carries the protocol, so console logging is always disabled inside
`rhizome acp`. To capture diagnostics:

```bash
RHIZOME_LOG_FILE=~/.rhizome/acp.log rhizome acp
```

---

## External ACP agents (Rhizome as client)

Bind an external ACP-capable agent to a Rhizome agent id:

```json
{
  "agents": {
    "list": [
      {
        "id": "gemini",
        "acp": {
          "command": "gemini",
          "args": ["--acp"],
          "env": { "GEMINI_API_KEY": "..." },
          "cwd": "D:\\work\\project"
        }
      }
    ]
  }
}
```

The bound id is routable like any other agent:

- `delegate`/`subagent`/`spawn` tools can target it (subject to the usual
  `subagents.allow_agents` rules).
- Trusted mesh peers can delegate into it — the node advertises it in its
  capability manifest and the dispatch is served by the local ACP client
  (the agent never routes off-box by accident: the mesh checker treats
  ACP-bound ids as local).
- The `acp_run` tool invokes it explicitly.

### Lifecycle

The external process is spawned lazily on first use and reused across
delegations; each delegation creates a fresh `session/new` (prompt text
only — media attachments are dropped with a warning). On gateway shutdown
the processes are terminated.

### What the external agent can do

Rhizome answers client-bound ACP requests like this:

| Capability             | Behaviour                                                                                  |
| ---------------------- | ------------------------------------------------------------------------------------------ |
| `fs/read_text_file`    | Served through the workspace sandbox (`agents.defaults.restrict_to_workspace` + `tools.allow_read_paths`), 64 KiB cap. |
| `fs/write_text_file`   | Served through the same sandbox (`tools.allow_write_paths`); **disabled** under `allow-read-only`. |
| `session/request_permission` | Answered by `acp.client.permission_policy` (below).                                   |
| `terminal/*`           | Refused by default; see `acp.client.terminal_policy` below.                            |

```json
{
  "acp": {
    "client": { "permission_policy": "deny", "terminal_policy": "deny" }
  }
}
```

| Policy            | Behaviour                                                             |
| ----------------- | --------------------------------------------------------------------- |
| `deny`            | *(default)* reject every permission request.                          |
| `allow-read-only` | allow `read`/`search`/`think`/`fetch` tool kinds; reject the rest.    |
| `allow`           | allow every permission request.                                        |

`terminal_policy` gates the `terminal/*` capability:

| Policy  | Behaviour                                                                     |
| ------- | ----------------------------------------------------------------------------- |
| `deny`  | *(default)* capability not advertised; every `terminal/*` method fails.       |
| `allow` | terminals spawn through the guarded exec path — the same deny patterns, workspace-restricted cwd, and bounded output as the `exec` tool. |

Terminal ids are bound to the requesting session and killed when the
external agent process exits.

Authentication: if the external agent advertises `authMethods` during
`initialize`, the connection fails — Rhizome does not perform ACP auth.
Give the agent its credentials via `env` instead.

### Trust posture

The external agent runs as a **plain child process** with its own
filesystem and environment — it is not sandboxed by `pkg/isolation`.
Rhizome's controls are the sandboxed fs bridge and the permission policy.
Treat an `acp` binding with the same trust you'd give running that agent's
CLI yourself.
