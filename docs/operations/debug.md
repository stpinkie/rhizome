# Debugging Rhizome

Rhizome performs multiple complex interactions under the hood for every single request it receives—from routing messages and evaluating complexity, to executing tools and adapting to model failures. Being able to see exactly what is happening is crucial, not just for troubleshooting potential issues, but also for truly understanding how the agent operates.
## Starting Rhizome in Debug Mode

To get detailed information about what the agent is doing (LLM requests, tool calls, message routing), you can start the Rhizome gateway with the debug flag:

```bash
rhizome gateway --debug
# or
rhizome gateway -d
```

In this mode, the system will format the logs extensively and display previews of system prompts and tool execution results.

## Disabling Log Truncation (Full Logs)

By default, Rhizome truncates very long strings (such as the *System Prompt* or large JSON output results) in the debug logs to keep the console readable.

If you need to inspect the complete output of a command or the exact payload sent to the LLM model, you can use the `--no-truncate` flag.

**Note:** This flag *only* works when combined with the `--debug` mode.

```bash
rhizome gateway --debug --no-truncate

```

When this flag is active, the global truncation function is disabled. This is extremely useful for:

* Verifying the exact syntax of the messages sent to the provider.
* Reading the complete output of tools like `exec`, `web_fetch`, or `read_file`.
* Debugging the session history saved in memory.

## Tool Call Visibility in Debug Logs

When debug mode is active, the agent emits structured log entries at each stage of the tool execution lifecycle. These entries carry a `component=agent` label and use `INFO` or `DEBUG` level depending on the amount of detail:

| Log message | Level | Key fields | Description |
|---|---|---|---|
| `LLM requested tool calls` | INFO | `tools`, `count`, `iteration` | List of tool names the model decided to call |
| `Tool call: <name>(<args>)` | INFO | `tool`, `iteration` | The tool name and a preview of its arguments (truncated to 200 chars) |
| `Sent tool result to user` | DEBUG | `tool`, `content_len` | Fired when a tool result is forwarded to the chat channel |
| `TTL tick after tool execution` | DEBUG | `agent_id`, `iteration` | MCP tool-discovery TTL decrement after each tool round |
| `Async tool completed, publishing result` | INFO | `tool`, `content_len`, `channel` | Only for tools that run asynchronously in the background |

### Reading a tool call log entry

A typical synchronous tool call produces two consecutive lines in the console:

```
[...] [INFO] agent: LLM requested tool calls {tools=[web_search], count=1, iteration=1}
[...] [INFO] agent: Tool call: web_search({"query":"rhizome release notes"}) {tool=web_search, iteration=1}
```

The arguments preview is hard-capped at **200 characters** in the logs regardless of the `--no-truncate` flag, because it belongs to the `INFO`-level path. Use `--no-truncate` together with `--debug` to see the full `tools_json` field emitted by the `Full LLM request` DEBUG entry, which contains every tool definition sent to the model.

## Real-Time Tool Feedback in Chat (tool_feedback)

Debug logs are server-side only. If you want the agent to send a visible notification directly into the chat channel every time it executes a tool—useful when sharing the bot with other users or for transparency—enable the `tool_feedback` feature in `config.json`:

```json
{
  "agents": {
    "defaults": {
      "tool_feedback": {
        "enabled": true,
        "max_args_length": 300,
        "separate_messages": true
      }
    }
  }
}
```

When `enabled` is `true`, every tool call sends a short message to the chat before the tool result is returned to the model. The message looks like:

```bash
🔧 `web_search`
{"query": "rhizome release notes"}
```


### Options

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Send a chat notification for each tool call |
| `separate_messages` | bool | `false` | Keep every tool feedback update as a separate chat message instead of reusing a single placeholder/progress message |
| `max_args_length` | int | `300` | Maximum characters of the serialised arguments included in the notification |

### Environment variables

Both fields can also be set via environment variables:

```bash
RHIZOME_AGENTS_DEFAULTS_TOOL_FEEDBACK_ENABLED=true
RHIZOME_AGENTS_DEFAULTS_TOOL_FEEDBACK_MAX_ARGS_LENGTH=300
```

> **Note:** `tool_feedback` is independent of `--debug` mode. It works in production and does not require the gateway to be started with any special flag.

## Mesh & Swarm Observability

When the daemon is running, these verbs answer "what happened on the
mesh" without digging through raw logs.

### Activity feed (persistent)

```bash
rhizome mesh activity --tail 50
rhizome mesh activity --kind 'swarm.*' --swarm ops --since 1h
rhizome mesh activity --peer 12D3KooW… --kind mesh.task.update
```

`mesh.*`/`swarm.*` runtime events from the daemon feed. Filters:
`--kind` (exact or `prefix.*`), `--peer` (any attribute mention),
`--swarm` (exact `swarm_id`), `--since` (Go duration or RFC3339). With
`mesh.activity_log` on (default), the feed persists to
`~/.rhizome/mesh-activity.jsonl` (rotated like the audit trail) so
`--since` reads back across restarts.

### Correlated traces

```bash
rhizome mesh trace  <task-id>
rhizome swarm trace <offer-id|run-id>
```

Assembles a report for one id from the task store, published and
observed swarm offers, run records, matching activity events, and the
audit tail — one command instead of grepping four files. Daemon
endpoint: `GET /network/trace?task=|offer=|run=|id=`.

### Peer score detail

```bash
rhizome mesh peer <peer-id>
```

Live peer detail: connections, score, and — since v0.15.0 — per-op
counters (`delegate`, `spawn`, `submit`, `status`, `result`, `cancel`,
`list`) plus evidence **decay**: peer scores halve per idle
`mesh.score_half_life` window (default `168h`, `0` disables). A peer
that was reliable last month but silent since shows as "stale" rather
than permanently trusted. Raw counters persist; only the read views
decay.

### Swarm health

```bash
rhizome swarm doctor <swarm-id>
```

Per-member diagnostic query (bounded, 5s each): asymmetric rosters
(members who don't list you), undiscovered members, coordinator
reachability, per-member epoch drift. Also `GET
/network/swarms/<id>/doctor` (and `/health`). `swarm status` shows
`last_state_write` freshness for the coordinator's shared state.

### Durable records on disk

| File | Contents |
| ---- | -------- |
| `~/.rhizome/mesh-audit.jsonl` | signed audit trail of mesh ops |
| `~/.rhizome/mesh-activity.jsonl` | persisted activity feed |
| `~/.rhizome/mesh-tasks.jsonl` | remote task records |
| `~/.rhizome/swarm-offers.jsonl` | published offer queue (re-driven on restart) |
| `~/.rhizome/swarm-runs.jsonl` | goal run records (`interrupted` after restart sweep) |

