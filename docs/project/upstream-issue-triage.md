# Upstream Issue Triage

Rhizome is a hard fork of [PicoClaw](https://github.com/sipeed/picoclaw) and has
no GitHub issue tracker of its own — issues are disabled on
`stpinkie/rhizome`, and work is tracked in `.todo.md`. The upstream tracker,
however, contains reports filed against code that Rhizome inherited. This
document records the triage of those issues: which still apply to Rhizome,
which were already shipped here, and which are out of scope.

Issue numbers below refer to `sipeed/picoclaw` issues. Links of the form
`stpinkie/rhizome/issues/NNN` in `ROADMAP.md` are rebrand-rewrites of these
upstream numbers and do not resolve to a live Rhizome tracker.

## v0.15.0 Track 112 sweep (2026-10-06)

**Sync point**: upstream `main` tip is still `bbf6893c` (2026-08-19) —
`git rev-list bbf6893c..upstream/main` returns 0. **No new upstream commits
to cherry-pick**; upstream merges remain dormant (latest merged PR is #3286,
2026-07-23). All activity since the Track-148 sweep lives in open PRs and
issues.

**Ported adapted** (open fix PR applied to shared code — the #3378 precedent):

| Item | What | Action |
|---|---|---|
| PR #3412 | `fix(agent)`: a dead turn's error notice never reached the user — suppressed by the `message` tool's already-sent heuristic, killed by the already-canceled teardown context, and `PublishOutbound` errors discarded silently | **Ported adapted** — `publishTurnFailureNotice` + `publishResponse` options (`skipMessageToolSuppression`, `surviveCanceledContext`) in `pkg/agent/agent_outbound.go`; panic-path notice in `agent.go`; `constants.IsInternalChannel` skip; 10 s bounded publish + warn-on-failure; `agent_outbound_test.go` ports the 10-case suite |

**Held — applicable fix, upstream in flight**:

| Item | What | Verdict |
|---|---|---|
| PR #3410 | `fix(pico/web)`: steering-queue feedback — queued/dropped inbound messages surfaced via `agent.interrupt.received` attrs (`steering_result`, `queue_depth`) | **Watch** — backend half touches shared code (`pkg/agent/steering.go`, `pkg/channels/pico`, `pkg/channels/runtime_events.go`); the web half diverged post-fork (our chat path is WebSocket-based). Part of the #3404 reliability wave still in flight — port adapted when upstream settles or the wave lands |
| Issue #3408 | The queued-message silent-drop issue Track 148 marked *Verify* | Upgraded: a real fix exists upstream (PR #3410) — applicability confirmed for `pkg/channels/pico` + `pkg/agent` steering queue; held with the PR above |
| Issue #3407 | Ghost session while the model is still thinking | Still **Verify** — no upstream fix has appeared; our session handling diverged |

**New issues filed since the last sweep** (2026-09-30 → 2026-10-06; #3409
filed 2026-09-29 inside Track 148's window but absent from its table):

| Item | What | Verdict |
|---|---|---|
| Issue #3409 | `ScheduleWakeup` used as a wait mechanism fires an unwanted autonomous-loop tick | **Not applicable** — no wakeup/scheduling or autonomous-loop primitive exists in Rhizome (verified: nothing under `pkg/` matches) |
| Issue #3415 | Reverse-proxy subpath mounting (nginx `/pico`) | **Deferred** — feature request, not a fix |

**New open PRs since the last sweep** (feature PRs — sign-off batch
candidates; none are fixes to port):

| Item | What | Verdict |
|---|---|---|
| PR #3416 | Sendblue iMessage/SMS channel | Feature — sign-off batch |
| PR #3414 | Wall-clock turn time budget | Feature — sign-off batch |
| PR #3413 | Web global multi-channel session sidebar | Feature — sign-off batch |
| PR #3411 | Honest state-driven working indicator (answers #3406) | Feature — sign-off batch |

**Watch list** (carried, statuses re-verified this sweep):

| Item | State 2026-10-06 |
|---|---|
| PR #3381 (OpenAI → Responses API) | Still **open**, unmerged — keep waiting; an unmerged wire-protocol switch stays unportable |
| `dingtalk-stream-sdk-go` fix for #3382 | Still **blocked** — latest tag remains `v0.9.2-beta.1` (pre-release); issue #3382 open; restore waits on a stable release |
| PR #3371 (opencode-go) | Still open — ported adapted in Track 108; keep watching for follow-ups |
| Feature-PR sign-off batch | **Changed**: #3354 (IRCv3 multiline) closed unmerged 2026-10-05; #3368 (docs MCP) closed unmerged 2026-10-02; #3370/#3259/#3222/#1951 still open; new candidates #3411/#3413/#3414/#3416 |
| Issue #3404 (reliability wave) | Active — children #3410/#3411/#3412 filed 09-29…09-30; #3412 ported this sweep, #3410 held, #3411 feature |
| Issues #3394 (QQ API drift), #3407 (ghost session) | Still verify — unchanged from Track 148 |
| Dependabot PRs (#3385–#3389 class) | No action — our own `dependabot.yml` proposes the same bumps |

## v0.18.0 Track 148 sweep (2026-09-30)

**Sync point**: upstream `main` tip is still `bbf6893c` (2026-08-19) —
`git rev-list bbf6893c..upstream/main` returns 0. **No new upstream commits
to cherry-pick**; upstream merges are dormant (latest merged PR is #3286,
2026-07-23). This is the first executed sweep since Track 108 — the
v0.15.0–v0.17.0 sweeps were planned but their sprints have not run.

**Picked**: none — nothing to pick.

**New issues filed since the last sweep** (2026-09-25 → 2026-09-30):

| Item | What | Verdict |
|---|---|---|
| Issue #3408 | Web UI queues sends while the agent is busy, then silently drops them when the queue fills; asks for a queue/events surface | **Verify** — Rhizome's chat path is WebSocket-based (`web/frontend/src/features/chat/websocket.ts`) and diverged post-fork; no `queuedMessages`/`sendQueue` surface found, but busy-turn drop behavior is unconfirmed |
| Issue #3407 | Web UI session vanishes from the list while the model is still thinking ("ghost session") | **Verify** — same caveat: rewritten web session handling; not confirmed applicable |
| Issue #3404 | "Reliability fixes with reproducers (wave 1)" tracking issue | **Watch** — check for linked fix PRs next sweep; port applicable reproducer fixes |
| Issue #3394 | QQ channel: upstream reports QQ's API drifted and the channel no longer works | **Verify** — `pkg/channels/qq` is inherited code; check against current QQ bot API before next release |
| Issue #3406 | Feature: clearer working indicator, session archiving | Deferred — feature, not a fix |
| Issue #3397 | Feature: Tsubasa OpenAI-compatible provider preset | Deferred — `openai-compatible` preset already covers the mechanism |
| Issue #3395 | Feature: OneBot `reaction_enabled` setting | Deferred — feature, not a fix |
| Issue #3398 | Fork maintenance notice | Not applicable — informational |
| Issue #3405 | Private vulnerability reporting request | Not applicable — upstream repo process, not Rhizome code |
| Issue #3392 | CLAassistant signature detection | Not applicable — upstream CI plumbing |

**Watch list** (carried, statuses re-verified this sweep):

| Item | State 2026-09-30 |
|---|---|
| PR #3381 (OpenAI → Responses API) | Still **open**, unmerged — keep waiting; an unmerged wire-protocol switch stays unportable |
| `dingtalk-stream-sdk-go` fix for #3382 | Still **blocked** — latest tag remains `v0.9.2-beta.1` (unverifiable beta); issue #3382 open; restore waits on a stable release |
| PR #3371 (opencode-go) | Still open — ported adapted in Track 108; keep watching for follow-ups |
| Feature-PR sign-off batch (#3370, #3354, #3368, #3259, #3222, #1951) | All still open — batch stays queued v0.20.0+ |
| Issues #3404/#3394/#3407/#3408 | New watch entries — see verdicts above |

## v0.14.0 Track 108 sweep (2026-09-25)

**Sync point**: upstream `main` tip is still `bbf6893c` (2026-08-19) — the GitHub
compare API reports `ahead_by: 0` for `bbf6893c...main`. There are **no new
upstream commits** to cherry-pick; all upstream activity since the sync lives in
open PRs and issues. The next sweep can start from this state.

**Picked**:

| Item | What | Action |
|---|---|---|
| PR #3378 | `RefreshAccessToken` sent hardcoded `openid profile email` instead of `cfg.Scopes` | Ported — `pkg/auth/oauth.go` now sends `cfg.Scopes`; `TestRefreshAccessToken` asserts the configured scope reaches the wire |

**Removed**:

| Item | What | Action |
|---|---|---|
| Issue #3382 | DingTalk channel panics with `send on closed channel` inside `dingtalk-stream-sdk-go v0.9.1`'s own `processLoop` goroutine — unrecoverable from Rhizome's `Start()`; only candidate fix is the unverifiable `v0.9.2-beta.1` | DingTalk channel implementation removed pending a stable upstream SDK fix. `ChannelDingTalk`/`DingTalkSettings` stay registered so existing `channel_list.dingtalk` configs still validate; `pkg/channels/dingtalk` is a stub whose factory fails with a clear error. Implementation + `dingtalk-stream-sdk-go` dep + UI pickers + docs deleted |

**Already shipped / not applicable / out of scope** (verified against our tree):

| Item | Verdict |
|---|---|
| PR #3353 (bound tool-feedback animations) | Already shipped v0.9.0 — `ChannelToolFeedbackMaxDuration` + consecutive-error abort + per-edit timeout |
| PR #3347 (laggy web UI) | Already shipped v0.9.0 — `React.memo` on the same chat components |
| PR #3376 (deltachat `RegisterChannelSettings`) | Not applicable — `ChannelDeltaChat` is already in our static `channelSettingsFactory` (`config_channel.go`) |
| Issue #3391 (pico multi-line input split) | Out of scope — lives in the upstream pico mobile-TUI *client* app, not this repo |
| Dependabot PRs #3385–#3389 | No action — Rhizome's own `.github/dependabot.yml` proposes the same bumps |

**Watch list** (revisit next sweep):

| Item | Trigger |
|---|---|
| PR #3381 (switch OpenAI provider to Responses API) | Port only after upstream merges — unmerged default-path wire-protocol switch we cannot verify against the live API |
| `dingtalk-stream-sdk-go` ≥ stable release fixing #3382 | Restore the DingTalk channel (config type retained, so restoration is a re-add of `pkg/channels/dingtalk` impl + dep + UI/docs) |
| PR #3371 (opencode-go provider) | Ported this track (adapted — see Track 108 notes); track upstream for follow-ups |
| Feature PRs #3370 (Keenable search), #3354 (IRCv3 multiline), #3368/#3259 (docs), #3222 (deltachat refactor), #1951 (scripts) | Fixes-only policy — flagged for future sign-off |

## Fixed in this release (v0.9.0)

| Issue | Defect | Fix |
|---|---|---|
| #3373 | `SaveConfig` dropped every `api_key` after the first on a load→save round trip (credential loss, incl. via `rhizome onboard`) | `collapseMultiKeyModels` folds expanded `__key_i` virtual entries back into their primary before save; orphaned virtuals demote rather than drop |
| #3374 | `initSensitiveCache` lazy-init race returned a nil `*strings.Replacer` → panic in `FilterSensitiveData` under concurrent turns | Guarded first-use initialization; `SensitiveDataReplacer()` can no longer return nil |
| #3343 | Telegram tool-feedback animator could edit a message forever after a failed turn | `Channel.ToolFeedbackMaxDuration` bound (default 10m) + consecutive-edit-error abort + self-detach on exit |
| #3365, #3349 | QQ channel 401 — botgo v0.2.1 + resty ≥2.17 emits `Authorization: Bearer` instead of `QQBot` | botgo `RegisterReqFilter` hook rewrites the scheme on outbound requests (`pkg/channels/qq/auth_filter.go`) |
| #3351 | Session compaction physically deleted original history (`rewriteJSONL` overwrite) | Active history is archived before every rewrite (`SetHistory`, `Compact`, `TruncateHistory`) in `pkg/memory/jsonl.go` |
| #3350, #3281 | Web chat input lag — composer `input` state lived in `ChatPage`, re-rendering the whole history per keystroke | Draft state moved into `ChatComposer`; `AssistantMessage`/`UserMessage` wrapped in `React.memo` |
| #3355 | `channel_list.<name>.<setting>` flat keys rejected by strict unknown-field validation and silently dropped | `Channel.UnmarshalJSON`/`UnmarshalYAML` fold non-base keys into `Settings` (nested `settings` wins); diagnostics exempt flat channel keys |

## Feature requests — folded into v0.9.0 tracks

| Issue | Ask | Track |
|---|---|---|
| #348 | Attachment support | Track 55 — `SendMedia` coverage for whatsapp/whatsapp_native/vk/dingtalk + inbound audit |
| #350 | Interactive CLI onboarding wizard | Track 56 — guided `rhizome onboard` + web `/setup` |
| #294 | Multi-agent shared context | Track 57 — swarm blackboard |
| #296 | Consistent agent identity (AIEOS) | Track 58 — signed agent manifests |
| #3369 | `x-opencode-session` header for OpenCode Go | Track 59 — opt-in session-header mapping |
| #3366 | Named "OpenAI compatible" provider | Track 59 — provider preset |
| #3287 | IRC long-message handling | Track 59 — message splitting |

## Already shipped in Rhizome (no action)

| Issue | Shipped as |
|---|---|
| #293 (browser automation) | `pkg/browser` + `browser_*` tools (v0.7.1, CDP in v0.8.0) |
| #284 (swarm mode) | `pkg/rhizome/swarm` (v0.7.0) |
| #295 (model routing) | `pkg/routing` |
| #806 (web UI) | Launcher dashboard |

## Out of scope / blocked

| Issue | Reason |
|---|---|
| #3377 | `picoclaw.io` TLS certificate — upstream's domain infrastructure, not Rhizome code |
| #463 | Logo/branding — no art pipeline |
| #292 | Android automation — no test hardware |
| #2181 | Lichee RV / Nezha CM support — hardware portability question |
| #346 | Memory footprint — deferred, not a defect |
| #772, #988 | Roadmap/refactor meta issues — superseded by `.todo.md` tracks |
