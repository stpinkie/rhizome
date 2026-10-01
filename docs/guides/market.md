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
- **Self-attested sandbox.** A seller advertises a `runtime` (exec /
  sandbox / container) — the claim is honest but self-reported. Until the
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
rate limit, offer enabled, `amount` equals the offer's `per_task` price
in token base units, then `SettlementRail.VerifyLock` — the escrow must
exist on-chain, hold ≥ the locked amount, be unreleased and live, and
carry the same `task_hash` in `details`. Only then does `session/new`
spawn the offer's `agent_binding` under the configured `runtime` with
`permission_policy=deny`, no fs/terminal capabilities, a per-session
scratch cwd, and forced no-egress networking.

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
- `market-audit.jsonl` — bounded audit trail (opens, gates, receipts,
  purchases, escrow events).
- `purchases/<id>.json`, `receipts/<session_id>.json` — durable records.

## Current limits

- **No curated index / DHT tier / reputation fields** — discovery is
  direct-peer adverts or an operator-hosted signed index.
- **No recorded testnet E2E** — the Smart Invoice path is bound and
  unit-tested over FakeChain, but hasn't run a filmed Sepolia round-trip;
  that's the v0.16.0 graduation gate (Track 132).
- **Fixture rail default** — money movement is opt-in and explicitly
  experimental.
- `rhizome-market`'s catalog `releases` stay empty until the first
  digest pin lands from release `checksums.txt` post-tag — `module
  install rhizome-market` reports that honestly until then.
