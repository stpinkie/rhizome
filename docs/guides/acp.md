# ACP — Agent Client Protocol

Rhizome can act as an [ACP](https://agentclientprotocol.com) agent, so editors
such as **Zed** and **JetBrains IDEs** can drive your Rhizome agents directly
— with streamed responses, tool-call progress, and permission prompts.

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
  within the session. `session/load` is not supported.
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

## Diagnostics

stdout carries the protocol, so console logging is always disabled inside
`rhizome acp`. To capture diagnostics:

```bash
RHIZOME_LOG_FILE=~/.rhizome/acp.log rhizome acp
```
