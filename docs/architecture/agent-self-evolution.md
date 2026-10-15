# Agent Self-Evolution

Agent self-evolution lets Rhizome learn from completed turns and turn repeated successful behavior into skill improvements. The runtime is controlled by the top-level `evolution` config block.

## Flow

The hot path runs at the end of an agent turn. When `evolution.enabled` is true, it records a learning record with the turn summary, success state, used skills, tool executions, and session/workspace metadata. Heartbeat turns are skipped.

The cold path groups related task records, checks the configured success threshold, and prepares skill drafts for patterns that have enough evidence. Drafts can target new skills or append/replace/merge existing workspace skills.

The apply path validates generated `SKILL.md` content before writing. Invalid drafts are rejected before a skill directory or file is created.

## Safety Considerations

Evolution creates a persistent feedback loop: user input can become a task record, task records can be clustered into an LLM-generated draft, and an accepted draft can become `SKILL.md` content that is loaded into future agent prompts. Treat generated skill content as prompt-sensitive material, especially in `apply` mode.

The review scanner is a guardrail, not a complete safety boundary. It reuses `pkg/guard`'s prompt-injection patterns over `body_or_patch`, `human_summary`, and the structured draft fields, and `pkg/redact`'s secret-pattern set for credential-shaped substrings; findings land in `scan_findings` prefixed `guard:`/`secret:`/`structure:`/`size:`. `guard:`/`secret:` findings quarantine the draft while `evolution.review_auto_quarantine` is on (default); `structure:` findings are recorded as notes and `size:` (over `max_draft_bytes`, default 64 KiB) always quarantines. Detection covers obvious attempts, not adversarial evasion — use `observe` or `draft` when human review is required before skill changes reach disk.

In `apply` mode, accepted drafts can update workspace skills automatically. Existing skills are backed up before replacement — a timestamped pre-apply snapshot under the evolution `backups/` directory plus a per-version snapshot under `versions/` — and `evolution.backup_keep` (default 5) bounds both sets per skill. `rhizome evolution rollback <skill>` restores the newest pre-apply backup; `--to <version>` restores a named version's snapshot; `--dry-run` reports what would be restored without touching disk. The restored content passes through the same `validateAppliedSkillBody` gate as a fresh apply, the current skill is snapshotted before replacement so rollback stays undoable, and a `SkillVersionEntry` with `Rollback: true` records the operation in the skill's version history (`rhizome evolution skills history <name>`). When the requested snapshot has been pruned, the error names the archived path it would have lived at.

## Modes

| Mode | Behavior |
|------|----------|
| `observe` | Record learning data only. No cold-path draft generation runs automatically. |
| `draft` | Record learning data and generate candidate skill drafts when the cold path runs. |
| `apply` | Generate drafts and allow accepted drafts to update workspace skills. |

When `evolution.enabled` is false, `mode` is treated as disabled at runtime.

## Cold Path Trigger

`cold_path_trigger` only matters in `draft` and `apply` modes.

| Trigger | Behavior |
|---------|----------|
| `after_turn` | Run the cold path after eligible turns. |
| `scheduled` | Run the cold path at configured `cold_path_times`. |
| `manual` | Do not run automatically. There is no user-facing Web/API/CLI trigger yet; code can still invoke `Runtime.RunColdPathOnce`. |

`cold_path_times` uses `HH:MM` strings and is ignored unless the trigger is `scheduled`.

## State

By default, evolution state is stored under the workspace. `state_dir` can redirect that state to another directory. The state includes learning records, clustered pattern records, drafts, and skill profiles.

For user-facing configuration fields, see the [Configuration Guide](../guides/configuration.md#agent-self-evolution).
