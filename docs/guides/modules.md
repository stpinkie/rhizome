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

## Catalog

### `nimbus-verified-proxy` (daemon)

Verified Ethereum JSON-RPC: the [nimbus-eth1](https://github.com/status-im/nimbus-eth1)
consensus light client serving execution-API responses proven against
beacon-chain proofs. You supply an untrusted execution endpoint and a beacon
API — the proxy trusts neither.

| Field                 | Secret | Required | Default                  |
| --------------------- | ------ | -------- | ------------------------ |
| `execution_api_url`   | yes    | yes      | —                        |
| `beacon_api_url`      | yes    | yes      | —                        |
| `trusted_block_root`  | no     | yes      | —                        |
| `network`             | no     | no       | `mainnet`                |
| `listen_url`          | no     | no       | `http://127.0.0.1:8545`  |

Fetch a `trusted_block_root` from your beacon API at
`/eth/v1/beacon/headers/finalized` — it must be recent. The light client
syncs on first start; `eth_syncing` reports progress. Platforms:
linux amd64/arm64, windows amd64, macos arm64 (no darwin-amd64 build).

```bash
rhizome module install nimbus-verified-proxy
rhizome module set --secret nimbus-verified-proxy \
  execution_api_url=https://mainnet.infura.io/v3/KEY \
  beacon_api_url=https://your-beacon-node
rhizome module set nimbus-verified-proxy trusted_block_root=0x…
rhizome module enable nimbus-verified-proxy
rhizome module start nimbus-verified-proxy   # or restart the daemon
```

Once healthy, `listen_url` serves a verified `eth_*` JSON-RPC endpoint for
your scripts and dapps.

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
- Module directories are `0700`; secrets never touch `config.json`.
- `module-state.json` under the module dir persists last-known status so the
  Modules page and `module list` report accurately when the daemon is down.

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
