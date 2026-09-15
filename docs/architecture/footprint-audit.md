# Footprint Audit (v0.10.0)

> **Positioning (v0.9.x+):** the project goal is omnipresence on meaningful
> hardware (aging laptops, old phones, SBCs, free-tier VMs). The metrics that
> matter are idle RSS, idle CPU, and cold-start time on that class of hardware,
> measured per release by `scripts/measure-footprint.sh`. Binary size is a
> secondary concern gated in CI mainly to catch dependency creep.

## v0.10.0 measurement

Measured by `scripts/measure-footprint.sh` — stripped linux-amd64 build
(`-ldflags="-s -w"`, `CGO_ENABLED=0`, tags `goolm,stdjson`), WSL2 Ubuntu,
VmRSS sampled from `/proc/<pid>/status` every 1–2 s over a ~60 s window.

| Profile | Command | Cold start | Steady VmRSS |
| --- | --- | --- | --- |
| One-shot CLI | `rhizome agent -m hi` | wall ≈7.8 s (includes a failed provider call; process-only start is sub-second) | peak ~44 MB |
| Worker daemon | `rhizome daemon --no-dht --no-gateway` + `mesh.role=worker` | ~30–650 ms | **~50 MB** |
| Full daemon | `rhizome daemon` (mesh + DHT + gateway) | ~0.7–1.4 s (to gateway port) | **~55 MB** |

Worker vs. full shows a ~3.5 MB idle-RSS delta on an isolated LAN where the
DHT never bootstraps; on a real network the delta grows with routing-table,
relay-service, and AutoNAT-service activity. The role's real value is that a
worker never *initiates* that infrastructure.

Reproduce:

```bash
./scripts/measure-footprint.sh > footprint.json           # builds stripped
RHIZOME_BINARY=./rhizome ./scripts/measure-footprint.sh   # or a prebuilt bin
SAMPLE_SECONDS=60 SAMPLE_INTERVAL=2                       # defaults
```

Requires Linux `/proc` (WSL2 works). Daemon profiles need only a generated
identity (`network onboard --generate --non-interactive`); the script creates
a stub `model_list` entry so the gateway can initialize without a real
provider.

## Binary size

| Metric | Value |
| --- | --- |
| `rhizome` binary (unstripped, windows-amd64, v0.7.1) | **98.7 MB** |
| `rhizome` binary (`-s -w` stripped, linux-amd64, v0.9.1) | **73.7 MB** |
| `rhizome` binary (`-s -w` stripped, linux-amd64, v0.10.0) | **74.5 MB** |
| Go module deps | ~200 |

## Where the size comes from

The mesh/P2P stack is the dominant contributor — this is expected: it is
Rhizome's core differentiator over PicoClaw.

| Area | Notable deps | Notes |
| --- | --- | --- |
| libp2p stack | `go-libp2p` v0.49, `go-libp2p-kad-dht`, `go-libp2p-pubsub`, `quic-go`, `webtransport`, `zeroconf` | ~20 packages; transports, DHT, gossipsub, NAT traversal |
| Crypto | `mautrix` (libolm E2EE), `go-crypto`, `circl`, `go-bip39`, `go-slip10`, `secp256k1` | Matrix E2EE + node identity |
| Channel SDKs | `discordgo` (fork), `oapi-sdk-go` (Feishu/Lark), `irc-go`, `paho.mqtt`, `teams-notify`, `vksdk` | Each channel pulls a full SDK |
| Provider SDKs | `aws-sdk-go-v2`, Kagi/Anthropic clients | Mostly thin HTTP, AWS SDK is the largest |
| Web console | embedded frontend + launcher | Pre-built React bundle |

## What v0.7.1 added

- **`pkg/browser`**: ~1,200 LOC, **zero new dependencies**, no binary-size
  impact — the driver shells out to the external `agent-browser` CLI; nothing
  is embedded or downloaded by Rhizome itself.
- **`pkg/redact` / `pkg/guard`**: regex-only, no deps.
- Cloud browser providers are reached over plain `net/http` — no provider SDKs.

## Why no `nonetwork` build

A network-free build tag would strip the libp2p/mesh/swarm code — the very
features that differentiate Rhizome from PicoClaw — and would effectively
re-create upstream. Rejected (originally dropped in `b6764ac0`, re-affirmed in
this sprint).

## Recommendations (v0.8.0+)

1. **Strip + UPX** for release binaries: `-ldflags="-s -w"` plus optional
   UPX compression typically recovers 30–50% of size; measure against the
   anti-virus false-positive risk before enabling in CI.
2. **Per-channel build tags** — e.g. `nochannels` for headless daemon-only
   installs where the chat SDKs are dead weight (Discord, Feishu, VK, Teams
   SDKs are individually large).
3. **Browser stays external** — keep `agent-browser` (and any future stealth
   driver) outside the binary; a native Go CDP client (planned for Cloudflare
   auth-WS support) should remain a thin package.
4. **Measure with `go build -ldflags="-dumpdep"` / size analysis** in CI to
   catch dependency creep; ~200 deps is a large surface — new dependencies
   must justify their RSS/binary cost.

## Runtime memory notes

- Daemon idles at ~55 MB RSS (v0.10.0, measured); libp2p (DHT + relay +
  connection pools) dominates steady-state usage.
- **`mesh.role: "worker"`** drops the DHT client, circuit-relay service, and
  AutoNAT service entirely — the role wins over `mesh.dht_enabled`,
  `mesh.relay_service`, and `mesh.nat_service` (contradictions warn at
  startup). Pair with `rhizome daemon --no-dht --no-gateway` for the smallest
  always-on node; the gateway is a separate flag because some workers still
  want the local HTTP API.
- Browser sessions add **zero** daemon memory — the browser process is
  external and short-lived; cloud sessions are released on timeout/close.
