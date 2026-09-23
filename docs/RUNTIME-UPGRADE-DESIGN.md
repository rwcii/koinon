# Resumable runtime upgrade design

Status: peer-reviewed design for DQ-12 and issue #43, delivered as `scripts/upgrade.py`. It
does not authorize changing a live installation. The current native installer refuses
different runtime bytes during ordinary repeat installation.

## Scope and command

Entrypoint, as delivered:

```sh
python3 scripts/upgrade.py --prefix /selected/runtime --source /selected/release
python3 scripts/upgrade.py --status /selected/runtime
python3 scripts/upgrade.py --resume /selected/runtime/.upgrade/OPERATION --plan PLAN_DIGEST
```

The three modes are mutually exclusive. `--status` and `--resume` take their target as the
option's own value, and `--resume` requires the plan digest that `--status` reports. An
earlier draft of this document proposed `--prefix … --resume` and `--prefix … --status`;
neither form parses.

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
source is an explicitly selected local release tree.

One exception, approved with the peer status sprint: the upgrade sets up the Claude Code
status-line wrapper ([Claude status line](INSTALL.md#claude-status-line)) unless the user
declined it, so that an existing installation reports Claude context without a separate
step. Preflight records the planned action (`set_up`, `declined` or `skipped`) and a digest of
the settings file in the `prepared-checks` document. The frozen `install.json` cannot record
the saved entry while the operation runs, so the set-up runs after `finish()` restores
ordinary admission, under the ordinary installation lock. A settings file that changed
since preflight is reported as `settings_conflict` and is not written. The outcome is
returned with the completion result and retained as the `claude-statusline` document, so a
repeated `--resume` reports the first outcome. A failure never fails the runtime upgrade;
the result names the repair command. Its complete runtime manifest and
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
| Preflight | Exact prefix, source, state roots, repository identities, participant targets, required SQLite capabilities (3.37+), supported transitions, store capacity, backup space/privacy, destination ancestors and all owned components validated before shutdown. |
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

Release is one durable prefix-wide decision for the verified plan. Releasing-pending
keeps every writer gated. Only the confirmed completion record permits a service to
resume writes. The journal reports this as `release_decision_committed`, not as an
observation of any process. Services acknowledge that decision independently; a coordinator crash during acknowledgement does not permit
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

Preflight must probe the required SQLite `table_list` capability on a transient empty
database before shutdown; SQLite introduced it in
[3.37.0](https://www.sqlite.org/pragma.html#pragma_table_list). An unsupported build
refuses as `unsupported_sqlite` while all selected services remain untouched.

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

The first internal primitive, `koinon/upgrade_inventory.py`, captures a consistent SQLite
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
partial inventory; they are not wall-clock I/O limits. The required SQLite `table_list` capability
(SQLite 3.37+) is probed first; its absence reports `unsupported_sqlite` rather than
misclassifying the retained store.

Exact comparison lists added, removed and changed tables and catalog changes. It grants
no migration exceptions. Store-specific identity fields, supported migration adapters,
private backup/manifest publication, exclusion gates and the coordinator remain separate
implementation steps before the proposed public command can become available.

`koinon/upgrade_manifest.py` freezes an explicit bounded list of selected source files with
per-file sizes and SHA-256 hashes. Initial root selection resolves aliases once, including
macOS system paths; the manifest freezes that canonical root. Resume never follows a
new alias at the frozen root. Reads beneath it refuse symlink components, hardlinks, unsafe
owners or permissions, changing file identities, missing files and capacity overflow.
Verification compares the complete selection with its frozen manifest; it does not
silently recapture a different source. This helper does not discover an allowlist or
prove that mutable files across separate reads form an atomic snapshot. The coordinator
must retain the manifest digest in its durable plan, stage and verify the frozen bytes,
and establish stopped ownership before using file copies as state-backup evidence.
The helper performs no copy, installation or permission repair.

## Coordinator integration constraints

Preserve manager registration and running state as separate plan fields. An upgrade
must not resurrect every historical session registration. Unknown observations refuse
preflight. Previously running components need verified readiness after release; inactive
components must remain stopped with their selection and retained data preserved. If
restoring a native registration causes temporary startup (for example launchd `RunAtLoad`),
that startup remains gated and its owned stop must be verified before global release.
Interruptions between temporary startup and stop need explicit native coverage.

The source runtime may predate the new gate. Component-era runtimes validate
`installation_state` against `installed` and `removing`; a future `upgrading` state can
therefore make older commands or manager restarts that read configuration refuse
with their existing invalid-configuration diagnostic. Running bridge and notifier
processes do not universally reread installation configuration. This exclusion
mechanism is not proof that a running process stopped. New coordinator code must validate
its frozen plan before allowing upgrade-specific access to that state. An old process
that already read configuration still requires owned shutdown and exit verification.
Old invocations waiting on installation or component locks must revalidate after the
coordinator publishes exclusion; tests must exercise that race.

Legacy thread-only installs may have no `install.json`, and absence historically selects
legacy behavior. A lifecycle field cannot protect an absent file. Likewise, a legacy
manual session supervisor lacks the native supervisor's complete ownership record.
Do not infer either into the component upgrade inventory. A supported legacy/manual
adapter must supply explicit inventory and process-exit evidence before those paths can
pass preflight; until then they refuse before shutdown. That adapter is deferred and not
implemented. #43 closes on the delivered operation, which refuses and reports these paths.

After proving owned processes stopped, deactivate only their verified manager
registrations and retain artifacts and state. Startup exclusion during backup requires
more than endpoint probing: memory constructs its database worker while holding
`start.lock`, before completing socket binding. Acquire its permanent start lock only
after shutdown, because cleanup also needs it. A control-socket reservation alone cannot
exclude that early database open. Bridge startup instead reserves its control endpoint
before opening its database. The coordinator must revalidate all stopped owners and
retained paths after taking the appropriate locks or endpoint reservations, keep them
through consistent capture, and release only those needed by explicitly gated new starts.
Neither a failed connection nor the new lifecycle marker substitutes for that evidence.

A standalone recovery bundle must contain the coordinator and its imported dependencies
from the frozen source, bound by the durable plan digest. Resume must not import modules
from the partly replaced installation. Tests must remove or corrupt a prefix module
mid-replacement and demonstrate recovery through that retained bundle, then reject a
changed bundle or source. Ordinary install, uninstall and ensure must refuse incomplete
upgrade state; status and plan-validated recovery must remain available.

The staged `koinon/upgrade_journal.py` records alternating intent and completion for each
coordinator phase. Completion carries the digest of separately verified evidence;
phase records do not establish that the evidence is correct or authorize an action.
The journal is bound to the frozen plan digest, validates its entire bounded shape,
and serializes publication through a permanent private lock. An identical lost-reply
retry is idempotent; a different or stale predecessor requires rereading. Resume refuses
a missing or malformed journal. Initialization belongs only to preparation of a new
operation, before its active-installation marker is published, never to recovery.

The coordinator must separately hold its operation lock while observing and performing
external actions. A short journal publication lock cannot stop two callers from executing
the same pending action. Completing the releasing phase is the single durable release
decision; subsequent completion/acknowledgement records cannot undo it. Synthetic tests
cover publication failures before and after atomic replacement, retained lock identity,
wrong-plan refusal and monotonic release. They do not replace process-interruption,
native-manager or complete coordinator acceptance fixtures.

`koinon/upgrade_documents.py` retains immutable private JSON manifests and evidence, each
bounded to 1 MiB. Their expected digests belong in the frozen plan or journal; a
self-reported digest is not authority. Existing service-owner records retain their
4 KiB default. Exact repeats retain the original document, changed content refuses,
and reads never recreate missing evidence. The phase-journal name is reserved.
Publication refuses a new document when the directory already has 1,024 entries
(including locks and retained scratch files); existing identical documents remain
readable at capacity. Atomic publication may briefly add one replacement file.
Domain schemas and complete recovery-bundle validation remain coordinator work.

A visible record is not proof that its final directory/device flush succeeded.
Journal reads and retained-document reads therefore confirm the current expected
bytes and repeat file, parent-directory, and final file synchronization under the
publication lock before returning durable evidence. Identical retries use the same
confirmation and preserve the original inode. Missing, substituted or unflushable
records refuse; observing a release record whose durability cannot be confirmed
must not release a service. The shared ordinary state reader remains read-only.
Synthetic fault tests cover failures after rename and continuing flush failures;
these do not simulate hardware power loss or prove filesystem/device compliance.

`koinon/upgrade_bundle.py` stages the selected source files and explicit Python entrypoint
as a deterministic private zip application, currently bounded to 4 MiB. Its descriptor
binds the entire source manifest and archive digest; the coordinator must retain that
descriptor in its frozen plan and supply a complete import dependency allowlist.
Preparation verifies source bytes again before publishing, refuses different retained
archives, and confirms all durability flushes on an identical retry. Verification
refuses changed source or archive bytes and never substitutes a new release.

A synthetic archive runs with Python's isolated `-I` option beside a deliberately
broken installed module and conflicting `PYTHONPATH`. This demonstrates archive import
isolation for the supplied fixture, not yet a complete coordinator dependency closure
or native upgrade recovery. The future recovery entrypoint must work inside the archive,
validate its plan before actions, and use the selected interpreter with `-I`; this helper
does not execute a bundle or expose a public upgrade command.


Release acknowledgement integration will use one immutable document per selected
component, named from its fixed plan index. Preflight will cap a plan at 128 components
and reserve space for all acknowledgements, aggregate manifests, phase evidence and
locks before stopping anything; each retry reuses its existing slot. A receipt records
that component's plan/selection identity, observed generation, release decision and
observation time. It is historical evidence, not a substitute for fresh readiness.
Resume validates its domain fields against the frozen plan and reobserves live ownership;
a retained receipt never licenses repeating the gated data comparison after release.

The releasing phase completion commits the global decision first. Per-component
acknowledgements and final readiness observations follow that commit, during the final
completion phase. Partial acknowledgement therefore cannot leave the journal reporting
an uncommitted release while writers have been deliberately released. These are
coordinator ordering requirements; the acknowledgement schema, preflight reservation
and service-gate consumers remain to be integrated and tested.

Archive confirmation uses the same bounded private-file open/reopen primitive as
JSON state recovery; its 4 MiB binary limit does not change JSON limits. Verification
returns a path, so the later isolated interpreter launch still assumes no hostile
same-user replacement between verification and execution. Rechecking the path does
not remove that trust-boundary assumption.

`koinon/upgrade_observation.py` captures a validated native selection's manager registration
and running state separately. Manager observations must agree before and after the
service probe and match the owned runner when running. An inactive result additionally
requires confirmed child exit, no retained control endpoint, and no held owner lock.
Unknown or changing evidence refuses. This is a read-only preflight observation, not
installation-wide enumeration or exclusion against a subsequent start; quiescence
must revalidate under the appropriate locks. Manual selections require their own
explicit adapter before they can enter this native observation path.

`koinon/upgrade_plan.py` binds source, old runtime, installation configuration, component
observations and the recovery descriptor through immutable document digests. It
checks the 128-component bound and reserves directory entries for acknowledgements
and aggregate evidence before publishing plan documents. Preparation only records
pending intent; it does not publish an installation marker or complete preflight.
The coordinator still must prove complete session enumeration, backup space and
ownership before preparing, then establish exclusion before shutdown.

Explicit resume requires the caller's expected plan digest and every retained
document. It never initializes missing phase state. Source and recovery bytes must
still match, and a configured prefix alias cannot retarget the operation. The old
runtime manifest is validated as retained data, not compared with current installed
bytes during every resume: partial replacement is expected in some phases. The
phase-specific coordinator must decide which current bytes are valid and cannot use
successful plan loading alone as permission to replace or release anything.


`koinon/upgrade_backup.py` streams an explicit stopped-state file selection, including
recorded absent sidecars, into a private destination. Bounds are 256 selected names,
1 GiB per file, and 4 GiB of selected bytes; reads use at most 1 MiB chunks. JSON
state/document limits are unchanged. The snapshot records file bytes, hashes and
original modes; copied files are private (0600). The completed descriptor keeps
source and destination evidence separate for later explicit restoration.

A differing retained backup refuses without overwrite. Identical partial copies
are reverified and their file/directory/device flushes repeated. Nested renames
flush every directory link from the destination parent through the backup root,
including intermediate directories retained after an interrupted creation. A free-space
check covers remaining selected bytes plus 64 KiB per selected name and one extra
metadata allowance before copying; it is conservative, does not reserve space,
and cannot prevent later I/O failure. Scratch files are bounded separately by the
per-file limit. Completion is returned only after the selected source and complete
destination both verify. Missing sidecars remain explicit evidence, so a sidecar
appearing since the stopped snapshot refuses.

The caller still must enumerate every required file and prove continuous writer
exclusion. This helper cannot turn copying a live database into a consistent backup,
or certify that an incomplete caller-supplied selection contains all service data.
Complete pre-shutdown space checks, ownership revalidation, SQLite logical comparison
and publication of the plan-bound completed backup remain coordinator integration.

`koinon/upgrade_backup_inventory.py` opens only a disposable, byte-verified copy when
capturing logical SQLite evidence. The main database, WAL, shared-memory file
and rollback journal must all be selected explicitly, including absences. This
allows SQLite recovery and shared-memory bookkeeping to affect the disposable
copy without changing retained recovery bytes. The copy uses query-only SQL,
and the complete retained snapshot is verified again before returning a result
bound to its digest. Missing sidecar selections, changed backup bytes and unsafe
workspaces refuse. A synthetic abrupt writer exit verifies that committed WAL
rows are included and all retained backup file hashes remain unchanged.

Streaming backup verification performs multiple full source reads and destination
verification; disposable logical inspection adds another copy. These costs belong
in the outage and temporary-space budget. None of these bounded operations has a
wall-clock deadline, and these helpers still do not establish writer exclusion.

Native installation observation now enumerates bounded saved session registrations
and configured memory selections while holding the permanent installation lock.
It validates all selections before observing any component, then rechecks config,
the session directory inventory and saved records. It refuses unknown entries,
legacy/manual or unfinished selections, ambiguous shared state roots, and component
capacity overflow. A missing installation configuration never means an empty
upgrade selection. Missing session directories remain absent. This observation
releases its lock on return; the coordinator still must revalidate and publish
startup exclusion before acting. It neither discovers arbitrary unmanaged writers
nor supplies the manual/legacy upgrade adapters required by final acceptance.

Native observation currently requires the recorded Python executable path. Session
records identify a mismatched interpreter explicitly before probing components.
Memory artifacts encode the interpreter without a separate saved field, so a
verification refusal names the current interpreter and asks for verification of
both interpreter and retained selection/artifact; it does not misclassify every
artifact error as a proven Python mismatch. Changing the configured Python executable path is deferred. Run this coordinator
with the executable path recorded by the installation; do not replace interpreter
selection or manager artifacts manually to get past preflight. A supported
interpreter-change adapter must record both old and new paths, verify old artifact
ownership, validate target runtime/SQLite compatibility, and switch the selected
jobs under the same upgrade gates before this operation can offer that change.
Existing selection metadata binds paths, not interpreter binary hashes or version
identities. Replacing a Python binary in place is outside this coordinated
operation and is not detected as a path mismatch.


The staged admission integration publishes an exact plan-bound `upgrading` marker
under installation and component admission locks. Ordinary installation readers
refuse it with `installation_upgrading` and exit 78; the memory supervisor and
session CLI preserve that cause and direct the operator to upgrade status/resume.
They do not request automatic restart or configuration repair. The proposed public
status/resume command is still part of the unfinished coordinator integration.

Selected service status, owned stop and gated startup validate the active plan and
frozen selection before using original installation inputs. Artifact verification
still checks the current owning selection and literal artifact bytes. Ordinary
ensure and deactivation do not acquire this access. New children refuse startup
before the migration phase; thereafter bridge mutations, notifier delivery and
memory maintenance remain gated until the confirmed global release decision.
The bridge reserves its peer socket without listening while its private control
endpoint supports verification. A missing or changed marker never releases a bound
gate. Gate failures stop the affected service and drain its database worker before
releasing owned endpoints.

Synthetic tests cover these admission paths and service cleanup. They are not the
complete stop/backup/replace/migrate/restart operation or native platform acceptance;
those remain required before closing the upgrade gate.


Owned shutdown and guarded component capture are now connected internally.
Shutdown reobserves native manager absence and recorded process exit, handles
already inactive selections without starting them, and refuses memory shutdown
until sessions are stopped. Capture retains permanent supervisor/startup locks;
bridge control endpoints are reserved without listening. It rechecks manager,
owner, directory, lock and endpoint identities through copy completion.

Component inventories include all regular state files and explicit main/WAL/SHM/
rollback-journal absences. Unexpected sockets, links and oversized inventories
refuse. The complete source inventory is frozen before copying, so retry cannot
silently omit a removed record. Copies and their descriptors remain private under
the operation. The old runtime is captured separately from its frozen file list.
Its backup can reside beneath the installed prefix only when no selected source
file overlaps the backup destination; component copies still require disjoint roots. Permanent lock files may appear as retained evidence in a backup;
they must never be restored over live lock inodes.

Reservations record creation intent and captured endpoint identity. After a
coordinator process dies, a recorded reservation can be reclaimed only with the
matching inode and proven owner exit. A crash between bind and inode publication
leaves ambiguous evidence: recovery preserves the endpoint and refuses automatic
removal. Synthetic tests exercise actual owner-process death, binding exclusion,
memory start-lock exclusion, sidecar absence and unchanged retained bytes. Native
end-to-end orchestration, source/runtime replacement and migration verification
remain incomplete; this integration does not make the public command available.


Runtime replacement now consumes the completed backup receipt while retaining the
capture guard. Each selected file must match its frozen old bytes or its recorded
new postimage. A durable file index precedes publication; retries reconfirm completed
postimages and all flushes without rewriting them. Changed completed files refuse.
This adapter permits unchanged or expanded file selections. File removal or layout
migration requires a separate adapter and must refuse during preflight before shutdown.

Memory migration expectations are derived on disposable verified backup copies.
Schema 3 to 5 may assign the verified gated store UUID; schema 4 to 5 preserves it;
schema 5 requires exact canonical equality. Existing replay fields, metadata and
business tables are checked independently of the added work schema. The actual gated
inventory must match the complete expected catalog and logical rows. Search-index
creation and reconciliation wait until durable release. These internal helpers still
require public orchestration, individual identity/cursor reporting, restart integration
and native/manual interruption acceptance before the coordinated command is available.


Internal native manager admission now accepts an explicit coordinator context only
for the selected pending migration/startup phases. It revalidates the active plan,
component membership and complete new runtime bytes before manager actions and
readiness return. Ordinary ensure remains blocked by the installation marker.
These manager results do not themselves verify the children's gate or data inventory;
coordinator orchestration must still join the child generation and private gate status
before migration comparison and release.


Private gated probes now join manager readiness, retained supervisor/child ownership,
kernel control-socket peer PID and child generation with the exact unreleased plan.
Session probes require both bridge and notifier gates. Inventory requests name the
verified generation and are bracketed by fresh gate/ownership observations; a changed
supervisor or successor PID refuses. These observations are still internal inputs to
the unfinished coordinator, not release authorization or native acceptance evidence.


The internal coordinator driver now connects prepared shutdown through replacement
completion (journal steps 2 through 9). Backup destinations must already exist privately
before shutdown. Aggregate backup and replacement receipts bind phase completion;
retries reuse frozen capture evidence and revalidate completed replacement bytes.
Step 9 remains an unavailable, gated installation awaiting migration and restart
verification. This driver does not expose a public upgrade command or complete the
remaining preflight, migration/release, report and platform acceptance requirements.


Inbox expectations currently support exact schema-4 preservation, including allocated
sequence head, acknowledgement watermark, journal activation and delivery identity.
Canonical comparison still covers all inbox and binding rows. Earlier inbox schemas
require a declared migration adapter and must be rejected in preflight before shutdown;
the expectation helper does not migrate or reinterpret them implicitly.


The memory private-inventory request carries both repository and generation, matching
the ordinary target guard before plan authorization. An isolated copied-runtime child
test exercises actual gated startup, exact inventory preservation, wrong-repository
refusal and blocked ordinary writes. After release, deferred search initialization
retries ordinary queue capacity without consuming reserved control slots or terminating
the service. Stop remains responsive during the retry delay. A real worker test blocks
a control job, fills all sixteen ordinary slots, and verifies one successful index
resume after the queue drains. These are portable process/worker checks, not complete
native manager upgrade acceptance or physical power-loss tests.


Release receipts bind the plan to verified component/child generations. Every originally
running selection requires membership (both children of a session pair). A child whose
gate was constructed before release must match that membership before admitting traffic;
a replacement born between comparison and the global decision cannot inherit admission.
A fresh child constructed after the durable release is a separately marked post-release
restart: it validates the release evidence and supplies live readiness, without another
preservation comparison. The construction boundary uses a fresh journal read rather than
the earlier plan snapshot. The marker remains until recorded final readiness at step 19.

Preflight can now retain one permanent installation lock through bounded component
observation, frozen-plan preparation and exclusion publication. This prevents registration
between enumeration and marker publication; component admission locks still serialize
starts that read the old configuration before exclusion. These internal APIs do not
perform the remaining schema/capacity/backup-space probes or expose the public entrypoint.
