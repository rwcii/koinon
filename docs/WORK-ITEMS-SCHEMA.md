# Work items v1 schema and transaction specification

Design companion to [the implementation design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md).
These statements describe the proposed schema-5 migration; they are not executed by
the current runtime. Existing schema-4 tables retain their definitions except for
the two explicitly added idempotency columns.

## DDL

Execute each statement separately inside the one migration transaction. Validate the
source schema first. Fresh schema-5 creation uses the resulting definitions directly.
Do not run these statements against a live runtime database with a separate SQLite client.

```sql
ALTER TABLE idem ADD COLUMN operation TEXT NOT NULL DEFAULT 'note';
ALTER TABLE idem ADD COLUMN result TEXT;

CREATE TABLE work_items (
    work_id TEXT PRIMARY KEY NOT NULL CHECK(length(work_id) = 32),
    revision INTEGER NOT NULL CHECK(revision > 0),
    lifecycle TEXT NOT NULL CHECK(lifecycle IN ('open','active','blocked','finished')),
    title TEXT NOT NULL,
    criteria TEXT NOT NULL,
    non_goals TEXT NOT NULL,
    proposed_assignee TEXT,
    created_at REAL NOT NULL,
    created_consumer TEXT NOT NULL,
    first_start_revision INTEGER,
    scope_revision INTEGER NOT NULL CHECK(scope_revision > 0),
    progress_epoch INTEGER NOT NULL DEFAULT 0 CHECK(progress_epoch >= 0),
    last_progress_at REAL,
    progress_deadline REAL,
    progress TEXT NOT NULL DEFAULT '',
    checkpoint TEXT NOT NULL DEFAULT '',
    next_artifact TEXT NOT NULL DEFAULT '',
    blocker TEXT NOT NULL DEFAULT '',
    last_writer TEXT,
    last_generation INTEGER,
    last_lease_expires REAL,
    lease_expired INTEGER NOT NULL DEFAULT 0 CHECK(lease_expired IN (0,1)),
    outcome TEXT CHECK(outcome IN ('completed','withdrawn')),
    reason TEXT NOT NULL DEFAULT '',
    references_json TEXT NOT NULL DEFAULT '[]',
    finished_at REAL,
    expires_at REAL,
    latest_seq INTEGER NOT NULL CHECK(latest_seq > 0),
    CHECK((lifecycle = 'finished' AND outcome IS NOT NULL
           AND finished_at IS NOT NULL AND expires_at IS NOT NULL)
       OR (lifecycle <> 'finished' AND outcome IS NULL
           AND finished_at IS NULL AND expires_at IS NULL))
);

CREATE TABLE work_scope_revisions (
    work_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    ts REAL NOT NULL,
    consumer TEXT NOT NULL,
    author TEXT,
    title TEXT NOT NULL,
    criteria TEXT NOT NULL,
    non_goals TEXT NOT NULL,
    PRIMARY KEY(work_id, revision)
);

CREATE TABLE claim_bundles (
    generation INTEGER PRIMARY KEY CHECK(generation > 0),
    work_id TEXT NOT NULL UNIQUE,
    consumer TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    issued_at REAL NOT NULL,
    renewed_at REAL NOT NULL,
    expires_at REAL NOT NULL,
    progress_epoch INTEGER NOT NULL CHECK(progress_epoch > 0),
    overdue_recorded INTEGER NOT NULL DEFAULT 0 CHECK(overdue_recorded IN (0,1)),
    active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
    overdue_credit INTEGER NOT NULL DEFAULT 1 CHECK(overdue_credit IN (0,1)),
    end_credit INTEGER NOT NULL DEFAULT 1 CHECK(end_credit IN (0,1))
);

CREATE TABLE claim_resources (
    generation INTEGER NOT NULL,
    ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 8),
    kind TEXT NOT NULL CHECK(kind IN ('writer','path','exact')),
    resource TEXT NOT NULL,
    PRIMARY KEY(generation, ordinal)
);

CREATE TABLE work_events (
    seq INTEGER PRIMARY KEY CHECK(seq > 0),
    work_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision > 0),
    kind TEXT NOT NULL CHECK(kind IN (
        'created','proposed','edited','started','updated','released','finished',
        'progress-overdue','lease-expired')),
    payload TEXT NOT NULL
);
CREATE INDEX work_events_item ON work_events(work_id, seq);
```

Add `work_id_counter` and `claim_generation` to meta with initial value zero. Allocate
IDs by incrementing the first counter and encoding it as 32 lowercase hexadecimal
characters. An ID is opaque to callers, not a secret. This replaces the initial random
UUID proposal and ensures reclaimed IDs are not reused within a store. Repository and
store identity still accompany the ID. Preserve both counters across restart; reject
increment past `2**63-1` without wrapping. A rollback does not consume an ID returned
as successful, because success is returned only after commit.

Declare all five new tables in the schema ownership and data-presence sets, and the
explicit index in the owned-index set. SQLite-generated primary/unique indexes remain
recognized as such. There are no triggers, foreign-key cascades, expression indexes,
or work FTS tables. Cross-table integrity is checked explicitly in transaction code
and migration tests; this does not require changing global foreign-key pragmas for
legacy tables. Do not add schema objects opportunistically to make a probe pass.
For initialized stores, schema-5 startup and migration must inspect the complete owned
catalog after identity validation and before write-capable configuration: compare table
and explicit-index definitions, allow only verified SQLite autoindexes/FTS shadows,
and refuse extra triggers, generated columns or indexes. Metadata identity alone is
insufficient to establish the storage proof's exact-schema precondition. Validate each
supported source version against its own expected DDL before applying the migration.

## Validation beyond SQL constraints

Use strict JSON types: booleans are not integers, times are finite numbers, and every
counter fits signed 64-bit range. A current item serialized with its public metadata
and bounded claim observation fits 16 KiB. Scope-history payloads fit 8 KiB. Each
event payload fits 16 KiB and a replay result fits 2 KiB. These encoded limits apply
in addition to the implementation design's individual UTF-8 bounds. Consumer/author
labels are at most 128 Unicode characters and 512 UTF-8 bytes; optional assignees
obey the consumer bound. `references_json` is an array of at most eight inert strings.

Validate immutable provenance at creation. Mutations append event provenance through
the corresponding `entries` row, with the observed kernel PID recorded separately.
Scope changes also append their asserted author/consumer/time to scope history. A
proposal has no accepted owner effect. Reject edits/proposals on finished items.
The scope revision is the work revision of the last scope edit; creation records
scope revision 1. Every later change to title, criteria or non-goals creates one row.
The criteria-changed flag tests retained criteria/non-goal differences after the first
start, including later reversion, rather than treating a title-only edit as scope change.

## Cross-table invariants

1. Every `work_events.seq` matches exactly one `entries.seq` with type `work-event`,
   scope `repo`, scope_target equal to work ID, empty body, and NULL note-history links.
   Work stream rows have NULL `expires`; only finished-item cleanup removes them.
2. Every current item has its scope revision and latest event. During cleanup a single
   transaction removes the entire expired item; no visible half-item exists.
3. One bundle exists at most per item. Count all bundle rows toward the limit of 16,
   including expired bundles awaiting reconciliation. Each has ordinal 0 of kind
   writer, whose resource is its work ID, plus up to eight distinct optional members.
4. Resource overlap is checked against active, unexpired bundles by scanning at most
   144 rows. An additional prefix index is unnecessary at this bound. Treat `.` as
   the explicit whole-repository path key; it is the only permitted dot spelling.
5. The latest accepted writer/generation/deadline remains historical in the item
   after release/expiry/finish. Current ownership is derived only from an active, live bundle.
   Renewal updates the bundle deadline and claim revision; it does not rewrite the
   historical work view, work revision or event head. Get overlays the live observation.
6. A successful start sets active, clears lease-expired and blocker, advances progress
   epoch, records checkpoint/deadline, and creates a fresh funded bundle. First-start
   revision is set once. Start is allowed for open or unclaimed active/blocked work.
7. Update advances the work revision and progress epoch, clears/sets blocker according
   to active/blocked lifecycle, updates the bundle's matching epoch, and rearms its
   overdue obligation only after funding admission. Optional renewal also advances
   the bundle revision. An expired bundle cannot update.
8. The overdue transition increments the work revision/head once and marks the bundle's
   overdue bit and spends its overdue credit. It does not change progress time/deadline
   or grant ownership. If lease
   expiry and progress overdue are both due, emit lease-expired only: that transition
   already reports unverified progress and ends that generation's remaining obligations.
9. Lease expiry increments work revision/head, sets lease-expired, preserves the last
   checkpoint, and marks the bundle inactive with both credit flags cleared. Cancel that generation's later
   overdue obligation. The work lifecycle stays active/blocked as recorded. Stale-owner
   operations refuse; they cannot renew an inactive generation.
10. Release/finish mark the bundle inactive and clear its credit flags in the same
    transaction as their event and current-state update. Cancel unused generation obligations. Release clears the
    current progress deadline; finish fixes its 30-day expiry and terminal outcome.

Control SQL updates only boolean flag columns on claim_bundles. Do not include unchanged
indexed generation/work_id columns in the SET list. Integer 0/1 flags have equal record
size under SQLite schema format 4, so the row overwrite needs no new pages and resources
remain physically unchanged. Verify schema format 4 in the owned database header before
advertising work support. The [storage derivation](WORK-ITEMS-STORAGE.md) makes this a
required precondition, not an assumed optimization.

Bounded maintenance removes one inactive bundle and its at-most-nine resource rows
using shared control headroom while preserving all remaining claim debt, with rollback
on capacity refusal. This avoids preventing retention cleanup merely because controls
have raised the store above the ordinary ceiling. Request-boundary start cleanup
still uses ordinary admission. Such rows count toward the retained-bundle cap until
removal. A start cannot overwrite an inactive
bundle merely to avoid paying deletion costs; it must first successfully reclaim it.
This can temporarily refuse starts, but cannot prevent a promised active claim from ending.

Before a new request begins its own mutation, bounded maintenance may independently
record already-due transitions. State that side effect in the API: refusal of a start
means no new assignment, not that an older lease-expiry event could not have committed.
Idempotency replay precedes maintenance of the request's target so retries return the
original result rather than failing newly stale preconditions. Unknown targets and
malformed requests do not cause maintenance.

## Encoding the existing stream and replay table

A work entry uses existing provenance fields, revision, and scope_target; note-history
link fields and expires are NULL. Exclude it from note `recall` and FTS maintenance.
Update the FTS coverage marker across work-only head increments without inserting a
document. Rebuild indexes only ordinary note rows; an index current through a work event
is not incomplete merely because that event has no note text.

Use a canonical JSON fingerprint of the full work request with transport PID omitted.
The work namespace key is `work:` plus SHA-256 of canonical `[repo, consumer, key]`.
Legacy note keys start with their repository identity and NUL separator and are unchanged.
Store `operation`, `result`, original sequence (NULL for renewal), timestamp and deadline.
On replay return the retained original result with `duplicate:true`; do not reserialize
today's mutable item. Failed requests create no successful replay record.

## Atomic expiry job

Select at most one item where lifecycle is finished and `expires_at <= now`, ordered
by expiry then work ID. Within one transaction, recheck that predicate, collect the
largest sequence for that work ID, delete matching entries and work_events, delete
scope revisions and the work item, and set `floor = max(floor, removed_max_seq)`.
No active bundle may exist for a finished item; corruption refuses rather than deleting a
claimed item. Reclaim an inactive bundle through its separate bounded maintenance job
before reclaiming the item. Leave existing snapshot payloads and replay rows untouched. The work ID
and claim counters never decrease. Do not change head or fabricate a new work event
for the cleanup of already-expired data.

One item can occupy at most the global 2,048-event budget and its own 64 scope rows;
2,048 is not a per-item quota. The job is
submitted through the ordinary worker and is not overlapped. Recheck the predicates
and join conditions with synthetic full-size items. The cleanup must not call the
FTS unindex path for empty work-event bodies. If a SQLite failure occurs, rollback
all deletion/floor changes and retain the explicit storage fault for recovery.

Readers whose cursor is below the resulting floor resnapshot. Queries and newly
frozen snapshots exclude expired items before physical cleanup, while existing
snapshots remain immutable. This is time-based reclamation, not semantic summarization.
