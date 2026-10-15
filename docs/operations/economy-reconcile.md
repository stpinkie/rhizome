# Economy Reconciliation Runbook

Bilateral ledgers diverge when the two sides disagree about what a
settle covers: a charge booked on one side but not the other, a settle
ack that crashed mid-commit, an entry disputed on one side only, or a
`--mark-only` marker written on exactly one side. The settle handshake
is designed to *detect* this — `econ.MatchReceivables` requires the
offered refs to hit the payee's accrued receivables exactly — and a
divergence answers `settle_reject` rather than settling mismatched
state. This runbook is the path from `settle_reject` back to a
converged pair of ledgers.

## Symptom: `settle_reject` with `econ:diverged`

The payee's receivable view of your offer didn't match — different
entry set, different total, or refs it doesn't recognize. Both sides
log an `econ:diverged` audit line and the payer records an
`econ_settle` `disputed` outcome. Nothing settles; nothing is lost.

### Step 1 — pull both ledger views

Your local view is daemonless:

```bash
rhizome mesh economy ledger --peer <peer-id> --state accrued --json
rhizome mesh economy balance <peer-id>
```

The peer's view of *your* shared ledger is the `ledger_query` verb on
`/rhizome/econ/1.0.0`: the peer returns `entries_digest` (sha256 over
its filtered view of the entries you're a party to) plus its per-unit
`balance` — a compact compare, not raw entries. `Mesh.EconomyLedgerQuery`
issues it programmatically; the operator-side equivalent is asking the
peer operator to run the same `economy ledger`/`balance` on their end
and diffing the outputs.

A digest match + equal balances means the ledgers already agree — the
reject was a stale-cache or timing artifact; retry the settle. A
mismatch continues below.

### Step 2 — diff the accrued sets

Compare your accrued **payable** entries (what you owe) against the
peer's accrued **receivable** entries (what they're owed). The usual
shapes:

- **An entry exists on only one side.** A charge the callee booked but
  the caller never verified (network drop between emit and verify), or
  a payable the caller booked that the callee rejected. The entry is
  one-sided — it cannot settle until both sides hold it. Decide
  out of band whether the charge is real, then either have the missing
  side dispute-and-resolve its own copy (to mirror state) or have the
  holding side write it off (step 4).
- **Same refs, different amounts.** One side's `amount` disagrees —
  almost always a stale price-sheet advert. Re-fetch the peer's advert
  (`network status --peers --json` shows the current signed terms),
  `dispute` the mismatched task, and resolve with the agreed correction.
- **A subset refuses to match.** Some entries settle fine and some
  don't — settle per-unit (`--unit`) to shrink the failing set, or
  `dispute` the offending task ids so they leave the matching pool and
  the clean remainder can settle.

### Step 3 — retry the clean set

Once `economy ledger --state accrued` on both ends lists the same entry
refs and totals for the unit:

```bash
rhizome mesh economy invoice <peer-id>     # the exact signed offer — diff before sending
rhizome mesh economy settle <peer-id> --unit <unit>
```

`invoice` emits the `SettleOffer` document the handshake will send
(nothing transmits). If the offer's refs and total equal the peer's
accrued receivable set, the ack path settles identical entries on both
ledgers under the shared `settle_id` (`sha256` of the signed offer) and
`settle_tx=econ-handshake`.

### Step 4 — the `--mark-only` escape hatch

When the disagreement is resolved *socially* — you paid out of band, or
agreed the entry isn't owed — attest it on your own ledger:

```bash
rhizome mesh economy settle <peer-id> --mark-only [--unit u]
```

This writes `settled` markers with `settle_tx=mark-local` on *your*
payables only and records an `attested` outcome — **the peer's
receivables stay accrued.** `--mark-only` is deliberately divergent:
use it when the divergence is the intent. The peer closes its side with
its own `settle`/`dispute`/`resolve` — or marks its receivables off
with its own attested write. Both sides converging on `settled` (or one
side `settled`, the other `written_off` per agreement) is the terminal
state; watch `balance`/`peer` until the flags clear.

### Step 5 — dispute workflow for contested entries

```bash
rhizome mesh economy dispute <task-id> "reason"
rhizome mesh economy resolve <task-id> --credit   # charge stands → back to accrued
rhizome mesh economy resolve <task-id> --drop     # not owed → written_off, never settles
```

Dispute freezes the task's entries on *your* ledger — they exit the
settle-matching pool immediately (a disputed set can't be offered or
matched). Resolution is the terminal call and it is per-side: there is
no wire dispute protocol — the two operators agree out of band and each
applies the same verdict on their own copy. `econ_dispute` score
outcomes land pending on the open and terminal (`resolved_for_peer` /
`resolved_against_peer`) on the resolve.

## web3 divergence

`--backend web3` adds a phase-2 failure mode: the payee acked
`awaiting_tx` but the on-chain transfer never verified. The in-flight
journal at `<RHIZOME_HOME>/economy-settle-inflight.json` holds the
(peer, unit) state machine — check `rhizome web3 pending` /
`web3 show <id>` for a queued approval (`settle_signer=approval`), or
re-drive: the mesh watcher resumes live records on startup and
re-broadcasts the *same* signed raw rather than double-paying. A
`rejected` phase-2 record means the payee's receipt verification failed
(chain id / sender / destination / amount mismatch) — that's a real
divergence about what was paid vs what was owed; reconcile the ledger
side per steps 1–5 and settle the disputed entries separately. `GET
/network/economy` (daemon) and the launcher `/api/network/economy`
surface live in-flight records for monitoring.

## What reconciliation cannot do

- It cannot force the peer's ledger to change — every transition is
  local. Convergence is both operators applying the same verdict.
- It cannot settle `disputed` or `written_off` entries — `resolve
  --credit` re-accrues first.
- It cannot repair a one-sided charge the other side refuses to
  acknowledge — write it off (`resolve --drop`) or leave it accrued
  forever. The ledger records; it does not collect.

## Observability hooks

- `mesh audit` / `mesh-audit.jsonl` — `econ.settle_offer`,
  `econ.ledger_query`, `econ.dispute`, `econ.resolve`,
  `econ.mark_settled`, `econ.overdue` lines carry unit/amount/entry
  ids/status.
- `mesh activity --kind 'mesh.econ.*'` — the live event stream
  (`charge`, `dispute`, `resolve`, `settle`, `settle_due`, `overdue`).
- `mesh peer <id>` — the `econ` split: counts, balances, and the
  `over_threshold`/`overdue` unit flags that tell you at a glance which
  pair needs attention.
