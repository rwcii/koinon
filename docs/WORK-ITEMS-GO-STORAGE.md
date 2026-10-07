# Work-items storage in the Go daemon

The Go daemon keeps work items in its one state database, beside every store, session,
message and launch. This note adapts the [Python derivation](WORK-ITEMS-STORAGE.md) to that
database. Its method, its 0/1 flag-only control shape and its slot and logical-byte ledgers are
unchanged; its page figures are replaced, because the Go database is larger, holds every
repository, and keys the memory rows by the repository path.

## Source assumptions

- The page ceiling of the [daemon's storage bound](../PROTOCOL.md#memory-stores): 4,096-byte
  pages, a write-ahead log proven empty before every write, and at most 130,681 database pages
  (1 GiB for the database and its log). Ordinary writes keep 2,048 reserve pages free.
- SQLite as `modernc.org/sqlite` v1.60.1 builds it (SQLite 3.53.4), with the allocator of
  `btree.c`: a balancing walk can allocate up to four pages per level plus one root-deepening
  page; an overflow chain carries 4,092 bytes per page.
- Header schema format 4. In that format a 0/1 integer is stored in its record header alone, so
  the flag-only `UPDATE` of `claim_bundles` (no key or indexed column in its `SET` list) rewrites
  a cell of equal size and allocates no page. The daemon reads the format at offset 44 of the
  database header when it opens the database and refuses any other value.
- The work tables key a store by its 32-character `store_id`, not by the repository path, so
  their rows and indexes have bounded keys. The memory stream rows (`memory_entries`, its
  primary-key index and its live index) and the store row keep the repository path, at most
  4,096 bytes.

## Allocation per control

Python bounds a tree's height by its row count. One Go database holds every repository, so row
counts are not bounded per tree; the page ceiling is. A B-tree in a file of at most 130,681
pages, with at least two children per interior page, has height at most
1 + floor(log2 130,681) = 17. One insertion or size-changing update therefore allows
4 × 17 + 1 = 69 pages plus its overflow pages.

| Changed object | Pages |
| --- | ---: |
| `memory_entries` row of the event (repository path, consumer, author) | 69 + 2 |
| its primary-key index `(repository, seq)` | 69 + 2 |
| `memory_entries_live` index | 69 + 2 |
| `memory_stores` row (the head; its size can change) | 69 + 2 |
| `work_events` row (16 KiB payload) | 69 + 5 |
| its primary-key index and `work_events_item` | 69 + 69 |
| `work_items` rewrite (16 KiB image) | 69 + 5 |
| `work_replays` row (2 KiB result) and its primary-key index | 69 + 1 + 69 + 1 |
| claim flag overwrites | 0 |

A release or finish with its replay row changes all ten objects: 690 pages, 20 overflow pages,
and ceil(710 / 819) + 1 = 2 pointer-map pages, **712**. An overdue or expiry control writes no
replay row: **572**. The credits are rounded up: **576 pages for an overdue credit** and
**768 pages for an end credit**, 1,344 per bundle. The sum, not an observation, establishes
the allowance.

## Limits and the ledger

A store retains at most 16 claim bundles and the daemon at most 32, so the page debt is at most
32 × 1,344 = 43,008 pages (168 MiB); ordinary writes keep at least 85,623 pages. The debt is
derived from the durable credit flags of the retained bundles and subtracted, after the
mutation, in the one storage boundary that every daemon write passes:

| Write class | Page ceiling inside the transaction |
| --- | --- |
| Ordinary writes (sessions, messages, launches, notes, snapshots, consumers, work creation, starts, updates) | max pages − 2,048 − debt − commit slack |
| Control writes (progress, withdrawals, expiry, work controls) | max pages − debt − commit slack |

A work control clears the credit it spends in its own transaction, so it may use exactly that
allowance; every other write keeps all remaining debt free. The per-store ledger keeps, for each
unspent credit, one entry and one event slot and 48 KiB of logical bytes, and for each end credit
one replay slot, in the ceilings of every memory and work write of that store. The Go row charges
are the UTF-8 bytes of the text columns plus 512 per work row, and key, consumer, operation and
result plus 192 per replay row; one control writes at most a 16 KiB image increase, a 16 KiB
event, a 2 KiB result and about 2 KiB of keys and provenance, within the 48 KiB.

## Evidence

`TestWorkSaturationAndControlAllowances` fills the ordinary band with notes beside 16 funded
bundles, then commits every overdue and finish control, note withdrawals, an acknowledgement
and the cleanup of every bundle, and checks each control's page growth against its allowance
and the database's integrity. On SQLite 3.53.4 (Linux) the largest growth was 3 pages for an
overdue control and 4 for a finish with a short repository path, and 7 and 11 with a
4,000-byte path. These observations illustrate the bound; they do not establish it. A changed
SQLite version, schema or allocation path requires revisiting this derivation.
