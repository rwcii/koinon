# Resumable runtime upgrade design

Status: peer-reviewed design for DQ-12 and issue #43; implementation in progress. This describes the next
operation after the component installer gate; it does not claim an available upgrade
command or authorize changing a live installation. The current native installer refuses
different runtime bytes during ordinary repeat installation.

## Scope and command

Proposed entrypoint:

```sh
python3 scripts/upgrade.py --prefix /selected/runtime --source /selected/release
python3 scripts/upgrade.py --prefix /selected/runtime --resume
python3 scripts/upgrade.py --prefix /selected/runtime --status
```

The operation is scoped to one installed prefix and every registered component loading
its files. A runtime is shared by its sessions and selected repositories; upgrading
only one binding while leaving another process importing that prefix is insufficient.
Explicit repository and participant identities remain recorded and must match on every
resume. Another installation sharing a user manager remains outside the operation.
There is no discovery-based adoption of unmanaged services or unrecorded state roots.
An incomplete inventory refuses before shutdown or replacement and names the missing
ownership evidence. Legacy migration needs an explicit supported adapter; absence of a
native selection is not permission to invent one.

Automated restoration is outside #43. The existing explicit
[rollback runbook](WORK-ITEMS-UPGRADE.md) remains the recovery path: first prove all
selected writers stopped, retain the failed/new state, validate a complete compatible
backup and its original targets, and restore runtime and state together. The generated
report must name the verified backup manifest, its compatibility limits and that runbook;
it must never imply that a backup alone guarantees an automated restore operation.

No implicit repository relocation, state-root migration, participant retargeting, new
memory selection, guidance opt-in, traffic test, or rollback is part of upgrade. The
source is an explicitly selected local release tree. Its complete runtime manifest and
content digests are frozen before mutation. Resume rejects a changed source rather
than silently choosing a newer version.

## Durable plan and exclusion

Keep a private upgrade directory inside the prefix, containing a versioned plan,
source and old-runtime manifests, selected component inventory, phase journal, backup
manifest, and verification report. Each publication uses atomic replacement, file
flush, and directory flush. Bound every inventory and reject duplicate or unexpected
paths; never accept a manifest that can name arbitrary files outside the selected
runtime/state roots.

The permanent installation lock serializes transitions. An additional durable upgrade
marker makes ordinary install, ensure, removal and another upgrade refuse while an
operation is incomplete. Status, owned stop and explicit resume remain available.
Publish a private, digest-verified recovery bundle before replacing any runtime module.
It must remain executable if replacement stops halfway through; `--resume` cannot depend
on importing a partially replaced prefix. The ordinary launcher and the retained
standalone recovery entrypoint identify the same frozen plan and coordinator version.

Long waits must not hold a lock required by the selected child's startup or shutdown.
Revalidate the plan and phase after reacquiring a lock; do not treat an unlocked wait
as exclusive ownership. Permanent lock inodes are never replaced or removed.

The coordinator records intent before an external action and observes the action's
outcome afterward. A timeout means the outcome is unknown. Resume establishes current
manager, process, artifact and file identities before deciding whether an action is
already complete or may be repeated. It never repeats a destructive operation solely
because its completion record is missing.

## Phases

| Phase | Completion evidence |
| --- | --- |
| Preflight | Exact prefix, source, state roots, repository identities, participant targets, supported transitions, store capacity, backup space/privacy, destination ancestors and all owned components validated before shutdown. |
| Prepared | Durable source/selection manifest and exclusion marker published; private backup/recovery/report destinations created and durable write probes completed before shutdown. |
| Quiescing sessions | Selected notifier/bridge pairs stopped in dependency order; manager and PID/start/generation evidence proves owned process exit. |
| Quiescing memory | Selected memory runners and children have exited; no unowned listener or writer is accepted as absence. |
| Backed up | Stopped canonical inventory and complete private state/runtime backup durably published and verified. |
| Replacing | Each runtime file and changed owned artifact published from the frozen source manifest; per-file intent/completion permits retry. |
| Verifying migration | New runtime opens selected stores under the verification gate; only declared schema migration is allowed. Canonical inventory comparison succeeds. |
| Starting gated | Selected managers and children are ready with exact new runtime/selection identities while ordinary traffic and maintenance remain gated. |
| Verified | Private comparison report accounts for every required field and all declared changes; no unexplained data difference remains. |
| Releasing | The durable gate is released for the verified generation; partial release is recorded and resumable. |
| Complete | All selected services report readiness; final report records the release boundary and live observations separately. |

A failed phase leaves its plan, backup and diagnostic evidence intact. An error does
not automatically restart old code against a migrated database. Removal must not erase
an incomplete upgrade's recovery program or backup.

## Inventory and backup

Capture all pages of bounded status/inventory interfaces, then capture a canonical
stopped-state inventory after quiescence. Include:

- Repository identity, store UUID, schema, protocol and capabilities; stream head/floor.
- Notes, replay/idempotency rows, frozen snapshots, all consumer cursors and acknowledgements.
- Work items and history, unfinished work, scope revisions, active claims and reserved credits.
- Bridge inbox/handled records and acknowledgement watermark; notification journal counters,
  activation, pending/uncertain delivery state, checkpoints and receipts.
- Explicit memory bindings and their selected identities, work selections, and participant targets.
- Runtime/manager artifacts, ownership records, and captured PID/start/generation evidence.

Counts are useful diagnostics but insufficient preservation proof. Define canonical
ordered representations and hashes for retained logical records, alongside individually
reported identity/cursor fields. Exclude volatile process metadata from data hashes;
do not exclude business data merely because maintenance could change it later.

Before shutdown, measure the complete proposed state/runtime backup and staged source,
account for SQLite sidecars and recovery/report overhead, and compare that requirement
with free space on each destination filesystem. Validate ownership, privacy, symlink
policy and writability of every destination and its existing ancestors; never chmod an
existing ancestor to make it pass. Prepared creates the selected private directories and
completes durable write probes before stopping any service. Recheck available space
before capture. These checks cannot reserve free space against unrelated writers or
prevent later I/O failure; such a failure retains the incomplete phase and leaves runtime
replacement forbidden until a complete verified backup exists. The report must distinguish
that stopped/incomplete state from a usable backup.

Back up stopped databases together with their SQLite sidecars and related configuration,
checkpoint, binding and ownership evidence. Never copy only a live main database. Verify
that the selected writers remain stopped throughout capture. Publish the completed backup
manifest only after all files are copied, flushed and digest-verified. Runtime files are
backed up separately from state; restoring either remains an explicit recovery decision.

## Verification boundary

A startup that immediately admits traffic or runs retention maintenance cannot provide
an unambiguous before/after preservation check. The new runtime therefore needs an
explicit upgrade gate shared by its memory, bridge and notifier entrypoints. It allows
ownership handshakes, health/status and coordinator verification, while refusing ordinary
mutations, suppressing external notification delivery and delaying maintenance.

During quiescence and gated startup, the peer transport listener does not accept new
connections; only the private control interface needed for health and verification is
available. There is no upgrade-time ingress queue and no automatic sender replay.
Senders can observe missing endpoints, connection refusal or timeout. A send racing with
shutdown may have an uncertain outcome: native socket-write completion is not evidence
of durable storage or model processing. Already accepted database work drains before
the stopped snapshot. Senders must inspect their available delivery evidence before an
explicit retry; the upgrade report records the unavailable interval and this limitation.
After release, peers must refresh discovery if the process/address changed.

The gate is bound to the selected upgrade plan and service generation. It survives
coordinator exit and must not become a generic bypass for normal admission or ownership
checks. A child that cannot verify the plan stays unavailable. Native manager restarts
must preserve the gate; a restart is not release authority.

Verify migration and retained data before releasing the gate. Report physical byte
changes after checkpoint, process incarnations and declared migration effects explicitly.
Initial supported memory transitions must be enumerated from the implemented migrations:
schema 4 to 5 preserves the store UUID; schema 3 to 5 assigns one. Same-schema replacement
preserves canonical logical records. Undeclared transitions refuse.

Release is one durable prefix-wide decision for the verified plan. Services acknowledge
that decision independently; a coordinator crash during acknowledgement does not permit
a second comparison against already resumed writers or an automatic rollback. Lease time
continues during downtime: preserving raw lease records does not silently extend their
validity, and expiry maintenance begins only after release.

After durable release, traffic and maintenance may legitimately change data. Capture a
separate live observation with its timestamp; do not label arbitrary subsequent count
changes as proven preservation. The report names the exact gated comparison boundary and
which fields were verified there. A near-full store refuses before replacement wherever
the target transition's capacity requirement can be checked in preflight; any later
migration refusal leaves a resumable phase and the original backup untouched.

## Platform and recovery acceptance

Use the existing platform layer and exact owned manager operations on Linux and macOS.
Manual supervision needs an explicit persistent-process handoff and readiness proof;
printing a command is not completion. Never create system services, enable lingering,
change existing directory permissions, or modify an unrelated manager job.

Exercise synthetic interruption after every durable phase and each external action,
including coordinator death during partial file replacement, shutdown uncertainty,
manager restart under the gate, migration refusal, failed comparison and partial release.
Verify source/config retargeting refusal, permanent lock preservation, unchanged inbox
watermarks and consumer cursors, bounded inventories, and unrelated-prefix survival.
Run native Linux/macOS end-to-end upgrade fixtures plus manual-supervisor handoff coverage.
The release gate is a working resumable command and its generated report, not this design
or a rewritten manual runbook.

## Implementation slices

The first internal primitive, `upgrade_inventory.py`, captures a consistent SQLite
snapshot from a dedicated connection after the coordinator has established stopped
ownership (or selected a verified consistent backup). It does not open paths, validate
service ownership, migrate data or authorize replacement. Every catalog object is
fingerprinted; ordinary and FTS shadow tables include every stored column and duplicate
row. Virtual interfaces and views are recorded without querying their results.

Rows use storage-class tags and length framing. Each table hashes the sorted multiset of
SHA-256 row digests, including multiplicity, so physical order and collations cannot hide
changes. SQLite's sequence table is included even when its inbox is empty. The assumption
is SHA-256 collision resistance. Counts accompany the hashes as diagnostics. Catalog,
column, row, value and total encoded-byte bounds produce explicit refusal rather than a
partial inventory; they are not wall-clock I/O limits.

Exact comparison lists added, removed and changed tables and catalog changes. It grants
no migration exceptions. Store-specific identity fields, supported migration adapters,
private backup/manifest publication, exclusion gates and the coordinator remain separate
implementation steps before the proposed public command can become available.

`upgrade_manifest.py` freezes an explicit bounded list of selected source files with
per-file sizes and SHA-256 hashes. Reads refuse symlink components, hardlinks, unsafe
owners or permissions, changing file identities, missing files and capacity overflow.
Verification compares the complete selection with its frozen manifest; it does not
silently recapture a different source. This helper does not discover an allowlist or
prove that mutable files across separate reads form an atomic snapshot. The coordinator
must retain the manifest digest in its durable plan, stage and verify the frozen bytes,
and establish stopped ownership before using file copies as state-backup evidence.
The helper performs no copy, installation or permission repair.
