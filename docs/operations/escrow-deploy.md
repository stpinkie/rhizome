# Graduated Escrow Deployment (RhizomeEscrow)

Track 127 (v0.16.0) adds the graduated settlement rail:
`contracts/RhizomeEscrow.sol` — a single session-keyed escrow contract
replacing Smart Invoice's clone-per-session factory for the market
module. Sessions are keyed by a bytes32 id the buyer derives off-chain
(`keccak256(correlationID)`), which buys two things the Smart Invoice
surface could not express:

- **amount-based partial releases** — `release(sessionId, amount)` is the
  drawdown primitive Track 128 rides (Smart Invoice `release()` is
  milestone-indexed and pays whole milestones),
- **a native seller claim** — `claim(sessionId)` pays the undisputed
  remainder to the seller after the dispute deadline (Smart Invoice's
  only post-window valve paid the *client*, stranding providers whose
  buyers went silent — the claim gap documented in
  `docs/design/escrow-survey.md`).

Selecting it: `escrow_rail = rhizome` on the market module (default is
`smart_invoice`, unchanged). `escrow_contract` then points at the
RhizomeEscrow deployment address — the module refuses to start with an
unknown `escrow_rail` value or a malformed contract/arbiter address.
The graduated rail is ERC-20-only: no wrapped-native address is needed
or accepted.

## Contract surface

`open(sessionId, seller, token, amount, taskHash, disputeWindow,
arbiter)` — buyer funds via `transferFrom` (approve first);
`release(sessionId, amount)` — buyer-only partial while the window runs;
`dispute(sessionId, evidenceHash)` — either party, freezes the session;
`submitEvidence(sessionId, uri)` — ERC-1497 `Evidence` emission while
locked; `resolve(sessionId, buyerAward, sellerAward, rulingHash)` —
arbiter-only, awards must consume the remaining balance exactly;
`claim(sessionId)` — seller-only after the deadline;
`withdraw(sessionId)` — buyer-only after `deadline + CLAIM_GRACE`
(14 days) — the abandonment clawback so a session nobody settles cannot
strand funds. `sessions`/`sessionOf` are the verification views.

The module's `eth_getLogs` watcher filters `contract-address +
sessionId topic` under this rail (the clone-address filter under
Smart Invoice), so `Opened`/`Released`/`Disputed`/`Resolved`/`Claimed`/
`Withdrawn` all carry `sessionId` as `topic[1]`.

## Deploy runbook (Sepolia)

Toolchain: any solc ≥ 0.8.24 (the pragma). The checked-in contract was
compiled clean with solc 0.8.30 (`optimizer enabled, runs 200`) — no
warnings, ~6.8 KB bytecode. With solc-js:

```sh
npm install solc@0.8.30
node compile.js   # standard-json input; emit abi + evm.bytecode
```

1. Fund the deployer key with Sepolia ETH (a faucet is fine; deployment
   is ~6.8 KB of bytecode — well under 0.01 ETH of gas at typical
   testnet prices).
2. Deploy `RhizomeEscrow` — it has **no constructor arguments**. Every
   session carries its own arbiter, token, and window, so one deployment
   serves every market pairing on the chain.
3. Source-verify on Etherscan (`Standard JSON Input`, optimizer
   `enabled / 200 runs`, `evmVersion: default`). Verification matters:
   `escrow_contract` is an operator-visible address and buyers/sellers
   check the source before committing funds.
4. Configure the market module on every participating node:
   `escrow_rail=rhizome`, `escrow_contract=<deployed>`,
   `escrow_token=<ERC-20>`, `escrow_arbiter=<multisig>`,
   `escrow_chain_id=11155111`, `escrow_dispute_window=<seconds>`.
5. Record the deployment in `.todo.md` / the release notes: address,
   tx hash, verify link, deployer key custody note.

## Arbiter v1 — designated multisig

The v1 arbiter is a **designated multisig** (Safe recommended) whose
address is the module's `escrow_arbiter`. It calls
`resolve(sessionId, buyerAward, sellerAward, rulingHash)` on `Disputed`
sessions; awards must sum to the session's remaining balance exactly —
there is no fee parameter (a fee-taking arbiter is a Track 129 adapter
contract, not a config value here).

Governance requirements:

- The multisig SHOULD be ≥ 2-of-3 with signers documented in the
  operator's governance notes; a single-EOA arbiter is a live rug
  surface (it can award the entire escrow to either party).
- Signers MUST be independent of the operators who run market seller
  nodes on the same deployment, or arbitration is self-dealing.
- `rulingHash` SHOULD commit to the off-chain ruling document (e.g.
  `keccak256` of the signed ruling JSON) so the on-chain record anchors
  the reasoning.
- Key custody: the multisig, not the deployer key, holds arbiter power —
  the deployer has no privileged role in the contract at all.

## Security-review checklist (pre-mainnet gate)

The contract is deliberately small (~290 lines, no inheritance, no
external calls except ERC-20 `transfer`/`transferFrom`). Before any
non-testnet deployment, walk this checklist — each item maps to a real
failure mode:

- **Reentrancy** — every state-mutating external call (`open`,
  `release`, `dispute`, `resolve`, `claim`, `withdraw`) carries
  `nonReentrant`; state writes precede token transfers
  (checks-effects-interactions). A malicious ERC-20 with callback hooks
  is the threat model — verify a re-entering `release` cannot double-pay
  (`status`/`released` are already updated).
- **Window arithmetic** — `deadline = block.timestamp + disputeWindow`
  uses uint64 with `MIN_DISPUTE_WINDOW` (1 h) / `MAX_DISPUTE_WINDOW`
  (365 d) bounds; claim/withdraw comparisons are strict `>` so the
  boundary block belongs to the seller's claim, not the buyer's
  withdraw.
- **Balance accounting** — `released` can never exceed `amount`
  (`released + amount <= s.amount` under ^0.8 checked arithmetic);
  `resolve` requires awards to consume the remainder exactly, so no
  dust strands and no award can exceed the escrow.
- **Refund path** — `withdraw` is unreachable until
  `deadline + CLAIM_GRACE`; `claim` takes precedence for the whole
  grace window. Funds can only leave to `buyer` or `seller`, never to
  the arbiter or a third party.
- **Arbiter key custody** — arbiter is per-session, set at `open()` by
  the buyer from module config; a compromised arbiter key affects only
  sessions opened after the compromise IF the config is rotated, so
  rotate `escrow_arbiter` on any suspected key leak and treat the
  multisig signers as the audit trail.
- **Token trust** — the contract trusts `token` to implement ERC-20
  honestly (return-true + real transfer). `escrow_token` must point at
  a known-good token; a malicious token could lie about `transferFrom`
  and create unfunded sessions — `VerifyLock` cannot detect that on its
  own, so token admission is an operator decision.
- **Session id collisions** — `open` refuses a used `sessionId`; buyers
  derive it from `keccak256(correlationID)` with a random 16-byte
  correlation id, so collisions are a caller bug, not a contract risk.
- **ERC-1497/792 hooks** — `Evidence` carries the session id as the
  evidence group and the per-session arbiter as `arbitrator`; a Track
  129 ERC-792 adapter contract can occupy the `arbiter` slot without a
  redeploy (it receives `Ruling`-style callbacks through `resolve`).

Fake-chain coverage of every clause above lives in
`pkg/settlement/track127_test.go` (open/funding, partial and full
release, claim, withdraw, dispute/resolve, wrong-caller and bad-amount
reverts, deadline/grace arithmetic, event queries).
