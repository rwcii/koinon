# Work items v1 implementation design

Status: implementation-design candidate for review. The [behavioral contract](WORK-ITEMS-V1.md)
is approved; this document does not claim an implementation or completed validation.
The schema, reserve derivation and configuration specification are linked below.
Runtime verification remains implementation acceptance work. Finished-work retention
is settled at 30 days in the approved contract.
DQ-08 session removal and DQ-05 history pruning are separate work.

## Baseline and integration decisions

The baseline is memory schema 4 and protocol 1 on `develop` at `077d904`.
Source references name functions so the design remains readable after line numbers move.

| Existing boundary | Required treatment |
| --- | --- |
| `Store.transaction` compares `meta.head` and calls `on_change` only after commit | Every observable work transition advances that same head atomically. |
| `MemoryCommands.sync` selects `entries.seq` and tests its cursor against head | Every new head value has a deliverable entry. A head-only increment would strand delta readers. |
| `freeze` copies live entries, with per-type ordering/tails | Snapshot current work records once per work ID, not the complete work event history. |
| `memory_bindings.observe` and `refresh` compare store UUID and head | Preserve both identities; no second independent work-notification counter. |
| `Store.usage`, `charge`, `mutation`, and `progress` share admission | Charge work data, replay responses, and outstanding control reservations in all write paths. |
| `Store.inspect` admits schema 3/4; classification checks owned objects | Add explicit 4-to-5 migration and exact new table/index definitions. |
| `memory_bindings.observe` requires exactly `memory.SCHEMA` | Upgrade bound bridges with the memory runtime; an old bridge cannot silently keep serving schema-5 pointers. |
| `maybe_expire` runs at request boundaries | Add one bounded maintenance loop; the existing interval alone cannot wake an idle participant. |

Retain one repository service, SQLite connection, database worker, and control endpoint.
Add `koinon/work_items.py` for record validation, commands, and persistence, and `koinon/claims.py` for
the reusable advisory lease engine. Neither opens a database or owns a daemon. `memory.py`
owns transactions, schema migration, admission, stream serialization, and lifecycle;
pass the existing store into the new components. Keep pure claim conflict functions
independent of work commands so DQ-05 can reuse them.

## Stored data and identity

The [schema specification](WORK-ITEMS-SCHEMA.md) supplies exact DDL and transactional
invariants for these schema-5 additions:

| Object | Purpose and bounded contents |
| --- | --- |
| `work_items` | Current structured record keyed by a server-allocated 32-character hexadecimal counter value; lifecycle, revision, scope fields, progress, proposed assignee, last writer, latest event sequence, first-start marker. |
| `work_scope_revisions` | Immutable title/criteria/non-goal versions with work revision, asserted author, consumer and server timestamp. Never delete one to make an edit fit. |
| `claim_bundles` | One retained generation per work ID, stable consumer, expiry, explicit renewal revision, progress epoch, and active/overdue_credit/end_credit flags. |
| `claim_resources` | Bundle members: one work writer key and up to eight optional path/exact resource keys. Unique work writer key and indexed resource identity; no filesystem access. |
| `work_events` | Immutable sequence-keyed, structured work-transition payloads; one-to-one with internal work entries in the existing stream. |
| `meta.claim_generation` | Monotonic signed 64-bit generation allocator. Never reset or reuse a committed value; overflow refuses acquisition. |
| `meta.work_id_counter` | Monotonic work ID allocator, preserved when old items expire. |
| Extended `idem` | Add operation and bounded serialized result columns for work replay, preserving legacy note rows and their sequence result. |

Repository identity remains the existing common-directory hash. All new tables belong to
that database, so work IDs have meaning only alongside the repository and store identity.
Claims use stable consumer keys, not PIDs or service-instance generations. A service restart
changes its endpoint generation but does not allocate fresh claim generations.

Use explicit columns for fields queried by list/conflict/deadline operations. JSON is for
bounded payloads and inert artifact/usage references, not an unvalidated replacement for
the schema. Reject unknown request fields. Enforce individual UTF-8 bounds and an aggregate
encoded-record limit, since escaping can make wire size exceed input byte size.

## Commands, revision checks, and replay

The provider-neutral CLI adds `memory.py work create|get|list|propose|edit|start|update|release|finish`
and `memory.py claim renew`. Wire operations are `work-create`, `work-get`, etc.
`work get --revision N` exposes a retained scope revision; current `work get` reports
available scope revision numbers and `criteria_changed_after_start`. This is a bounded
scope-history query, not a new arbitrary memory-retrieval interface.

Stateful commands require `--consumer`. Create and start require `--key` and `--deadline`;
other mutations accept the pair for safe replay. `--if-revision` is mandatory except on
create. Owner mutations require `--claim-generation`. Renewal operates on the claim's
own revision with `--if-claim-revision`, leaving the work record revision unchanged.
CLI help distinguishes service generation from claim generation.

`work start` takes checkpoint, next artifact, progress deadline, lease duration, and a
resource bundle. It records both the exclusive writer and the active state atomically.
Lease duration defaults to 900 seconds and accepts 60 through 3,600 seconds. A progress
deadline is required and must be after server time and at most 86,400 seconds ahead.
Neither duration permits automatic work execution. `work update` supplies the next
progress deadline and may also explicitly request `--renew-for SECONDS`. That optional
renewal is atomic with the progress update; otherwise the existing lease deadline stays.
Renewal alone extends only the lease from the server's current time. Guidance requests
renewal at checkpoints and before an expected long tool call; it cannot promise a tool
boundary before expiry. An expired owner must reconcile and reacquire, never renew late.
This deliberately limits the normal-clock wait after a crash to the remaining lease period,
at most one hour. A replacement session with a different consumer key cannot claim its
predecessor's still-live bundle. Long tool calls without a renewal boundary may expire;
their owner must reconcile actual work before attempting reacquisition. Wall-clock steps
can alter the effective wait, so the one-hour limit is not a monotonic elapsed-time guarantee.

The worker serializes requests, but the database transaction remains the correctness
boundary. Processing order for a mutation is:

1. Validate the exact repository/service target, capability, field types and bounds.
2. Check the retry deadline and a retained idempotency record before mutable preconditions.
   A valid replay returns its original response even if the item has since changed.
3. Check work and claim revisions, lifecycle, consumer, generation, lease validity,
   overlap conflicts, and storage/slot reservations inside the write transaction.
4. Apply the entire record/claim mutation, scope history if applicable, stream event,
   durable head, replay result, and reservation accounting together.
5. Commit, then publish the existing content-free wake. An ambiguous caller timeout is
   not rollback; the accepted worker operation completes and remains queryable.

Work replay keys have an unambiguous namespace distinct from legacy note keys, scoped to
repository, stable consumer, and supplied key. Fingerprints bind operation, target work ID,
expected revisions, generation, all request content, and deadline. Changing operation under
one work key is a conflict. Preserve the existing 24-hour maximum deadline and expiry rule.
Store a bounded original result, not merely a pointer to mutable current state. Migration
does not rewrite old note fingerprints or alter their replay results.

Scope edits are retained even when later reverted. The changed-after-start flag is derived
from retained history, not a writer-supplied boolean. Finish requires `completed` with evidence
references or `withdrawn` with a reason. Both are terminal and atomically release the bundle.
Release returns the item to open with its final checkpoint. No public force-transfer or
force-release operation is introduced. An expired owner must reacquire through normal start.

The lease engine matches path components, not raw string prefixes. Path keys use `/`, forbid
absolute paths, empty segments, `.` and `..`, and have an explicit repository-root key for a
whole-repository claim. Exact resource keys occupy a separate namespace. Reject backslashes
and control characters rather than interpreting platform-dependent alternate separators.
Case and symlink aliases are not resolved. Overlapping bundles conflict even when the same
consumer owns them for different work IDs. Expired bundles are inactive by server time,
regardless of whether maintenance has recorded their expiry yet.

Renewal changes only the bundle's expiry and claim revision, with a replay result if requested.
It must not advance the observable stream head or trigger model notices. A renewal that
cannot reserve its next possible expiry transition refuses rather than promising an
unrecordable deadline. Due/expiry transition identities include generation and deadline
revision so superseded deadlines cannot generate current-state events.

## One durable stream, structured payloads

Add an internal stream discriminator `work-event`. It is not a user-selectable `note` type.
Each observable transition inserts an `entries` row carrying sequence, timestamp,
repository scope, work ID, provenance and an empty body, plus its structured `work_events`
payload. The work database, not note bodies, is authoritative. No work text is indexed by
the existing note FTS table. `note --supersedes` and `--revokes` must reject work-event targets;
ordinary note operations cannot rewrite work state or work history.

`sync` serializes a tagged work record with event kind, work ID, revision and the immutable
event payload. A current-item view is separate from historical event evidence: querying
current state must never silently replace a past payload. Extend stream row conversion and
queries to join the payload in bounded batches; no per-record socket or filesystem access.

All observable work mutations advance the same `meta.head`. Preserve non-finished work and
its events. Completed and withdrawn work expires after the approved 30-day retention window,
including its events and scope history. Until eligible reclamation frees space, finite limits
can still refuse high-volume or long-running work; they do not promise unlimited throughput.
Every head advance initially has a deliverable row. Removing expired work or note events
advances the history floor and requires stale readers to resnapshot.

Snapshot creation retains the existing note-selection rules and appends one immutable current
work view per retained work ID, ordered by work ID. It excludes work-event history from the
ordinary note selector. Include unexpired finished work so a fresh reader does not lose completion or
withdrawal evidence. Capture the whole selection and fixed head through the same worker;
copy serialized payloads into `snapshot_items` and charge their bytes. A snapshot that cannot
fit refuses explicitly; it must not truncate work or silently omit directives. Snapshot
pages and acknowledgements keep their existing lifetimes and replay rules.

Lease validity and progress freshness are observations at response time. Include
`observed_at` and the deadlines used. A frozen snapshot labels these as observations at
snapshot creation, not as a current live lease assertion on later pages. Work get/list
compute fresh effective state without changing the cursor or renewing a claim.

### Older readers and bindings

Keep transport protocol 1; advertise `work_items_v1` and `memory_record_format_2` on hello/status.
Schema 5 is a storage boundary, while record format 2 is an explicit reader capability.
Every schema-5 `sync` and `ack` request must carry `record_format: 2`; the new CLI supplies
it automatically. Refuse all missing/other formats with `client_upgrade_required` before
maintenance, issuance or cursor mutation, even when the current store contains only notes.
The server guard is required on both bound and unbound routes; an older unbound client
does not necessarily perform the service protocol handshake before issuing its request.

New readers accept the existing note payload shape and the new tagged work shape. They
can resume legacy frozen snapshots without rewriting immutable payloads or resetting
cursors. Work events created after the frozen head arrive as subsequent deltas. No format
metadata per cursor range or snapshot is needed. Test both old-client refusals and resumed
legacy snapshots; merely guarding sync would leave old ack able to skip unseen work.

Bridge pointers continue to depend on `(store_id, head)`, not work payloads. Explicit
bindings stay explicit and the notification remains content-free. Binding schema checks
require upgraded bridge/session runtimes alongside schema-5 memory. No new audience filter,
second acknowledgement cursor, or automatic binding is part of this design.

## Maintenance, queries, and failure behavior

One service-owned loop waits 30 seconds using a monotonic wait and submits at most one
ordinary worker job. Each job processes at most 32 due transitions in one bounded batch,
then yields. With a full worker queue, skip submission and retry on the next interval;
never bypass admission or use the reserved status/stop queue. Report last successful sweep,
observed pending due work, and maintenance faults in status. This is a bounded retry
schedule, not a wall-clock latency guarantee when storage or the worker is unavailable.

Run an initial due check at startup and at mutating request boundaries using the same
transition function. Existing note expiry maintenance may be invoked separately, but do
not inherit its whole-store loop as one supposedly bounded work sweep. Record only overdue
or expired transitions whose generation/deadline marker has not already been recorded.
Do not change ownership to another consumer. Publish only after a durable commit.

At shutdown stop scheduling, cancel the wait, drain any accepted maintenance operation,
then close the existing worker. Cancellation does not abandon a transaction already accepted.
Status and work reads skip write-maintenance preconditions, remaining available in a blocked
store whenever the database is readable. Expiry and overdue flags are computed on query
even while publication is delayed. Clock steps may shorten or lengthen leases, as stated
in the approved contract; no new cross-reboot clock authority is assumed.

`work list` reads summaries under one consistent read transaction. Support lifecycle,
current owner, proposed assignee, stale and blocked filters in combination. Defaults:
100 rows and at most 48 KiB encoded response, deterministic work-ID order, explicit
`truncated`. Never use truncation as proof of absence. `work get` retrieves a known ID;
v1 has no paginated list sessions or FTS/topic queries for work records.

New errors need explicit CLI classifications and source-coverage tests. Request refusals
include `work_not_found`, `revision_conflict`, `stale_claim`, `claim_conflict`,
`invalid_transition` and `record_too_large`. Capacity codes distinguish work history,
claims and replay capacity. `client_upgrade_required` is an operator-action refusal.
Keep storage/programming failures distinct and keep ambiguous `no_reply` outcomes visible.
Conflict responses disclose bounded holder/resource metadata, not instructions to evict it.

## Budgets and progress reserves

Retain the 32 MiB logical and 128 MiB total physical ceilings. They are independent
admission limits, not a guarantee every individual count ceiling fits simultaneously.
These v1 design limits are independent ceilings, not measured simultaneous capacity:

The budget supports a bounded repository workload, not indefinite active-event history.
For example, 16 writers each reporting once an hour generate 384 events per day and
reach 2,048 events in about 5.3 days, sooner after other mutations. Keeping every such
event until the item finishes and another 30 days pass cannot fit this store indefinitely.
Renewal alone creates no work event and guidance does not require a progress event for
each renewal; meaningful checkpoints still consume space. No automatic history trimming
is introduced to conceal this limitation.

| Quantity | Proposed limit |
| --- | --- |
| Retained work items, including finished | 128 |
| Retained writer bundles, including inactive or expired pending reconciliation | 16; storage admission can bind sooner |
| Optional resource claims per bundle | 8, plus one writer key |
| Aggregate current work payload per item | 16 KiB encoded |
| Title / criteria / non-goals | 256 / 4,096 / 2,048 UTF-8 bytes |
| Progress / checkpoint / next artifact | 2,048 / 1,024 / 1,024 UTF-8 bytes |
| Blocker / withdrawal reason | 1,024 UTF-8 bytes each |
| Artifact/usage references | 8 references, each at most 512 UTF-8 bytes |
| Resource key / work replay key | 512 / 128 UTF-8 bytes |
| Scope revisions | 64 per item, 1,024 total |
| Work events | 2,048 total, with reserved control slots excluded from ordinary admission |
| Work replay result | 2 KiB encoded, containing identifiers/revisions rather than the full item |
| All replay rows | Existing 20,000 shared limit, with explicit work-control reservations |
| Work-specific actual logical usage | 12 MiB within the shared 32 MiB |
| Snapshot copies | Up to 2 MiB of work views per snapshot, charged under existing snapshot limits |

The initial planning workload is up to 60 genuine work blocks in a retained window,
averaging at most 20 observable events per block and 4 KiB per event image. That is
1,200 events and approximately 4.7 MiB of event payload, leaving room within the work
sub-budget for current records, scope history and controls. It is a sizing example,
not a throughput guarantee: accounting and physical admission still decide each write.

At capacity, refuse new ordinary work mutations and report the exhausted dimension,
retained usage, outstanding reservations and earliest eligible finished-item expiry
(or explicitly none). Funded end operations remain available; a full event budget
does not prevent release/finish of an accepted active claim. Finish only actually
completed or withdrawn work, then allow eligible 30-day cleanup to free capacity.
Do not mark incomplete work finished merely to free history, rotate to an untracked
store, delete active records, or shorten retention as an implicit recovery step.
Higher sustained workloads require a separately reviewed capacity/retention design;
general event-history pruning remains outside v1. This limitation is deliberate and
must be in user-facing capacity documentation before the feature ships.

### Approved finished-work retention

The finish transaction sets `finished_at` from server time and `expires_at` to
`finished_at + 2,592,000` seconds for both completed and withdrawn outcomes. Retries,
reads and service restarts never extend it. Open, active and blocked work has no expiry.
The same wall-clock limitations as leases apply; this is a server-time retention window.

At expiry, get/list and new snapshots exclude that finished item even if cleanup is delayed.
Get returns `work_not_found` for absent or expired IDs, without claiming an ID never existed.
Existing immutable snapshots remain usable until their own promised expiry. Preserve
idempotency results until their original deadlines, independently of item reclamation.

Maintenance reclaims one expired finished item per job in an atomic transaction, including
its scope revisions, structured events and corresponding stream rows. The proposed global
event cap bounds this to at most 2,048 events and the per-item scope cap to 64 scope revisions;
the worker never overlaps cleanup jobs. Use the shared control reserve while preserving
claim debt. Capacity refusal rolls back the whole job and leaves the expired item hidden
but intact for a bounded retry; report the maintenance fault. Test the maximum row count
and shutdown behavior, without claiming a disk-I/O latency bound.

Set the shared history floor to at least the greatest removed stream sequence in the same
transaction, preserving the existing snapshot/resnapshot semantics. Keep head monotonic;
do not reuse removed IDs or sequence numbers. Capacity remains a valid refusal until safe
cleanup completes. This is expiry of finished work, not DQ-05 general history pruning or
semantic consolidation. No current runtime data is changed by this documentation update.

### Reservations and snapshot admission

Track outstanding obligations per accepted claim, not merely a static spare row count.
One generation may require one overdue event and one end event (expiry or release/finish,
mutually exclusive). Ordinary acquisition/progress/renewal must reserve whichever obligations they create
before committing. A fresh progress deadline replenishes its overdue allowance only if
admission can fund it. Deleting a claim must not accidentally discard an unrecorded prior
generation's due event; reconcile it or record it before replacement in the same transaction.

A control credit reserves 48 KiB of logical growth: up to 16 KiB current-record growth,
16 KiB event payload, 2 KiB replay result, and 14 KiB for bounded provenance, keys, row
charges and metadata. Two credits per bundle total at most 1.5 MiB for 16 bundles. No control
appends scope history. Charge every new-table row as its actual UTF-8/BLOB payload bytes
plus 512 bytes; extend legacy idempotency accounting with actual work operation/result
bytes and 128 bytes of work metadata, preserving old note charges. Charge includes all
copies, not only user text. The 48 KiB allowance must be checked against this formula
for every permitted control payload. Keep the existing note reserve distinct.

Work snapshots add at most `128 * 16 KiB = 2 MiB` of payload per snapshot, plus snapshot
metadata and existing note payloads. Four retained snapshots for one consumer can therefore
charge up to 8 MiB of work views; multiple consumers can exhaust the shared budget much
sooner than their individual count limits. Charge each copy at actual encoded size. Ordinary
snapshot admission must preserve the terminal-operation reservations. A refused snapshot
blocks that reader's bootstrap and must report capacity explicitly; a current-work list is
not a substitute for a complete snapshot or permission to acknowledge it.

The sizing gate must exercise new-reader bootstrap alongside existing note load and retained
snapshots, state the admitted concurrent-reader workload, and specify recovery when retained
snapshots expire or finished work is reclaimed. V1 does not promise bootstrap at arbitrary
saturation; it must not advertise per-consumer maxima as simultaneously available capacity.

Every write path, including note controls, snapshots and consumer maintenance, must preserve
unspent work obligations. Release/finish/expiry consume only their own reservation; they do
not acquire unbounded control privileges. Preserve both slots and logical bytes. A full replay
table must not prevent an already-promised terminal operation. Capacity refusal must leave
the item, event head, claims and replay records unchanged.

Physical storage still follows the existing [WAL bound](STORAGE-BOUND-DERIVATION.md):
`N = 16,328`, page size 4,096, frame size 4,120, padding at most 16 frames, and
`N*4096 + 32 + (N+16)*4120 <= 128 MiB`. New tables do not invalidate that engine ceiling,
provided every mutation still uses the existing spill-disabled, log-reset transaction path.
They do change the amount of reserve required for progress.

The [storage derivation](WORK-ITEMS-STORAGE.md) fixes the remaining page allowance:
256 pages per overdue credit and 384 per end credit, plus a matching slot/byte ledger.
Inactive flags release ownership without allocating delete/index work on the critical
control path. Physical cleanup is separately admitted and may refuse/retry while data
stays intact. Every write class preserves the remaining debt. Source assumptions and
required implementation/CI checks are stated separately from the local design probes.

## Migration and rollout

Schema 5 creation and upgrade execute all DDL statements individually inside one transaction,
never `executescript`. Validate repository/store identity and source schema before any write.
For schema 4, preserve store UUID, durable head/floor, entries, note idempotency results,
snapshots, issued ranges and acknowledged cursors. Add the new tables and replay columns,
then bump schema last in that transaction. A failure rolls back all changes.
Schema 3 upgrades through the existing store-UUID step and the new DDL within one atomic
upgrade transaction; reject unknown intermediate schemas and missing schema-4 identities.

Update exact schema ownership classification, data-table enumeration and index allowlists.
An identity-free file with populated work tables is user data, not an unfinished empty start.
An existing foreign or malformed table must never be adopted because its name matches.
Opening a schema-5 file with an old runtime returns the existing too-new refusal.

Before deployment: stop the repository memory service and affected bound session supervisors,
take consistent private backups, install matching runtime files, restart memory and bindings,
then verify schema, unchanged store identity/cursors, explicit binding health and one authorized
delivery check. Do not stop unrelated sessions or reset inboxes. Memory is still optional and
the installer still does not start a repository memory service automatically. Rollback requires
restoring a compatible backup under an explicit plan; changing the schema number is not rollback.

## Opt-in guidance and first adoption

The [configuration specification](WORK-ITEMS-CONFIGURATION.md) fixes the installer flags,
explicit file target for every participant, repository/participant selection, independent
section parser, lock order, pending/enabled recovery, policy query and removal semantics.
It does not guess Claude's file location or reuse the Codex renderer. Installation remains
disabled by default, unrelated guidance is preserved, and direct session permissions prevail.
No provider hooks or automatic memory-service startup are added.

Bootstrap follows the approved contract: synthetic two-consumer exercise first, then explicitly
configured live participants. The writer owns one work item; read-only review does not claim
the writer's resource bundle. Any separate reviewer writing task needs its own non-conflicting
work ID or an explicit handoff. Record work IDs with artifacts, revisions and checkpoints.

## Implementation sequence and evidence

1. Review the schema, reserve derivation and explicit configuration specifications together.
2. After implementation approval, implement schema/migration and one lease engine with synthetic transactional tests.
3. Add work commands, structured stream records, snapshots and replay results together.
4. Add bounded maintenance, query diagnostics and shutdown behavior; verify idle notification.
5. Add installer opt-in, preservation tests and user documentation, then independent review.

Each implementation slice must retain runnable compatibility and complete failure behavior;
do not release an event writer before readers can consume its records. Required test groups:

- Competing starts, bundle rollback, prefix boundaries, same-owner/different-item conflicts,
  stale generations, renewal without progress, and full lifecycle assertions.
- Lost replies, original-result replay after later revisions, expired keys, key conflicts,
  and crash injection at every multi-table transaction boundary.
- Stream head continuity, note/work coexistence, mixed client sync/ack, frozen snapshots under
  concurrent work changes, capacity refusal, FTS exclusion and note-target rejection.
- Idle due events, generation replacement, saturated worker queues, shutdown with an accepted
  sweep, storage faults, clock steps, and query freshness before sweep completion.
- Finished-work expiry at 30 days, retained active items, immutable snapshot replay across
  expiry, retry results within their deadlines, floor advancement and resnapshot after cleanup.
- Near-full legacy upgrade, malformed/foreign schema, unchanged existing cursors/snapshots,
  old-runtime refusal, bridge schema mismatch and recovery after matching upgrades.
- Full-store completion/release/expiry for every admitted bundle alongside note withdrawal,
  snapshot acknowledgement and consumer retirement; test actual logical/page accounting.
- Opt-in repeated install/removal, repository and participant isolation, unknown/duplicate
  markers, CRLF preservation, explicit Claude target handling, and disabled-default behavior.

Run the complete suite and exact-commit CI on Linux/macOS, Python 3.11–3.13 before claiming
implementation acceptance. The documentation draft supplies none of that runtime evidence.
