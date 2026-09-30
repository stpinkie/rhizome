# Companion Modules

Companion modules extend Rhizome with sidecar binaries that don't grow the
base binary. They install under `~/.rhizome/modules/<id>/<version>/`, are
verified against pinned release digests, and (for daemon-kind modules) are
supervised by `rhizome daemon` with restart-on-crash and bounded logs.

Three module kinds:

| Kind       | Lifecycle                                                          |
| ---------- | ------------------------------------------------------------------ |
| `daemon`   | Long-running supervised process; autostarts with `rhizome daemon`.    |
| `ondemand` | `module start` launches once — exit is final, never auto-restarted.    |
| `config`   | No process — exposes a configured endpoint to the rest of the system. |

## CLI

```bash
rhizome module list                    # catalog + install/enabled status
rhizome module status <id>             # version, pid, health, missing fields
rhizome module install <id>            # download + digest-verify + extract
rhizome module uninstall <id>          # remove the module directory
rhizome module enable <id>             # modules.<id>.enabled = true
rhizome module disable <id>
rhizome module start|stop|restart <id> # via the running daemon
rhizome module logs <id> --tail 50     # bounded stdout/stderr
rhizome module set <id> key=value ...  # write modules.<id>.fields
rhizome module set --secret <id> k=v   # write .security.yml, never config.json
rhizome module validate                # check config against the catalog
```

`module list`/`status`/`set`/`logs`/`validate` work without a daemon;
`start`/`stop`/`restart` require one. The web console mirrors all of this on the **Modules** page
(`/modules`, proxied via `/api/modules*`).

## Configuration

Per-module settings live under `modules.<id>`:

```json
{
  "modules": {
    "nimbus-verified-proxy": {
      "enabled": true,
      "fields": {
        "network": "mainnet",
        "listen_url": "http://127.0.0.1:8545"
      }
    }
  }
}
```

Fields marked `secret` in the catalog (provider URLs that embed API keys,
tokens, …) are stored in `.security.yml` next to `config.json` and masked in
logs and the UI — use `module set --secret` or the Modules page so they never
land in `config.json`.

Required fields gate `enabled`: a module with missing required config stays
`configured: false` and won't start until every required field is set
(`module status <id>` lists what's missing).

## Wire protocols (catalog schema v4) + stream bridge

A module can declare libp2p `protocols` in its catalog entry (schema v4 —
emitted only when a module actually declares one, so older binaries keep
working on v1–v3 catalogs):

```json
{
  "id": "rhizome-market",
  "protocols": ["/rhizome-market/offer/1.0.0"]
}
```

When an enabled module declares protocols, the daemon runs a **loopback
stream bridge** (works under `--no-gateway`; it's a dedicated listener,
not the gateway mux) and injects three variables into the module's
environment:

| Variable               | Purpose                                                        |
| ---------------------- | -------------------------------------------------------------- |
| `RHIZOME_BRIDGE_ADDR`  | `127.0.0.1:<port>` — the module dials here to open peer streams |
| `RHIZOME_BRIDGE_TOKEN` | per-module bearer, minted at install and rotated on reinstall   |
| `RHIZOME_MODULE_DIR`   | the module dir, where it publishes `bridge.addr` for inbound    |

Outbound (module → peer): connect to the bridge, send one JSON hello line
`{"token","action":"dial","peer":<peer-id-or-multiaddr>,"protocol":<id>}`,
then raw bytes splice onto a fresh libp2p stream — but only for protocols
the module declared and only after the token authenticates.

Inbound (peer → module): the bridge claims each declared protocol with a
libp2p stream handler, gated on the mesh trust set — untrusted peers are
reset before any bytes flow. For trusted peers the daemon dials the
module's own loopback listener (published at `<module_dir>/bridge.addr`),
sends `{"token","action":"accept","peer":<source peer>,"protocol":<id>}`,
and splices.

Declarations are validated on both sides: malformed ids, duplicates, and
collisions with core Rhizome protocols (`/rhizome/agent/1.0.0`, …) are
config errors; protocols already registered by the running node, or
claimed by another enabled module, are skipped with a warning.
`/rhizome/acp/1.0.0` is deliberately *not* in the static refuse list —
it's only core-claimed while `acp.server.remote` serves, so a module may
declare it when core is not.

## Capability adverts + module localhost API

A serving module can publish a capability advert that rides inside the
node's **signed** mesh capability manifest — the same authenticated
channel that carries models, skills, and agent manifests:

- The module writes `<module_dir>/advert.json` — at most **16 KiB** of
  valid JSON. The shape is module-defined (offers, price sheet, payout,
  runtime posture); core treats it as opaque bytes.
- The daemon includes the advert as
  `Capability.ModuleAdverts["<module id>"]` only when the module is
  catalog-known, `enabled`, **and** has a truthy `serve_enabled` field —
  the explicit opt-in, because the manifest broadcasts to **every**
  connected peer, trusted or not. Adverts are deliberately public data:
  never put secrets, keys, or private topology in `advert.json`.
- When any advert is included the manifest also sets
  `allows.market_serve: true` — the map key an old build can re-marshal
  unchanged. The `module_adverts` field itself is emit-when-set (the
  `Role` precedent): an old build drops it on decode, fails the signature
  check, and rejects the whole manifest — acceptable because advert
  consumers require the new build anyway.
- Oversized, invalid, or unreadable `advert.json` files are dropped with
  a warning/debug log — advert serving never blocks manifest signing.
  The receiving side enforces the same 16 KiB bound per advert (64 KiB
  total per manifest) post-verification.

The market module also publishes a **loopback HTTP API** for the CLI:

- `<module_dir>/api.addr` — its `127.0.0.1:<port>` listener. The CLI
  refuses non-loopback values so a tampered file can't leak the token
  off-host.
- `<module_dir>/bridge-token` — the per-module bearer minted at install;
  reused to authenticate the API (`Authorization: Bearer <token>`).

`rhizome market` is a thin client over that API (`POST /v1/<verb>`):

```
rhizome market find <service>
rhizome market buy <provider> <offer> <task> [--max-cost <amt>]
rhizome market session <id>
rhizome market receipt <id>
```

Without the module the verbs print `rhizome-market module not installed —
run \`rhizome module install rhizome-market\``; an installed-but-not-
serving module points at `rhizome module status rhizome-market`. Track 102
made the sell-side verbs real: `POST /v1/session {session_id}` reports a
live session's state/offer/peer/duration and `POST /v1/receipt
{session_id}` serves the signed receipt JSON (live session or persisted
under `<module_dir>/receipts/`); both 404 on an unknown session.
`find`/`buy` remain an honest
`501 {"error":{"code":"not_implemented","track":103}}` until the buy-side
track lands. `GET /v1/health` reports version, uptime, `serve_enabled`,
offer count, live session count, bridge state, escrow posture, config
errors.

## Catalog

### `nimbus-verified-proxy` (daemon)

Verified Ethereum JSON-RPC: the [nimbus-eth1](https://github.com/status-im/nimbus-eth1)
consensus light client serving execution-API responses proven against
beacon-chain proofs. You supply an untrusted execution endpoint and a
trusted block root; light-client data syncs over the beacon P2P network by
default — no beacon REST endpoint needed.

| Field                 | Secret | Required | Default                  |
| --------------------- | ------ | -------- | ------------------------ |
| `execution_api_url`   | yes    | yes      | —                        |
| `p2p`                 | no     | no       | `true`                   |
| `beacon_api_url`      | yes    | no†      | —                        |
| `trusted_block_root`  | no     | yes      | —                        |
| `network`             | no     | no       | `mainnet`                |
| `listen_url`          | no     | no       | `http://127.0.0.1:8545`  |
| `p2p_tcp_port`        | no     | no       | `9000`                   |
| `p2p_udp_port`        | no     | no       | `9000`                   |
| `p2p_max_peers`       | no     | no       | `160`                    |

† `beacon_api_url` is required only when `p2p=false`; with p2p on it acts as
an optional REST supplement alongside the P2P backend.

Fetch a `trusted_block_root` from a beacon API at
`/eth/v1/beacon/headers/finalized` (e.g.
`https://lodestar-mainnet.chainsafe.io`) or from a trusted provider such as
beaconcha.in — it must be recent. The light client
syncs on first start; `eth_syncing` reports progress. Platforms:
linux amd64/arm64, windows amd64, macos arm64 (no darwin-amd64 build).

```bash
rhizome module install nimbus-verified-proxy
rhizome module set --secret nimbus-verified-proxy \
  execution_api_url=https://mainnet.infura.io/v3/KEY
rhizome module set nimbus-verified-proxy trusted_block_root=0x…
rhizome module enable nimbus-verified-proxy
rhizome module start nimbus-verified-proxy   # or restart the daemon
```

Once healthy, `listen_url` serves a verified `eth_*` JSON-RPC endpoint for
your scripts and dapps.

### `helios` (daemon)

[Helios](https://github.com/a16z/helios) — a fast Ethereum light client
serving a local JSON-RPC endpoint synced from beacon-chain data. It
complements nimbus-verified-proxy: no Windows build, but it covers
darwin/amd64 which nimbus lacks.

| Field               | Secret | Required | Default                        |
| ------------------- | ------ | -------- | ------------------------------ |
| `execution_api_url` | yes    | yes      | —                              |
| `checkpoint`        | no     | yes      | —                              |
| `consensus_rpc_url` | yes    | no       | (upstream: lightclientdata.org)|
| `fallback`          | yes    | no       | —                              |
| `network`           | no     | no       | `mainnet`                      |
| `rpc_bind_ip`       | no     | no       | `127.0.0.1`                    |
| `rpc_port`          | no     | no       | `8546`                         |
| `data_dir`          | no     | no       | `{module_dir}/data`            |

All settings reach the `helios ethereum` subcommand through environment
variables. `checkpoint` is a trusted weak-subjectivity beacon block root —
fetch a recent one from a beacon API at `/eth/v1/beacon/headers/finalized`
(same source as nimbus's `trusted_block_root`). Default port `8546` leaves
`8545` free for nimbus; set `rpc_port=8545` only when nimbus isn't
installed. `data_dir` stays under the module directory, so
`module uninstall` removes the checkpoint DB with it. Platforms:
linux amd64/arm64, darwin amd64/arm64.

```bash
rhizome module install helios
rhizome module set --secret helios \
  execution_api_url=https://mainnet.infura.io/v3/KEY
rhizome module set helios checkpoint=0x…
rhizome module enable helios
```

Health is a `jsonrpc` probe (`eth_chainId`) against `rpc_bind_ip:rpc_port`.

### `ipfs-kubo` (daemon)

[Kubo](https://github.com/ipfs/kubo) — a full IPFS node: pinning, gateway,
and DHT/libp2p participation.

| Field          | Secret | Required | Default              |
| -------------- | ------ | -------- | -------------------- |
| `repo_dir`     | no     | no       | `{module_dir}/repo`  |
| `profile`      | no     | no       | `default-networking` |
| `api_port`     | no     | no       | `5001`               |
| `gateway_port` | no     | no       | `8080`               |
| `swarm_port`   | no     | no       | `4001`               |
| `routing`      | no     | no       | `dht`                |

First start runs `ipfs init --profile=<profile>` once (guarded by the
repo's `config` file existing — it is not re-run on restart). Kubo has no
daemon address flags, so before every launch the module re-applies
`api_port`/`gateway_port`/`swarm_port` to the repo config — changing a
port field takes effect on the next restart. `repo_dir` is exported as
`IPFS_PATH`; the repo lives under the module directory, so
`module uninstall` removes it. API and gateway bind loopback only; swarm
listens on all interfaces (tcp + quic-v1 + webtransport) for DHT
participation. Platforms: linux amd64/arm64, darwin amd64/arm64, windows
amd64/arm64 (Windows assets are `.zip` — extraction handles the
`kubo/ipfs.exe` layout).

```bash
rhizome module install ipfs-kubo
rhizome module enable ipfs-kubo
# API:    http://127.0.0.1:5001/api/v0/…
# Gateway: http://127.0.0.1:8080/ipfs/<cid>
```

Health is a TCP probe on the API port.

### `ethereum-rpc` (config)

A remote Ethereum JSON-RPC endpoint without running a node — the
zero-binary answer on any platform.

| Field          | Secret | Required |
| -------------- | ------ | -------- |
| `endpoint_url` | no     | yes      |
| `api_key`      | yes    | no       |

```bash
rhizome module set ethereum-rpc endpoint_url=https://mainnet.infura.io/v3/KEY
rhizome module enable ethereum-rpc
```

### `rhizome-market` (daemon)

First-party market module (ships from this repo): the sell/buy side of the
open agent work market. Supervised daemon; claims the
`/rhizome/acp/1.0.0` stream protocol (bridged); serves the loopback API the
`rhizome market` verbs dial; writes `advert.json` (merged into the signed
mesh manifest when `serve_enabled`) and a bounded `market-audit.jsonl`.

| Field | Secret | Default |
| ----- | ------ | ------- |
| `serve_enabled` | no | `false` |
| `offers_json` | no | — |
| `runtime` | no | `sandbox` |
| `payout_chain_id` / `payout_address` / `payout_asset` | no | — / — / `USDC` |
| `max_concurrent_sessions` / `session_ttl` | no | `4` / `30m` |
| `buy_max_cost_per_task` / `buy_max_cost_per_day` | no | — |
| `export_allow_attachments` / `export_redact` / `export_require_review` | no | `false` / `true` / `prompt` |
| `market_index_enabled` / `market_index_url` / `market_index_pubkey` | no / yes / no | `false` / — / release key |
| `buyer_address` / `buy_auto_release` / `settlement_signer` | no | default wallet / `true` / `approval` |
| `escrow_chain_id` / `escrow_contract` / `escrow_token` / `escrow_arbiter` / `escrow_dispute_window` | no | — (unset contract = fixture rail) |
| `escrow_allow_mainnet` / `escrow_watch_interval` | no | `false` / `60s` |

```bash
rhizome module install rhizome-market   # once a release is pinned
rhizome module set rhizome-market serve_enabled=true \
  payout_address=0x… payout_chain_id=8453 \
  offers_json='[{"id":"research","agent_binding":"main","price_sheet":{"per_task":"0.50","asset":"USDC","chain_id":8453}}]'
rhizome module enable rhizome-market
```

Sell-side sessions (Track 102): a bridged `/rhizome/acp/1.0.0` peer opens
work with the `_rhizome.session_open` extension method —

```json
{"session_id": "0x<escrow clone addr>", "task_hash": "0x<sha256 prompt>",
 "offer_id": "…", "buyer": "0x<buyer EVM>",
 "terms": {"amount": "<base units>", "token": "0x…", "chain_id": 8453}}
```

The gate rejects before any spawn: `serve_enabled`, per-peer rate limit
(12 opens/min), offer enabled, `amount` equals the offer's `per_task`
price in token base units, then `SettlementRail.VerifyLock` — the escrow
must exist, hold ≥ the locked amount, be unreleased/unlocked/live, and
carry the same `task_hash` in its on-chain `details`. `session/new` then
spawns the offer's `agent_binding` under `runtime` with
`permission_policy=deny`, no fs/terminal capabilities, a per-session
scratch cwd, and forced `--network none`/sandbox `NetModeNone` (the
allowlist egress mode is unimplemented — none is the only honest bound).
Prompt text must hash to `task_hash` (single-prompt sessions); agent
`session/update` relays to the buyer verbatim; duration + optional
`_rhizome.usage` self-report meter the session. Completion, close,
conn-drop, or `session_ttl` expiry mints a `_rhizome.receipt` —
`{session_id, offer_id, seller_peer_id, buyer, duration_ms, usage,
result_sha256, terms{price(base units), asset, chain_id}, settlement,
interrupted, issued_at, signature}` — Ed25519-signed by the node identity
over the signature-stripped canonical JSON, persisted to
`receipts/<session_id>.json`, verifiable against `seller_peer_id`. An
unencrypted-missing identity mints unsigned receipts honestly.

Buy-side purchases (Track 103): `rhizome market buy <provider> <offer>
<task> [--max-cost]` is asynchronous — it returns a persisted purchase
record (`purchases/<id>.json` under the module dir) and the lifecycle runs
in the background: escrow open → bridge dial → `_rhizome.session_open` →
single prompt → `_rhizome.receipt` → release. `rhizome market session
<id>` reports the live state (`queued → escrow_opening → session →
awaiting_receipt → awaiting_release|disputable → completed|disputed|
refunded|failed`). Providers resolve from the daemon's
`peer-adverts.json` journal (direct-peer discovery) or, when
`market_index_enabled`+`market_index_url` are set, from a signed
`<url>/index.json` verified against `market_index_pubkey` (the release
key when unset) with a 1 h cache and stale-serve on outage. Every
purchase enforces `buy_max_cost_per_task`, `buy_max_cost_per_day`, and
`--max-cost` (exact decimal arithmetic); `export_redact` runs
`pkg/redact` on the task text; `export_allow_attachments` gates
URI-reference attachments (ACP `resource_link` blocks — bytes never
leave the module); `export_require_review` (`prompt`|`always`|`never`)
mints a short-lived `review_id` the `buy --confirm <id>` call replays —
the preview binds the exact post-redact task, so confirming can't
substitute a different task. Settlement signing is human-gated by
default: `settlement_signer=approval` queues each escrow tx into the
shared `web3-pending.json` (`pending_ids` land on the purchase record);
`direct` talks to unlocked/dev endpoints (anvil, FakeChain); `wallet`
(Track 104) signs locally with the buyer's key from the shared web3
wallet store (keyring/`RHIZOME_WALLET_PASSPHRASE` decrypt, zeroed after
each sign) — autonomous spend power, so `buyer_address` must resolve to
a wallet-store key and `buy_max_cost_*` caps apply as usual. Chain
pinning guards every real rail: the endpoint's live `eth_chainId` must
equal `escrow_chain_id` at startup and on each wallet sign, and chain 1
is refused unless `escrow_allow_mainnet=true`. On configured rails the
module also runs an escrow event watcher — `eth_getLogs` per
non-terminal purchase at `escrow_watch_interval` (0 disables) —
transitioning purchases on observed Release/Lock/Withdraw/Resolve and
auditing `market.escrow.event`. `buy_auto_release=false` parks verified
purchases in `awaiting_release` for `rhizome market release <id>`;
failures park in `disputable` — `rhizome market dispute <id> [reason]`
locks the escrow, `rhizome market refund <id>` withdraws after
termination. A receipt that fails signature or hash verification never
releases.

Notes:

- `offers.json` under the module dir overrides `offers_json` when present —
  larger offer sets stay editable without rewriting config.
- `advert.json` is only emitted for a *valid* serving config
  (`serve_enabled` + `payout_address` + ≥1 offer); it carries `runtime` +
  `runtime_available` so an advert never claims a runtime the host lacks.
  The file is removed on graceful shutdown so a stopped module stops
  advertising.
- `/rhizome/acp/1.0.0` is conditionally core-claimed: while
  `acp.server.remote` serves, the bridge skips the module's claim — market
  serving and remote ACP are mutually exclusive per daemon.
- `escrow_contract` unset ⇒ the fixture `SettlementRail` posture; setting
  it opts into Smart Invoice (`escrow_dispute_window` is seconds →
  `terminationTime`). The canonical Sepolia deployments the survey
  pinned: factory `0x8227b9868e00B8eE951F17B480D369b84Cd17c20`, `escrow`
  implementation `0x49B76dE305933d75fC0eAd6ef090F555bcCD9735` — the
  escrow path stays **experimental** until the recorded testnet E2E
  (v0.16.0 Track 132) lands.
- `releases: []` until the first digest pin (Track 107) — the entry is
  visible but not installable yet.

### `llama-cpp` / `llama-cpp-vulkan` (ondemand)

[llama.cpp](https://github.com/ggml-org/llama.cpp) — a local OpenAI-compatible
LLM server (`llama-server`). On-demand: `module start` launches it once and an
exit is final — no restart supervision. `llama-cpp` is the CPU build (macOS
binaries already carry Metal); `llama-cpp-vulkan` is the discrete-GPU variant
for linux/windows hosts with a working Vulkan driver. CUDA is deferred —
upstream ships it as a companion `cudart` archive pair a single release pin
cannot express yet.

| Field | Secret | Required | Default |
| ----- | ------ | -------- | ------- |
| `model_url` | yes | yes | — |
| `model_sha256` | no | yes | — |
| `model_path` | no | no | `{module_dir}/models/model.gguf` |
| `port` | no | no | `8080` |
| `ctx_size` / `threads` / `gpu_layers` | no | no | `4096` / — / `0` (`99` on vulkan) |
| `api_key` | yes | no | — (lands in argv — no env-secret support upstream) |
| `mlock` / `flash_attn` | no | no | off / `true` |
| `extra_args` | no | no | — (whitespace-split, appended verbatim) |

The model weights are a **declared fetch**: before every start the module
downloads `model_url` to `model_path`, verifies it against `model_sha256`
(sha256 is required whenever a URL resolves — fetches are never TLS-only),
and skips the download entirely when the `<dest>.digest` sidecar already
matches. `model_url` is a secret — it may embed a signed-token URL. Archives
keep their upstream layout (`binary_path`): the nested `llama-bNNNNN/` dir
on linux/macos, flat on windows, so the co-packaged shared libraries resolve
next to the binary.

```bash
rhizome module install llama-cpp
rhizome module set --secret llama-cpp model_url=https://…/model.gguf
rhizome module set llama-cpp model_sha256=<lowercase hex>
rhizome module start llama-cpp
# OpenAI-compatible API: http://127.0.0.1:8080/v1  (health: GET /health)
```

Point Rhizome's provider config at `http://127.0.0.1:{port}/v1` once
`/health` reports ready. Platforms: cpu on linux amd64/arm64, darwin
amd64/arm64, windows amd64/arm64; vulkan on linux amd64/arm64 +
windows amd64.

## Security model

- Releases are **pinned** in the catalog: exact version + build plus a
  per-platform sha256 or sha512 digest (whatever upstream publishes — e.g.
  nimbus ships sha512). Install downloads from the upstream repo, verifies
  the digest, then extracts; tampered or truncated downloads are rejected.
- No user-supplied download URLs — the catalog is the only source of truth.
- `.tar.gz` and `.zip` archives extract under traversal/size guards (no
  `..`/absolute members, no symlinks/hardlinks, per-member size cap); the
  module binary is flattened to the version-dir root unless the entry
  declares `binary_path` (schema v5), which keeps the archive layout so
  co-packaged shared libraries resolve (llama.cpp's nested dir).
- Catalog `run` blocks may declare one-time `init_args` (guarded by
  `init_marker`) and per-launch `setup_args` for repo/config writes —
  kubo uses these for `ipfs init` and `ipfs config Addresses.*`.
- `{module_dir}` in a catalog field default expands to the module's
  directory, keeping data paths (helios `data_dir`, kubo `repo_dir`)
  under module-owned state.
- Module directories are `0700`; secrets never touch `config.json`.
- `module-state.json` under the module dir persists last-known status so the
  Modules page and `module list` report accurately when the daemon is down.
- **Upstream signatures (catalog schema v3)** — catalog entries may declare
  a `signature` per release pin (`cosign-blob`, `minisign`, or `gpg` kind,
  with a detached signature URL + public key). When declared, install
  verifies the signature after the digest check and fails closed; a catalog
  declaring signatures is rejected entirely by binaries too old to verify
  them. Today no upstream tenant publishes signatures, so this is
  mechanism + verified fixtures — the digest remains the mandatory floor.
- Adding a new module (e.g. a llama.cpp inference daemon): see
  `docs/design/llama-cpp-module.md` for the catalog-authoring pattern —
  extraction shape, weights trust story, and GPU/accelerant fields.
- **Stream bridge (catalog schema v4)** — modules that declare
  `protocols` get a loopback bridge: per-module bearer tokens (`0600`,
  minted at install, rotated on reinstall, injected via
  `RHIZOME_BRIDGE_TOKEN`) gate every outbound dial and every inbound
  splice; a token resolves to exactly one module and its declared
  allowlist. Inbound peer streams additionally require the peer to be in
  `mesh.trusted_peers` — refused before a single byte reaches the module.
- **Declared fetches (catalog schema v5)** — an entry's `fetch` list is
  resolved before every start: each URL is field-templated (`{model_url}`),
  always digest-pinned (sha256 is required when a URL resolves), HTTPS-only,
  and skipped when the `<dest>.digest` sidecar already matches. Destinations
  must be absolute — keep them under `{module_dir}` but outside `<version>/`
  so binary upgrades never re-download weights.

Daemon-kind modules emit `module.started`/`module.stopped`/`module.crashed`
events (plus `installed`/`uninstalled`/`enabled`/`disabled`), visible in the
dashboard's mesh activity feed and `rhizome mesh activity`.

## API

Daemon (bearer auth): `GET /modules`, `GET /modules/<id>`,
`GET /modules/<id>/logs?tail=N`, `POST /modules/<id>` (`{"action": "start"}`,
`"stop"`, `"restart"`, `"enable"`, `"disable"`, `"install"` + `version`,
`"uninstall"`), `PUT /modules/<id>/fields`, `PUT /modules/<id>/secrets`.
The launcher proxies the same shapes under `/api/modules*` and falls back to
local state for reads/install when no daemon is running.
