# Work-items implementation foundation

The first implementation slice stages schema and lease primitives for the
[merged design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md). These modules are exercised
with synthetic databases. They are not imported by the service or copied by the
installer. `memory.SCHEMA` remains 4; no work command or schema-5 capability is
advertised, and existing runtime databases are not migrated by this slice.

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
whether the operation consumes its control reservation. It is not an implemented
store-wide budget enforcer. The caller must also account for the complete work,
event, replay and scope-history transaction before commit.
The work-command caller must check that the target item exists, validate its
lifecycle/revision, and keep the progress epoch and event changes atomic with the
lease mutation. The reusable lease engine alone does not establish these work-item
invariants. A release retains its inactive bundle until separately admitted cleanup;
an immediate restart reports the maintenance recovery path.

The synthetic tests cover retained note/snapshot/replay preservation, schema-3/4
migration, failures after each migration write, malformed catalogs and identities,
old-runtime refusal, stale owners, conflicts, renewal without stream progress,
rollback on admission failures, restart and counter exhaustion. They do not prove
work-command delivery, full-store completion, or a deployed upgrade.

## Integration still required

Before enabling schema 5, connect the helpers to `Store` startup and fresh creation,
run catalog validation before write-capable configuration, then verify the schema
format after the owner's WAL reset and before enabling work. Update all schema
ownership/data/index sets. Preserve one atomic
migration and the current read-only rejection behavior for foreign stores.

Integrate the debt with every existing page, logical-byte, event and replay admission
path, using the same accounting formula at admission and commit. Implement work
records and lifecycle transitions, progress-credit rearming/overdue events, immutable
stream payloads, note/FTS separation, frozen current-work snapshots, idempotency,
and the record-format guard for both sync and acknowledgement. A lease primitive
alone must never be exposed as a command that omits its corresponding work event.

Then add bounded maintenance and finished-item expiry, service/CLI error mapping,
binding compatibility, opt-in installer configuration, and user capacity/upgrade
documentation. Run the design's full-store, bootstrap, shutdown and mixed-version
tests and Linux/macOS CI before claiming runtime implementation acceptance.
