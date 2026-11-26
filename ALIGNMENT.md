# Rhizome — ALIGNMENT.md

> **Status:** VERIFIED skeleton. Prepared 2026-11-25, verified against the
> repo on 2026-11-26 at `834c005d` (`main` == `archive/pre-alignment` ==
> `v0.17-pre-alignment`). Every classification below was checked against
> the actual tree, git history vs `upstream/main`, and the import graph.
> Bucket assignments are owner decisions; status markers are code facts.
>
> **Identity rule:** this document uses project name/handle only. No personal
> names, ever, anywhere in this repo's files or commits.

**Status marker legend (bucket A only):**
`[EXISTS]` shipped and verified in tree · `[PARTIAL]` exists but incomplete
vs the design · `[NOT BUILT]` no code yet.

## 0. The design sentence

**Rhizome is a private, per-user agent mesh: one human, their devices, their
state, their money — self-hosted, physically held, the antithesis of
cloud-hosted AI agents.**

Every task traces back to this sentence. If it doesn't, it's bucket B work
pretending to be core.

## 1. How to use this document

- Read this file **before** touching any prompt or task.
- Classify the task's subsystem against buckets A / B / C below.
- **Bucket B = refuse and redirect** to the parked branch. No exceptions
  without an owner decision made in prose.
- **Counterparty-class questions** ("is this for anyone?", "should we build
  X market feature?") are answered by the **owner in prose, before any agent
  work starts** — never by the track loop, never by a megaplan, never by
  "the PR looked fine."
- **Cut lines are declared before work begins**, not discovered after.

## 2. Bucket A — CORE (build here)

### A1. Mesh core

- libp2p mesh transport — NAT traversal + relay; **no public-internet
  exposure required** for any component. **[EXISTS]** —
  `pkg/rhizome/network`, `pkg/rhizome/p2putil`; NAT traversal (v0.5.0),
  relay, DHT; worker-node tier limits public roles.
- Signed identity: BIP39 master seed → HD derivation. **[PARTIAL]** —
  `pkg/rhizome/identity` does BIP39 → single SLIP-0010 path
  `m/44'/123456'/0'/<nodeIndex>'` + HKDF-derived network key. The
  domain-separated multi-path scheme (identity keys / funds keys /
  session keys) is **[NOT BUILT]** — funds keys are today independent
  random secp256k1 in `pkg/web3/keys.json`, not mnemonic-derived. The key
  that syncs state must never be the key that spends.
- State sync: git-style merge, blob dedup. **[EXISTS]** —
  `pkg/rhizome/sync` (go-git packfile transport, deterministic commits),
  `pkg/rhizome/merge` (diff3 three-way), `pkg/rhizome/blob` (offset-resume,
  dedup, `.partial-<hash>` staging). Signed manifests for *state*:
  **[PARTIAL]** — agent manifests are signed (`pkg/rhizome/agentmanifest`)
  but workspace commits are unsigned.
- Signed **hash-chained log** — appended only by the current lease-holder.
  **[NOT BUILT]** — no hash-chained or prev-hash log exists anywhere
  (`econ` ledger and `mesh-audit.jsonl` are plain JSONL). This belongs to
  the A2 scope, not A1.
- Skill/module distribution, signed module catalogs. **[EXISTS]** — mesh
  skill share over `/rhizome/skill/1.0.0`, signed curated skill index
  (`pkg/sigverify`, Track 90), signed module catalog + remote index +
  `module verify` (`pkg/modules`).

### A2. Boot authority — *the milestone-critical, unbuilt piece*

All items **[NOT BUILT]** — verified: no lease/fencing/HLC machinery exists
(any `epoch` hits are swarm-coordinator epochs and econ debt epochs,
unrelated).

- **Lease:** TTL + heartbeat + automatic acquisition. Expiry is
  time-driven; handoff requires no cooperation from the old holder.
- **Fencing epochs:** every state write and every signing request carries
  its epoch; stale-epoch writes are rejected on arrival. Self-fencing on
  partition: a holder that cannot renew stops signing on its own.
- **HLC timestamps at ingress:** answers "who spoke first" without
  synchronized wall clocks.
- **Ingress routing:** all surfaces (phone, laptop, old phone) forward to
  the current lease-holder — one voice, arbitration unnecessary.
- **Per-datatype CAP policy:** money = fence hard (L1 nonce is the final
  arbiter); conversational state = choose availability, reconcile via
  hash-chain after partition heals.
- Status: **NOT BUILT.** This is the research question the project turns on.

### A3. Inference mesh (GPU serving)

- A GPU node serves a llama.cpp **API** across the mesh — no weight
  distribution, no firewall holes (libp2p transport only). **[PARTIAL]** —
  `llama-cpp`/`llama-cpp-vulkan` catalog modules (ondemand, digest-pinned
  weights, `module start`); serving composition across a real mesh hop is
  untested.
- Discovery via existing DHT + capability manifests. **[EXISTS]**.
  Auth via signed capability tokens. **[PARTIAL]** — module bridge tokens
  + mesh trust exist; no per-inference capability tokens.
- **Metering, not billing** — usage counters for a trusted private fleet.
  **[EXISTS]** — `want_usage`/`usage` wire fields (Track 94). The paired
  economy that also meters is bucket B (parked) — do not conflate.

### A4. Optional Ethereum module — install on demand

- **Nimbus Light Proxy** light client. **[EXISTS]** — `nimbus-verified-proxy`
  (P2P light-client sync default), `helios`, `ethereum-rpc` catalog
  tenants. Core mesh builds/runs/tests with the module absent — the module
  is opt-in. Full archival node = config option.
- **Wallet: hot by design** — an agent that spends, not cold storage.
  **[PARTIAL]** — hot key store exists (`pkg/web3/keys.go`: OS keyring /
  scrypt-sealed `keys.json`, per-entry AEAD, pending-approval queue,
  allowlists + spend caps — v0.12.0 Tracks 77–79). Two-tier design is
  **[NOT BUILT]**: no TEE/TPM enclave sealing (StrongBox / Secure
  Enclave / TPM 2.0), no offline root recovery phrase, and funds keys are
  not yet derived from the BIP39 seed (see A1).
- **Transaction lifecycle:**
  1. compose + sign **locally** — transaction intent never gossiped.
     **[EXISTS]** for agent sends (`web3_send`/`web3_sign` → pending queue).
  2. verify inclusion via light client + confirmation threshold.
     **[PARTIAL]** — `waitConfirmed`/`confDepth` polling exists
     (`pkg/settlement/rpc.go`, `pkg/web3/watch.go`); no light-client
     Merkle-proof verification.
  3. timeout → recompute gas, resubmit **same nonce** — thermostat, never
     RBF war. **[NOT BUILT]** — no nonce-resubmit logic exists.
  4. reconcile pending txs against receipts. **[PARTIAL]** —
     `web3_send_status`/receipt decode exist; no systematic reconciliation.
- `web3_*` agent tools — all 18 inventoried and tagged **A4** (reads,
  contract call/send, ERC-20/721, ENS, watch, gated send/sign/approve,
  wallet/pending/status). `pkg/web3` library stays A4; bucket-B packages
  depend on it, never the reverse.
- **Signing only by the lease-holder** (epoch-checked). **[NOT BUILT]** —
  depends on A2.

### A5. Mesh collaboration — *verified EXISTS, was unnamed until 2026-11-26*

- Swarm mode: offer/claim queue, coordinator election, DAG orchestration,
  `swarm run`, gossipsub option. **[EXISTS]** — `pkg/rhizome/swarm`
  (v0.7.0, deepened v0.15.0 Tracks 113–117).
- Shared blackboard. **[EXISTS]** — `pkg/rhizome/blackboard` (v0.9.0).
- Signed agent manifests (AIEOS artifact). **[EXISTS]** —
  `pkg/rhizome/agentmanifest` (v0.9.0 Track 58); claims/lifecycle/
  attestation depth planned v0.19.0 Tracks 159–169.
- Trust pairing, task protocols, peer adverts. **[EXISTS]** —
  `pkg/rhizome/{pair,agenttask,agentrpc,peeradverts}` over
  `/rhizome/{pair,agent-task,agent,skill}/1.0.0`.
- Stream mux. **[EXISTS]** — `pkg/rhizome/stream`.
- **Entanglement warning:** `econ` wire fields (`econ`/`charge`,
  `max_price`, `Claim.Quote`) are woven through `agenttask`, `agentrpc`,
  `swarm/queue.go`, and `mesh/{mesh,task,taskstore,fanout}.go` — see §3
  before touching any of it.

### A6. Agent runtime, surfaces & interop — *verified EXISTS, was unnamed until 2026-11-26*

- Self-evolution (`pkg/evolution`). **[EXISTS]** — **upstream-origin**
  (ships in PicoClaw; `git log upstream/main -- pkg/evolution` is non-empty).
  Rhizome extends it: v0.18.0 Tracks 148–154 shipped (verbs + daemon API,
  draft-review hardening, rollback, apply-policy, lifecycle, feedback);
  155–158 open. Owner classified **bucket A** 2026-11-26.
- ACP — server (`rhizome acp`) + client (`agents.list[].acp`) +
  `acp.remote` over `/rhizome/acp/1.0.0`. **[EXISTS]** — `pkg/acp`,
  **fork-built, not inherited** (no `pkg/acp` on `upstream/main`).
- Browser automation. **[EXISTS]** — `pkg/browser` + `pkg/browser/cdp`,
  **fork-built** (v0.7.1 Tracks 31–32).
- Companion module system. **[EXISTS]** — `pkg/modules` (catalog,
  checksum pins, signed catalog, supervision, bridge); lifecycle/
  `provides`/federation depth planned v0.20.0–v0.21.0.
- `rhizome mcp serve` — MCP-server exposure of allowlisted tools.
  **[EXISTS]** — fork-added surface on inherited `pkg/mcp`.

## 3. Bucket B — PARKED (cross-user economy; NOT deleted)

**Location:** `archive/pre-alignment` branch (history intact — not erased,
just not stood on). **Status: quarantined on `aligned`** — the INERT
strategy landed: B packages carry `//go:build economy`, mesh econ runtime
is stubbed under `!economy` (`MeshEconomyConfig.Enabled` forced false),
wire/persisted fields (`econ`, `charge`, `max_cost`, `econ_sig`) stay in
the schema always-empty, and `pkg/rhizome/econ` remains compiled as dead
code. Default build `go build -tags goolm,stdjson ./...` is green and
inert; `-tags economy` restores full function. `main` still has B live —
the quarantine reaches it at §8 step 5.

- Open agent work market: escrow Solidity contracts, Kleros adapter,
  settlement rail, paired credit ledgers, market DHT + signed curated
  index, reputation, TEE attestations, market web3 wallet, graduation
  megaplan tracks.
- **Why parked:** the mesh is **private to each user**. There is no second
  player; the counterparty was always the owner's own fleet. The economy
  answers a question this design doesn't ask.
- **Revisit condition:** only if the owner changes the design sentence, in
  prose, explicitly.
- Side effect: v0.16.0's graduation gate was blocked on a funded Sepolia
  deployer key — **parking the market makes that blocker moot.**

### B quarantine map (verified 2026-11-26)

**B cluster (self-contained, safe to delete/tag):**
`pkg/settlement`, `pkg/rhizome/econ`, `pkg/rhizome/econweb3`,
`pkg/marketindex`, `cmd/rhizome-market`, `cmd/rhizome/internal/market`,
`cmd/rhizome/internal/network/economy.go`, `pkg/gateway/networkecon.go`,
`web/backend/api/{market.go,network_economy.go}`.

**Shared dependency (do NOT quarantine):** `pkg/web3` + `pkg/tools/web3`
stay A4 — B packages import ~60 `web3.*` symbols including the shared
pending-approval queue (`pending.go`/`policy.go` serve both agent sends
and market settlement txs).

**Entanglement — the four surgical risk points, in order:**

1. **Wire/store schema:** `econ`/`charge`/`max_cost`/`max_price` fields on
   `/rhizome/agent-task`, `/rhizome/agent`, swarm offers, and persisted
   `taskstore` records (`Econ`, `Charge` fields). Fields are `omitempty`
   and manifest-negotiated (Track 139 pinned unnegotiated bytes
   identical), so removal is *probably* wire-safe — but taskstore record
   schema + `PeerScore.Outcomes` (`market_buy`/`market_sell` labels,
   Track 124) need explicit migration/drop decisions.
2. **`pkg/modules` embedded catalog:** full `rhizome-market` spec at
   `catalog.go:731–901` — must be excised or the build breaks.
3. **`pkg/config` schema:** `MeshEconomyConfig`/`EconAdvert`/
   `EconPriceSheet` types are referenced by `pair` bundles
   (`economy`+`econ_sig` fields), `peeradverts`, and `mesh` — config
   surgery, not just package deletion.
4. **Core-file weave:** `mesh/{mesh,task,taskstore,fanout}.go` call
   `econ.Negotiate`/`econTermsFor`/`econCharge`/`verifyCharge` on every
   agent call; `swarm/queue.go`, `agenttask`, `agentrpc` carry econ wire
   fields. Dedicated econ files (`mesh/econ*.go`) are cleanly removable;
   the woven call sites are the work.

## 4. Bucket C — HARNESS UTILITY (renamed from "INHERITED", 2026-11-26)

The harness Rhizome runs on, not what Rhizome is. **Rule: maintain for
security only. No new surface area.**

Verified contents (corrected — the old list misfiled fork-built ACP and
browser, and overstated/understated counts):

- **Channels:** 21 packages under `pkg/channels/` (incl. webhook variants,
  `pico`, `maixcam`, `mqtt`) — upstream.
- **Providers:** 45 catalog options in `pkg/providers` — upstream.
- **MCP** (`pkg/mcp` upstream; the `mcp serve` surface is fork-added —
  see A6), **skills** registries (`pkg/skills` upstream base + fork signed
  index), **seahorse** memory/context engine, **session/state/memory**
  stack — upstream.
- **Web UI** (`web/` upstream base, heavily extended by fork features —
  Market/Evolution panels are B/A surface riding on C chassis).
- **Android** = cross-compile targets in `Makefile` + Termux docs —
  **not an app**; no Android project exists in-repo.
- **Fork-added but harness-class:** `pkg/guard`, `pkg/redact`,
  `pkg/sigverify`, `pkg/workspace-plain` — security/signing plumbing;
  maintained for security, no new features.
- **Moved out:** `pkg/acp`, `pkg/browser`, `pkg/evolution` → bucket A
  (A6); `pkg/web3`, `pkg/tools/web3` → A4; market/econ/settlement/index →
  bucket B.
- Known cost: upstream dormant since 2026-08-19 (`upstream/main` tip
  `bbf6893c`) + a competing maintained fork → fork tax accepted, but no
  feature chasing.

## 5. Unclassified — owner decides, agent must not

- **Any subsystem not named in this document** → the agent flags it for
  classification rather than assuming it belongs somewhere.
- As of 2026-11-26 every fork-only `pkg/` directory is classified (A or
  B or C above); `pkg/evolution` was the last open item — resolved to A.

## 6. Process rules (learned the hard way — this repo's own history)

1. **Human-gate:** direction and counterparty questions are answered by the
   owner before any prompt exists. The track loop structurally cannot
   answer them — that is how the September drift happened.
2. **Cut lines + never-cut list** declared before each wave of work.
3. **Honest-limits section** required per subsystem: what it does NOT do.
4. **One milestone at a time.** Current milestone: **two-node E2E demo** —
   two machines, one agent, lease takeover, live resume over the open
   internet. Everything in A2 exists to make this pass.
5. README describes bucket A only. No inherited marketing drift (upstream
   star counts, upstream feature lists).
6. Planning documents are human-questioned before, not reviewed after.

## 7. Current release state (verified 2026-11-26 @ `834c005d`)

- `v0.14.0` tagged 2026-10-01; `v0.15.0` + `v0.17.0` code-complete but
  untagged; `v0.16.0` half-done (Tracks 123–135 shipped; 132 Sepolia E2E +
  136 docs/release open — graduation gate now moot per §3).
- `archive/pre-alignment` branch + `v0.17-pre-alignment` tag exist at
  `834c005d` — §8 step 1 done ahead of schedule.
- `.todo.md`: tracks planned through 193 (v0.21.0) + v0.22.0 queue →
  reclassify every track per buckets; **delete bucket B tracks**; freeze
  bucket C except security.
- Build bar: `go build -tags goolm,stdjson ./...` clean + relevant test
  slice green.

## 8. VCS plan — no rewind

1. ~~`git branch archive/pre-alignment main && git tag v0.17-pre-alignment`~~
   — **DONE** (both exist at `834c005d`).
2. `git switch -c aligned` — the new line starts here. **DONE**.
3. ~~On `aligned`: quarantine bucket B~~ — **DONE** via the `economy`
   build tag (INERT strategy; entanglement points resolved by keeping
   wire schema + `pkg/rhizome/econ` compiled and gating runtime behind
   `!economy` stubs). Build + vet + affected tests green under both
   tag sets.
4. Trim `.todo.md` to bucket A — **DONE** (B sections collapsed to
   parked notes; Tracks 132/136 kept as parked open items). Remaining:
   rewrite README; add `ROADMAP.md` whose single milestone is the
   two-node E2E demo.
5. Only after green: `git push origin aligned:main` — fast-forward, since
   `main` never moved. (Fork: 1 star, zero external consumers — repointing
   costs nobody anything.)
