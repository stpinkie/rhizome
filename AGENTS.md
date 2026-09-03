# Agent Notes for Rhizome

This is a hard fork/rebrand of PicoClaw. Module path: `github.com/stpinkie/rhizome`.

## Build & Test

- Always build/test with the tags used in `Makefile`:

```powershell
$env:CGO_ENABLED='0'
go build -tags goolm,stdjson ./...
go test -tags goolm,stdjson ./...
```

- Some packages (e.g. `maunium.net/go/mautrix/crypto/libolm`) require CGO unless the `goolm` build tag is set. The `stdjson` tag selects the standard `encoding/json` fallback.
- Windows-specific: `CGO_ENABLED=0` avoids MinGW linker issues when the user home path contains spaces.
- Cache / scratch locations on Windows: the CI and local build commands use `D:\tmp` to avoid filling `C:\tmp`:

```powershell
$env:GOCACHE='D:\tmp\rhizome-gocache'
$env:GOMODCACHE='D:\tmp\rhizome-gomodcache'
$env:TEMP='D:\tmp'
$env:TMP='D:\tmp'
```

## Rhizome P2P Commands

- `rhizome network onboard` — create a node identity from a BIP39 mnemonic (now supports `--generate` and `--encrypt {keyring|passphrase|none}`).
- `rhizome network status` — show the saved node identity (`--json` for machine-readable output).
  - `rhizome network status --peers` — start a temporary node and show connected peers with capabilities.
  - `rhizome network status --dht` — start a temporary node and show public DHT status.
- `rhizome mesh status` — shortcut for `rhizome network status --peers --dht`.
- `rhizome mesh peers` — shortcut for `rhizome network status --peers`.
- `rhizome network ping <multiaddr>` — start a temporary libp2p host and ping a peer.
- `rhizome network peers` — list trusted peers from config.
- `rhizome network trust <peer-id>` / `rhizome network untrust <peer-id>` — manage the mesh trusted peers list.
- `rhizome network delegate <peer-multiaddr> <agent-id> <task>` — synchronously delegate a task to a remote peer agent.
- `rhizome network spawn <peer-multiaddr> <agent-id> <task>` — asynchronously spawn a task on a remote peer agent.
- `rhizome sync status|log|commit|pull|push` — manage the workspace git repo.
- `rhizome daemon` — start a long-running P2P node, workspace syncer, agent gateway, and (when enabled) the decentralised mesh.
  - `--no-dht` disables public DHT discovery.
  - `--no-gateway` starts the P2P node and syncer without the HTTP gateway.
  - `--sync-commit-interval` and `--sync-announce-interval` tune auto-sync.

## Key Packages

- `pkg/rhizome/identity` — BIP39/SLIP-0010 Ed25519 node identity, persistence, and Ed25519 signing; now supports OS keyring and passphrase encryption.
- `pkg/rhizome/network` — libp2p host, mDNS discovery, bootstrap, ping, and public DHT discovery.
- `pkg/rhizome/sync` — workspace Git sync, packfile transport, file watcher, and three-way merge.
- `pkg/rhizome/merge` — diff3-based file and tree merging.
- `pkg/rhizome/agentrpc` — libp2p request/response framing for remote agent tasks.
- `pkg/rhizome/mesh` — peer capability exchange, trust, and remote `delegate`/`spawn`.
- `cmd/rhizome/internal/network`, `cmd/rhizome/internal/daemon`, and `cmd/rhizome/internal/sync` — CLI commands.

## Mesh Configuration

Add a `mesh` section to `config.json`:

```json
{
  "mesh": {
    "enabled": true,
    "trusted_peers": ["12D3KooW..."],
    "allow_remote_delegate": true,
    "allow_remote_spawn": true,
    "remote_timeout": "5m"
  }
}
```

When `mesh.enabled` is true, `rhizome daemon` advertises local capabilities over `/rhizome/caps/1.0.0`, accepts remote agent requests over `/rhizome/agent/1.0.0` from trusted peers, and publishes mesh/DHT runtime events to the shared event bus.

## DHT Configuration

The public IPFS DHT is enabled by default. The daemon provides and looks up a rendezvous CID so other Rhizome nodes can discover each other without explicit bootstrap addresses.

```json
{
  "mesh": {
    "dht_enabled": true,
    "dht_rendezvous": "/rhizome/network/1.0.0",
    "dht_server": false,
    "dht_reprovide_interval": "10m"
  }
}
```

- `dht_enabled` — turn public DHT discovery on or off.
- `dht_server` — run a DHT server (helps the public DHT) instead of a client-only node.
- `dht_bootstrap` — list of custom DHT bootstrap multiaddrs.
- `dht_rendezvous` — string used to derive the DHT rendezvous key.
- `dht_reprovide_interval` — how often to re-advertise the rendezvous record.

Use `rhizome daemon --no-dht` to disable DHT discovery for a single run. Inspect live DHT state with `rhizome network status --dht` or `rhizome mesh status`.

## Identity Encryption

By default `rhizome network onboard` prompts for an encryption method. You can also use:

- `rhizome network onboard --encrypt keyring` — store the private key in the OS credential store (macOS Keychain, Windows Credential Manager, or Linux Secret Service).
- `rhizome network onboard --encrypt passphrase` — derive the key from a passphrase with scrypt and store it in `node.json`.
- `rhizome network onboard --encrypt none` — keep the legacy unencrypted `node.json`.

To load an encrypted identity in a non-interactive environment, set `RHIZOME_IDENTITY_PASSPHRASE`. When the keyring is unavailable, `rhizome daemon` and the `network`/`sync` commands will fall back to the passphrase.

Legacy unencrypted `node.json` files continue to load without any changes.

## BIP39 Onboarding

`rhizome network onboard --generate` creates a fresh 24-word BIP39 mnemonic. The `--non-interactive` and `--yes` flags allow fully scripted onboarding:

```powershell
$env:RHIZOME_HOME = 'C:\path\to\home'
rhizome network onboard --generate --name a --node-index 0 --encrypt none --yes --non-interactive
```

The same mnemonic and a different `--node-index` produce a different Rhizome peer. Re-using an existing index in the same `RHIZOME_HOME` is rejected unless `--yes` is used to overwrite.

## Integration Testing

The P2P mesh integration test builds `rhizome`, starts two real daemons, pings a peer, and verifies workspace sync:

```bash
make integration-mesh
```

On Windows the equivalent is:

```powershell
.\scripts\integration-mesh.ps1
```

This script is also run in CI on `ubuntu-latest` as the `mesh-integration` job.

## Configuration / Environment

- `RHIZOME_HOME` overrides the home directory (default `~/.rhizome`).
- `RHIZOME_CONFIG` overrides the config file path.
- `RHIZOME_IDENTITY_PASSPHRASE` unlocks an encrypted identity without prompting.

## Track 7 — Timeouts & P2P Resilience

- The `timeouts` section in `config.json` (and `RHIZOME_TIMEOUTS_*` env vars) controls all user-visible wait times (LLM, tools, P2P, sync, HTTP, media, gateway, cron, evolution, health, heartbeat, updater, channels).
- `pkg/rhizome/stream` provides `ReliableConn`, a stop-and-wait framing layer with per-frame CRC, ACK/NACK, and retransmission.
- `pkg/rhizome/network` now monitors peer connect/disconnect events and runs an auto-reconnect loop for known peers.
- `pkg/rhizome/agentrpc` uses `ReliableConn`, caches results by `CorrelationID` for idempotent retries, and `Mesh.CallRemote` retries up to three times with reconnect.
- `pkg/rhizome/sync` packfile and announce traffic also runs over `ReliableConn` with retry.
- Capability exchange in `pkg/rhizome/mesh` is re-advertised eagerly when a trusted peer connects.
- Libraries use `config.Global()` to resolve timeouts after the daemon or gateway calls `config.SetGlobal(cfg)` during startup.
- The Web UI has a `Timeouts` section under Config where users can edit key duration strings (LLM, tools, HTTP, media, gateway, agent, mesh, sync, network/DHT).

## Web Build

If `pnpm` is not installed globally, use it through `npx`:

```powershell
npx pnpm install
npx pnpm build
```

## Upstream

- `sipeed/picoclaw` is the upstream remote for cherry-picking future fixes.

## Cross-Agent Testing

- The `.rhizome-tests/` dot-folder contains a testing skill and report templates for cloud agents.
- Point agents at `.rhizome-tests/SKILL.md`.
- After running the suite, agents write a Markdown and JSON report to `.rhizome-tests/reports/`.
- The GitHub Actions matrix in `.github/workflows/pr.yml` runs the same core checks on Linux, Windows, and web.
