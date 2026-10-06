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

## v0.16.0 Track 123 sweep (2026-10-06)

**Sync point**: upstream `main` tip is still `bbf6893c` (2026-08-19) —
`git rev-list bbf6893c..upstream/main` returns 0. **No new upstream commits
to cherry-pick**; upstream merges remain dormant (latest merged PR is still
#3286, 2026-07-23). Note: the Track 112 section was written on
`devin/track-112-upstream-sweep` (PR #68), a sibling branch off `main` —
this sweep's record continues on the release line.

**Picked** (all four were closed-unmerged fix PRs under tracking issue
#3404 "reliability fixes wave 1"; ported adapted, same posture as the
Track 112 port of #3412):

| Item | What | Action |
|---|---|---|
| PR #3402 | Context managers resolved `GetDefaultAgent()` instead of the session's owning agent — routed agents' sessions assembled empty context; seahorse bootstrap imported only the default agent's store | Ported — `agentForSession` at all three `context_legacy.go` sites; seahorse bootstrap iterates every registered agent's store; `bootstrapSession` takes the store as a param |
| PR #3401 | `Manager.Reload` ran channel register/unregister in a detached goroutine racing `initChannels`, plus nil-channel derefs when a factory failed | Ported — `asyncTask` carries its ctx; removals go through new `removeChannelLocked` before `initChannels`; HTTP handler registration is synchronous; the deferred `RegisterChannel` re-write is gone |
| PR #3403 | Async tool results always routed to the default agent's main session — cross-chat mixing + parallel-turn races | Ported — `turnState.originSessionKey` walks to the root turn; follow-ups carry `SessionKey`; `processSystemMessage` resolves the named session/agent and claims the session (busy → steering enqueue) |
| PR #3399 | Updater arch match was substring-based (`"arm"` ⊂ `"arm64"`) and scanned assets before aliases — wrong asset picked for 32-bit ARM | Ported — suffix-aware `assetMatchesArch`, alias-preference loop order, GOARM-aware `arm` aliases via `debug.ReadBuildInfo`; `i386` synonyms added |

**New issues filed since the last sweep** (2026-09-30 → 2026-10-06):

| Item | What | Verdict |
|---|---|---|
| Issue #3415 | Feature: reverse-proxy/nginx support behind the gateway | Deferred — feature, not a fix |

**Watch list** (carried, statuses re-verified this sweep):

| Item | State 2026-10-06 |
|---|---|
| PR #3381 (OpenAI → Responses API) | Still **open**, unmerged — keep waiting |
| `dingtalk-stream-sdk-go` fix for #3382 | Still **blocked** — latest tag remains `v0.9.2-beta.1`; upstream closed #3382 as stale (NOT_PLANNED), not fixed — restore posture unchanged |
| PR #3412 (turn-failure notice) | Still **open** — ported adapted in Track 112; drop from watch if upstream merges |
| PR #3410 (steering-queue feedback) | Still **open** — held pending upstream merge (Track 112 decision) |
| PR #3371 (opencode-go) | Still open — ported adapted in Track 108; keep watching for follow-ups |
| Feature-PR sign-off batch | #3370, #3259, #3222, #1951 still open — queued v0.20.0+; **#3354 and #3368 closed unmerged** — dropped from the batch |
| Issues #3404 | Open; its linked fix PRs #3399–#3403 all closed unmerged — four ported this sweep (#3400 duplicates our shipped #3373 fix) |
| Issues #3394/#3407/#3408 | Still open — Verify verdicts carry (QQ API drift; web ghost-session; web busy-queue drop) |

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
