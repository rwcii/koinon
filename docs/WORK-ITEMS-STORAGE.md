# Work-items storage admission and progress reserves

Design derivation for the [schema](WORK-ITEMS-SCHEMA.md). This separates the hard
database/WAL ceiling, conservative progress allowances, and local observations.
Implementation must enforce the assumptions and verify them on supported CI runtimes.

## Physical ceiling and source assumptions

The existing [storage derivation](STORAGE-BOUND-DERIVATION.md) retains its 128 MiB
bound, 4,096-byte pages, spill-disabled transactions, exclusive owner, and WAL reset
before every transaction. `MAX_PAGES` remains 16,328. The note reserve remains 2,048
pages; work reservations are subtracted in addition to it for ordinary writes.

Allocator analysis is pinned to SQLite 3.46.1:
[btree.c](https://raw.githubusercontent.com/sqlite/sqlite/version-3.46.1/src/btree.c),
[update.c](https://raw.githubusercontent.com/sqlite/sqlite/version-3.46.1/src/update.c),
and the [file format](https://www.sqlite.org/fileformat2.html).
Non-root interior pages have at least two children after balancing. A balancing walk
can allocate up to four pages per level plus one root-deepening page. Index deletion
can involve two walks; deletion is therefore not allocation-free. Work controls avoid
deletion. Unchanged-size, unindexed 0/1 flag overwrites use the existing-cell path.

Preconditions: schema format 4; the published DDL without extra triggers/indexes;
no primary/indexed key in a flag-only UPDATE's SET list; row/encoded-size caps enforced;
and valid service-produced metadata. The nine meta keys are repo, protocol, schema,
head, floor, store_id, indexed_through, work_id_counter, and claim_generation. Repo
is a fixed 16-character identity hash, UUID is 32 characters, and counters are bounded
integers. Their maximum serialized contents fit in one page, including keys/headers;
counter updates neither split that table nor change its key index. A future metadata
extension must preserve this invariant or revise the bound. Refuse work activation for
an incompatible header/schema instead of silently assuming these conditions.

## Allocation per control transition

For a table with at most N rows, use height `1 + floor(log2(N))`; for an index use
`floor(log2(N + 1))`. These conservative bounds use minimum branching, not average
occupancy. One insertion/size-changing table update has allowance `4*h + 1` plus
overflow pages. At 4,096 usable bytes an overflow chain carries 4,092 bytes per page;
allow five overflow pages for a bounded 16 KiB work image plus its row metadata.

| Changed object | Height | Page allowance |
| --- | ---: | ---: |
| entries table, all note and work rows together capped at 5,000 | 13 | 53 |
| entries_live index | 12 | 49 |
| work_events, at most 2,048 rows | 12 | 49 + 5 |
| work_events_item index | 11 | 45 |
| work_items rewrite, at most 128 rows; unchanged ID index | 8 | 33 + 5 |
| idem table, at most 20,000 rows; bounded work result fits a leaf | 15 | 61 |
| idem key index | 14 | 57 |
| claim flag overwrites, untouched resources, bounded meta updates | — | 0 |

Overdue and lease-expiry events do not append replay rows: base allowance 239 pages.
A replayable release/finish includes the two idem terms: 357 pages. Add
`ceil(base/819) + 1` pointer-map allowance, giving 241 and 359 pages respectively.
Use **256 pages for an overdue credit** and **384 pages for an end credit**. The
rounding is extra headroom; the preceding sum, not an observation, establishes the
allowance. Expiry consumes the end credit, releases any unused overdue credit, and
does not also reserve a future terminal event for that generation.

At most 16 retained bundles have at most two credits each, so work debt is at most
`16 * (256 + 384) = 10,240` pages (40 MiB of database pages). This is a conservative
reservation, not measured data size. With every credit held, ordinary data has
`16,328 - 2,048 - 10,240 = 4,040` pages before commit slack. One or two writers hold
far less debt. Admission may refuse before any individual row-count limit is reached;
the limits are not a promise that every maximum workload fits simultaneously.

## One ledger applied to every write path

Let D be page debt from the two flags on all retained bundles. Derive it from durable
rows, never a process-only counter. A new generation begins with both flags set. An
overdue event clears the first; an end event clears both. An update may rearm an
overdue flag only through ordinary admission. Renewal alone cannot reset a recorded
progress obligation. Inactive bundles have no credits and remain in the row-count
limit until their separate maintenance deletion succeeds.

The following use debt **after** the transaction's proposed mutations:

| Write class | Inside-transaction page ceiling before commit |
| --- | --- |
| Ordinary writes, including any new/rearmed obligation | MAX_PAGES - note reserve - remaining D - commit slack |
| A funded work control | MAX_PAGES - remaining D - commit slack |
| Other controls, including note controls | MAX_PAGES - unchanged D - commit slack |

Admission and post-mutation enforcement must share these expressions. A work control
can spend only the credit it atomically clears; note controls cannot borrow outstanding
work credits. Every table mutation still uses Store.transaction. For control o, if
its growth is at most its consumed allowance, `pages + D` cannot increase because of
o; cancelling another credit only releases more headroom. Existing engine enforcement
still catches disk/full failures. Commit-time pointer-map slack remains a guard rather
than a replacement for the engine ceiling, as documented in the base derivation.

Maintain matching slot and logical-byte ledgers. Each unspent credit reserves one
work event and one entries row. End credits additionally reserve one idem slot.
Ordinary note/work writes preserve those slots plus existing note reservations;
controls preserve other outstanding work slots. Thus a full ordinary idem/event
table cannot stop a previously funded end operation. Slots cancelled on expiry are
released atomically. Charge already-written event/replay rows as actual usage; do
not charge them again as outstanding obligations.

Each credit reserves 48 KiB logical growth, covering a 16 KiB current-image increase,
16 KiB event payload, 2 KiB result and 14 KiB bounded row/provenance/key charges. Two
credits for 16 bundles reserve at most 1.5 MiB. Scope history is not appended by these
controls. New-table charges equal UTF-8/BLOB field bytes plus 512 per row; legacy note
charges stay unchanged, with work operation/result bytes and 128 metadata bytes added
for work replay. Use the same formula for projection and actual usage. Both the work
12 MiB sub-budget and total 32 MiB budget preserve remaining work debt; only the shared
budget also includes existing note/snapshot accounting.

## Cleanup, snapshots and recovery

Do not disguise deletion as a zero-page operation. Inactive-bundle cleanup runs one
bundle at a time. Background maintenance uses shared control headroom while preserving
every remaining work credit; start-boundary cleanup uses ordinary admission. Requiring
background deletion to fit the ordinary ceiling would stall retention cleanup after
controls allocate into the reserve. Capacity refusal still rolls back the deletion
and may refuse new starts until cleanup can fit; active claims retain their end credits. Finished-item cleanup is a separate bounded maintenance transaction
using the shared control reserve while preserving every remaining work credit.

An expired item stays hidden from queries even if physical cleanup is refused. Roll
back all partial deletions and floor changes, report capacity/storage health, and
retry on the existing bounded maintenance schedule or after explicit recovery. No
silent eviction of a live item, active lease, snapshot or replay promise makes room.
This does not claim deletion always succeeds at arbitrary saturation. Its worst-case
record count and WAL are bounded; latency is tested, not guaranteed under stalled I/O.

At most 128 current work views contribute 2 MiB to one frozen snapshot. Four retained
snapshots can contribute 8 MiB per consumer. Admission charges all copies and preserves
control debt, so some combinations refuse with snapshot_capacity/capacity. A new
reader cannot acknowledge a truncated substitute. Recovery is expiry of eligible
retained snapshots/work, not dropping active records. The bootstrap target is two
consumers each taking one snapshot of a small admitted working set; maximum occupancy
does not promise bootstrap to an unlimited number of consumers.

## Evidence and implementation acceptance

Design probes ran only on disposable synthetic stores using Python's SQLite 3.46.1.
The candidate DDL parsed, nine injected migration failure points rolled back all new
objects/columns and preserved note, replay, cursor, frozen-snapshot and identity rows.
A successful probe preserved head/store identity, passed integrity_check, and the
old runtime refused schema 5. These are DDL/migration probes, not a work-command test.

Preliminary allocator samples across payload sizes 0, 4,000, 4,057, 4,096, 6,080,
6,144, 8,192 and 16,384 bytes observed 2–9 added pages for a terminal-shaped transaction
with 19,000 replay rows. A 2,017-event cleanup took about 4 ms with no page-count growth
on that host. Those samples used the earlier delete-on-end candidate and deliberately
varied raw storage payload sizes; they are neither application-validation evidence nor
an upper bound. They motivated checking source instead of using observed growth as policy.

Implementation tests must verify the final flag-only shape, compare measured growth to
the derived allowance across split thresholds, fill the ordinary band with FTS-heavy
notes, then finish every admitted bundle while exercising note withdrawals and snapshot
acks. Cover full slot/replay budgets, failed cleanup/retry, interrupted sweep and all
crash boundaries. Run the suite on supported Linux/macOS SQLite builds. A changed engine
allocation path or schema requires revisiting this derivation before release.

A final physical saturation probe used the flag-only schema, 16 bundles with nine
resources each, 19,000 replay rows and 2,016 existing work events. FTS-heavy notes reached
ordinary refusal after 1,726 appends at 4,033 database pages with 10,240 pages reserved.
All 16 overdue and 16 replayable terminal-shaped transitions committed, followed by
64 note withdrawals and all 16 inactive-bundle cleanups. Maximum observed transition
growth was 6 pages for overdue and 10 for terminal; the database held 4,287 pages after
the controls, with zero remaining work debt. Integrity checking passed. This is one
SQLite 3.46.1 physical-allocation experiment, not a full logical-admission, work-command,
notification, snapshot, or cross-platform implementation test.

## Accounting cost and usable capacity

The shared transaction checks durable debt and all usage dimensions unconditionally.
Work payload sizes are aggregated inside SQLite rather than loading every payload
into Python. The aggregate still scans retained rows; its cost grows with storage.
No declared-charge or progress shortcut bypasses this boundary.

A local Linux observation used 16 funded bundles, 1,000 roughly 8 KiB work events
with stream rows, and 922 maximum-size notes: 1,922 entries, 17,167,290 shared
logical bytes and 9,136,600 work logical bytes. It reached 4,031 of 4,038 ordinary
pages. Across 100 samples, full usage averaged 5.46 ms (p95 5.65 ms), and a complete
activity refresh 5.54 ms (p95 5.72 ms). On the identical synthetic connection,
this revision's schema-4 accounting path averaged 2.30 ms (p95 2.41 ms), so work
accounting added about 3.24 ms. The previous revision's refresh on that connection
averaged 0.033 ms; the combined increase was about 5.50 ms.

A separate genuine schema-4 store, with no work tables and 1,922 notes (922 at the
maximum size and 1,000 short notes), measured the cost to existing installations.
On that same store, the previous revision averaged 0.036 ms (p95 0.039 ms) and this
revision 2.35 ms (p95 2.45 ms): an increase of about 2.31 ms from unconditional
boundary enforcement. The comparisons use test-only implementation substitution;
the schema-5 comparison also changes the accounting metadata in the synthetic
store. Neither is a supported startup path. These are local observations, not
latency guarantees. The saturation test emits operation timings
on CI without timing assertions, including snapshot issuance and acknowledgement.
In the separate saturation fixture with 1,880 maximum-size notes, a seed note,
one frozen snapshot and 16 funded bundles, a single snapshot acknowledgement took
9.77 ms and page issuance 4.85 ms. These operations have different transaction
counts from activity refresh and should be measured separately.

The 1,922-entry workload does not measure the shared entry or logical ceiling.
Other admitted shapes with fewer funded bundles can retain more rows and bytes,
so their scans and progress operations can cost more. None of these measurements
is a worst-case bound; the relation is not assumed to be exactly linear across
payload sizes, indexes, caches or SQLite builds.

With all 16 bundles funded, ordinary writes have 4,038 database pages, about
15.8 MiB, before the append allowance and other limits apply. The 128 MiB combined
database/WAL ceiling is not the ordinary-write capacity. Row, byte and page maxima
are independent ceilings, not simultaneous capacity: 2,048 full-size 16 KiB work
events alone would exceed the 12 MiB work budget.
