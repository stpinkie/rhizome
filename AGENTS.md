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
- `rhizome network saved-peers [--json]` — list all saved peers (trusted + bootstrap) merged by peer id.
- `rhizome network trust <peer-id>` / `rhizome network untrust <peer-id>` — manage the mesh trusted peers list.
- `rhizome network remove <peer-id>` — remove a peer from both `mesh.trusted_peers` and matching `mesh.bootstrap_peers`.
- `rhizome network delegate <peer-multiaddr> <agent-id> <task>` — synchronously delegate a task to a remote peer agent.
- `rhizome network spawn <peer-multiaddr> <agent-id> <task>` — asynchronously spawn a task on a remote peer agent.
- `rhizome network task submit|status|result|cancel|list <peer-multiaddr> …` — manage asynchronous remote tasks over `/rhizome/agent-task/1.0.0`. The same commands are mirrored under `rhizome mesh task`.
- `rhizome sync status|log|commit|pull|push` — manage the workspace git repo. `sync status` shows HEAD, branch, workspace state, conflicts, last error, and per-peer heads (`--json` for machine-readable).
- `rhizome daemon` — start a long-running P2P node, workspace syncer, agent gateway, and (when enabled) the decentralised mesh.
  - `--no-dht` disables public DHT discovery.
  - `--no-gateway` starts the P2P node and syncer without the HTTP gateway.
  - `--sync-commit-interval` and `--sync-announce-interval` tune auto-sync.

## Web Backend Network API

The web console (the launcher) exposes authenticated JSON endpoints that wrap `rhizome network status` so the dashboard can display live mesh/DHT status:

- `GET /api/network/peers` — start a temporary libp2p node and return connected peers with trust status and advertised capabilities.
  - Query parameters: `bootstrap` (repeatable), `timeout` (e.g. `10s`), `listen` (repeatable).
- `GET /api/network/dht` — start a temporary libp2p node and return the DHT status snapshot.
  - Query parameters: `bootstrap` (repeatable), `timeout` (e.g. `10s`), `listen` (repeatable).
- `GET /api/network/saved-peers` — list persistent mesh peers from `mesh.trusted_peers` and `mesh.bootstrap_peers`, merged by peer id and augmented with live connection/capability status when the daemon is running.
- `POST /api/network/saved-peers?action=untrust&peer=<peer-id>` — remove the peer from runtime trust and `mesh.trusted_peers`.
- `DELETE /api/network/saved-peers?peer=<peer-id>` — remove the peer from runtime trust, `mesh.trusted_peers`, and any matching `mesh.bootstrap_peers`.

The dashboard has a **Network** page (`/network`) that visualizes these endpoints: it shows connected peers with trust/capability badges, a DHT status snapshot, a Saved Peers panel with Untrust/Remove actions, optional bootstrap overrides, and auto-refreshes every 60 seconds.

Both endpoints require a valid node identity and use the launcher's `RHIZOME_HOME` and `RHIZOME_CONFIG` automatically. Results are cached for 5 seconds to avoid spawning multiple overlapping nodes.

## Key Packages

- `pkg/rhizome/identity` — BIP39/SLIP-0010 Ed25519 node identity, persistence, and Ed25519 signing; now supports OS keyring and passphrase encryption.
- `pkg/rhizome/network` — libp2p host, mDNS discovery, bootstrap, ping, and public DHT discovery.
- `pkg/rhizome/sync` — workspace Git sync, packfile transport, file watcher, and three-way merge.
- `pkg/rhizome/merge` — diff3-based file and tree merging.
- `pkg/rhizome/agentrpc` — libp2p request/response framing for remote agent tasks (`/rhizome/agent/1.0.0`), with signed nonce+timestamp replay fields and a bounded idempotency cache.
- `pkg/rhizome/agenttask` — asynchronous task protocol (`/rhizome/agent-task/1.0.0`): submit/status/result(long-poll)/cancel/list.
- `pkg/rhizome/mesh` — peer capability exchange (signed manifests), trust, remote `delegate`/`spawn`, per-peer ACL + rate limits, replay protection, and the audit trail (`~/.rhizome/mesh-audit.jsonl`).
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
    "remote_timeout": "5m",
    "request_max_skew": "2m",
    "rate_limit_per_peer": 30,
    "rate_limit_global": 300,
    "audit_log": true,
    "require_signed_caps": true,
    "acl": [
      {
        "peer_id": "12D3KooW...",
        "allow_delegate": true,
        "allow_spawn": false,
        "agents": ["main"],
        "rate_limit": 10
      }
    ]
  }
}
```

- `request_max_skew` — max accepted clock difference for signed request timestamps (replay protection window).
- `rate_limit_per_peer` / `rate_limit_global` — remote request caps in requests per minute (0 = unlimited).
- `audit_log` — append-only `~/.rhizome/mesh-audit.jsonl` trail (10 MB × 3 rotation); a `mesh.remote.audit` runtime event is always emitted.
- `require_signed_caps` — reject unsigned capability manifests (default `true`); set `false` to accept unsigned manifests from trusted peers. A `mesh.cap.unsigned` event is emitted either way.
- `acl` — per-peer overrides: `allow_delegate`/`allow_spawn` fall back to the global flags when omitted; `agents` restricts which agent ids the peer may run (`"*"` for all); `rate_limit` overrides the per-peer cap (negative = unlimited).
- Rejected remote calls carry machine-readable prefixes: `forbidden:` (ACL) and `rate_limited:`.

### NAT traversal (v0.5.0)

- `nat_traversal` (default true) — AutoNATv2, hole punching, and circuit-relay v2 client + AutoRelay reservations.
- `relay_service` / `nat_service` (default true) — publicly reachable nodes volunteer as relays and AutoNAT dial-back servers.
- `static_relays` — relay multiaddrs to always reserve.
- `force_reachability` — `"public"`/`"private"` override when AutoNAT is wrong.
- `public_addrs` — extra multiaddrs advertised to peers (e.g. a static public endpoint).
- `network status` reports `reachability`, `addrs`, and `relayed_addrs`; relayed (`Limited`) connections count as usable.

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

## Saved Peer Management (v0.4.7)

- The daemon exposes `GET/POST/DELETE /network/saved-peers` on its gateway HTTP mux.
  - `GET` returns merged `mesh.trusted_peers` and `mesh.bootstrap_peers`, optionally merged with live connection, trust, and capability state.
  - `POST ?action=untrust&peer=<peer-id>` removes the peer from the runtime trust set and `mesh.trusted_peers`.
  - `DELETE ?peer=<peer-id>` untrusts the peer, disconnects it, removes it from `mesh.trusted_peers`, and removes all matching `mesh.bootstrap_peers`.
- The launcher proxies these calls to the daemon when it is running; otherwise it edits `config.json` directly and serves the saved list from the file.
- CLI adds `rhizome network saved-peers`/`rhizome mesh saved-peers` and `rhizome network remove`/`rhizome mesh remove <peer-id>`.
- The Network dashboard has a **Saved Peers** panel with Untrust and Remove actions; Remove shows a confirmation dialog.

## Live Network Status API (v0.4.4)

- `GET /api/network/status` on the launcher returns a combined mesh/DHT snapshot.
- When a `rhizome daemon` is running, the backend proxies to the daemon's `GET /network/status` endpoint, protected by the PID file token (`Authorization: Bearer <pid-token>`).
- If the daemon is unavailable, the launcher falls back to a single `rhizome network status --peers --dht --json` CLI spawn.
- The daemon source lives in `pkg/gateway/networkapi.go` and reads from `Mesh.NetworkStatus()` in `pkg/rhizome/mesh`.
- Old `GET /api/network/peers` and `GET /api/network/dht` endpoints remain as compatibility aliases over the combined response.
- The frontend Network page now uses one `useQuery` for `getNetworkStatus` and renders both panels from the same response.

## Daemon Bootstrap Override (v0.4.5)

- `GET /network/status` on the daemon now honors `?bootstrap=<multiaddr>&timeout=<duration>`.
- The daemon validates each multiaddr and attempts `Mesh.Connect` for it before building the status snapshot. The trust set is not modified.
- `GET /api/network/status` on the launcher now forwards `bootstrap` and `timeout` to the daemon and only falls back to the CLI for `?listen=...` overrides or when the daemon is unavailable.
- A custom `?listen=...` still requires a temporary node because the daemon's bound listeners cannot be changed at runtime.

## Bootstrap Trust and Persistence (v0.4.6)

- `GET /network/status` on the daemon now accepts `?trust=true` alongside `?bootstrap=...`.
- When `trust=true` and the bootstrap succeeds, the daemon:
  - trusts the peer (`Mesh.TrustPeer`),
  - eagerly fetches and stores the remote capability (`Mesh.TrustAndDiscover`),
  - advertises the local capability back to the peer,
  - persists the multiaddr to `mesh.bootstrap_peers` and the peer ID to `mesh.trusted_peers` in `config.json`.
- `GET /api/network/status` on the launcher forwards `trust=true` to the daemon. If the daemon is unavailable, the launcher falls back to `rhizome network status ... --trust`, which also persists the bootstrapped peer.
- A custom `?listen=...` still forces the CLI fallback, and the CLI fallback honors `--trust`.
- The Network dashboard has a "Trust & remember this peer" toggle that adds `trust=true` to the status query.
- On the next daemon startup, `mesh.bootstrap_peers` are merged into the libp2p bootstrap list and `mesh.trusted_peers` are loaded into the mesh trust set, so saved peers reconnect and are trusted automatically.

## Provider Protocol Refactor (v0.4.8)

- LLM providers are now driven by a protocol catalog in `pkg/providers/model_catalog.go`.
- `ModelProviderOption` carries `ProtocolFamily`, `StripModelPrefix`, `ExtraBodyDefaults`, `EmptyAPIKeyAllowed`, and construction flags.
- `pkg/providers/factory_provider.go` dispatches by `ProtocolFamily`; OpenAI-compatible providers are created with one helper instead of a per-provider `switch`.
- Anthropic Claude uses the native Messages protocol (`anthropic-messages`); the old `pkg/providers/anthropic` SDK wrapper and `pkg/providers/httpapi` facade have been removed.
- Gemini lives in its own package: `pkg/providers/gemini`.
- First-class local protocols (`ollama`, `vllm`, `lmstudio`, `litellm`) set `Local: true` and `EmptyAPIKeyAllowed: true`; the frontend shows them as "local" and allows blank API keys.
- `openai_compat.Provider` strips the leading `provider/` segment when configured (`WithStripModelPrefix`) and preserves prefixes for `openrouter` endpoints.
- When editing provider code, run `go build -tags goolm,stdjson ./...` and `go test -tags goolm,stdjson ./pkg/... ./web/...` (full `./...` includes slow `cmd/membench`).
