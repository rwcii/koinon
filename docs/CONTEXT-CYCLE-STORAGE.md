# Managed context cycle storage

A managed context cycle (#196, chunk 02) promises every remaining transition after its
admission and one terminal outcome: complete, cancelled, failed or uncertain. This note derives
the page credit that keeps those transitions possible when ordinary storage is full. It uses
the method and the source assumptions of the [work-items storage note](WORK-ITEMS-GO-STORAGE.md):
4,096-byte pages, at most 130,681 database pages, SQLite 3.53.4 as `modernc.org/sqlite` v1.60.1
builds it, header schema format 4, and the catalog check that refuses any schema object the
daemon did not create. One insertion or size-changing update of a B-tree allows 4 × 17 + 1 =
69 pages plus its overflow pages.

## What a cycle funds

A cycle has at most 16 events. Admission writes the first, and the last is kept for the
terminal outcome, so at most 14 non-terminal stages follow admission. Every one of them is
funded, as the plan's shared contract requires:

- Each non-terminal stage (`ready`, the intents and receipts, `reset_verified`,
  `waiting_pickup`, `waiting_prompt`, and the return to `waiting_ready`, to a prompt's preceding
  stage or, in the prompt wait of a pickup, to `waiting_prompt` (#520)) is a control write that debits **280 pages** from the cycle's credit in its own
  transaction.
- The terminal outcome is a control write that sets the credit to 0. Every control that ends a
  cycle (Disable, a family mode without the cycle, retirement, purge, deletion, the
  maintainer's holder choice, a deadline, an unproven input, the event bound and a pickup
  completion) is a control write.
- A stage that would take the 16th event slot ends the cycle instead, so a cycle never needs
  more than 14 stage debits and one terminal outcome.

The credit at admission is 14 × 280 + 491 = **4,411 pages**. A cycle that ends early releases
the rest at its terminal commit.

## Allocation of a stage

| Changed object | Pages |
| --- | ---: |
| `context_cycles` row rewrite (at most about 7.4 KB: a 4,096-byte socket path, a 1,024-byte report, three 256-byte keys and names, the 96-byte refusal slot) | 69 + 2 |
| `context_cycle_events` row (a `WITHOUT ROWID` table: its key is its row, under 300 bytes) | 69 |
| `audit` row (fixed codes and the cycle ID and peer name) | 69 |
| its `audit_at` index entry | 69 |

The sum is 278 pages, with ceil(278 / 819) + 1 = 2 pointer-map pages, **280**. `audit` is an
`AUTOINCREMENT` table, so each audit insert also rewrites its `sqlite_sequence` row. That table
holds one row of under 64 bytes for each of the database's four `AUTOINCREMENT` tables, so it
stays a single page that never splits, and the rewrite allocates nothing.

## Allocation of a terminal outcome

The largest terminal outcome is a completion by a successor, which also moves the Enable.

| Changed object | Pages |
| --- | ---: |
| `context_cycles` row rewrite | 69 + 2 |
| `context_cycle_events` row | 69 |
| `audit` row | 69 |
| its `audit_at` index entry | 69 |
| the requester's `context_enables` row (it holds the socket path) | 69 + 2 |
| the successor's `context_enables` row | 69 + 2 |
| its primary-key index entry `(family, session_id)` | 69 |

The sum is 483 pages and 6 overflow pages, 489 pages, with ceil(489 / 819) + 1 = 2 pointer-map
pages, **491**. Other terminal outcomes change a subset of these objects. A dashboard action
that ends a cycle also writes its own change and its own audit record; those are control writes
that use the 2,048-page reserve, as retirement, purge and the holder choice did before this
chunk.

At most 16 cycles are unfinished, so the cycle debt is at most 16 × 4,411 = 70,576 pages
(about 276 MiB). `debtPages` adds the remaining credits of the unfinished cycles to the work
debt, and every write ceiling subtracts it after the mutation. An admission that does not fit
below the ordinary ceiling with its credit is refused and stores nothing.

Schema 24 (#196, chunk 03) adds the interactive bootstrap stages `bootstrap_intent` and
`bootstrap_verified`. They are non-terminal stages like the others: each debits 280 pages, and a
cycle with a bootstrap still has at most 14 stages under the 16-event bound, so the credit is
unchanged. The migration rebuilds `context_cycles` and returns the old table's pages before the
first write. Since #406 no cycle enters these stages; the schema keeps them for cycles that an
earlier binary stored in them.

Schema 34 (#509) adds the native compaction stages `compact_waiting`, `compact_intent` and
`compact_sent`, by the same rebuild. A compaction is a cycle row with the same credit: its
admission writes no request message and protects no claim, so it writes less than a handoff
cycle's, and each of its stages and its terminal outcome is funded as above. A return from
`compact_intent` to `compact_waiting` is a stage like any other, so the 16-event bound also
bounds the retries of one compaction.

## Refused ready reports

Each cycle row has a `ready_refusal` slot of 96 bytes (#485): a 20-digit revision, a space and
the refusal code padded to 75 bytes. Admission writes it empty, in the row that the admission
already inserts; schema 30 writes it into each unfinished cycle. A refused ready report at the
current `waiting_ready` revision overwrites the slot with a value of the same width, in a
control write. A cell rewritten at the same size is overwritten in place, so the write
allocates no page and needs no credit; the test checks the page and freelist counts. The
`waiting_ready` timeout names the slot's code when the slot holds the current revision.

The text of each refused report is kept in `context_cycle_refusals`, the newest 8 per cycle,
each with a code and at most 1 KiB of text. That history is an ordinary write outside the
cycle's credit and takes no event slot: when the store has no room for an ordinary write, the
history keeps no text, and the slot still names the code.

## Retention

Finished cycles are deleted with their events and refused ready reports after 30 days, or beyond the newest 1,024, in a
control write once a minute. Unfinished cycles and their credits are never deleted. Deletion
frees pages; it allocates none beyond the B-tree rebalancing that every deletion in the
database already relies on.

## Evidence

`TestContextCycleControlsAtSaturation` admits 16 cycles with a 4,096-byte socket path, long
session IDs and epochs, the longest handoff file name, a funded work claim and a pending work
verdict. It fragments the audit, cycle and event trees (rows in random key order, then every
other row deleted) and fills ordinary storage. Then one cycle spends all 14 funded stages on
the most prompt rounds the event bound allows and ends at the bound; three clear, prove the
reset and enter pickup; one completes as the retained caller and two as managed successors
with 1,024-byte reports; and the rest end by Disable, retirement and timeouts. The work finish
and verdict also commit. Each control's page growth is checked against the credit it releases,
and no cycle or verdict credit remains. These observations illustrate the bound; the sums above
establish it. A changed SQLite version, schema or allocation path requires revisiting this
derivation.
