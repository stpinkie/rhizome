# Agent Notes for Rhizome

This is a hard fork/rebrand of PicoClaw. Module path: `github.com/stpinkie/rhizome`.

- Remotes: `origin` = `github.com/stpinkie/rhizome` (canonical repo — all PRs,
  issues, releases live here); `upstream` = `github.com/sipeed/picoclaw` (the
  project this forked from — sync only, **never open PRs against it**). `gh`
  resolves the base repo to upstream when both remotes exist, so always pass
  `--repo stpinkie/rhizome` to `gh` commands (`gh pr create`, `gh pr view`,
  `gh issue`, …), or run `gh repo set-default stpinkie/rhizome` once per clone.

## Build & Test

- Always build/test with the tags used in `Makefile`:

```powershell
$env:CGO_ENABLED='0'
go build -tags goolm,stdjson ./...
# -p 1 runs packages sequentially, avoiding libp2p/mDNS port and scheduler
# contention that can make the full suite flaky on local Windows runners.
go test -p 1 -tags goolm,stdjson ./...
```

- Some packages (e.g. `maunium.net/go/mautrix/crypto/libolm`) require CGO unless the `goolm` build tag is set. The `stdjson` tag selects the standard `encoding/json` fallback.
- Lint with `make lint` (golangci-lint with the build tags + `scripts/lint-docs.sh`), or directly:

```powershell
golangci-lint run --build-tags goolm,stdjson ./...
```

Use golangci-lint v2.13.2 (the version pinned in `.github/workflows/pr.yml`); older releases do not know linters such as `exhaustruct_v5` referenced in `.golangci.yaml`.
- Windows-specific: `CGO_ENABLED=0` avoids MinGW linker issues when the user home path contains spaces.
- Cache / scratch locations on Windows: the CI and local build commands use `D:\tmp` to avoid filling `C:\tmp`:

```powershell
$env:GOCACHE='D:\tmp\rhizome-gocache'
$env:GOMODCACHE='D:\tmp\rhizome-gomodcache'
$env:TEMP='D:\tmp'
$env:TMP='D:\tmp'
```

### Writing networked tests

Any test that calls `network.NewNode` must isolate itself — parallel test
binaries otherwise share the LAN and can cross-connect:

- Identity: `testutil.NewIdentity(t)` (fresh random Ed25519). Never reuse
  the shared `abandon…about` mnemonic for a networked node — two processes
  holding the same key authenticate as the same peer.
- Config: set `DisableMDNS: true` and `DisableNATPortMap: true` (mDNS
  cross-discovers foreign test nodes; NAT-PMP/UPnP/SSDP probe the LAN for
  the process lifetime and just burn CPU on loopback).
- Wire peers explicitly via `BootstrapPeers` — the tests' connectivity is
  intentional, not ambient.
- `TestMDNSDiscoversPeer` (`pkg/rhizome/network/host_test.go`) covers the
  real discovery path under a per-run `MDNSServiceName`.
- `scripts/flake-hunt.sh [RUNS]` (`.ps1` on Windows) repeats the networked
  packages under default `-p` parallelism and reports per-test pass rates;
  `pr.yml`'s `test-parallel` job is the same signal, non-blocking, per PR.

## Validation on resource-constrained Linux VMs

The full release gate (`go build ./...`, `go test ./...`, `golangci-lint ./...`,
`make build-all`) needs a runner with ≥ 10 GB free disk and ≥ 4 GB of memory
for `CGO_ENABLED=1` race builds.

For small Linux VMs with only ~4 GB of root-FS and ~2 GB of memory, use the
reduced validation path instead:

```bash
bash ./scripts/validate-small-vm.sh
```

This builds the `cmd/rhizome` binary, runs `go vet`/`go test` on
`pkg/evolution`, `pkg/rhizome`, `pkg/media`, and `pkg/tools/fs`, runs
`make lint-slim`, then builds the core cross-compile targets. It frees the Go
build cache and cross-compile binaries when it detects it is on the small
overlay, and reuses the cache when `GOCACHE`/`GOMODCACHE` are placed on a large
host mount. `GOTMPDIR` and `TMPDIR` are also redirected under `GOCACHE` so Go's
`$WORK` directories and test temp files stay off the overlay. It is a
pre-flight subset, not a replacement for the full release gate.

If the host provides a large mount (for example `/var/lib/docker/rhizome-cache/`),
use it for caches and build output:

```bash
export GOCACHE=/var/lib/docker/rhizome-cache/gocache
export GOMODCACHE=/var/lib/docker/rhizome-cache/gomodcache
export RHIZOME_BUILD_DIR=/var/lib/docker/rhizome-cache/build
export RHIZOME_BUILD_CLEAN=0
bash ./scripts/validate-small-vm.sh
```

This keeps the 4 GB overlay from filling and makes the cross-compile phase
much faster because the Go build cache is reused across targets.

## Cutting a Release

1. Push a `v*` tag — either `gh workflow run create-tag.yml -f tag=vX.Y.Z`
   or `git push origin vX.Y.Z`. Both paths run `release.yml`: a manual tag
   push fires it directly; `create-tag.yml` dispatches it explicitly because
   `GITHUB_TOKEN` pushes never trigger `push` events.
2. `release.yml` (GoReleaser, ~2–3 h) publishes the GitHub release with all
   platform assets, GHCR images (`ghcr.io/<owner>/rhizome:<tag>` + `:latest`
   and the `-launcher` variants), the Android universal zip, and deb/rpm
   packages. `vX.Y.Z-suffix` tags are marked pre-release automatically
   (`prerelease: auto` in `.goreleaser.yaml`); `workflow_dispatch` runs can
   still force draft/prerelease via inputs.
3. Verify: the release page has all archives, `docker pull
   ghcr.io/<owner>/rhizome:vX.Y.Z` works, and `rhizome version` inside an
   artifact reports the tag.

Registry & secrets posture:

- **GHCR is canonical** — works via `GITHUB_TOKEN` on every run.
- **Docker Hub** images are only pushed when `DOCKERHUB_USERNAME`/
  `DOCKERHUB_TOKEN` secrets exist; otherwise `docker.io` targets are
  stripped from the run (`release.yml` and `nightly.yml` share the pattern).
- **macOS** binaries ship unsigned until `MACOS_SIGN_*`/`MACOS_NOTARY_*`
  secrets exist (`notarize.macos` in `.goreleaser.yaml` is env-gated).
- **Tags `v0.4.2`–`v0.9.0` have no published artifacts** — no backfill, by
  decision; `v0.9.1` is the first tag released by the current pipeline.

Anti-rot:

- `pr.yml` runs `goreleaser check` on every PR.
- `release-smoke.yml` builds the full matrix weekly
  (`goreleaser build --snapshot --clean`, Mondays 06:00 UTC, no publish,
  no NDK).
- GoReleaser is pinned to `~> v2.18` in all three workflows — bump
  deliberately, after a green smoke run.
- `nightly.yml` is manual-only (cron commented); it runs without Docker Hub
  creds and publishes `nightly`/`nightly-launcher` GHCR images but no
  GitHub release.

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
- `rhizome network route <agent-id> <task>` / `rhizome mesh route` — pick the best connected, trusted peer for an agent id (capability + load aware, via `Mesh.PickPeer`) and dispatch the task. `--sync` delegates synchronously; `--wait <dur>` long-polls the result after submitting.
- `rhizome network audit` / `rhizome mesh audit` — print the tail of the local mesh audit trail (`~/.rhizome/mesh-audit.jsonl`); `--tail N`, `--json`.
- `rhizome mesh activity [--tail N] [--json]` — recent `mesh.*`/`swarm.*` runtime events from the daemon's in-memory feed (daemon required; proxies `GET /network/activity`). `rhizome mesh peer <id>` — live detail for a connected peer: connections (transport/direction/streams), score, capabilities, manifest fingerprints, recent tasks (daemon required).
 - `rhizome mesh skill list <peer-id>` / `rhizome mesh skill pull <peer-id> <name>` - mesh skill distribution over `/rhizome/skill/1.0.0`: peers advertise skills from the `mesh.skill_share` allowlist (default deny-all); `pull` fetches a zip bundle over the blob protocol, guard-scans it (`pkg/guard`), and installs to `~/.rhizome/skills/<name>` with `.skill-origin.json` `origin_kind: "mesh"` + `mesh:<peer-id>` provenance (web UI shows a violet Mesh badge). Daemon endpoints `GET /network/skills?peer=<id>` and `POST /network/skills/pull`; `mesh.skill.pull/push` events + audit.
 - `rhizome network pair --create [--ttl 15m]` / `--accept <bundle>` - trust pairing over `/rhizome/pair/1.0.0`: mints a single-use, signed bundle (peer id + addrs + code + expiry) to share out of band; accepting verifies signatures, connects, and both sides persist `mesh.trusted_peers` + `mesh.bootstrap_peers`. Daemon endpoints `POST /network/pair` and `POST /network/pair/accept`; launcher proxies `/api/network/pair*`; Network page has a Pair panel. `mesh.pair.*` events; codes persist at `<RHIZOME_HOME>/pair-codes.json`.
- `rhizome network scatter <agent-id> <task>` / `rhizome mesh scatter` — fan a task out to up to `--n` capable trusted peers and aggregate results (`--strategy first|quorum|all`, `--k`, `--wait`).
- `rhizome swarm join|leave <id>` — persist a swarm membership in `swarm.memberships` (effective on next daemon start).
- `rhizome swarm list|status|members <id>` — inspect configured memberships and the saved roster (`~/.rhizome/swarms.json`).
- `rhizome swarm offer <swarm> <agent-id> <task>` / `rhizome swarm offers <swarm>` — publish/track/cancel work-queue offers on the running daemon (`--cancel <offer-id>`, `--require-agent/--require-model/--require-skill` for capability-aware claims).
- `rhizome swarm run <swarm> <goal>` — decompose a goal into dependency-aware subtasks (DAG waves, `depends_on`), dispatch them over the work queue with retries, aggregate results, and persist run records to `<RHIZOME_HOME>/swarm-runs.jsonl` (bounded). `rhizome swarm runs <swarm>` / `rhizome swarm run-status <swarm> <run-id>` inspect recorded runs; `swarm.run.subtask` events stream per-subtask lifecycle (daemon required).
- `rhizome swarm context <swarm>` — read a swarm's shared context: the coordinator-curated `context.md` plus recent member notes (`--since <rfc3339>` filters notes, `--set <file>` replaces the curated document — coordinator only). `rhizome swarm note <swarm> <content>` posts a note to the local member's shard and broadcasts it (`--kind`, `--key`, `--ttl`). Daemon required.
- `rhizome sync status|log|commit|pull|push` — manage the workspace git repo. `sync status` shows HEAD, branch, workspace state, conflicts, last error, and per-peer heads (`--json` for machine-readable).
- `rhizome daemon` — start a long-running P2P node, workspace syncer, agent gateway, and (when enabled) the decentralised mesh.
  - `--no-dht` disables public DHT discovery.
  - `--no-gateway` starts the P2P node and syncer without the HTTP gateway.
  - `--sync-commit-interval` and `--sync-announce-interval` tune auto-sync.

## Companion Modules

`pkg/modules` is the sidecar module system (v0.10.0): catalog-driven binaries
installed under `<RHIZOME_HOME>/modules/<id>/<version>/`, verified against a
catalog-pinned sha256/sha512 digest at install time, and supervised by the
daemon. Modules extend Rhizome without
growing the base binary. Kinds: `daemon` (supervised, restart-on-exit with
backoff), `ondemand` (spawned by a consumer), `config` (endpoint descriptor,
no process). `nimbus-verified-proxy` (status-im/nimbus-eth1 verified
Ethereum RPC) is the first daemon-kind entry — fields `execution_api_url`,
`trusted_block_root` (required; URLs are secrets), `p2p` (default true:
light-client sync over the beacon P2P network), `beacon_api_url` (optional
REST supplement; required when `p2p=false`), `network`, `listen_url`,
`p2p_tcp_port`/`p2p_udp_port`/`p2p_max_peers`. `ethereum-rpc` is the first
config-kind entry. Track 71 (v0.11.0) added `helios` (a16z/helios Ethereum
light client — linux/darwin, env-var config behind the `ethereum`
subcommand) and `ipfs-kubo` (IPFS node — all platforms; `.zip` extraction,
`ipfs init` behind `init_marker`, `ipfs config Addresses.*` per launch via
`setup_args`, `IPFS_PATH`/`repo_dir` under the module dir via the
`{module_dir}` placeholder; catalog schema v2).

- `rhizome module list` — catalog modules with kind/status/enabled (`--json`).
- `rhizome module status <id>` — detail: version, pid, restarts, missing fields, health.
- `rhizome module install <id> [--version v]` — download, sha256-verify, extract.
- `rhizome module uninstall <id>` — remove the module directory.
- `rhizome module enable|disable <id>` — set `modules.<id>.enabled` (daemon-kind modules autostart with the daemon).
- `rhizome module start|stop|restart <id>` — lifecycle via the running daemon (daemon required).
- `rhizome module logs <id> [--tail N]` — bounded stdout/stderr logs under the module dir.
- `rhizome module set <id> key=value…` — write `modules.<id>.fields`; `--secret` writes `.secrets` (`.security.yml`, never config.json).
- `rhizome module validate` — check the `modules` config section against the catalog.
- `rhizome module verify <id>` — re-hash the installed binary against its install-time sha256 record (drift detection; daemonless).
- `rhizome module catalog [--out f] [--sign-with-env VAR]` — emit the embedded catalog as canonical `catalog.json` (+ `.sig`); `rhizome module catalog-keygen` (hidden) generates a signing keypair.

Daemon endpoints (bearer auth): `GET /modules`, `GET /modules/<id>`,
`GET /modules/<id>/logs?tail=N`, `POST /modules/<id>` `{action,
version}` (actions include `verify`), `PUT /modules/<id>/fields`,
`PUT /modules/<id>/secrets`.
Launcher mirrors them under `/api/modules*` with a local fallback for
reads/config/install/verify when no daemon runs. Events: `module.installed`,
`module.uninstalled`, `module.started`, `module.stopped`, `module.crashed`,
`module.enabled`, `module.disabled`, `module.verify.*`, `module.catalog.*`.
`module-state.json` persists pid/restarts/last-exit so status works
daemonless. Security: HTTPS-only downloads, mandatory per-platform digest
(sha256 or sha512 — nimbus publishes sha512), no user-supplied URLs, module
dirs `0700`.

Remote index (v0.11.0, Track 70): `module_index = {enabled, url}` is a
sibling of `modules` (a key inside `modules` would decode as a module id).
When enabled, `<url>/catalog.json` + `.sig` (Ed25519 over the served bytes,
verified against the baked-in `releasePubKeyB64`) merge into the catalog —
embedded entries win id collisions; unsigned/bad-sig content is refused
outright; verified catalogs cache under `<RHIZOME_HOME>/catalog-cache/`
(1 h TTL, stale-serve on fetch failure only). Releases sign via the
`MODULE_CATALOG_SIGNING_KEY` secret; see
`docs/operations/module-catalog-signing.md`.

## Web3 Read Tools (v0.11.0, Track 68)

`pkg/web3` is a dependency-free Ethereum JSON-RPC layer; the `web3_*`
agent tools live in `pkg/tools/web3` and are **disabled by default**
(`tools.web3.enabled`). `web3_rpc` passthrough stays read-only via a
hard-coded method allowlist (`eth_call`, `eth_get*`, `net_*`, `web3_*`;
`eth_accounts` is deliberately excluded) — the signing path added in
v0.12.0 lives in separate, double-gated tools (see next section).

Endpoint resolution (`pkg/web3/endpoint.go`, `Provider`) is
operator-config only — tools never accept URLs:

1. `tools.web3.endpoint` (+ `tools.web3.api_key` Bearer, a SecureString →
   `.security.yml` + `SensitiveDataReplacer`)
2. `modules.ethereum-rpc` `fields.endpoint_url` (+ secret `api_key`)
3. Running `nimbus-verified-proxy` `listen_url`, probed via `eth_chainId`
   (probe doubles as the running+healthy check — works daemonless)

Results cache for 30 s. `tools.web3.chain_ids` (decimal list) hard-fails
on chain mismatch; `max_log_range` (default 10000) caps `web3_logs` block
width (tag bounds resolve to concrete numbers first); `allow_private_endpoints`
(default **false** — fail closed) at false routes through
`utils.CreateSafeHTTPClient`'s safe-dial SSRF guard; nimbus on loopback
needs the explicit opt-in. Transport errors are unwrapped of `url.Error`
so endpoint URLs (which can embed path keys) never reach tool output.
Guide: `docs/guides/web3.md`.

## Web3 Wallet & Signing (v0.12.0, Track 77)

`pkg/web3` also holds the write side: secp256k1 wallet keystore
(`keys.go` — AES-256-GCM per key, OS keyring master key, scrypt fallback via
`RHIZOME_WALLET_PASSPHRASE`, `RHIZOME_WALLET_KEYSOURCE` override), minimal
ABI/RLP codecs, EIP-1559+legacy tx signing (`tx.go`), EIP-191 message
signing, a policy engine (`policy.go` — from/contract/method allowlists,
per-tx and per-UTC-day wei caps) with an append-only spend ledger at
`<RHIZOME_HOME>/web3-ledger.jsonl`, and a durable pending-approval queue
(`pending.go` — `<RHIZOME_HOME>/web3/web3-pending.json`, bounded 100,
15 min TTL, expiry denies).

Signing is **double-gated**: `tools.web3.enabled` and
`tools.web3.signing.enabled` (both default false). `web3_send`/`web3_sign`/
`web3_approve` (in `pkg/tools/web3/signing.go`) never sign inline — they
evaluate policy, run the `ApproveTool` hook veto pass (normalized
`Web3ApprovalAction`, adapted in `pkg/agent/web3_hooks.go`), then enqueue a
`PendingEntry`; a human resolves it, and `PendingStore.ExecuteApproved`
signs+broadcasts (chain re-verified against the live endpoint at execution).
`web3_wallet`/`web3_pending`/`web3_send_status` are read-only under the
outer gate. Key material never appears in tool output, API responses, or UI.

- `rhizome wallet create|import|list|reveal` — daemonless keystore ops;
  `reveal` needs `--confirm` (+ `--show` for unmasked output).
- `rhizome web3 pending|approve <id>|reject <id>` — queue ops; proxies to
  the daemon when live, resolves locally otherwise.
- Daemon endpoints (bearer auth): `GET /web3/pending[/<id>]`,
  `GET /web3/wallet`, `POST /web3/approvals/<id>` `{action}`. Launcher
  mirrors them under `/api/web3/*` with daemonless fallback. The dashboard
  `/web3` page lists the wallet and resolves approvals; a sidebar badge
  shows the pending count. Events: `web3.pending`, `web3.approved`,
  `web3.rejected`, `web3.sent`, `web3.done`, `web3.failed` (surfaced in the
  mesh activity feed).

## Web3 Contracts & ENS (v0.12.0, Track 78)

Contract interaction builds on the signing stack — still no go-ethereum,
everything runs through the minimal ABI codec and the human-approval queue.

- **ABI registry** (`pkg/web3/abireg.go`): `<RHIZOME_HOME>/web3/abi/<label>.json`
  — `{label, address?, chain_ids?, abi, added_at}`, dir `0700`, ≤200 files,
  ≤512 KiB each, labels `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`. CLI:
  `rhizome web3 abi add <label> <file> [--address 0x…] [--chain-ids 1,11155111]`
  | `list` | `show` | `remove`.
- **Resolution** (`pkg/web3/resolve.go`): contract args resolve 0x literal →
  registry label → ENS name (in that order). Literal addresses pick up a
  registered ABI via `ByAddress`. `chain_ids` on an entry gates resolution
  to the live endpoint chain.
- **Tools** (`pkg/tools/web3/contract.go`): `web3_contract_call` (read-only
  `eth_call`, decodes outputs, not gated), `web3_contract_send` (policy +
  durable approval; pending `kind:"contract"` carries `selector` and a
  `label.method(args)` summary), `web3_erc20` (`info|balance|allowance` read;
  `transfer|approve` write — `amount` is decimals-aware token units or
  `amount_units` raw base units, `"unlimited"` = uint256 max),
  `web3_erc721` (`name|symbol|ownerOf|balanceOf|tokenURI` read;
  `transferFrom|safeTransferFrom` write), `web3_ens` (`name` forward /
  `address` reverse). ERC helpers ship embedded minimal ABIs — no registry
  entry needed.
- **Policy**: `allow_contracts` accepts registry labels (matched against the
  bound address); `allow_methods` accepts raw `0x` selectors or
  `label:method` / `0xaddr:method` — a bound label scopes the method to that
  contract. Unresolvable entries never match.
- **ENS** (`pkg/web3/ens.go`): canonical registry→resolver→record two-hop
  via `eth_call` (registry `0x00000000000C2E4eC4a74a1268e2c4358E8D1170` on
  mainnet/sepolia/holesky/hoodi; other chains fail clearly). Namehash is
  computed locally. CLI: `rhizome web3 ens <name> [--reverse 0x…]`.
- Events: `web3.contract.call`, `web3.contract.send`,
  `web3.contract.rejected` — picked up by the activity feed's `web3.*`
  prefix.

## Web3 Wallet Ops, Watches & Channel Approvals (v0.12.0, Track 79)

- **Wallet CLI**: `rhizome wallet set-default <address>` | `rename
  <address> <label>` | `remove <address>` (with `--confirm`; the default
  signer is what tools use when `from` is omitted). `GET /web3/wallet`
  accepts `?balances=true` → per-address `balance_wei`; the launcher
  proxy preserves query strings and mirrors the behavior daemonless, and
  the dashboard `/web3` page shows ETH balances.
- **Log watches** (`pkg/web3/watch.go`): `tools.web3.watches[]` =
  `{name, contract, topics?, interval_seconds?}` — contract resolves
  0x/label/ENS at daemon start; daemon-side poller emits `web3.event`
  runtime events (block/tx/index/topics/data), cursors persist to
  `<RHIZOME_HOME>/web3/watches.json` so restarts resume rather than
  replay. Bounds: ≤8 watches, 30s floor / 60s default interval, range
  capped by `max_log_range`, ≤50 logs per tick, first-run lookback 500
  blocks. `tools.web3.watch_confirmations` delays emission until logs
  are N blocks deep (default 0 = at head; cap 64). The `web3_watch`
  agent tool lists/adds/removes watches — writes persist via
  `SaveConfig` and take effect on daemon restart; watch lifecycle is
  tied to daemon start/stop (`Web3WatchCancel`).
- **Channel approvals**: `tools.web3.approval_channels` scopes where
  `/web3 pending|approve <id>|reject <id>` builtin commands work —
  entries are `"channel"` or `"channel:chat_id"`; empty (default)
  disables channel approvals entirely. Resolutions go through the same
  durable pending store, recorded `resolved_by` `"channel:<chan>:<chat>"`,
  and emit the same `web3.approved`/`rejected`/`sent`/`failed` events as
  CLI/UI. Commands reply "unavailable" from non-allowlisted scopes —
  the allowlist is checked per-message inside the handlers
  (`pkg/commands/cmd_web3.go`), and the runtime callbacks are only wired
  when `tools.web3.enabled` + a non-empty allowlist.

## ACP (Agent Client Protocol)

`rhizome acp [--agent <id>]` serves ACP over stdio (Zed/JetBrains drive
Rhizome as an editor agent). `pkg/acp` contains all
`github.com/coder/acp-go-sdk` usage. Architecture:

- **stdout is protocol-only.** `main()` skips the banner/TZ prints under
  `stdioProtocolCommand()`; the command calls `logger.DisableConsole()`
  *before* `LoadConfig` (config warnings go through the stdout-bound
  console writer). Diagnostics: `RHIZOME_LOG_FILE` or slog→stderr.
- **Sessions** map to `agent:<agent-id>:acp:<session-id>` (legacy explicit
  key so `resolveScopeKey` preserves it) and prompts run through
  `AgentLoop.ProcessInbound` on channel `acp`, chatID = sessionId.
- **Streaming** uses the `bus.Streamer` delegate seam (`Server` implements
  `bus.StreamDelegate` for channel `acp` only). The command force-enables
  `ModelList[].Streaming.Enabled` and injects a process-local
  `Channels["acp"]` entry — no global `acp` channel type is registered.
  Non-streaming providers degrade to a single final chunk.
- **Tool calls** → `tool_call`/`tool_call_update` notifications from
  `agent.tool.exec_*` runtime events, correlated by `CallID` (`tc.ID`)
  carried on the exec payloads.
- **Permissions**: `acp.server.permission_policy` = `prompt` (default:
  `session/request_permission` per tool call, `*_always` cached per
  session, failures deny) | `allow` | `deny`.
- See `docs/guides/acp.md` for Zed `agent_servers` / JetBrains setup.

### ACP client (external agents)

`agents.list[].acp = {command, args, env, cwd}` binds an external ACP
agent (`gemini --acp`, `claude-code acp`, …) to a routable agent id:

- `AgentInstance.ACP` marks the binding; `pkg/acp.ClientManager` lazily
  spawns the child process (plain `exec.Command` — NOT `pkg/isolation`;
  trust posture documented in the guide), handshakes `initialize`
  (`fs:{read,write}` + `terminal:false`), and runs one `session/new` +
  `session/prompt` per delegation, accumulating `session/update` chunks.
- Spawner chain in `pkg/gateway`: `RemoteSpawner` → `acp.Spawner` → local
  `AgentLoopSpawner`. `hasLocal(id)` is true for ACP ids (registry
  membership) so they never route off-box. Without mesh, `acp.Spawner` is
  installed directly.
- Remote peers delegating into an ACP id go through
  `AgentLoop.SetExternalAgentRunner` (`ClientManager.RunRemote`) —
  `ProcessRemoteDispatch` bypasses the spawner chain, so the seam lives
  on the loop.
- `acp_run` tool invokes an ACP id explicitly (`tools.ACPInvoker`
  interface keeps the SDK out of pkg/tools).
- `acp.client.permission_policy` = `deny` (default) | `allow-read-only` |
  `allow` answers the external agent's `session/request_permission`;
  `fs/*` requests are served through the workspace sandbox
  (`restrict_to_workspace` + `tools.allow_*_paths`); `terminal/*` is
  refused. `authMethods` advertised at initialize → clear error.
- Registry is resolved lazily (`func() *AgentRegistry`) in both the
  manager and spawner — reload swaps the registry pointer.

## Web Backend Network API

The web console (the launcher) exposes authenticated JSON endpoints that wrap `rhizome network status` so the dashboard can display live mesh/DHT status:

- `GET /api/network/peers` — start a temporary libp2p node and return connected peers with trust status and advertised capabilities.
  - Query parameters: `bootstrap` (repeatable), `timeout` (e.g. `10s`), `listen` (repeatable).
- `GET /api/network/dht` — start a temporary libp2p node and return the DHT status snapshot.
  - Query parameters: `bootstrap` (repeatable), `timeout` (e.g. `10s`), `listen` (repeatable).
- `GET /api/network/saved-peers` — list persistent mesh peers from `mesh.trusted_peers` and `mesh.bootstrap_peers`, merged by peer id and augmented with live connection/capability status when the daemon is running.
- `POST /api/network/saved-peers?action=untrust&peer=<peer-id>` — remove the peer from runtime trust and `mesh.trusted_peers`.
- `DELETE /api/network/saved-peers?peer=<peer-id>` — remove the peer from runtime trust, `mesh.trusted_peers`, and any matching `mesh.bootstrap_peers`.
- `GET /api/network/tasks?peer=<id>[&task=<id>][&wait=<dur>]` — list remote tasks on a peer, or fetch status/result of one task. Proxies the daemon's `GET /network/tasks`; falls back to `rhizome network task …` using the peer's saved bootstrap address.
- `POST /api/network/tasks` — submit a remote task (`{"peer","agent_id","model","task","tools"}`); daemon required.
- `POST /api/network/tasks?action=cancel&peer=<id>&task=<id>` — cancel a running remote task.
- `GET /api/network/tasks/events?peer=<id>` — Server-Sent Event stream of `mesh.task.submit` and `mesh.task.update` events. Proxies the daemon's `GET /network/tasks/events`; requires a running daemon.
- `GET /api/network/audit?tail=N` — tail of the daemon's mesh audit trail; falls back to reading `mesh-audit.jsonl` under `RHIZOME_HOME` directly.
- `GET /api/network/activity?tail=N` — the daemon's in-memory `mesh.*`/`swarm.*` activity feed (bounded ~200 entries); daemon required, no offline fallback.
- `GET /api/network/events` — SSE stream of all `mesh.*`/`swarm.*` runtime events; proxies the daemon's `GET /network/events`, daemon required.

Remote task state is also persisted to `<RHIZOME_HOME>/mesh-tasks.jsonl` so in-flight tasks are cleanly marked as `error: daemon restarted` after an unclean daemon shutdown.

The dashboard has a **Network** page (`/network`) that visualizes these endpoints: it shows connected peers with trust/capability/transport badges, score tooltips, and a per-peer detail drawer (connections, latency, score, bandwidth, manifest fingerprints); a DHT status snapshot; a Saved Peers panel with Untrust/Remove actions; optional bootstrap overrides; a Remote Tasks panel (peer picker, submit form, live status, cancel, result viewer); a Mesh Activity panel (SSE on `/api/network/events` with 10 s polling fallback); and a Mesh Audit Log panel. The status query auto-refreshes every 60 seconds.

Both endpoints require a valid node identity and use the launcher's `RHIZOME_HOME` and `RHIZOME_CONFIG` automatically. Results are cached for 5 seconds to avoid spawning multiple overlapping nodes.

## Key Packages

- `pkg/browser` — pluggable browser-automation backends (catalog, `AgentBrowserDriver` CLI wrapper, endpoint resolvers, session manager, Cloudflare REST); `pkg/tools/browser` exposes the eight `browser_*` agent tools.
- `pkg/modules` — companion-module sidecar system (catalog, sha256-verified install, daemon supervision with restart backoff, bounded logs, `module.*` events); `cmd/rhizome/internal/module` exposes the `rhizome module` CLI, `pkg/gateway/moduleapi.go` the `/modules*` daemon endpoints, `web/backend/api/modules.go` the `/api/modules*` launcher routes.
- `pkg/acp` — ACP (Agent Client Protocol) both ways, all `coder/acp-go-sdk` usage isolated here: **server** (`rhizome acp` exposes the agent loop to editor clients over stdio JSON-RPC; sessions map to `agent:<id>:acp:<sid>` keys on channel `acp` through `AgentLoop.ProcessInbound`; streaming via a `bus.StreamDelegate`; tool approvals bridge to `session/request_permission`) and **client** (`ClientManager` spawns `agents.list[].acp` external agents; `Spawner` intercepts ACP-bound `SubTurn` targets; `RunRemote` backs `ExternalAgentRunner` for mesh dispatch; sandboxed `fs/*` serving + `acp.client.permission_policy` for permission requests).
- `pkg/redact` — leaf package for secret masking (generic patterns + configured `SecureString` values); used by `pkg/logger` and `Config.FilterSensitiveData`.
- `pkg/guard` — leaf package for prompt-injection phrase detection; used by shell-command screening, the tool-argument scan, and CLI tool-call extraction.
- `pkg/rhizome/identity` — BIP39/SLIP-0010 Ed25519 node identity, persistence, and Ed25519 signing; now supports OS keyring and passphrase encryption.
- `pkg/rhizome/network` — libp2p host, mDNS discovery, bootstrap, ping, and public DHT discovery.
- `pkg/rhizome/sync` — workspace Git sync, packfile transport, file watcher, and three-way merge.
- `pkg/rhizome/merge` — diff3-based file and tree merging.
- `pkg/rhizome/agentrpc` — libp2p request/response framing for remote agent tasks (`/rhizome/agent/1.0.0`), with signed nonce+timestamp replay fields and a bounded idempotency cache.
- `pkg/rhizome/agenttask` — asynchronous task protocol (`/rhizome/agent-task/1.0.0`): submit/status/result(long-poll)/cancel/list.
- `pkg/rhizome/blob` — content-addressed file transfer between trusted peers (`/rhizome/blob/1.0.0`) over `stream.ReliableConn`: signed put/get/stat ops, SHA-256-verified chunked streaming, per-blob size cap, TTL reaper.
- `pkg/rhizome/agentmanifest` — AIEOS-style agent identity manifests: signed Ed25519 documents (agent id, name, persona, models, skills, peer id, timestamp) embedded in capability announcements and persisted to `<workspace>/agents/<id>.manifest.json`.
- `pkg/rhizome/mesh` — peer capability exchange (signed manifests), trust, remote `delegate`/`spawn`, scatter-gather fan-out (`FanoutTask`), per-peer ACL + rate limits, replay protection, and the audit trail (`~/.rhizome/mesh-audit.jsonl`).
- `pkg/rhizome/swarm` — swarm layer over the mesh: signed envelopes on `/rhizome/swarm/1.0.0`, join/leave + roster gossip, presence heartbeats, offer/claim work queue, deterministic coordinator election (lowest peer id) with shared state written to `swarm/<id>/state.json` in the synced workspace, goal orchestration (`RunGoal`), and pluggable broadcast transport (`direct` fan-out or `gossipsub`).
- `cmd/rhizome/internal/network`, `cmd/rhizome/internal/daemon`, `cmd/rhizome/internal/swarm`, and `cmd/rhizome/internal/sync` — CLI commands.

## Mesh Configuration

Add a `mesh` section to `config.json`:

```json
{
  "mesh": {
    "enabled": true,
    "role": "full",
    "trusted_peers": ["12D3KooW..."],
    "allow_remote_delegate": true,
    "allow_remote_spawn": true,
    "remote_timeout": "5m",
    "request_max_skew": "2m",
    "rate_limit_per_peer": 30,
    "rate_limit_global": 300,
    "audit_log": true,
    "require_signed_caps": true,
    "blob_enabled": true,
    "blob_max_bytes": 67108864,
    "blob_ttl": "24h",
    "routing": { "role_aware": true },
    "acl": [
      {
        "peer_id": "12D3KooW...",
        "allow_delegate": true,
        "allow_spawn": false,
        "allow_blob": true,
        "agents": ["main"],
        "rate_limit": 10
      }
    ]
  }
}
```

- `role` — `"full"` (default) or `"worker"`. Worker nodes join the mesh to serve work but run no routing infrastructure: `role=worker` forces `dht_enabled=false`, `relay_service=false`, and `nat_service=false` (the role wins over those settings; contradictions warn at startup). Pair with `rhizome daemon --no-gateway` for the smallest always-on footprint — the gateway stays a separate flag since some workers still want the local HTTP API. The role is signed into the capability manifest (`role` field, emitted only for `worker` so full nodes stay wire-compatible with older peers) and surfaces in `mesh status`/`mesh peer` output and the dashboard's peer badges.
- `routing.role_aware` (v0.12.0, Track 80; default `true`) — `Mesh.PickPeer` gives a `1<<19` role bonus: task ops (`delegate`/`spawn`) prefer `worker` peers, infra ops (`sync`, …) prefer `full`. The bonus sits below the `1<<20` direct-connection term so it breaks ties rather than overriding connectivity, and a saturated worker still loses to a healthy full node. Set `false` to restore flat ranking.
- `request_max_skew` — max accepted clock difference for signed request timestamps (replay protection window).
- `rate_limit_per_peer` / `rate_limit_global` — remote request caps in requests per minute (0 = unlimited).
- `audit_log` — append-only `~/.rhizome/mesh-audit.jsonl` trail (10 MB × 3 rotation); a `mesh.remote.audit` runtime event is always emitted.
- `require_signed_caps` — reject unsigned capability manifests (default `true`); set `false` to accept unsigned manifests from trusted peers. A `mesh.cap.unsigned` event is emitted either way.
- `acl` — per-peer overrides: `allow_delegate`/`allow_spawn` fall back to the global flags when omitted; `allow_blob` gates `/rhizome/blob/1.0.0` transfers (default: trusted peers allowed); `agents` restricts which agent ids the peer may run (`"*"` for all); `rate_limit` overrides the per-peer cap (negative = unlimited).
- `blob_enabled` / `blob_max_bytes` / `blob_ttl` — content-addressed file transfer between trusted peers (`/rhizome/blob/1.0.0`), used by remote task attachments and mesh skill distribution. Blobs are stored under `~/.rhizome/blobs/` by SHA-256 hash, hash-verified on receipt, and reaped after `blob_ttl` (default 24h; `0` = keep forever). `blob_enabled` defaults to `true` when the mesh is enabled.
- Rejected remote calls carry machine-readable prefixes: `forbidden:` (ACL) and `rate_limited:`.

### NAT traversal (v0.5.0)

- `nat_traversal` (default true) — AutoNATv2, hole punching, and circuit-relay v2 client + AutoRelay reservations.
- `relay_service` / `nat_service` (default true) — publicly reachable nodes volunteer as relays and AutoNAT dial-back servers.
- `static_relays` — relay multiaddrs to always reserve.
- `force_reachability` — `"public"`/`"private"` override when AutoNAT is wrong.
- `public_addrs` — extra multiaddrs advertised to peers (e.g. a static public endpoint).
- `network status` reports `reachability`, `addrs`, and `relayed_addrs`; relayed (`Limited`) connections count as usable.

When `mesh.enabled` is true, `rhizome daemon` advertises local capabilities over `/rhizome/caps/1.0.0`, accepts remote agent requests over `/rhizome/agent/1.0.0` from trusted peers, and publishes mesh/DHT runtime events to the shared event bus.

Agent identity manifests ride inside the capability payload (`agent_manifests`): each configured agent gets an `agentmanifest.Manifest` signed with the node key and bound to the sender's peer id, and the gateway persists each signed copy to `<workspace>/agents/<id>.manifest.json` so manifests sync to peers. Receivers verify every manifest against the sender's key after the outer capability signature check and drop forgeries (a `mesh.error` event with stage `capability.agent_manifest` is emitted). Manifest model/skill fields honor `advertise_models`/`advertise_skills`; status views (`network status --peers`, saved-peers) expose per-agent fingerprints. Manifests prove *who* issued an identity claim — execution authorization remains governed by trust + ACL.

## Swarm Mode (v0.7.0, Tracks 21–30)

Swarms are named groups of trusted mesh peers. Membership is always a subset
of `mesh.trusted_peers`; swarm requires `mesh.enabled`.

```json
{
  "swarm": {
    "enabled": true,
    "memberships": ["ops"],
    "max_members": 32,
    "max_message_bytes": 262144,
    "request_max_skew": "2m",
    "transport": "direct",
    "presence": { "heartbeat_interval": "15s", "expire_after": "45s" },
    "queue": { "offer_ttl": "2m", "claim_window": "5s", "max_offers": 100, "assign_timeout": "10m", "max_retries": 1 },
    "coordination": { "enabled": true, "state_interval": "30s" },
    "context": { "enabled": true, "max_note_bytes": 4096, "max_notes": 500, "digest_bytes": 8192, "note_ttl": "0s" },
    "rate_limit_per_peer": 60,
    "rate_limit_global": 600,
    "audit_log": true,
    "acl": [
      { "swarm_id": "ops", "peer_id": "*", "allow_offer": true, "allow_claim": true, "agents": ["*"] }
    ]
  }
}
```

- `transport` — `direct` (default, per-member stream fan-out) or `gossipsub` (one pub/sub topic per swarm, `rhizome/swarm/<id>`; join/leave/ping stay on direct streams).
- `acl` — per-(swarm, peer) rules; missing rules default to "trusted peers may offer and claim". `rate_limit` overrides the per-peer cap (negative = unlimited).
- Swarm ops audit into the shared `mesh-audit.jsonl` with `swarm.`-prefixed ops.
- `context` — the shared-context blackboard (v0.9.0). Members append notes to per-author shards at `swarm/<id>/notes/<peer-id>.jsonl` in the synced workspace (conflict-free under git sync); the coordinator curates `swarm/<id>/context.md`. `swarm run` injects a blackboard digest (curated doc + newest notes, capped at `digest_bytes`) into the decomposer prompt. Posted notes also propagate live via `MsgNote` envelopes on `/rhizome/swarm/1.0.0`; remote writes are signature-attributed to the sender's shard. The `swarm_context` agent tool (read/list/post/set_context) is registered when swarm is enabled. Events: `swarm.context.note`, `swarm.context.written`.
- Daemon API: `GET /network/swarms`, `GET /network/swarms/<id>[/{members,offers}]`, `POST /network/swarms` (`{"swarm","action":"join|leave"}`), `POST /network/swarms/<id>/offers`, `POST /network/swarms/<id>/offers/cancel`, `POST /network/swarms/<id>/run`, `GET /network/swarms/<id>/runs` (`?run=<id>` for one), `GET|POST /network/swarms/<id>/context` (`?since=<rfc3339>` filters notes; POST `{"action":"note"|"set_context","kind","key","content","ttl_seconds"}`), `GET /network/swarms/events` (SSE). The launcher proxies them under `/api/network/swarms*` with file/config fallbacks for reads and join/leave.
- The gateway wires swarm seams (`SetTaskSubmitter`, `SetTaskCanceller`, `SetOfferEvaluator`, `SetCapMatcher`, `SetCapProbe`, `SetStateWriter`, `SetContextDirFunc`, `SetDecomposer`, `SetSynthesizer`, `SetResultFetcher`) in `pkg/gateway/swarm.go`; the daemon registers the instance via `gateway.SetSwarm`.
- The Network dashboard has a **Swarms** panel (roster, coordinator, offers, goal runs) fed by `/api/network/swarms*` and the swarm SSE stream, and a **Topology** panel (v0.12.0) — a hand-rolled SVG radial fed by `network status` `peers[].conns`: direction → arrow/dash, transport → edge color, latency → thickness, `role` → node label; click-through opens the shared peer-detail Sheet.
- **Role-aware election** (v0.12.0, Track 80): `pingPayload` carries `role` (additive — older peers ignore it), `Member`/`MemberInfo` persist it, and `reelectLocked` sorts candidates by `(roleRank, peerID)` — `full` members coordinate before `worker`s. The local node's role comes from the same `capProbeFunc` that fills heartbeats (signature extended to return the role; wired to `Mesh.Role()` in `pkg/gateway/swarm.go`). A member's role arriving via its first heartbeat triggers re-election; mixed-version flapping is transient and advisory-only. `swarm members` prints the role column.

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

The swarm integration test builds `rhizome`, starts two daemons joined to a shared `ops` swarm, and verifies mutual roster exchange and restart persistence:

```powershell
.\scripts\integration-swarm.ps1
```

## Browser Automation (v0.7.1)

- `tools.browser.enabled` (default `false`) turns on the eight `browser_*` agent tools.
- `tools.browser.default_backend` selects the backend (default `agent-browser`); `tools.browser.backends.<id>` holds per-backend settings — `api_key` is a `SecureString` (can live in `.security.yml`); `env` is plaintext — prefer `api_key` for credentials (secret-looking env values are still masked in logs/tool output).
- `tools.browser.session_timeout` (default `10m`) bounds sessions — idle sessions are reaped in the background; `tools.browser.private_host_whitelist` relaxes the SSRF guard on browser tool URLs (literal hosts and DNS-resolved addresses).
- Web console: `/browser` page + `GET/PUT /api/browser`, `GET /api/browser/backends`, `POST /api/browser/install|uninstall?backend=<id>`, `GET /api/browser/diskspace`.
- Cloudflare is REST-only (snapshot/screenshot); interactive ops return a clear unsupported-backend error.
- `rhizome-cdp` backend (v0.8.0, Track 41): `pkg/browser/cdp` is a built-in Go CDP client (gorilla WebSocket, Target/Page/Runtime/DOM/Input domains) — no Node.js/agent-browser needed. `endpoint_url` accepts `ws(s)://` or an `http(s)://` debug base (`/json/version`); `api_key` is sent as `Authorization: Bearer` on the WS handshake, covering authenticated endpoints such as Cloudflare Browser Rendering connect.
- Stealth path: `workspace/skills/stealth-browser` (Patchright MCP server); see `docs/guides/browser-automation.md`.

## MCP Presets (v0.8.0, Track 40)

- `tools.mcp.presets.<name>` wires first-class hosted MCP servers. Currently `context7` (`{enabled, api_key}`) expands to an `http` server at `https://mcp.context7.com/mcp` with a `CONTEXT7_API_KEY` header; `api_key` is a `SecureString` (persists via `config.security.yml`, supports `file://`/`enc://` refs) and falls back to the `CONTEXT7_API_KEY` env var.
- `MCPConfig.EffectiveServers()` merges `servers` + expanded presets — explicit `servers.<name>` always wins.
- CLI: `rhizome mcp preset context7 --key <k> --enable`; `mcp list` shows preset-expanded servers.
- Web: `GET /api/tools/mcp-presets`, `PUT /api/tools/mcp-presets/<name>`; a presets card on the Tools page handles enable + key entry (keys never echoed back).

## Media & Attachments (v0.8.0, Tracks 34-35)

- `tools.media.vision_mode` — `auto` (default) attaches user-sent images inline to the provider request when the model is vision-capable (or an `agents.defaults.image_model` fallback is configured); `tool` keeps path tags only (agent calls `load_image`); `off` disables inline images entirely.
- `tools.media.max_video_frames` (default 8) bounds keyframes extracted by `load_video`; `tools.media.ffmpeg_path` overrides the ffmpeg binary (ffmpeg is an optional external dependency, like `agent-browser`).
- New tools: `load_video` (ffmpeg keyframes → media:// image refs) and `transcribe_audio` (on-demand ASR via the configured voice transcriber); both are `tools.load_video` / `tools.transcribe_audio` enabled by default.
- Telegram inbound now downloads video: the API-provided thumbnail is attached as an image (vision works without ffmpeg) plus the video file for `load_video`.
- Remote task attachments: `network delegate|spawn|route`, `mesh route`, and `swarm offer` accept repeatable `--attach <path>`; local files are pushed over `/rhizome/blob/1.0.0` to the callee and localized into its media store; result artifacts return as blob refs and are localized on the caller. `tools.media` also governs audio attachments — audio refs on remote tasks are transcribed to `[voice: ...]` annotations when a transcriber is configured.

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

To type-check the frontend only (the root `tsconfig.json` only references
projects, it does not itself type-check):

```powershell
npx tsc --noEmit -p tsconfig.app.json
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

## Tracks 16–18 — Mesh operations surface

- `pkg/rhizome/agentrpc` and `pkg/rhizome/agenttask` now have unit suites covering round-trip calls, handler errors, nonce echo, idempotency cache (peer-scoped keys, TTL, 1024-entry bound), protocol advertisement checks, and malformed-frame handling. The fixed 500 ms connection sleeps in mesh tests were replaced with `require.Eventually` on `network.IsConnectednessUp`.
- The daemon exposes remote task ops on its gateway mux (PID-token auth):
  - `GET /network/tasks?peer=<id>` — list tasks this node owns on the peer.
  - `GET /network/tasks?peer=<id>&task=<id>` — status; `&wait=<dur>` turns it into a long-poll result fetch (cap 90 s).
  - `POST /network/tasks` — submit; JSON body `{peer, agent_id, model, task, tools}`. `peer` accepts a peer id or a `/p2p/` multiaddr (dialed on demand).
  - `POST /network/tasks?action=cancel&peer=<id>&task=<id>` — cancel.
  - `GET /network/audit?tail=N` — last N entries of `mesh-audit.jsonl` (1–1000, default 50) via `mesh.ReadAuditTail`.
- The launcher proxies both under `/api/network/tasks` and `/api/network/audit`. Task reads/cancel fall back to `rhizome network task …` using the peer's saved `mesh.bootstrap_peers` address; submit and unresolved peers return 503 (daemon required). Audit falls back to reading the local file.
- Capability manifests gained `active_tasks` (non-terminal task count, signed like the rest of the manifest) and `PeerCapability`/`network status` surface it.
- `Mesh.PickPeer(agentID, op)` picks a connected, trusted peer whose signed manifest lists the agent (or `"*"`) and allows the op, preferring direct connections then fewest `active_tasks`. `rhizome mesh route` builds on it.
- The Network dashboard gained Remote Tasks and Mesh Audit Log panels; `rhizome network audit` / `rhizome mesh audit` print the audit tail.

## Provider Protocol Refactor (v0.4.8)

- LLM providers are now driven by a protocol catalog in `pkg/providers/model_catalog.go`.
- `ModelProviderOption` carries `ProtocolFamily`, `StripModelPrefix`, `ExtraBodyDefaults`, `EmptyAPIKeyAllowed`, and construction flags.
- `pkg/providers/factory_provider.go` dispatches by `ProtocolFamily`; OpenAI-compatible providers are created with one helper instead of a per-provider `switch`.
- Anthropic Claude uses the native Messages protocol (`anthropic-messages`); the old `pkg/providers/anthropic` SDK wrapper and `pkg/providers/httpapi` facade have been removed.
- Gemini lives in its own package: `pkg/providers/gemini`.
- First-class local protocols (`ollama`, `vllm`, `lmstudio`, `litellm`) set `Local: true` and `EmptyAPIKeyAllowed: true`; the frontend shows them as "local" and allows blank API keys.
- `openai_compat.Provider` strips the leading `provider/` segment when configured (`WithStripModelPrefix`) and preserves prefixes for `openrouter` endpoints.
- When editing provider code, run `go build -tags goolm,stdjson ./...` and `go test -tags goolm,stdjson ./pkg/... ./web/...` (full `./...` includes slow `cmd/membench`).

## Windows Firewall (local dev)

Windows Defender Firewall prompts for every new `.exe` that opens a network socket. This is painful on Windows because `go test` writes each package's test binary to a random `go-build<random>` temp directory, and `NewNode` starts an mDNS service that uses UDP multicast on `224.0.0.251:5353`.

To stop the popup barrage while keeping the firewall enabled, build binaries to stable paths and run the provided PowerShell scripts as Administrator.

```powershell
# 1. Build the main binary and launcher as normal.
$env:CGO_ENABLED='0'
go build -tags goolm,stdjson -o build\rhizome.exe ./cmd/rhizome
# or: make build
# or: make build-launcher

# 2. Create firewall allow rules for every project .exe under build\ and web\build\. Run once.
.\scripts\dev-firewall-setup.ps1

# 3. Run unit tests. This compiles each package's test binary to build\tests\<pkg>.test.exe,
#    adds a rule for that exact .exe, and runs it.
.\scripts\test-windows.ps1

# 4. Run integration tests. They now build into stable build\integration-*\ paths and auto-allow.
.\scripts\integration-mesh.ps1
.\scripts\integration-swarm.ps1
```

Notes:

- `dev-firewall-setup.ps1` and `test-windows.ps1` require Administrator elevation so they can call `New-NetFirewallRule`.
- `test-windows.ps1` accepts `-Package ./pkg/rhizome/network` to run a single package; the full suite includes `cmd/membench`, which is slow.
- `make test` and raw `go test` still work and are unchanged for Linux, macOS, and CI. On Windows they may still prompt because they use the `go` toolchain's random temp directory.
- `go run` and `make dev-backend` also use a random temp binary, so for network listeners on Windows build first (`go build -o build\rhizome.exe` or `make build`) and run the resulting `.exe`.
- CI Windows runners (`.github/workflows/pr.yml`) are ephemeral and already disable the firewall; these scripts are for local dev only.
