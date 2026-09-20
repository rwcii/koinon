# Work-items implementation foundation

The implementation stages schema and lease primitives for the
[merged design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md), with reservation enforcement
at the shared memory transaction boundary. The installer copies the accounting
dependencies, but `memory.SCHEMA` remains 4: no work command or schema-5 capability
is enabled by public startup, which does not migrate existing stores to schema 5.
Tests upgrade synthetic Stores through the staged helper to exercise accounting.

## Implemented primitives

- `work_schema.py` contains the reviewed DDL, read-only catalog/identity validation,
  atomic migration steps from schema 3 or 4, and durable work/claim counter allocation.
  The expected catalog includes SQLite autoindexes and verified FTS shadow definitions.
  A transient in-memory reference database obtains the definitions from the current
  SQLite build. Extra or changed tables, indexes, and triggers are refused.
  Keyword case and SQL whitespace may differ; quoted literals remain exact.
  Database schema format 4 is checked after the owner's WAL reset and before migration;
  a residual WAL reports recovery required instead of trusting a stale file header.
  Metadata keys and values must satisfy the storage proof's bounds. A store requiring
  unavailable FTS5 reports an unsupported SQLite build.
- `claims.py` validates logical path and exact-resource keys, compares component
  boundaries without resolving paths, and implements bundle acquisition, owner checks,
  renewal, release, expiry, and separate inactive-bundle reclamation. Same-consumer
  conflicting work is refused. Expired generations cannot renew or release.
- Reservation debt is derived from persisted credit flags. It exposes page, byte,
  event-slot and replay-slot obligations, including unreconciled expired bundles.
  End transitions update only boolean flags; resource-row deletion remains a
  separately admitted ordinary mutation.

The transaction belongs to the caller. Migration and claim mutations refuse an
autocommit call. Any exception must propagate to the caller's rollback boundary.
Lease admission is a required callback receiving the before/after durable debt and
whether the operation consumes its control reservation. The shared Store transaction
now enforces the complete work, event, replay and scope-history usage before commit.
Admission must still project and classify the intended mutation correctly.
The work-command caller must check that the target item exists, validate its
lifecycle/revision, and keep the progress epoch and event changes atomic with the
lease mutation. The reusable lease engine alone does not establish these work-item
invariants. A release retains its inactive bundle until separately admitted cleanup;
an immediate restart reports the maintenance recovery path.

The synthetic tests cover retained note/snapshot/replay preservation, schema-3/4
migration, failures after each migration write, malformed catalogs and identities,
old-runtime refusal, stale owners, conflicts, renewal without stream progress,
rollback on admission failures, restart and counter exhaustion. They do not prove
work-command delivery or a deployed upgrade.

## Transaction reservation enforcement

Every Store transaction checks pages, shared logical bytes, entry and replay slots,
work-event slots, and the 12 MiB work sub-budget before commit. It reads remaining
credit flags after the mutation, so a funded control does not spend its allowance
twice. Ordinary writes also preserve the existing note reserve. Progress and note
controls can use that reserve, but a breach rolls back instead of spending an
outstanding work credit. FTS rebuilding and expiry use the same boundary.

The schema is read from durable metadata, with an explicit initialisation state
before metadata exists. Schema 3/4 has zero work debt by definition; a missing table
or failed query in schema 5 never means zero debt. Existing schema-4 ceilings remain
unchanged. `usage()` and `charge()` use one work-row/replay charge formula.

The two incremental-vacuum operations retain their own transactions and WAL-reset
guards. They verify that page count does not grow; unexpected growth blocks further
writes. Tests exercise rollback across progress, snapshots, acknowledgements, direct
transactions, expiry, FTS rebuild, and control events after ordinary capacity is full.
The saturation exercise uses representative event/replay writes; actual work-command
completion, current-record updates and lifecycle behavior remain integration tests
for the next slice.

## Integration still required

Before enabling schema 5, connect the helpers to `Store` startup and fresh creation,
run catalog validation before write-capable configuration, then verify the schema
format after the owner's WAL reset and before enabling work. Update all schema
ownership/data/index sets. Preserve one atomic
migration and the current read-only rejection behavior for foreign stores.

The [staged command slice](WORK-ITEMS-COMMANDS.md) implements work records and
lifecycle transitions, target-only due reconciliation, progress-credit rearming,
immutable stream payloads, note/FTS separation, frozen current-work snapshots,
idempotency, and the record-format guard for both sync and acknowledgement.
Commands pair claim changes with their corresponding work events; standalone
renewal preserves the work revision and stream head. Synthetic tests exercise
all 16 funded end commands at ordinary saturation and legacy snapshot continuation.

The [maintenance slice](WORK-ITEMS-MAINTENANCE.md) adds bounded due publication,
finished-item reclamation, diagnostics, idle hints and shutdown drainage.

The [policy and guidance slice](WORK-ITEMS-POLICY.md) adds explicit installer opt-in
and verified policy queries; [operator capacity planning](INSTALL.md#work-item-capacity-planning)
documents finite workload and recovery limits. Binding compatibility, activation and
upgrade procedures remain. Run the design's full-store, bootstrap, shutdown and mixed-version
tests and Linux/macOS CI before claiming runtime implementation acceptance.
