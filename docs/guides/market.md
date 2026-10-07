# Open Agent Work Market

The market lets **mutually untrusted** operators buy and sell agent work:
a seller advertises priced offers, a buyer locks payment in escrow, the
seller's agent runs the task inside a sandboxed ACP session, and a signed
receipt settles the escrow. It ships as the `rhizome-market` companion
module — never in the base binary — because serving paid work is an
elevated-trust posture most nodes don't want.

> **Experimental (v0.14.0).** The settlement rail defaults to a fixture —
> a local in-memory chain — so nothing moves real money until you bind a
> real escrow contract. Provider discovery is direct-peer only (signed
> manifest adverts + a journaled `peer-adverts.json`); the curated index,
> DHT tier, reputation fields, and the recorded testnet E2E land in the
> v0.16.0 market-graduation sprint. Treat every flow as testable plumbing,
> not production money movement.

## Threat model

- **Buyers never run seller code.** A purchase is prompt-in, result-out:
  the seller's agent executes the task; the buyer sees session updates and
  a signed receipt.
- **Sellers never see a buyer's keys.** Escrow locks happen on-chain (or
  on the fixture rail); the module only verifies the lock's existence and
  shape before spawning.
- **The prompt is the contract.** A session binds to `task_hash` — the
  sha256 of the exact prompt text — and the escrow's on-chain `details`
  must carry the same hash. The seller can't be paid for different work
  than locked; the buyer can't claim the prompt differed.
- **Adverts are public.** `advert.json` rides inside the *signed* mesh
  capability manifest broadcast to every connected peer. Never put
  secrets or private topology in it.
- **Self-attested sandbox.** A seller advertises a `runtime` (sandbox /
  container) — the claim is honest but self-reported. Paid serving
  refuses `exec` outright: it can't enforce no-egress, and `SpawnBound`
  fails closed rather than silently dropping the flag. Until the
  reputation/index work lands, only buy from operators you have an
  out-of-band reason to trust.
- **Don't send what you can't afford to leak.** `export_redact` runs
  `pkg/redact` on task text, but a prompt you send is data the seller
  receives. Redaction is a seatbelt, not a vault.

## Setup

```bash
rhizome module install rhizome-market   # once a release digest pin lands
rhizome module enable rhizome-market
```

The module is `kind:daemon` — it autostarts with the daemon, claims
`/rhizome/acp/1.0.0` through the stream bridge (mutually exclusive with
`acp.server.remote`), serves a loopback API the `rhizome market` verbs
dial, and writes `advert.json` + `market-audit.jsonl` under its module
dir. Fields/secrets live in `modules.rhizome-market.{fields,secrets}` —
nothing secret ever enters argv or env.

## Selling

```bash
rhizome module set rhizome-market \
  serve_enabled=true \
  payout_address=0x… payout_chain_id=8453 \
  offers_json='[{"id":"research","agent_binding":"main",
    "price_sheet":{"per_task":"0.50","asset":"USDC","chain_id":8453}}]'
```

`advert.json` is emitted only for a *valid* serving config —
`serve_enabled` + `payout_address` + at least one offer — and it carries
`runtime`/`runtime_available` so an advert never claims a runtime the
host lacks. The file disappears on graceful shutdown, so a stopped module
stops advertising. `offers.json` under the module dir overrides
`offers_json` for larger offer sets.

When a buyer connects (mesh bridge or WSS — see *Transports*), it opens
work with `_rhizome.session_open`:

```json
{"session_id": "0x<escrow clone addr>", "task_hash": "0x<sha256 prompt>",
 "offer_id": "research", "buyer": "0x<buyer EVM>",
 "terms": {"amount": "<base units>", "token": "0x…", "chain_id": 8453}}
```

The gate rejects **before any spawn**: `serve_enabled`, a per-peer open
rate limit (12 opens/min), offer enabled, `amount` equals the offer's
`per_task` price in token base units, then `SettlementRail.VerifyLock` —
the escrow must exist on-chain, hold ≥ the locked amount, be unreleased
and live, and carry the same `task_hash` in `details`. Registration then
enforces the concurrent-session caps (`max_concurrent_sessions` global,
half that per peer). Every refusal lands a `market.gate.reject` audit
event (`code`, `peer`, `session_id` when parseable); accepted opens land
`market.gate.open`. Only then does `session/new` spawn the offer's
`agent_binding` under `sandbox` or `container` — the only runtimes that
enforce no-egress — with `permission_policy=deny`, no fs/terminal
capabilities, a per-session scratch cwd, and forced no-egress networking
(`isolation.NetModeNone` / `--network none`; `allowlist` is
unimplemented and fails the spawn explicitly).

Serving adverts carry a `serving` block declaring that posture —
`{no_egress, max_sessions, per_peer_cap, open_rate_per_minute}` — next
to the Track 131 `attestation` TEE claim, so buyers can size redundant
fan-out and read refusal codes against what the provider advertised.

Completion, close, conn-drop, or `session_ttl` expiry mints a
`_rhizome.receipt` — an Ed25519-signed JSON document
(`session_id, offer_id, seller_peer_id, buyer, duration_ms, usage,
result_sha256, terms, settlement, interrupted, issued_at`) persisted to
`receipts/<session_id>.json` and verifiable against `seller_peer_id`.
Interrupted sessions mint honest `interrupted:true` receipts.

## Buying

```bash
rhizome market find research                      # search discovered offers
rhizome market buy <provider> <offer> "summarize …" [--max-cost 0.50]
rhizome market session <id>                       # live purchase state
rhizome market receipt <id>                       # the signed receipt
```

`buy` is **asynchronous**: it returns a `purchases/<id>.json` record and
the lifecycle runs in the background — escrow open → transport dial →
`session_open` → prompt → verified receipt → release. States:
`queued → escrow_opening → session → awaiting_receipt →
awaiting_release|disputable → completed|disputed|refunded|failed`.

Providers resolve two ways:

- **Direct-peer discovery** (default): the daemon journals every peer's
  adverts to `<RHIZOME_HOME>/peer-adverts.json` — `find` searches that.
- **Signed index** (opt-in): `market_index_enabled` +
  `market_index_url` fetch `<url>/index.json`, verified against
  `market_index_pubkey` (release key default), 1 h cache, stale-serve on
  outage.

Cost controls — all enforced per purchase: `buy_max_cost_per_task`,
`buy_max_cost_per_day`, and the per-call `--max-cost` (exact decimal
arithmetic on token base units).

Export policy — what your prompt may carry: `export_redact` (default on)
runs the shared redactor over the task text; `export_allow_attachments`
(default off) gates URI-reference `resource_link` blocks — attachment
bytes never leave the module either way; `export_require_review`
(`prompt`|`always`|`never`) mints a short-lived `review_id` — inspect
the post-redact preview, then `rhizome market buy --confirm <id>`
replays it. The preview binds the exact redacted task, so confirming
can't substitute different work.

Disputes: `buy_auto_release=false` parks verified purchases in
`awaiting_release` for `rhizome market release <id>`; failures park in
`disputable` — `market dispute <id> [reason]` locks the escrow for the
arbiter, `market refund <id>` withdraws after the dispute window
terminates. A receipt that fails signature or `result_sha256`
verification never auto-releases.

## Buying depth

**Redundant fan-out** — `rhizome market buy --redundant N <query> <task>`
fans one task to up to N providers (cap 8) whose offers match the query,
each under its own escrow so results settle — and can be disputed —
independently. This is the threat model's mitigation for work quality
you can't verify: compare `result_sha256` across the batch. Cost is
**N × per_task** (each provider's own price) and `buy_max_cost_*` caps
still bind per branch, so a fan can't overspend the daily budget.
Branches share a `redundant_group` id — `market sessions --all` groups
them for comparison. Partial failures are reported per branch; the buy
only fails when nothing purchased.

**Ledger** — `rhizome market sessions [--all]` lists the local purchase
records (status, terms, settlement state, group ids, TEE claims, result
hashes) newest-first plus spend reporting: per-asset committed spend in
the last 24 h against `buy_max_cost_per_day` headroom and the
`buy_max_cost_per_task` ceiling. The same view lives on the web
launcher's Network page — a Market panel proxies `GET /v1/sessions`
(buy ledger, live sell sessions, dispute-state purchases, spend) so a
serving operator sees both sides without the CLI; the peer detail sheet
renders market outcome badges from the peer-score record (`value_hash`
commitments, never raw amounts).

## Portable attestations

A buyer can issue a **signed completion attestation** for a terminal
purchase — `completed`, `resolved`, `refunded`, or `failed`:

```bash
rhizome market attest issue <purchase-id-or-session>
```

The output is a JSON claim `{provider_peer_id, session_id, outcome,
terms_hash, task_hash, issued_at, buyer_peer_id, signature}` signed by
the buyer's node Ed25519 key over the signature-stripped canonical
bytes (same convention as `_rhizome.receipt`). Issuance is **strictly
opt-in** — nothing mints automatically after a purchase, and each issue
also lands on the local peer-score record as a neutral `attested`
outcome ref (no success/failure counter movement).

Delivery is out of band: send the JSON to the seller however you like.
The seller stores it via `rhizome market attest register <json|@file>`
— the store verifies the signature before accepting, dedups on
`(session_id, buyer_peer_id)`, keeps the newest 64, and the newest 32
ride the seller's `advert.json.attestations` (trimmed further if the
advert's byte bound demands it).

Verify a claim with `rhizome market attest verify <json|@file>`. The
check always runs the signature → `buyer_peer_id` chain (the Ed25519
key must extract from the peer ID and verify); when the verifier holds
a purchase for that `session_id` it additionally cross-checks
`terms_hash` — otherwise `terms_match` comes back absent rather than
falsely claiming a full chain.

**Honest limits.** Attestations are *additive evidence, not a
reputation ledger*: a seller picks which to advertise (negatives can be
withheld), a buyer can decline to issue, and nothing stops a colluding
pair from attesting each other. They raise confidence in a provider;
they never replace the local peer-score ledger or the escrow lock —
buy on posture plus your own evidence, not on attestations alone.

## Settlement

`pkg/settlement` abstracts the escrow rail. Unset `escrow_contract` ⇒
**fixture rail** — an in-memory chain for development and tests. Setting
it opts into **Smart Invoice** (the survey's adopted contract;
`escrow_dispute_window` seconds → `terminationTime`; canonical Sepolia
pins in `modules.md`).

Signer modes (`settlement_signer`):

| Mode       | Behaviour |
| ---------- | --------- |
| `approval` | **Default.** Each escrow tx queues into `web3-pending.json` for human signing; `pending_ids` land on the purchase record. |
| `direct`   | Sends through unlocked/dev endpoints (anvil, FakeChain). No key needed. |
| `wallet`   | Signs locally — the buyer's key from the shared web3 wallet store (keyring or `RHIZOME_WALLET_PASSPHRASE`; zeroed after each sign). Autonomous spend: `buyer_address` must resolve to a wallet-store key, `buy_max_cost_*` caps still apply. |

Safety rails on real chains: the endpoint's live `eth_chainId` must equal
`escrow_chain_id` at rail assembly and again on every wallet sign; chain
1 refuses outright unless `escrow_allow_mainnet=true`; the fixture rail
warns when configured with `wallet`/`direct` signing (it signs nothing —
the fields do no work there).

An escrow event watcher polls `eth_getLogs` per non-terminal purchase at
`escrow_watch_interval` (default 60s, `0` disables): observed
Release/Lock/Withdraw/Resolve transition the purchase record
(`disputed` stays watched — an arbiter's `Resolve` can still land), and
`watch_block` persists scan progress across restarts.

## Transports

Two ways for a buyer's session to reach a seller's gate — identical
`_rhizome.session_open` → `VerifyLock` → sandboxed-session path either
way:

1. **Mesh bridge** (default): the daemon's loopback stream bridge claims
   `/rhizome/acp/1.0.0`; inbound ACP ndjson is spliced onto the module's
   session manager. Buyers need a Rhizome mesh identity and the seller in
   their peer set.
2. **WSS** (`serve_https_listen`): an optional TLS listener for buyers
   *without* a mesh identity — opens the market to non-Rhizome
   counterparties. See the wire spec below.

## WSS wire spec (for non-Rhizome counterparties)

Everything needed to buy from a Rhizome seller without running Rhizome:

- **Discovery**: the seller's signed manifest advert carries
  `endpoints: ["wss://host:port"]` and `tls_fingerprint` (SHA-256 of the
  leaf cert DER, lowercase hex). Fingerprint, not CA — sellers typically
  run a self-signed pair persisted under the module dir.
- **Connect**: `wss://<host>:<port>/rhizome/acp` — standard WebSocket
  upgrade over TLS. During the handshake, bypass CA verification but
  require the leaf certificate's SHA-256 to equal `tls_fingerprint`
  byte-exactly (TOFU — pin comes from the signed advert, not the wire).
  `serve_https_advertise` sets the advertised host for NAT/DNS cases.
- **Frames**: one JSON-RPC message per WS message, each a single ndjson
  line — byte-identical to what flows over the libp2p stream.
- **Open work**: send the `_rhizome.session_open` extension method with
  the payload shown under *Selling*. The reply reports the gate verdict;
  a rejected open closes the connection.
- **Session**: `session/new` + `session/prompt` per ACP; the seller's
  agent emits `session/update` notifications. Text you send must hash
  (sha256) to `task_hash` — single-prompt sessions.
- **Close**: completion/drop/timeout produces a `_rhizome.receipt`
  notification — the signed JSON document above. Verify the Ed25519
  signature over the signature-stripped canonical form against
  `seller_peer_id` before considering the purchase settled.
- **Bounds**: `serve_https_max_conns` caps concurrent connections;
  a ~15 s bound applies from upgrade to first message; WS messages cap
  at 4 MiB. Per-IP rate limits, session caps, and the escrow gate apply
  identically to mesh peers.

## Operational surface

- `rhizome module status rhizome-market` — version, pid, missing fields,
  health.
- `GET /v1/health` (via the loopback API) — serve_enabled, offer count,
  live sessions, bridge state, escrow posture, `config_error` for invalid
  `serve_https_*`/rail values.
- `market-audit.jsonl` — bounded audit trail (opens, gate accepts and
  rejects, session lifecycle, receipts, purchases, escrow events).
  `market.gate.open`/`market.session.active`/`market.session.end` all
  carry `session_id` + `escrow_id` + `terms_hash` (keccak256 over
  `sessionID|token|amount|taskHash` — the same commitment the dispute
  evidence bundle uses), and `market.session.active` snapshots the
  serve-time `tee_kind` when a TEE claim was set.
- `purchases/<id>.json`, `receipts/<session_id>.json` — durable records.

## Current limits

- **Attestations are additive, not a ledger** — the portable-reputation
  field (above) is opt-in buyer-signed evidence a seller may withhold;
  weight it accordingly.
- **No recorded testnet E2E** — the Smart Invoice path is bound and
  unit-tested over FakeChain, but hasn't run a filmed Sepolia round-trip;
  that's the v0.16.0 graduation gate (Track 132).
- **Fixture rail default** — money movement is opt-in and explicitly
  experimental.
- `rhizome-market`'s catalog `releases` stay empty until the first
  digest pin lands from release `checksums.txt` post-tag — `module
  install rhizome-market` reports that honestly until then.
