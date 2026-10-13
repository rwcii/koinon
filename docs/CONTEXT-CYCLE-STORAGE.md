# Managed context cycle storage

A managed context cycle (#196, chunk 02) promises one terminal outcome: complete, cancelled,
failed or uncertain. This note derives the page credit that keeps that outcome possible when
ordinary storage is full. It uses the method and the source assumptions of the
[work-items storage note](WORK-ITEMS-GO-STORAGE.md): 4,096-byte pages, at most 130,681
database pages, SQLite 3.53.4 as `modernc.org/sqlite` v1.60.1 builds it, header schema format
4, and the catalog check that refuses any schema object the daemon did not create.

## What a cycle funds

The plan proposed a credit of 512 pages per active cycle that funds every remaining
transition. One transition changes at least the cycle row, an event and an audit row. At a
B-tree height of 17, each changed object allows 4 × 17 + 1 = 69 pages, so 15 funded
transitions would need more than 5,000 pages, ten times the proposal. This chunk therefore
funds only the terminal outcome, which the definition of done requires to complete at
ordinary saturation, and corrects the allocation as the plan asks:

- Non-terminal stages (`ready`, the intents and receipts, `reset_verified`, `waiting_pickup`,
  `waiting_prompt`) are ordinary writes. At ordinary saturation they are refused; the cycle
  then waits until its stage or cycle deadline, and that deadline's terminal outcome is funded.
  An intent is recorded before its input, so a refused intent enters no input.
- The terminal outcome is a control write that clears the cycle's 0/1 `credit` flag in its
  own transaction. Every control that ends a cycle (Disable, a family mode without the cycle,
  retirement, purge, deletion, the maintainer's holder choice, a deadline, an unproven intent
  and a pickup completion) is a control write.
- The last of the 16 event slots is kept for the terminal outcome, so the outcome never needs
  a seventeenth event.

## Allocation of a terminal outcome

The largest terminal outcome is a completion by a successor, which also moves the Enable.

| Changed object | Pages |
| --- | ---: |
| `context_cycles` row rewrite (at most about 7.3 KB: a 4,096-byte socket path, a 1,024-byte report, three 256-byte keys and names) | 69 + 2 |
| `context_cycle_events` row (a `WITHOUT ROWID` table: its key is its row, under 300 bytes) | 69 |
| `audit` row (fixed codes and the cycle ID and peer name) | 69 |
| its `audit_at` index entry | 69 |
| the requester's `context_enables` row (it holds the socket path) | 69 + 2 |
| the successor's `context_enables` row | 69 + 2 |

The sum is 414 pages and 6 overflow pages, 420 pages, with ceil(420 / 819) + 1 = 2
pointer-map pages, **422**. The credit is rounded up to **512 pages per unfinished cycle**.
Other terminal outcomes change a subset of these objects. A dashboard action that ends a cycle
also writes its own change and its own audit record; those are control writes that use the
2,048-page reserve, as retirement, purge and the holder choice did before this chunk.

The `credit` flag has no index, and the catalog check refuses an index on it. At most 16
cycles are unfinished, so the cycle debt is at most 16 × 512 = 8,192 pages (32 MiB), added to
the work debt in `debtPages` and subtracted, after the mutation, in every write ceiling.

## Retention

Finished cycles are deleted with their events after 30 days, or beyond the newest 1,024, in a
control write once a minute. Unfinished cycles and their credits are never deleted. Deletion
frees pages; it allocates none beyond the B-tree rebalancing that every deletion in the
database already relies on.

## Evidence

`TestContextCycleControlsAtSaturation` admits 16 cycles with a 4,096-byte socket path, long
session IDs and epochs, drives four of them to `waiting_pickup`, fills ordinary storage, and
then commits four Disables, four retirements, four timeouts and four pickup completions with
1,024-byte reports. It checks each control's page growth against the 512-page credit, that
no credit remains and the database's integrity. These observations illustrate the bound;
the sum above establishes it. A changed SQLite version, schema or allocation path requires
revisiting this derivation.
