# Mesh Economy — Deferred Module Seed

Status: **deferred, not scheduled.** Recorded during v0.12.0 sprint planning
when the mesh-economy arc was descoped in favor of agentic web3. This page is
a seed for whoever picks it up — not a commitment.

## What it is

An opt-in economy between *mutually trusted* mesh peers: price remote work,
record signed receipts, keep a bilateral ledger, settle periodically.
Explicitly **not** a trustless marketplace — trust boundary is the existing
mesh trust set + ACL; receipts are evidence, disputes are operator-level.

## Why a module / opt-in, not core

- The economy is a policy surface (pricing, settlement, disputes) layered on
  mesh plumbing — keeping it out of `pkg/rhizome/mesh` core paths keeps the
  default mesh unchanged and lets the idea iterate without release risk.
- A config-gated subsystem (`mesh.economy.*`) or a companion module
  (`pkg/modules` daemon kind) are both viable shapes — decide at pick-up:
  a sidecar module can't reach the mesh wire today (manifests, task
  protocol), so module-shape needs a daemon seam, while config-gated
  subsystem reaches everything but lands in core. Lean toward
  config-gated subsystem *if* the wire integration stays emit-when-set.

## Substrate that already exists (verified v0.11.0-era)

- Signed Ed25519 capability manifests — an authenticated advert channel;
  emit-when-set fields (`role` precedent) keep wire compat.
- Signed `agenttask`/`agentrpc` envelopes — a signed invoice channel.
- `PeerScoreStore`, `ActiveTasks`, `rankedCandidates` — reputation +
  load-aware dispatch substrate.
- Swarm offer/claim queue (`OfferRequirements`, `Claim.CapDigest/
  ActiveTasks`) — already a market mechanism; `Offer.MaxPrice`/
  `Claim.Quote` are additive-safe (envelope signs raw payload bytes).
- `mesh-tasks.jsonl` + `mesh-audit.jsonl` — accounting-grade records.

## The missing primitive: metering

Remote task responses carry **no usage report** — `UsageInfo` exists per
LLM call (`pipeline_llm.go`, `turnState.SetLastUsage`) but is never
aggregated or returned to the caller. That gap matters even with no
billing: observability of what remote work costs. Sketch:

- Turn-level accumulator beside `SetLastUsage`; return via a
  `UsageSink *RemoteUsage` on `processOptions.Dispatch` (mirrors
  `MediaSink`; ACP external agents report nil — can't meter).
- Wire: `want_usage`/`usage` on `agenttask`+`agentrpc`. **Wire-compat
  rule** (verified): request/response signatures cover the *re-marshaled*
  struct — new populated fields break old-peer verification, so new fields
  are emit-only-when-negotiated via the capability advert (the `role`
  rule). Swarm payloads are additive-safe.

## Sketch (for the future design doc)

- `mesh.economy` config: `enabled, unit ("credits"), price_sheet
  {per_task, per_1k_prompt_tokens, per_1k_completion_tokens, per_second,
  min_charge}, accept_units, max_cost_per_task/day, settle_threshold`.
- `Capability.Economy` advert (emit-when-set): unit, sheet, optional
  `payout {rail, chain_id, address, asset}`.
- Charge = `min(sheet·usage, max_cost)` on success only; failures → score
  penalty + dispute path. Pre-check `max_cost ≥ min_charge` → `price_floor:`.
- Receipts: `UsageReport` + `charge` + `result_sha256`, signed inside the
  task response; bilateral `economy-ledger.jsonl` (payable/receivable/
  settlements/disputes; rotate ~10 MB × 3).
- `SettlementRail` iface — `ledger` (mutual credit, off-band close-out)
  default; `web3` rail via the v0.12.0 wallet/policy/pending machinery,
  paying the peer's manifest-signed payout address.
- Dispatch: `est_cost` filter+rank term in `rankedCandidates`;
  `--max-cost` on route/delegate/scatter; `swarm offer --max-price` +
  `Claim.Quote`.

## Open questions when picked up

- Currency semantics: abstract `unit` labels vs concrete assets per rail.
- ~~Whether metering lands standalone first (observability) or with the
  economy track.~~ Resolved: metering lands standalone as **v0.13.0
  Track 94** (observability-only `want_usage`/`usage` negotiated via the
  `Allows["usage_report"]` advert); this module's economy work builds on it.
- Module-with-daemon-seam vs config-gated subsystem (see above).
- Task/cost-aware dispatch and swarm settlement scope (earlier "Stage 3"
  notes folded here).
