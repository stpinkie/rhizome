# Paired Settlement — Mesh Economy

The mesh economy is **bilateral billing between paired operators**: two
people who have already exchanged trust over `/rhizome/pair/1.0.0` can
charge each other for remote work on a mutual-credit ledger, then settle
the balance periodically — off-chain (`ledger`) or on-chain (`web3`).
It answers "run work on my friend's GPU and square up later", not "buy
compute from a stranger".

> **Trusted tier only.** The economy meters work between *trusted peers*
> — nodes that paired out of band and already accept each other's tasks.
> Selling work to the open internet is the `rhizome-market` module's job
> (escrow, untrusted counterparties); see
> [the market guide](market.md). The economy never runs on a peer you
> didn't pair with, and it bills nobody unless both sides opt in.

## Honest limits

Know what the ledger is before turning it on:

- **It is bookkeeping, not enforcement.** A charge means "the callee
  claims this usage; the caller verified the math against the
  advertised price sheet". Nothing forces anyone to pay — settlement
  is an operator-to-operator act. The ledger records; it does not
  collect.
- **Usage is callee-reported.** The callee computes the charge from the
  `usage` it reports (per-task + per-token + per-second rates). The
  caller verifies the arithmetic and the sheet digest — it cannot verify
  that the reported token counts are honest. Bill peers you trust to
  meter correctly.
- **Disputes are conversations.** `economy dispute` freezes entries;
  `economy resolve` re-accrues them (`--credit`) or writes them off
  (`--drop`). There is no arbiter — the two operators talk it out, and
  each side applies its own resolution.
- **`--mark-only` diverges by design.** It attests "we settled out of
  band" on *your* ledger only; the peer's receivable stays accrued until
  it settles on its own side. Use it when the divergence is the intent
  (cash, bank transfer), never as a shortcut around a failed handshake.
- **Amounts are decimal strings.** `price_sheet` values are canonical
  decimals (digits, one optional `.`, no sign/exponent) — `"0.05"`, not
  `0.05` or `"5e-2"`. Malformed amounts fail validation loudly.

## Setup

Economy terms ride the pairing handshake. When the inviter's manifest
advertises economy terms, `--accept` prints the counterparty's price
sheet and payout, and the signed bundle carries them under a separate
`econ_sig` so pre-economy builds still pair normally.

Enable it in `mesh.economy` (see `config/config.example.json`):

```json
{
  "mesh": {
    "economy": {
      "enabled": true,
      "unit": "credits",
      "price_sheet": {
        "per_task": "0.01",
        "per_1k_prompt_tokens": "0.002",
        "per_1k_completion_tokens": "0.004",
        "per_second": "0",
        "min_charge": "0.001"
      },
      "accept_units": ["credits"],
      "bill_peers": ["<peer-id you bill>"],
      "max_cost_per_task": "0.05",
      "max_cost_per_day": "1",
      "settle_threshold": "10",
      "settle_grace_period": "72h",
      "settle_backend": "ledger"
    }
  }
}
```

- `unit` — your home unit; required when enabled. Charges in other
  units are refused unless the unit is in `accept_units`.
- `price_sheet` — what you *charge*. `min_charge` is also the rejection
  floor: a caller whose `max_charge` lands under it gets a
  `price_floor:` refusal before any work runs.
- `bill_peers` — the **explicit billing allowlist**. Pairing grants
  trust; billing needs the peer id here too. A billed peer that omits
  economy terms gets an `econ:` refusal; peers not on the list are
  served unbilled, exactly as before.
- `max_cost_per_task` / `max_cost_per_day` — your *spend* caps as the
  caller. The day cap counts the committed (accrued + settled) sum over
  the trailing 24 h.
- `settle_threshold` — advisory balance that means "time to settle".
  Purely a signal: nothing auto-settles.
- `settle_grace_period` — how long an over-threshold receivable may sit
  before the payee records an `econ_settle` score failure on the payer.
  Reputation pressure, not collection. Requires `settle_threshold`.
- `settle_backend` — `ledger` (default; mutual credit, you reconcile
  out of band) or `web3` (close out on-chain; needs `payout`).

Only seller terms are advertised: `unit`, `price_sheet`, `accepts`,
`payout`, `settle_backends` ride the signed capability manifest and the
pairing bundle. `bill_peers`, `max_cost_*`, and the settle policy never
leave your config.

## How charging works

When a caller on the callee's `bill_peers` sends `econ{accept,
max_charge, unit}` terms (automatic when both nodes have economy
enabled), the callee meters the run and returns a signed `charge` on
success — failures never charge. The charge computes exactly (rational
arithmetic) as `sheet·usage` bounded by the negotiated `max_charge`;
`truncated` marks charges clipped by the cap. The caller verifies the
sheet digest and recomputes the bound against the cached advert;
mismatches land `econ:charge_mismatch` and record a peer-call failure
plus an `econ_charge` score event.

Both sides then journal the same entry — callee `receivable`, caller
`payable` — in `<RHIZOME_HOME>/economy-ledger.jsonl` (event-sourced:
the latest line per `entry_id` is its state; 10 MB × 3 rotation).
Resubmits and result re-fetches dedupe on `(direction|peer|task|
correlation)` — a redelivered result never double-books.

Cost-aware dispatch rides on top: `--max-cost` on `route`/`scatter`/
`delegate`/`spawn`/`task submit` binds `econ.max_charge` (capped by
`max_cost_per_task`), drops candidates whose `min_charge` exceeds the
cap before sending, and adds a small proportional cost bonus to
candidate ranking. `swarm offer --max-price` does the same for claims —
each claim carries the claimant's `quote` estimate and over-cap claims
are filtered before the pick.

## Reading the ledger

```bash
rhizome mesh economy balance            # per-peer accrued/settled, both directions
rhizome mesh economy balance <peer-id>  # one peer
rhizome mesh economy ledger --peer <id> --state accrued --since 24h
rhizome mesh economy ledger --json      # raw entries for tooling
```

`balance` prints a "run `economy settle`" hint when an accrued payable
sits over `settle_threshold`. Reads are daemonless — they read the
ledger file directly. `rhizome mesh peer <id>` shows the same split
(charges issued/received, settled/disputed counts, per-unit balances,
`over_threshold`/`overdue` flags) under its economy section, and the
Network page's Economy panel shows balances, in-flight web3 settles,
and the recent-charge tail when a daemon is up.

## Settling

```bash
rhizome mesh economy invoice <peer-id>              # dry-run: the signed offer you'd send
rhizome mesh economy settle <peer-id> [--unit u]    # the handshake (daemon required)
rhizome mesh economy settle <peer-id> --mark-only   # attest out-of-band settlement
rhizome mesh economy settle <peer-id> --backend web3
```

**`ledger` backend** — the payer builds a signed `SettleOffer` over its
accrued payable entries and offers it over `/rhizome/econ/1.0.0`. The
payee matches every offered ref against *its* accrued receivables for
the payer/unit — same entries, same total — and either acks (both
ledgers mark the identical entry set `settled` under a shared
`settle_id`) or rejects with a reason (`settle_reject` +
`econ:diverged` audit on both sides). Disputed entries are never
offered or matched. `invoice` emits the exact signed document without
transmitting — diff it against `economy ledger` when you want to see
what a settle would cover.

**`web3` backend** — a two-phase close-out on the same protocol: the
payee verifies receivables and acks `awaiting_tx`; the payer transfers
the payee's manifest-advertised `payout` asset through the web3
machinery (`settle_signer=approval` queues it in the human-gated
pending-approval queue — `rhizome web3 approve <id>` executes;
`settle_signer=wallet` signs inline from `settle_from`); the payer
re-offers with `tx_hash`; the payee verifies the confirmed receipt —
chain id, sender, destination, exact amount — and rejects a hash the
ledger already settled under: a tx is single-use, so replaying it
against fresh receivables is a `settle_reject`, not a second
settlement. (The identical-entries retry after a lost ack re-acks
idempotently — the tx is the shared anchor.) Only then do both ledgers
settle under `settle_tx=<tx hash>`. A crash anywhere in phase 2 resumes
from `<RHIZOME_HOME>/economy-settle-inflight.json` rather than
double-paying. The peer must advertise `settle_backends:["web3"]` —
the payer fails closed otherwise.

**`--mark-only`** — writes a local attested marker (`settle_tx=
mark-local`) asserting you settled out of band. It is the divergence
*escape hatch*, not the settlement path: the peer's view keeps its
receivable accrued until it marks its own side. Use it after a real
out-of-band payment — or after a `settle_reject` you've manually
reconciled (see the
[reconciliation runbook](../operations/economy-reconcile.md)).

## Disputes

```bash
rhizome mesh economy dispute <task-id> "charged for a truncated run"
rhizome mesh economy resolve <task-id> --credit   # re-accrue: the charge stands
rhizome mesh economy resolve <task-id> --drop     # write off permanently
```

`dispute` freezes the task's entries — they leave the settle-matching
pool and stay visible in `economy ledger --state disputed`. `resolve
--credit` returns them to `accrued`; `resolve --drop` moves them to
`written_off`, which never settles. Disputes are per-side: your
dispute marks *your* payable/receivable view; the peer's copy is
unmoved until it acts. Score events land on both transitions —
`econ_dispute` pending on the open, `resolved_for_peer` /
`resolved_against_peer` on the terminal call.

## Observability and reputation pressure

Every charge, settle, dispute, and resolution lands as an audit line in
`mesh-audit.jsonl` (unit, amount, entry ids, refs) and a `mesh.econ.*`
runtime event on the activity feed (`mesh.econ.charge|dispute|resolve|
settle|settle_due|overdue`). Peer scores carry economy outcomes as
bounded refs — session ids + sha256 value commitments, never raw
amounts — under the `econ_charge`/`econ_settle`/`econ_dispute` op
counters.

`settle_threshold` + `settle_grace_period` are the pressure dial:

- A payable balance crossing `settle_threshold` fires
  `mesh.econ.settle_due` once (edge-triggered) and makes `economy
  balance` print the settle hint.
- A receivable sitting over `settle_threshold` longer than
  `settle_grace_period` emits `mesh.econ.overdue` and records **one
  `econ_settle` failure per debt epoch** on the payer — deduped on the
  contributing entry set, so a restart doesn't re-penalize and new
  accrual re-arms. Overdue units surface on `mesh peer` and the Network
  panel.

Neither moves money. They make "owes and won't settle" visible — to
you, and to the peer's score — and leave the actual settling to the two
operators.
