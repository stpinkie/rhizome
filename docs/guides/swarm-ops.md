# Swarm Operations

> Back to [README](../README.md)

Swarms are decentralized work groups over the Rhizome mesh: trusted peers
join a shared roster, publish task offers to a distributed queue, claim
each other's work, and coordinate through a deterministically elected
coordinator. All `swarm` verbs below read live state from the running
daemon unless noted.

## Membership & roster

```bash
rhizome swarm join ops          # persist membership (applies on next daemon start)
rhizome swarm leave ops
rhizome swarm list              # configured memberships + saved roster
rhizome swarm members ops       # roster with join state
rhizome swarm status ops        # roster + live coordination freshness
```

`swarm status` adds the coordination epoch age and `last_state_write` —
how stale the coordinator's shared state is — when the daemon answers.

## The offer queue

```bash
rhizome swarm offer ops researcher "summarize RFC 9000" \
    --require-skill pdf --require-model qwen3
rhizome swarm offers ops                 # track open offers
rhizome swarm offers ops --cancel <id>   # withdraw one
```

Offer lifecycle: `open` → `claimed` (assignee picked) → `done`, plus the
terminal failure states `failed`, `timeout`, `expired` (TTL elapsed
before a claim), and `dead_letter` (retry budget exhausted).

- **Claim picks** are score-weighted: claimant score descending → fewest
  active tasks → claim order. `swarm offers --json` shows the ranked
  claim list with each claimant's resolved score — the pick rationale.
- **`--require-agent` / `--require-model` / `--require-skill`** restrict
  claims to members whose capability manifest advertises them. A member
  that omits the class never satisfies the requirement; peers opt in via
  `mesh.advertise_models` / `mesh.advertise_skills` (and `mesh.skill_share`
  for skills).
- **Restart behavior**: published offers persist to
  `~/.rhizome/swarm-offers.jsonl` (bounded). On daemon start, still-open
  offers re-drive with a fresh claim window (`attempt` incremented);
  offers whose TTL already elapsed land `expired` instead of silently
  reviving.

## Goal runs

```bash
rhizome swarm run ops "draft the ops report"   # decompose + dispatch
rhizome swarm runs ops                          # recorded runs
rhizome swarm run-status ops <run-id>
rhizome swarm run-cancel ops <run-id>
rhizome swarm run-retry ops <run-id>
```

`swarm run` decomposes a goal into dependency-aware subtasks (`depends_on`
DAG waves), publishes each as an offer, and aggregates results into a run
record under `~/.rhizome/swarm-runs.jsonl`. Subtasks carry per-item
`requires`, `tools`, `model`, and `timeout` fields into the offer.

- **`run-cancel`** cancels live subtask offers; the run finalizes as
  `cancelled`.
- **`run-retry`** records a new run linked by `retry_of`: completed
  subtasks carry forward, failed/expired/dead-letter/timeout subtasks
  re-offer once their dependencies are done.
- **Daemon restart** sweeps persisted `running` records to `interrupted`
  (with a `swarm.run.interrupted` event) — nothing resumes silently.

## Coordination

The coordinator is elected deterministically (lowest peer id) and
refreshes shared state to `swarm/<id>/state.json` in the synced workspace
every `swarm.coordination.state_interval` (default 30s).

```json
{
  "swarm": {
    "coordination": { "prefer_connected": true }
  }
}
```

`prefer_connected` (default `false`) excludes currently-disconnected
members from election — trades deterministic whole-roster picks for an
always-live coordinator. Members with asymmetric connectivity can elect
different coordinators, so leave it off unless coordinator liveness beats
consistency for your swarm.

## Diagnostics

```bash
rhizome swarm doctor ops        # per-member roster diff + coordinator health
rhizome swarm trace <offer-id|run-id>
rhizome mesh activity --swarm ops --since 1h
```

`swarm doctor` queries every known member (bounded, 5s each) and reports
asymmetric rosters (members who don't list you), undiscovered members
(you're not in their roster), coordinator reachability, and per-member
epoch drift. The same report serves `GET /network/swarms/<id>/doctor`
(and `/health`). `swarm trace` correlates an offer or run id across the
queue, run records, the activity feed, and the audit tail — see
[Debugging Rhizome](../operations/debug.md#mesh--swarm-observability).
