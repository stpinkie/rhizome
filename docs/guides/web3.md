# Web3 — Read-Only Ethereum Tools

Rhizome ships read-only Ethereum JSON-RPC tools (`web3_*`) backed by the
companion-module system. They are **disabled by default** and never expose
signing or transaction submission — sending is a separate gated feature
(see the v0.11.0 sprint, Track 69).

## Tools

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
denied — by absence, not by pattern.

## Enabling

```json
"tools": {
  "web3": {
    "enabled": true,
    "endpoint": "",
    "chain_ids": [],
    "max_log_range": 10000,
    "timeout_seconds": 30,
    "allow_private_endpoints": false
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

## Safety posture

- `chain_ids` (decimal list) hard-fails every call if the endpoint reports
  a different chain — set it to pin the tools to mainnet `[1]` or a
  testnet like sepolia `[11155111]`.
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
