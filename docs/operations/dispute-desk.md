# Dispute Desk — Operator Runbook

Track 129 (v0.16.0) adds the decentralized-arbitration path: a purchase
dispute on the graduated rail can escalate to an ERC-792 arbitrator
(Kleros on Sepolia) through `contracts/RhizomeKlerosAdapter.sol`, with
the ruling mapped back onto `RhizomeEscrow.resolve` automatically.

This runbook covers the operator-facing flow: filing, evidence,
timeline, and fee responsibility. The escrow-deploy runbook
(`docs/operations/escrow-deploy.md`) covers the contract deployment;
the adapter deploys the same way with `(escrow, arbitrator, extraData)`
constructor args.

## Arbiter modes

`escrow_arbiter` is a spec, not just an address:

- **`0x…` (default)** — a designated EOA/multisig calls `resolve`
  directly. This is the v1 posture and remains the default; nothing in
  this document changes for designated-arbiter deployments.
- **`kleros:<court>`** — disputes escalate to an ERC-792 arbitrator.
  Requires `escrow_rail=rhizome` (Smart Invoice has no adapter slot)
  and `escrow_arbiter_adapter=<adapter contract>` — the adapter is what
  sessions name as their on-chain `arbiter`. The `<court>` is the
  Kleros subcourt id encoded into the adapter's `extraData` at deploy
  time; `kleros:0` is the general court.

Deploy one adapter per (escrow, arbitrator, subcourt) tuple. On Sepolia
the canonical arbitrator is `KlerosLiquid`; subcourt selection is a
governance decision (arbitration cost and juror specialization differ
per court).

## Filing a dispute

`rhizome market dispute <purchase|session> [reason]` — callable while
the purchase is `disputable`, `awaiting_release`, `session`, or
`awaiting_receipt`. On the graduated rail the module submits three
transactions in order:

1. `dispute(sessionId, evidenceHash)` — locks the escrow;
   `evidenceHash` is keccak256 over the evidence bundle below.
2. `submitEvidence(sessionId, bundle)` — emits the ERC-1497 `Evidence`
   event carrying the JSON bundle. Best-effort: a failed submission is
   audit-logged (`market.evidence.failed`) and can be retried out of
   band — the lock is already on-chain.
3. `createDispute(sessionId){value: arbitrationCost}` on the adapter —
   kleros-bound configs only; auto-submitted once. Best-effort the same
   way (`market.escalate.failed`), retryable via
   `rhizome market escalate <purchase|session>`.

On a designated-arbiter rail only step 1 runs — the multisig resolves
out of band per `escrow-deploy.md`'s governance rules.

## Evidence format

`rhizome market evidence <purchase|session>` prints the exact bytes the
arbiter reads:

```json
{
  "session_id": "0x<bytes32>",
  "terms_hash": "0x<keccak256>",
  "receipt": { /* the seller's signed _rhizome.receipt, verbatim */ },
  "reason": "the operator's dispute reason"
}
```

- `terms_hash` commits to the on-chain session facts:
  `keccak256(session_id | token | amount | task_hash)` — the arbiter
  verifies it against `sessions(sessionId)` before weighing anything
  else. A mismatched terms hash means the evidence describes a
  different session and carries no weight.
- `receipt` is the seller's signed receipt — the arbiter checks the
  Ed25519 signature against the seller's peer id, the `result_sha256`
  against delivered output, and the price against the session's
  released amount. A missing receipt is evidence *against* the seller
  (they control minting).
- `reason` is free-form operator text — jurors read it.

The seller's side is symmetric: sellers submit their own
`submitEvidence` transaction while the session is Locked (the module's
sell-side submits the receipt they minted). The contract allows
evidence from either party only while Locked.

## Timeline

- **Dispute window** — `escrow_dispute_window` seconds from `open`;
  `dispute` reverts after the deadline. File early.
- **Juror round** — after `createDispute`, the arbitrator's court round
  runs on-chain. Kleros general-court rounds on Sepolia are measured in
  hours-to-days; mainnet production courts are days. The module's
  watcher keeps the purchase in `disputed` state and applies the
  `Resolved` event when `rule()` lands — no operator action needed.
- **Appeal** — Kleros rulings are appealable on the arbitrator;
  appeals price each side separately and extend the timeline
  multiplicatively. The adapter forwards whatever final ruling the
  arbitrator delivers; appeal mechanics live entirely on Kleros.
- **No-arbitrator fallback** — if a dispute never escalates (fees
  unpaid, adapter down), the session stays Locked past the deadline:
  `claim`/`release`/`withdraw` all revert while Locked. The only
  release is `resolve` — which only the on-chain `arbiter` (the
  adapter) can call. Designated-arbiter sessions have the same shape
  with the multisig in that slot. Escalation after the deadline is
  still possible — `createDispute` only requires Locked status.

## Fee responsibility

- **Escalation fee** — `arbitrationCost(extraData)`, quoted live from
  the arbitrator at `createDispute` time and paid in the chain's
  native token (Sepolia ETH on testnet). **The party escalating pays.**
  Under auto-escalation that's the disputing buyer's configured wallet
  — the queued-signer path surfaces it in `web3-pending.json` like any
  other settlement tx.
- **Appeal fees** — priced per side by the arbitrator; the loser of the
  round funds the next. Outside the adapter's surface entirely.
- **No refund** — arbitration fees are consumed by the court and never
  return through the escrow, regardless of ruling.
- **Adapter contract** — takes no fee itself; it forwards `msg.value`
  verbatim to the arbitrator and holds no balance.

## Failure modes

| Symptom | Cause | Recovery |
|---|---|---|
| `escalate` reverts "session not locked" | `dispute` tx failed or the window lapsed before locking | Re-run `market dispute`; escalate only works on Locked sessions |
| `escalate` reverts "already escalated" | A dispute id exists for the session | Wait for `rule()`; check the adapter's `disputeOfSession` view |
| `escalate` reverts "not this adapter's session" | The session named a different arbiter at `open` | The session belongs to a designated arbiter — resolve via that path |
| `market.escalate.failed` in audit | Fee quote moved / endpoint hiccup | `market escalate` retries with a fresh quote |
| Evidence missing from the bundle | Receipt fetch never landed | Submit a corrected bundle out-of-band via `submitEvidence` |

## Deploy notes for the adapter

`RhizomeKlerosAdapter(escrow, arbitrator, extraData)` — one deployment
per tuple. `extraData` is the arbitrator's court-encoding (Kleros:
`abi.encodePacked(uint96(subcourt), uint96(jurors))` in the deployed
form — the standard encodes it per-arbitrator; consult the arbitrator's
docs for the byte layout). The constructor requires both addresses
non-zero. Sessions opt in by naming the adapter as `arbiter` at
`open()` — which the module does automatically whenever
`escrow_arbiter` resolves to the `kleros:` form.
