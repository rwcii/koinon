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
- The published tables and indexes and nothing else. At start the daemon compares every schema
  object with those it creates for the database's schema version, before and after a
  migration, and refuses an extra index, trigger or view: an index on a flag column would make
  the flag overwrite allocate, and a trigger could refuse a funded control.
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

## Verdict credit

A report promises a verdict (#140 chunk 02). An accept or a reject changes the ten objects of a
finish with its replay row: 690 pages and 20 overflow pages. The work item row is rewritten in
full, and that rewrite clears its `verdict_credit` flag. A reject also writes a scope revision
row (at most 8 KiB, 69 + 5) and its primary-key index entry (69), 143 pages. The sum is
853 pages, with ceil(853 / 819) + 1 = 3 pointer-map pages, **856**. The credit is rounded up
to **1,024 pages for a verdict credit**. A verdict writes no notification row.

The 0/1 `verdict_credit` flag lives on the reported work item, with no index. A report sets it
in an ordinary, capacity-checked write that also spends the bundle's end and overdue credits
when it releases the claim; the report refuses when the store cannot keep the verdict's
pages, slots and bytes free after every other promise, and the claim then stays. Schema 17
rebuilds `work_items` with the flag and the report pointers before the large text columns, so
the debt scan of the flags reads no overflow page. A store keeps at most 16 pending verdict
credits and the daemon 32.

## Limits and the ledger

A store retains at most 16 claim bundles and the daemon at most 32, and at most 32 verdicts
are pending across the daemon, so the page debt is at most 32 × 1,344 + 32 × 1,024 = 75,776
pages (296 MiB); ordinary writes keep at least 52,855 pages. The debt is derived from the
durable credit flags of the retained bundles and the reported items and subtracted, after the
mutation, in the one storage boundary that every daemon write passes:

| Write class | Page ceiling inside the transaction |
| --- | --- |
| Ordinary writes (sessions, messages, launches, notes, snapshots, consumers, work creation, starts, updates) | max pages − 2,048 − debt − commit slack |
| Control writes (progress, withdrawals, expiry, work controls, verdicts) | max pages − debt − commit slack |

A work control clears the credit it spends in its own transaction, so it may use exactly that
allowance; every other write keeps all remaining debt free. The per-store ledger keeps, for each
unspent credit, one entry and one event slot and 48 KiB of logical bytes, and for each end credit
one replay slot, in the ceilings of every memory and work write of that store. The Go row charges
are the UTF-8 bytes of the text columns plus 512 per work row, and key, consumer, operation and
result plus 192 per replay row; one control writes at most a 16 KiB image increase, a 16 KiB
event, a 2 KiB result and about 2 KiB of keys and provenance, within the 48 KiB. Each unspent
verdict credit keeps one entry, event, replay and scope revision slot and 64 KiB of logical
bytes: the image, the event and the scope revision at most 16 KiB each, the 2 KiB result and
up to 14 KiB of keys, provenance and row charges. The report checks the item's own scope
revision limit. A mutation's
view must leave 1 KiB of the 16 KiB free, which covers what a later observation adds (overdue
and expiry reasons, longer counters and times), so a funded due event is never refused for size. A verdict event drops the report's
summary, so its outcome, pointers and short evidence fit where the report fitted;
`TestWorkVerdictOnAMaximumItem` checks both verdicts on an item at its admission bound.

## Evidence

Schema 16 adds bounded proposal JSON and 64-byte native actor digests to the existing item,
claim and replay tables, with no new table or index. Their UTF-8 bytes are included in usage
and item-image charges. The unchanged 16,857-byte item-row admission bound includes the
proposal; the 16-KiB event/image and 2-KiB replay-result bounds still apply. The native digest
does not change any key or credit flag and fits the replay row's existing overflow allowance.
Funded controls still change only claim flags; actor binding is written during the ordinary
start. The 48-KiB logical credit and 576/768-page allowances remain unchanged.
Migration materializes the added column on retained bundles, so their first funded flag
update does not grow an old record header.
`TestWorkRoleFundedControlsAtSaturation` fills ordinary storage beside 16 native-bound claims
with maximum role/scope/checkpoint fields, then commits every overdue and finish control,
checking debt, page growth and integrity. Legacy short/long-repository saturation proofs
continue in `TestWorkSaturationAndControlAllowances`.

Schema 17 rebuilds `work_items` and `work_events` under their own names, with the same
primary keys and `work_events_item` index, to allow the `reported` lifecycle and the
`reported`, `accepted` and `rejected` event kinds. It adds no table or index, and the
catalog check still refuses an extra object, an index on `verdict_credit` included.
`TestWorkReportFundingAndVerdictsAtSaturation` fills ordinary storage beside 16 reported
items, then commits each accept or reject with maximum scope fields and checks its page
growth against the 1,024-page credit, the spent credits and the database's integrity. On
SQLite 3.53.4 (Linux) the largest growth was 5 pages for an accept and 7 for a reject.

`TestWorkSaturationAndControlAllowances` fills the ordinary band with notes beside 16 funded
bundles, then commits every overdue and finish control, note withdrawals, an acknowledgement
and the cleanup of every bundle, and checks each control's page growth against its allowance
and the database's integrity. On SQLite 3.53.4 (Linux) the largest growth was 3 pages for an
overdue control and 4 for a finish with a short repository path, and 7 and 11 with a
4,000-byte path. These observations illustrate the bound; they do not establish it. A changed
SQLite version, schema or allocation path requires revisiting this derivation.
