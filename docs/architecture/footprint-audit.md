# Footprint Audit (v0.7.1)

Date: 2026-09-08 · Build: `CGO_ENABLED=0 go build -tags goolm,stdjson` (Windows amd64)

## Binary size

| Metric | Value |
| --- | --- |
| `rhizome` binary | **98.7 MB** |
| Go module deps | 198 |
| Daemon private memory (idle) | ~60 MB |

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
   catch dependency creep; 200 deps is already a large surface for an
   "ultra-lightweight" agent.

## Runtime memory notes

- Daemon idles at ~60 MB private working set; libp2p (DHT + relay +
  connection pools) dominates steady-state usage.
- Browser sessions add **zero** daemon memory — the browser process is
  external and short-lived; cloud sessions are released on timeout/close.
