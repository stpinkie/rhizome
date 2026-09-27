# Escrow Survey — Settlement Rail for the Open Agent Work Market

Status: **decided.** v1 rail is **Smart Invoice** (`pkg/settlement`,
Track 101, v0.14.0). This document is the durable record of the
candidate evaluation, the semantic mapping onto the design's
`open/release/claim/dispute/resolve` lifecycle, and what remains for the
Track 104 binding/E2E and the v0.16.0 graduation arc (Tracks 127–129).

## Scope and criteria

The survey evaluated escrow candidates against the v0.14.0 market's
requirements:

- Sepolia / cheap-L2 deployability (E2E reachable; L1-cost escrows are a
  graduation problem)
- ERC-20 (USDC-class) support — sessions are priced in stable assets,
  not native gas tokens
- Dispute window + arbiter semantics matching
  `open/release/claim/dispute/resolve`
- `session_id`/`bytes32` keying (sessions are ACP wire objects)
- `eth_getLogs` event surface for `web3.WatchRunner`-style watches
- Audit/battle status and license

## Candidates

### Smart Invoice (lexDAO/RaidGuild) — **selected**

Factory-clone architecture: one canonical factory deploys a minimal
proxy escrow per invoice; escrow init data carries `(client,
resolverType, resolver, token, terminationTime, details,
wrappedNativeToken, requireVerification, factory)`. `resolverType` is
`ADR.INDIVIDUAL` (a designated address) or `ADR.ARBITRATOR` (an ERC-792
arbitrator); the v1 market uses INDIVIDUAL — the design's
designated-arbiter model verbatim.

- **Lifecycle match**: deposit → milestone `release()` → `lock()` →
  arbiter `resolve()` — a near-exact fit for
  open/release/dispute/resolve.
- **Deployability**: canonical Sepolia factory deployed and live
  (`deployments/sepolia.json`); `createDeterministic` +
  `predictDeterministicAddress` give the session escrow address *before*
  deployment — the seller can present the address the buyer will verify.
- **ERC-20**: any token; the escrow accrues balance via `balanceOf`
  (plain `transfer` in — no approve dance). Native-asset deposit exists
  via a wrapped-native path we don't use in v1.
- **Trust posture**: battle-tested by RaidGuild since ~2020 (guild
  service agreements), **never formally audited** — self-described beta.
  MIT license.
- **Cost**: per session = one clone deploy + one transfer; trivial on
  L2, acceptable on Sepolia, wrong on L1 — fine for the testnet-first
  posture.

### Kleros `MultipleArbitrableTokenTransaction` — deferred to Track 129

The *real* arbitration answer: ERC-792 arbitrator + ERC-1497 evidence
standard, ERC-20 variant exists, Kleros Court jurors with appeal rounds
(loser pays fees). Canonical arbitrator (`KlerosLiquid`) is deployed on
Sepolia, so testnet arbitration is reachable.

Deferred rather than selected because the v1 UX breaks: arbitration
fees are paid in the **native token** at lock time (fee-forfeiture on
timeout — a party that can't pay loses), the flow wants a court round
with human juror timing (minutes-hours, not the per-session latency
profile), and the contract keys escrows by transaction id that would
need a `session_id` indirection anyway. This is precisely the Track 129
decentralized-arbitration adapter: Smart Invoice's `ADR.ARBITRATOR`
resolverType is already ERC-792-shaped, so the graduated path slots
into the same rail without a new escrow.

### OpenZeppelin Escrow / RefundEscrow / ConditionalEscrow — rejected

Pull-payment primitives: a beneficiary withdraws what a depositor
released. No dispute semantics, no arbiter, no termination window. The
market's `dispute` verb would have no on-chain meaning.

### Custom-minimal fallback — documented, not built

The design's ~200-line sketch (open/release/claim/dispute/resolve +
designated arbiter) remains the fallback if Smart Invoice's deployment
surface rots. Its one property Smart Invoice lacks: a **native
seller-claim** (provider pulls after the dispute window lapses) — see
the claim gap below. Building a custom escrow is deferred to Track 127
(graduated contract), where it gets designed once for drawdown +
arbitration hooks rather than rushed for v1.

## Selection rationale

Smart Invoice wins on lifecycle fidelity *at testnet cost*: the only
candidate that is both Sepolia-live today and shaped exactly like the
v1 design (designated arbiter, dispute window as `terminationTime`,
per-session isolation). Its clone-per-session model also isolates
session state cleanly — a session_id *is* an address, not a row in a
shared contract.

## Semantic mapping — including the claim gap

The honest mapping onto the design's verbs:

| Design verb   | Smart Invoice call                    | Actor              |
|---------------|---------------------------------------|--------------------|
| `open`        | `createDeterministic` + `transfer`    | buyer              |
| `verifyLock`  | `eth_call` views + `balanceOf`        | seller (pre-work)  |
| `release`     | `release()` / `release(milestone)`    | buyer (client)     |
| `dispute`     | `lock(details)`                       | buyer              |
| `claim`       | `lock(details)` → arbiter `resolve`   | seller             |
| `resolve`     | `resolve(cA, pA, details)`            | arbiter (resolver) |

**The claim gap, stated plainly**: Smart Invoice has *no unilateral
seller-pull*. The only post-window safety valve is `withdraw()`, and it
pays the **client** — a provider cannot claw back if a buyer locks
payment and ghosts. The v1 `Claim` therefore maps to the provider
calling `lock(bytes32)` — freezing the escrow (either party may lock)
so the configured arbiter must `resolve()` an award. It is a real,
truthful action (forces resolution, stops the termination clock's
practical effect since `lock` reverts post-termination), but it is
*dispute mediation*, not a pull: the seller pays gas, the arbiter takes
the `resolutionRate` fee (default 20 ⇒ balance/20 = 5%), and an
unresponsive arbiter strands the escrow. Track 127's graduated contract
is where true seller-claim semantics land.

`terminationTime` IS the dispute window: after it, `release`/`lock`
revert and `withdraw()` opens the client clawback. Sellers should treat
`terminationTime − work_duration` as their real deadline — documented
here, enforced as market policy in Track 102/103 config (the rail
verifies `terminationTime > now` and exact match when terms carry one).

## `session_id` keying

`Open(correlationID, terms)` takes the buyer's off-chain reference.
`salt = keccak256(correlationID)`, and the escrow address is
`predictDeterministicAddress("escrow", salt)` — deterministic, so the
buyer computes the session_id before deploying and the seller verifies
the presented address on-chain. All other verbs take the escrow
address; the wire `session_id` IS the clone address.

## `escrow_*` field resolution

| Module field           | Smart Invoice concept                              |
|------------------------|----------------------------------------------------|
| `escrow_chain_id`      | EVM chain; drives `wrappedNativeToken` lookup      |
| `escrow_contract`      | **factory** address (not a single escrow)          |
| `escrow_token`         | default payment token (init `token`)               |
| `escrow_arbiter`       | `resolver` with `resolverType = ADR.INDIVIDUAL`    |
| `escrow_dispute_window`| seconds from open → `terminationTime = now+window` |

`escrow_contract` unset ⇒ fixture rail (`MockRail`) — the shipped
default posture.

## Deployments and ABI surface

Canonical Sepolia (from `deployments/sepolia.json` on `develop`):

- factory: `0x8227b9868e00B8eE951F17B480D369b84Cd17c20`
- `escrow` implementation: `0x49B76dE305933d75fC0eAd6ef090F555bcCD9735`

Bundled ABIs (`pkg/settlement/abi.go`): factory
(`create`/`createDeterministic`/`predictDeterministicAddress`/
`resolutionRateOf`, `LogNewInvoice` event), escrow (init, all public
state getters, `release`/`release(m)`/`lock`/`resolve`/`withdraw`/
`verify`, Deposit/Release/Withdraw/Lock/Resolve/Verified events), and
minimal ERC-20 (`transfer`/`balanceOf`/`allowance`/`approve`/`decimals`/
`symbol`, `Transfer`).

Notable contract details the rail models faithfully:

- `resolve` requires `clientAward + providerAward == balance −
  balance/resolutionRate`; the resolver fee defaults to `balance/20`
  (5%) when the factory has no registered rate.
- `lock` is callable by **either party**, is `payable` (msg.value only
  matters for ARBITRATOR type), reverts post-`terminationTime`.
- `verify()` is event-only in this contract version — no gating;
  `requireVerification=false` in init data avoids the extra client tx.
- `MAX_TERMINATION_TIME` = 63113904 s (2 yr) upper bound at init.
- `resolutionRate` is a divisor, not a percent: fee = balance/rate.

## Security/trust posture

- **Unaudited**: Smart Invoice carries real RaidGuild battle history but
  no formal audit; the market defaults to the fixture rail and marks
  testnet-verified-experimental, not mainnet.
- **Resolver trust**: INDIVIDUAL resolution is a designated-address
  decision — no appeal, no juror economic security. Arbiter config is
  seller-visible in the offer/advert; v1 assumes operators run or
  designate arbiters they trust (Track 129 upgrades the mechanism).
- **VerifyLock is the gate**: sellers must not spend LLM budget on a
  presentation that fails the on-chain check; the rail returns strict
  `(false, nil)` mismatches and errors only on evaluation failures.
- **No payment amounts in adverts**: pricing stays in `offers_json`;
  settlement facts live on-chain only.

## Deferred

- **Track 104**: Solidity `.sol` sources, wallet-backed `Sender`
  (key signing), chain policy wiring, real Sepolia deploy + recorded
  E2E with tx hashes.
- **Track 128**: drawdown — `release(milestone)` partials (the ABI
  already ships it).
- **Track 129**: `ADR.ARBITRATOR` + KlerosCourt/`IArbitrator` adapter,
  ERC-1497 evidence.
- **Track 127**: graduated contract decision — custom escrow with
  native seller-claim vs staying on Smart Invoice.

## References

- `smartinvoicexyz/smart-invoice` @ `develop` —
  `apps/contracts/contracts/SmartInvoiceFactory.sol`,
  `SmartInvoiceEscrow.sol`, `interfaces/{ISmartInvoiceFactory,
  ISmartInvoiceEscrow,ISmartInvoice}.sol`,
  `deployments/sepolia.json` (verified 2026-03: factory + escrow impl
  above, `escrow` type registered).
- `kleros/kleros` court + `escrow-contracts` (MATT) — Track 129.
- OpenZeppelin Contracts `utils/escrow` — rejected.
- `pkg/settlement` (this repo) — the rail + fixture + fake endpoint.
