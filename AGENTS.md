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

## Rhizome P2P Commands

- `rhizome network onboard` — create a node identity from a BIP39 mnemonic.
- `rhizome network status` — show the saved node identity.
- `rhizome network ping <multiaddr>` — start a temporary libp2p host and ping a peer.
- `rhizome network peers` — list trusted peers from config.
- `rhizome network trust <peer-id>` / `rhizome network untrust <peer-id>` — manage the mesh trusted peers list.
- `rhizome network delegate <peer-multiaddr> <agent-id> <task>` — synchronously delegate a task to a remote peer agent.
- `rhizome network spawn <peer-multiaddr> <agent-id> <task>` — asynchronously spawn a task on a remote peer agent.
- `rhizome sync status|log|commit|pull|push` — manage the workspace git repo.
- `rhizome daemon` — start a long-running P2P node, workspace syncer, agent gateway, and (when enabled) the decentralised mesh.

## Key Packages

- `pkg/rhizome/identity` — BIP39/SLIP-0010 Ed25519 node identity, persistence, and Ed25519 signing.
- `pkg/rhizome/network` — libp2p host, mDNS discovery, bootstrap, ping.
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

When `mesh.enabled` is true, `rhizome daemon` advertises local capabilities over `/rhizome/caps/1.0.0` and accepts remote agent requests over `/rhizome/agent/1.0.0` from trusted peers.

## Configuration / Environment

- `RHIZOME_HOME` overrides the home directory (default `~/.rhizome`).
- `RHIZOME_CONFIG` overrides the config file path.

## Upstream

- `sipeed/picoclaw` is the upstream remote for cherry-picking future fixes.
