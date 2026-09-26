# Open Agent Work Market — Design (opt-in companion module)

Status: **scheduled.** Experimental build ships in v0.14.0 (fixture rail,
direct-peer discovery); the graduation arc is planned as v0.16.0
(`v0.16.0-sprint.md`, Tracks 123–136). Supersedes the earlier "mesh
economy" seed (the old intra-mesh bilateral-ledger sketch is preserved in
the appendix).

## What it is

A paid market for agent work between *mutually untrusted* operators on the
open internet. A buyer locks payment in an on-chain escrow, opens an **ACP
session** to a seller's node, and the seller runs the task inside a
sandboxed or containerised agent. **Prompts in, results out — buyers never
execute seller code**, and sellers never run buyer-controlled binaries.

Why this shape instead of the libp2p task protocols:

- The intra-mesh economy was rejected: a mesh is one operator's trust
  domain (`mesh.trusted_peers` + `/rhizome/pair/1.0.0`); paying yourself is
  bookkeeping. The market is *between* meshes.
- ACP is already the project's agent-work protocol in both directions
  (`rhizome acp` server; `agents.list[].acp` client bindings). Its
  transport is stdio — which means `docker|podman run -i` yields a
  containerised agent with **zero protocol changes**, and a libp2p stream
  can carry the same ndjson for the remote case.
- The escrow contract makes settlement work between strangers; the shipped
  web3 machinery (wallet, policy, pending-approval, watches) carries it.

## Packaging: opt-in companion module, never in base

**`rhizome-market` is a first-party daemon-kind companion module.** A node
has zero market surface unless the operator runs
`rhizome module install rhizome-market` + `rhizome module enable
rhizome-market`. It ships as separate `rhizome-market_{goos}_{goarch}`
release assets — **not** in the base archives — with catalog-pinned
digests like every other module. Config lives under
`modules.rhizome-market.fields` / `.secrets`: **no market keys in the core
config schema.**

This resolves the old seed's module-vs-subsystem dilemma: a sidecar module
can't reach the libp2p wire today, so the design adds two small *generic*
core seams — inert when no module is installed, useful to any future
wire-speaking module:

1. **Module stream bridge.** `ModuleSpec` gains `protocols: []string`.
   When an enabled module declares protocol ids (market declares
   `/rhizome/acp/1.0.0`), the daemon's libp2p host registers stream
   handlers that splice inbound streams to the module's localhost
   listener; outbound, the module calls a daemon-local bridge endpoint
   (`POST /modules/bridge/dial {peer, protocol}`) to open streams — peer
   identity stays the node's. Loopback-only, guarded by a per-module auth
   token minted at install.
2. **Module capability adverts.** `Capability.ModuleAdverts
   map[string]json.RawMessage` (emit-when-set — the `Allows`/`Role`
   wire-compat rule: absent on old builds, byte-identical re-marshal). An
   enabled module may write `<module_dir>/advert.json`; the daemon merges
   it into the signed capability manifest and sets
   `Allows["market_serve"]=true` the same way.
3. **CLI stub.** `rhizome market …` is a thin core verb calling the
   module's localhost API; without the module it prints "rhizome-market
   module not installed" (the browser-backend/driver and
   daemon-required-posture precedents).

Module internals link first-party packages: `pkg/web3` (shared
`<RHIZOME_HOME>` wallet, `policy.Evaluate`, `web3-pending.json` — every
spend still surfaces in the existing CLI/UI approval flow), `pkg/isolation`,
`pkg/acp` client machinery, `pkg/rhizome/stream` framing, and
`pkg/rhizome/identity` (reads `node.json` to sign receipts as the node
peer id — same trust level as the daemon; installing the module is a real
trust decision, mitigated by digest-pinned signed-catalog delivery).

## Substrate that already exists (verified v0.13.0-era)

- Signed Ed25519 capability manifests — authenticated advert channel;
  emit-when-set fields keep wire compat.
- libp2p Noise/TLS transports — encrypted, peer-authenticated streams; no
  plaintext path.
- Signed `agenttask`/`agentrpc` envelopes + nonce/timestamp replay guard —
  the signing pattern receipts reuse.
- `PeerScoreStore`, `ActiveTasks`, `rankedCandidates` — local reputation +
  load-aware dispatch substrate.
- `pkg/isolation` — bubblewrap / sandbox-exec / job objects + restricted
  tokens; fs mounts + user-env redirection. **No network isolation today**
  (the darwin profile explicitly allows `network-outbound`; bwrap does not
  `--unshare-net`) — a net knob is required new work.
- `pkg/acp` — server + client; `m.dial` is an injectable spawn seam; the
  SDK transport is plain `io.Reader`/`io.Writer`; `ExtensionMethodHandler`
  supports `_`-prefixed methods.
- `pkg/web3` — wallet (`keys.json` under KeyProvider), `policy.Evaluate`,
  pending-approval queue, `web3_contract_send`, ERC-20/721 helpers, ENS,
  `eth_getLogs` watches.
- `pkg/modules` — catalog, digest-pinned install, daemon supervision,
  `modules.<id>.fields/secrets`, health checks, signed remote index.
- `mesh-tasks.jsonl` + `mesh-audit.jsonl` — accounting-grade records.
- **Track 94 (v0.13.0)** — `RemoteUsage` + `want_usage`/`usage`
  negotiation: the internal-observability primitive.

## Stage A — usage metering (Track 94; core; unchanged)

Ships in v0.13.0 as planned: `RemoteUsage`, `UsageSink`,
`want_usage`/`usage` on `agentrpc`/`agenttask` negotiated via
`Allows["usage_report"]`. Powers the internal stats dashboard; its shape
is reused as the receipt's usage schema. Honest caveat: market sessions
run *external ACP agents*, which the pipeline cannot meter (Track 94
reports nil) — market receipts therefore meter **session duration plus
agent self-reported tokens** where the agent answers `_rhizome.usage`.

## Stage B — hardened execution rail (generic core seams; no money)

**B1. ACP runtime modes (core; general hardening, off by default).**
`agents.list[].acp.runtime = exec | sandbox | container`, per-binding
override of `acp.client.runtime`:

- `exec` — today's plain `exec.Command` (unchanged default).
- `sandbox` — spawn under `pkg/isolation` plus a new **network-isolation
  knob**: bwrap `--unshare-net` (or slirp allowlist), a sandbox-exec
  profile without `network-outbound`, a Windows job-object/firewall rule.
  Filesystem exposure = a scratch instance dir only.
- `container` — spawn `docker|podman run -i --rm` (stdio pipes are the ACP
  transport); `acp.client.container = {engine:auto, image,
  network:none|bridge|allowlist, mem_mb, cpus, pull:missing|never}`.

Justified in core independently of the market: external ACP agents are
unsandboxed today by design (`client.go` trust-posture comment).

**B2. Module stream bridge (core seam; described above).**

**B3. Receipts as ACP extension methods (module-side).**
`_rhizome.receipt` via the SDK's `ExtensionMethodHandler`:

```json
{"session_id":"0x…","offer_id":"research-v1","duration_ms":41200,
 "usage":{"llm_calls":3,"prompt_tokens":18230,"completion_tokens":2410},
 "result_sha256":"…","terms":{"price":"…","asset":"USDC","chain_id":8453},
 "signature":"…"}
```

Signed with the seller's node key; verifiable against the peer id. This is
the escrow release payload.

## Stage C — market layer (all inside `rhizome-market`)

**C1. Offers + adverts (module fields).**

```
modules.rhizome-market.fields:
  serve_enabled=false
  offers=[{id, agent_binding, price_sheet{per_task,
          per_1k_prompt_tokens, per_1k_completion_tokens, min_charge,
          asset, chain_id}}]
  runtime=sandbox|container
  payout={chain_id, address, asset}
  max_concurrent_sessions, session_ttl
  buy_max_cost_per_task, buy_max_cost_per_day
  export={allow_attachments:false, redact:true, require_review:prompt}
  market_index={enabled:false, url}
  escrow={chain_id, contract, token, arbiter, dispute_window}
```

The module writes `advert.json` (offers + price sheet + payout + runtime
posture — deliberately public data); the daemon merges it into the
manifest per B2.

**C2. Discovery: signed provider index + DHT.** Clone the module-catalog
architecture (`pkg/modules/remote.go`): `<url>/index.json` + `.sig`
(Ed25519, curator key) listing
`{peer_id, addrs, offers, payout, runtime_posture, manifest_sig}`.
The curated index is the primary channel — it carries the Sybil/spam
resistance raw DHT lacks. Optional fallback: DHT rendezvous
`rhizome-market-v1` through the existing `Discovery` machinery, explicitly
marked "unvetted" tier.

**C3. Escrow contract + session lifecycle.**

Minimal contract (write-vs-adopt is an open question; ~200 lines is
realistic):

```
open(sessionId, seller, token, amount, taskHash, disputeWindow)  // locks ERC-20
release(sessionId)        // buyer → pays seller
claim(sessionId)          // seller → pays out after dispute window lapses
dispute(sessionId)        // buyer → freezes, within window
resolve(sessionId, sellerBps) // arbiter → splits
```

- Chain/asset: cheap L2 + stablecoin (USDC on Base/Arbitrum; Sepolia
  first — the testnet-first web3 posture).
- Arbiter v1 = designated address (curator/multisig). Arbitration
  governance is a documented open question, not solved here.
- Flow: `market buy` → ERC-20 `approve` + `open` via `web3_contract_send`
  + pending-approval → session id → buyer's module opens `/rhizome/acp`
  via the daemon bridge, presenting `{session_id, task_hash, offer_id}`
  → seller module `eth_call`-verifies the lock and terms **before burning
  any tokens** → ACP session runs (metered) → `_rhizome.receipt` → buyer
  `release`, or seller `claim` after the window → `dispute` freezes for
  the arbiter.
- The module polls `eth_getLogs` (the `tools.web3.watches` pattern) to
  auto-verify sessions and notice disputes.
- Caps: existing web3 policy (`max_value_wei_per_day`, `allow_contracts`
  /`allow_methods` `label:method` entries) + buyer `max_cost_*` fields.

**C4. Buy-side UX.** `rhizome market find <service>` (index query),
`market buy <provider> <offer> <task> [--max-cost]`,
`market session <id>` (status), `market receipt <id>` (verify signed
receipt). Outbound context passes through the export policy: task text
only, attachments/media off by default, `pkg/redact` pass, per-session
spend approval through the existing pending queue.

**C5. Sell-side serving profile.** Market sessions run under a dedicated
posture inside the module: `permission_policy=deny` forced, no
fs/terminal capabilities advertised, the offer's agent spawned under the
configured `runtime`, separate audit partition (`market-audit.jsonl`
under the module dir), per-peer rate limits + global cap — and **no work
until the escrow lock verifies on-chain**, so unpaid prompts cannot drain
LLM budget. Container egress = `none` or a model-endpoint allowlist,
which also bounds prompt exfiltration (protects buyer context from
seller-side leaks, not just the seller's host).

## Stage D — horizon (documented, not designed)

ACP-over-HTTPS transport for non-Rhizome counterparties; batched /
session-drawdown settlement; decentralized arbitration; TEE/attestation
for execution claims; portable signed reputation.

## Threat model

| Threat | Bound |
|---|---|
| Malicious prompt → seller host | Container/sandbox: no host fs, no secrets, egress none/allowlist, cpu/mem caps, killed at session end |
| Buyer prompt leaks via seller | Seller sandbox posture is **self-attested, unverifiable** — mitigations are export minimization, curated index, reputation. Rule: don't send what you can't afford to leak |
| Non-payment / non-delivery | Escrow: funds locked pre-work; auto-claim window protects sellers; dispute+arbiter for contested work |
| Inbound DoS | Sessions require a verified escrow lock before any LLM spend; rate limits + concurrent-session cap |
| Sybil adverts | Signed index curation; DHT tier marked unvetted |
| Work-quality unverifiability | Not cryptographically solvable — small first tasks, reputation laddering (`PeerScoreStore` + market outcome fields), redundancy (`FanoutTask` to N sellers, compare), dispute window |
| Provider ToS on resold inference | Market is service-neutral; docs recommend local-model-backed offers (llama.cpp module) for unambiguous resale |
| Module = elevated trust | First-party module shares RHIZOME_HOME, node identity, wallet — install is a real trust decision; digest-pinned signed delivery |

## Privacy

- Outbound: export policy — task text only by default, no attachments,
  `pkg/redact` pass, review-before-send.
- Inbound: quarantined profile + separate audit partition; foreign
  prompts never touch workspace or credentials.
- On-chain: only `taskHash` commitments; plaintext stays off-chain. The
  payment graph is public — aggregate settlement is the documented
  mitigation.
- Discovery: providing an index/DHT record is a public "for hire"
  announcement; buyer queries leak interest to the index/DHT —
  documented, not fixed.
- Identity: peer id is a stable pseudonym correlating adverts, sessions,
  and payments; rotation burns reputation — trade-off documented.

## Proposed track sequence (for the sprint that picks this up)

1. Core seams: `acp.runtime` modes + net-isolation knob; module stream
   bridge; `ModuleAdverts` merge; `rhizome market` CLI stub.
2. `rhizome-market` skeleton: catalog entry, daemon-kind supervision,
   localhost API, `advert.json` writer.
3. Sell-side: session manager + escrow verification + containerised
   serving + receipts.
4. Buy-side: index client + `market buy` + export policy + release flow.
5. Escrow contract: implement/select, deploy testnet, E2E.
6. Index curation + DHT tier + docs + release.

## Open questions

- Escrow: write the minimal contract vs adopt an existing one; arbiter
  model; which L2 + asset.
- Does `market_serve` require a container/sandbox runtime or merely
  advertise posture? (Recommend: advertise; buyers price risk.)
- Provider-index curation: who signs; one index vs federated.
- Session granularity: per-task escrow (v1) vs funded session draw-down.
- Market module distribution: embedded catalog pins vs signed remote
  index entry.
- Whether the stream bridge should also serve trusted-peer remote ACP
  (`agents.list[].acp.remote`) as a free byproduct — likely yes; decide
  at pick-up.

---

## Appendix — superseded seed: intra-mesh bilateral-ledger economy

Preserved for reference. If the market design is not picked up, this is
still the right shape for *inter-operator* settlement between paired
meshes (two different operators who paired — ledger as
invoice/reconciliation); it is not an economy between a single operator's
own nodes.

- `mesh.economy` config: `enabled, unit ("credits"), price_sheet
  {per_task, per_1k_prompt_tokens, per_1k_completion_tokens, per_second,
  min_charge}, accept_units, max_cost_per_task/day, settle_threshold`.
- `Capability.Economy` advert (emit-when-set): unit, sheet, optional
  `payout {rail, chain_id, address, asset}`.
- Charge = `min(sheet·usage, max_cost)` on success only; failures → score
  penalty + dispute path. Pre-check `max_cost ≥ min_charge` →
  `price_floor:`.
- Receipts: `UsageReport` + `charge` + `result_sha256`, signed inside the
  task response; bilateral `economy-ledger.jsonl` (payable/receivable/
  settlements/disputes; rotate ~10 MB × 3).
- `SettlementRail` iface — `ledger` (mutual credit, off-band close-out)
  default; `web3` rail via the v0.12.0 wallet/policy/pending machinery,
  paying the peer's manifest-signed payout address.
- Dispatch: `est_cost` filter+rank term in `rankedCandidates`;
  `--max-cost` on route/delegate/scatter; `swarm offer --max-price` +
  `Claim.Quote` (additive-safe).
