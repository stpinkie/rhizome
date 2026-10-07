# 🔄 Spawn & Async Tasks

> Back to [README](../README.md)

Rhizome supports **asynchronous task execution** via the `spawn` tool. This is primarily used by the **Heartbeat** system to run long-running tasks without blocking the main agent loop.

## Heartbeat

The heartbeat system periodically checks `workspace/HEARTBEAT.md` for scheduled tasks. On first run, a default template is auto-generated. You can customize it to define quick tasks (handled inline) and long tasks (delegated via `spawn`).

**Example `HEARTBEAT.md`:**

```markdown
## Quick Tasks (respond directly)

- Report current time

## Long Tasks (use spawn for async)

- Search the web for AI news and summarize
- Check email and report important messages
```

**Key behaviors:**

| Feature                 | Description                                               |
| ----------------------- | --------------------------------------------------------- |
| **spawn**               | Creates async subagent, doesn't block heartbeat           |
| **Independent context** | Subagent has its own context, no session history          |
| **message tool**        | Subagent communicates with user directly via message tool |
| **Non-blocking**        | After spawning, heartbeat continues to next task          |

#### How Subagent Communication Works

```
Heartbeat triggers
    ↓
Agent reads HEARTBEAT.md
    ↓
For long task: spawn subagent
    ↓                           ↓
Continue to next task      Subagent works independently
    ↓                           ↓
All tasks done            Subagent uses "message" tool
    ↓                           ↓
Respond HEARTBEAT_OK      User receives result directly
```

The subagent has access to tools (message, web_search, etc.) and can communicate with the user independently without going through the main agent.

**Configuration:**

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

| Option     | Default | Description                        |
| ---------- | ------- | ---------------------------------- |
| `enabled`  | `true`  | Enable/disable heartbeat           |
| `interval` | `30`    | Check interval in minutes (min: 5) |

**Environment variables:**

* `RHIZOME_HEARTBEAT_ENABLED=false` to disable
* `RHIZOME_HEARTBEAT_INTERVAL=60` to change interval

## Remote Tasks Over the Mesh

`spawn` runs work on *your* node. To run work on a *trusted peer's* node,
use the mesh remote-task verbs (`/rhizome/agent-task/1.0.0`):

```bash
rhizome network spawn <peer-multiaddr> <agent-id> <task>   # async submit
rhizome mesh task status <peer-multiaddr> <task-id>
rhizome mesh task result <peer-multiaddr> <task-id>        # long-poll
rhizome mesh task cancel <peer-multiaddr> <task-id>
rhizome mesh task list   <peer-multiaddr>
```

### Picking the peer for you

```bash
rhizome mesh route   <agent-id> <task> --require-model qwen3 --require-skill pdf
rhizome mesh scatter <agent-id> <task> --n 3 --strategy quorum --k 2
```

`route` picks the best connected, capable, trusted peer (capability +
load aware) and dispatches; `--wait <dur>` long-polls the result.
`--require-model` / `--require-skill` are repeatable and restrict the
pick — *and* failover candidates — to manifests advertising them. Peers
opt into advertising via `mesh.advertise_models` /
`mesh.advertise_skills` (or `mesh.skill_share` for skills); a manifest
that omits the class never satisfies the requirement. `scatter` fans the
task out to `--n` capable peers and aggregates (`--strategy
first|quorum|all`).

### Capacity and failover

A peer at `mesh.max_concurrent_tasks` (default `8`, `0` = unlimited)
rejects new submissions with a `capacity:` error; the caller skips the
rest of that peer's retry budget and fails over to the next ranked
candidate. Per-peer caps live in `mesh.acl[].max_concurrent` (`0` =
global only, negative = exempt). Resubmits with the same correlation id
bypass the cap — they're the same task, not new work.

Attachments ride `/rhizome/blob/1.0.0`: chunked, SHA-256-verified, and
resumable — a dropped transfer stages to `.partial-<hash>` and the next
attempt sends only the missing tail. Peers that already hold the blob
skip streaming entirely.

