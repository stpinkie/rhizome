# Companion Modules

Companion modules extend Rhizome with sidecar binaries that don't grow the
base binary. They install under `~/.rhizome/modules/<id>/<version>/`, are
verified against pinned release digests, and (for daemon-kind modules) are
supervised by `rhizome daemon` with restart-on-crash and bounded logs.

Three module kinds:

| Kind       | Lifecycle                                                          |
| ---------- | ------------------------------------------------------------------ |
| `daemon`   | Long-running supervised process; autostarts with `rhizome daemon`.    |
| `ondemand` | Binary spawned per use; installed and health-checked, not supervised. |
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
serving module points at `rhizome module status rhizome-market`. The API
payload schemas are defined by the market tracks (sell-side, buy-side,
escrow).

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

## Security model

- Releases are **pinned** in the catalog: exact version + build plus a
  per-platform sha256 or sha512 digest (whatever upstream publishes — e.g.
  nimbus ships sha512). Install downloads from the upstream repo, verifies
  the digest, then extracts; tampered or truncated downloads are rejected.
- No user-supplied download URLs — the catalog is the only source of truth.
- `.tar.gz` and `.zip` archives extract under traversal/size guards (no
  `..`/absolute members, no symlinks, per-member size cap); the module
  binary is flattened to the version-dir root.
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
