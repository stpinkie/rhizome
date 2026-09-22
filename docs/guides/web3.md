# Web3 — Ethereum Tools, Wallet & Gated Signing

Rhizome ships Ethereum JSON-RPC tools (`web3_*`) backed by the
companion-module system, plus an optional wallet + human-approval signing
path. All of it is **disabled by default**: `tools.web3.enabled` gates the
read tools, and signing additionally requires `tools.web3.signing.enabled`
plus `from_addresses` — every send/sign still waits for a durable human
approval before a key touches a transaction.

**Start on a testnet.** Point the endpoint at sepolia (`chain_ids:
[11155111]`) while you tune policy; only loosen `from_addresses`/caps for
mainnet once the approval flow is familiar.

## Read tools

| Tool | JSON-RPC | Purpose |
|---|---|---|
| `web3_chain` | `eth_chainId` + `eth_blockNumber` + `eth_gasPrice` + `eth_syncing` | Network identity, tip, gas, sync state |
| `web3_balance` | `eth_getBalance` | Address balance (wei + ether) |
| `web3_call` | `eth_call` | Read-only contract call (no state change, no broadcast) |
| `web3_block` | `eth_getBlockByNumber` | Block header + tx hashes (or up to 50 decoded txs) |
| `web3_transaction` | `eth_getTransactionByHash` + `eth_getTransactionReceipt` | Tx + receipt with success/reverted/pending status |
| `web3_logs` | `eth_getLogs` | Event logs over a bounded block range (≤ `max_log_range`) |
| `web3_rpc` | allowlisted only | Generic passthrough for other read methods (`eth_getProof`, `eth_feeHistory`, `net_*`, …) |

`web3_rpc` is restricted to a hard-coded allowlist. `eth_sendTransaction`,
`eth_sendRawTransaction`, `eth_sign*`, `eth_accounts`, `eth_subscribe`, and
all `personal_*`/`admin_*`/`debug_*`/`txpool_*`/`miner_*` methods are
denied — by absence, not by pattern. Writes go through the signing tools
below, never through `web3_rpc`.

## Write tools (v0.12.0)

| Tool | Purpose |
|---|---|
| `web3_wallet` | List wallet addresses (default marked), labels, balances |
| `web3_send` | Queue a value/data send (EIP-1559, legacy on non-1559 chains) |
| `web3_sign` | Queue a message sign (`personal_sign`-style, EIP-191) |
| `web3_pending` | List signing requests awaiting approval |
| `web3_approve` | Queue-side approve helper (the durable decision is the human's) |
| `web3_send_status` | Poll a queued/broadcast request's status + tx hash |
| `web3_contract_call` | ABI-decoded read call via registry label/`0x`/ENS |
| `web3_contract_send` | ABI-encoded write — queues for approval |
| `web3_erc20` | `info\|balance\|allowance` read; `transfer\|approve` queued |
| `web3_erc721` | `name\|symbol\|ownerOf\|balanceOf\|tokenURI` read; `transferFrom\|safeTransferFrom` queued |
| `web3_ens` | ENS forward (`name` → address) / reverse (`address` → name) |
| `web3_watch` | Manage `tools.web3.watches` log watches (persisted) |

Every write tool returns `{pending_id, status: "pending"}` — nothing is
signed or broadcast until a human approves.

## Enabling

```json
"tools": {
  "web3": {
    "enabled": true,
    "endpoint": "",
    "chain_ids": [11155111],
    "max_log_range": 10000,
    "timeout_seconds": 30,
    "allow_private_endpoints": false,
    "signing": {
      "enabled": true,
      "from_addresses": ["0x…"],
      "allow_contracts": [],
      "allow_methods": ["usdc:transfer"],
      "max_value_wei_per_tx": "10000000000000000",
      "max_value_wei_per_day": "50000000000000000",
      "chain_ids": [11155111],
      "approval_timeout_seconds": 900
    },
    "watch_confirmations": 2,
    "watches": [
      {
        "name": "usdc-in",
        "contract": "usdc",
        "topics": ["0xddf252ad…"],
        "interval_seconds": 60
      }
    ],
    "approval_channels": ["telegram:123456"]
  }
}
```

## Endpoint resolution

The tools never accept a URL at call time. The endpoint is resolved, in
order:

1. **`tools.web3.endpoint`** — explicit override (e.g.
   `http://127.0.0.1:8545`). Pair it with `tools.web3.api_key` for Bearer
   auth; the key lives in `.security.yml`, never `config.json`.
2. **`ethereum-rpc` module** — `modules.ethereum-rpc.fields.endpoint_url`
   plus the module's secret `api_key`:
   `rhizome module set ethereum-rpc endpoint_url=https://…` and
   `rhizome module set --secret ethereum-rpc api_key=…`.
3. **Running `nimbus-verified-proxy`** — its `listen_url` (default
   `http://127.0.0.1:8545`) is probed with `eth_chainId`; a live verified
   proxy is used automatically with zero extra config.
4. Otherwise a descriptive error telling you which knob to turn.

Resolution is cached for 30 s; a stopped nimbus that comes up is picked up
without a restart.

## Wallet

Keys are secp256k1, stored encrypted at
`<RHIZOME_HOME>/web3/keys.json` (`0700`) using the identity `KeyProvider`
posture — keyring by default, passphrase where no keyring exists.

```bash
rhizome wallet create --label ops        # generate + encrypt
rhizome wallet import --label treasury   # prompt for hex key (never echoed)
rhizome wallet list                      # labels + checksummed addresses
rhizome wallet set-default <addr|label>  # default signer for "from" omission
rhizome wallet rename <addr> <label>
rhizome wallet remove <addr|label>
rhizome wallet reveal <addr|label> --confirm   # prints the private key — handle with care
```

## Signing policy

`tools.web3.signing` is a **policy pre-filter**, evaluated before a request
may even enqueue:

| Field | Meaning |
|---|---|
| `enabled` | Master switch for the write path (also requires `tools.web3.enabled`) |
| `from_addresses` | Explicit signer allowlist — **empty denies all signing**; every `from` must be listed |
| `allow_contracts` | `to` allowlist for value/data-bearing sends (empty = any) |
| `allow_methods` | Calldata 4-byte selectors (`0xa9059cbb`) **or** `label:method` entries (`usdc:transfer`) — label entries only match the contract bound to that label |
| `max_value_wei_per_tx` | Per-tx value cap, decimal wei string (uint256 doesn't fit int64) |
| `max_value_wei_per_day` | Aggregate value cap per UTC day (bounded ledger) |
| `chain_ids` | Signing restricted to these chains (empty = any) |
| `approval_timeout_seconds` | Pending-request TTL (default 15 m); expiry denies |

## Approval flow

1. Agent calls a write tool → policy check → `hook.approve_tool` veto →
   entry persisted to `<RHIZOME_HOME>/web3-pending.json` (atomic,
   ~100-entry bound) → tool returns `{pending_id}`.
2. Human resolves:
   - CLI: `rhizome web3 pending`, `rhizome web3 approve <id>`,
     `rhizome web3 reject <id>` (daemon-proxied, daemonless fallback)
   - Daemon API: `GET /web3/pending[/id]`, `POST /web3/approvals/<id>`
     (bearer auth)
   - Dashboard `/web3` page: pending card with approve/reject dialog;
     sidebar badge shows the pending count
   - Channel: `/web3 pending|approve|reject` **only** from scopes listed
     in `tools.web3.approval_channels` (`"channel"` or
     `"channel:chat_id"`; empty = disabled). The approving scope is
     recorded as `resolved_by` on the entry.
3. On approve, the daemon signs and broadcasts (`eth_sendRawTransaction`);
   `web3_send_status` / `rhizome web3 pending` show the resulting hash.
   Reject, timeout, or cancel denies the request.

## Contracts, tokens, ENS

- **ABI registry** (`<RHIZOME_HOME>/web3-abis.json`):
  `rhizome web3 abi add <label> <file>` registers a contract ABI bound to
  an address + chain; `list`/`show`/`remove` manage it.
- `contract` args on contract/token tools accept a `0x` address, a
  registry label, or an ENS name — resolved in that order.
- `web3_erc20` amounts are decimals-aware (`"5.25"` USDC) or raw base
  units via `amount_units`; `approve` accepts `"unlimited"`.
- `web3_ens` works on mainnet, sepolia, holesky, hoodi.

## Log watches

`tools.web3.watches` registers daemon-polled `eth_getLogs` subscriptions
(the nimbus proxy has no subscription transport, so polling is the only
option — `interval_seconds` floor 30, default 60). Matching logs emit
`web3.event` runtime events into the activity feed. Per-watch cursors
persist across restarts; `watch_confirmations` (0–64) delays emission
until a log is N blocks deep for reorg protection. Manage at runtime with
the `web3_watch` tool (`list`/`add`/`remove` — persisted to
`config.json`, capped at 8 watches, effective on daemon restart).

## Safety posture

- `chain_ids` (decimal list) hard-fails every call if the endpoint reports
  a different chain — set it to pin the tools to mainnet `[1]` or a
  testnet like sepolia `[11155111]`. `signing.chain_ids` narrows writes
  further.
- `allow_private_endpoints` defaults **false** — fail closed. The flagship
  endpoint is loopback nimbus, so the typical setup needs
  `allow_private_endpoints: true` alongside `enabled: true`; resolution
  errors name the flag. When left `false`, endpoint traffic goes through
  the safe-dial SSRF guard (private/restricted IPs, DNS-rebinding, and
  redirect targets blocked).
- Endpoint URLs are never agent-supplied, and tool output reports the
  endpoint *source* (`ethereum-rpc module`, …) rather than raw URLs.
- `api_key` values are `SecureString`s: they land in `.security.yml`, join
  the log/tool-output redaction set, and are sent only as Bearer headers.
- Note: `ethereum-rpc`'s `endpoint_url` is a *non-secret* field — if your
  provider embeds the key in the URL path (`…/v3/<key>`), that value sits
  in `config.json`. Prefer `api_key` (Bearer) when your provider supports
  it.
- Signing is double-gated (`tools.web3.enabled` + `tools.web3.signing.enabled`)
  and every write still waits on a human; `from_addresses` empty =
  deny-all. Keys never leave `keys.json`; tool output masks them.

## Wallet & signing (optional)

Signing is a second gate on top of `tools.web3.enabled`:

```json
"tools": {
  "web3": {
    "enabled": true,
    "signing": {
      "enabled": true,
      "from_addresses": ["0xYourAddress"],
      "allow_contracts": [],
      "allow_methods": [],
      "max_value_wei": "100000000000000000",
      "max_daily_wei": "500000000000000",
      "chain_ids": [1],
      "approval_timeout_seconds": 86400
    }
  }
}
```

- `rhizome wallet create|import|list|reveal|set-default|rename|remove` —
  encrypted secp256k1 keystore under `<RHIZOME_HOME>/web3/` (keyring or
  passphrase encryption; `reveal`/`remove` require `--confirm`).
- Agent tools `web3_send`, `web3_sign`, `web3_approve` never sign inline.
  Each call checks the policy allowlists, runs the approval-hook veto,
  enqueues a durable pending entry (`web3-pending.json`, survives daemon
  restarts), then waits for a human: `rhizome web3 pending`,
  `rhizome web3 approve|reject <id>`, the dashboard `/web3` page, or the
  `/web3` channel commands (below). Execution re-validates the live chain
  before broadcasting.
- `from_addresses` empty denies all signing — an explicit signer list is
  mandatory. `allow_contracts`/`allow_methods` scope what calldata may
  carry; `max_value_wei`/`max_daily_wei` are decimal-string caps enforced
  by a spend ledger (`web3-ledger.jsonl`).

## Contracts, tokens & ENS

```json
"web3": { "enabled": true }
```

- **ABI registry**: `rhizome web3 abi add usdc ./usdc.json --address 0xA0b8…
  --chain-ids 1` stores a parsed ABI at `<RHIZOME_HOME>/web3/abi/usdc.json`.
  `rhizome web3 abi list|show|remove` manage entries.
- **Contract args** resolve `0x` literals → registry labels → ENS names,
  in that order.
- **Tools**: `web3_contract_call` (read `eth_call`, decoded outputs),
  `web3_contract_send` (approval-gated write), `web3_erc20`
  (`info|balance|allowance` reads; `transfer|approve` writes),
  `web3_erc721` (`ownerOf|tokenURI|…` reads; `transferFrom` writes),
  `web3_ens` (`name` forward / `address` reverse). ERC helpers ship
  embedded ABIs — no registry entry needed.
- **Policy integration**: `allow_contracts` accepts registry labels;
  `allow_methods` accepts `0x` selectors, `label:method`, or
  `0xaddr:method` — a bound label scopes the selector to that contract.
- **CLI**: `rhizome web3 ens vitalik.eth` / `--reverse 0x…`.

## Log watches & channel approvals (optional stretches)

```json
"tools": {
  "web3": {
    "enabled": true,
    "watches": [
      {"name": "usdc-in", "contract": "usdc",
       "topics": ["0xddf252ad…"], "interval_seconds": 60}
    ],
    "watch_confirmations": 3,
    "approval_channels": ["telegram:123456"]
  }
}
```

- **Watches** are daemon-polled `eth_getLogs` subscriptions (the nimbus
  proxy has no subscription RPC). Each match emits a `web3.event` runtime
  event into the activity feed. Cursors persist across restarts;
  `watch_confirmations` adds reorg depth (default 0 = emit at head).
  The `web3_watch` agent tool lists/adds/removes entries — changes land
  in `config.json` and apply on daemon restart.
- **`approval_channels`** scopes where `/web3 pending`, `/web3 approve
  <id>`, `/web3 reject <id>` work: `"telegram"` (whole channel) or
  `"telegram:123456"` (one chat). Empty disables channel approvals —
  the safe default. Resolutions record `resolved_by` as
  `channel:<chan>:<chat>`.

## Pairing with nimbus-verified-proxy

The recommended endpoint is the local verified proxy — responses are
checked against Ethereum consensus proofs rather than trusting a remote
provider:

```bash
rhizome module install nimbus-verified-proxy
rhizome module set --secret nimbus-verified-proxy execution_api_url=https://<el-endpoint>
rhizome module set nimbus-verified-proxy trusted_block_root=0x…
rhizome module enable nimbus-verified-proxy   # autostarts with the daemon
# then: tools.web3.enabled = true + allow_private_endpoints = true (loopback)
```

Fetch a recent `trusted_block_root` from any beacon API at
`/eth/v1/beacon/headers/finalized`. With the default `p2p` mode, the proxy
syncs light-client data over the beacon P2P network and needs no beacon
REST endpoint.
