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
- `rhizome daemon` — start a long-running P2P node.

## Key Packages

- `pkg/rhizome/identity` — BIP39/SLIP-0010 Ed25519 node identity and persistence.
- `pkg/rhizome/network` — libp2p host, mDNS discovery, bootstrap, ping.
- `cmd/rhizome/internal/network` and `cmd/rhizome/internal/daemon` — CLI commands.

## Configuration / Environment

- `RHIZOME_HOME` overrides the home directory (default `~/.rhizome`).
- `RHIZOME_CONFIG` overrides the config file path.

## Upstream

- `sipeed/picoclaw` is the upstream remote for cherry-picking future fixes.
