# Upgrading repository memory for work items

The current memory runtime creates schema-5 stores and upgrades schema 3 or 4 on
startup. Transport protocol remains 1. It advertises `work_items_v1` and
`memory_record_format_2`; every sync and acknowledgement requires integer
`record_format: 2`, including stores that contain only notes. The current CLI supplies
this field. Older readers receive `client_upgrade_required` before maintenance or
cursor mutation. Upgrade bridge, session, notifier and memory runtime files together:
bindings reject memory schema versions that do not match their runtime.

Installing code and configuring work guidance are distinct from starting a repository
memory service. Memory remains optional; explicit repository component installation can
start its owned supervisor, but never creates participant bindings automatically.
Native component selections refuse different runtime bytes during an ordinary reinstall;
the coordinated replacement operation is tracked separately in DQ-12. The following
manual runbook applies to legacy/manual deployments, not a bypass for that refusal.
The development branch also provides an **experimental upgrade coordinator**:

```sh
python3 /path/to/new-source/scripts/upgrade.py --prefix /absolute/installed/prefix --source /path/to/new-source
python3 /path/to/new-source/scripts/upgrade.py --status /absolute/installed/prefix
python3 /path/to/new-source/scripts/upgrade.py --resume /absolute/installed/prefix/.upgrade/OPERATION --plan PLAN_DIGEST
```

Use the complete selected source checkout for these commands, including recovery
when the installed runtime is partially replaced. Preserve that checkout unchanged
until completion: resume verifies its frozen bytes. The dispatcher retains a private
recovery archive and executes it with isolated Python before stopping anything.
`--status` identifies the recorded operation, plan digest and durable phase. A lost
reply is a reason to inspect or resume that operation, never to remove its marker.
The original runtime, component backups, migration expectations and completion report
remain under the private operation directory. JSON output contains private identities
and cursors; keep it and the recovery directory out of Git.

The executable adapter currently covers **owned native components**
(systemd or launchd selections) and saved manual memory selections, schema-3/4/5
memory and schema-4 inboxes. Inactive native components are temporarily started behind the gate for migration and
comparison, then returned to their original stopped and registered/deactivated state
before release. They are excluded from the release membership and cannot restart while
the upgrade marker remains active. Manual memory uses the explicit foreground handoff below. Legacy manual sessions
without saved supervisor ownership still refuse before shutdown; they are not silently
adopted. Complete #43 acceptance remains outstanding. This command is not a claim that
#43 or promotion is done.

For manual memory, the command returns exit status 75 with JSON status
`manual_handoff_required`, the exact `argv`/shell-quoted `command`, operation path,
plan digest and pending phase. Run that command in a persistent managed terminal or
process session and retain that session for the service lifetime. Then invoke `--resume`
with the returned operation and plan digest. The coordinator never detaches a child.
Printing or starting the command does not complete handoff: resume joins the saved
supervisor generation to kernel process identity and the child's private control
handshake, verifies the migration gate, and performs the same preservation comparison
as a native service. Failed or ambiguous ownership refuses; do not start a duplicate.
An originally stopped manual memory service is temporarily handed off for migration
and stopped again before release. Interruption retains the same pending operation.

Preflight reads literal installer file lists without executing them, checks supported
schema transitions on disposable online SQLite copies, and reserves backup and report
capacity. Those copies are only preflight evidence: authoritative backups include
all SQLite sidecars and are made after confirmed owned shutdown. Replacement invalidates only owned bytecode caches for the selected Python files,
including unchecked-hash caches; unrelated cache-directory entries are preserved.
File removal/layout migration is not supported by this adapter. Insufficient space, near-full memory,
changed selections or unrecognized state refuse without silently resetting anything.

Memory-state discovery covers the installation's configured state root and saved memory
roots. Any directory there without a saved managed selection produces an explicit
unowned-state report and refuses before shutdown, even if stopped. The command does
not adopt, relabel, stop or delete that state. Arbitrary custom service locations outside
those roots are not discovered; complete external-service discovery is still an acceptance
gap. Do not interpret the report's stated discovery scope as a host-wide ownership claim.

Ordinary ingress and notification delivery stay gated while migration and preservation
are verified. The release receipt identifies verified child generations. After release,
only live readiness is checked; preservation comparisons are never repeated over renewed
traffic. An interrupted operation retains exclusion and evidence. Rollback remains an
explicit separate recovery decision and never automatically replaces new writes.

## Recovering from unowned-memory refusal

An `unowned_memory_requires_inventory` error means the installation has no saved
managed selection for a reported memory directory. It does **not** mean the directory
is unused or disposable. It can contain the only copy of notes, work, claims and
consumer cursors. The error occurs before shutdown or runtime replacement; keep the
current runtime available while investigating.

1. Preserve each reported directory in place, including its SQLite sidecars. Do not
   delete, move, rename it, add ownership markers, or edit `install.json` to make the
   check pass. A running database must not be backed up by copying its main file alone.
2. Privately inventory the service that uses it: repository Git common directory,
   exact state path, runtime prefix, interpreter, startup command, native unit/plist
   or persistent terminal, and dependent bound sessions. Read the existing service
   definition and use that runtime's status command. A service may be stopped; absence
   of a running process does not establish that its data can be discarded. If ownership
   cannot be established, stop here and retain the current installation and data.
3. For a legacy or hand-written service, use the maintenance procedure below with
   **every service sharing the runtime**, including that service and its bound sessions.
   Stop them through their existing controls, confirm process exit, and retain a
   consistent private backup of all state before changing runtime bytes. Keep the
   original service definition and startup command so its exact paths can be restored.
   Do not run an ordinary reinstall over a prefix still used by an unaccounted service.
4. The coordinator does not currently adopt a legacy service or import its store into
   a managed selection. Continue using its established ownership arrangement after
   the verified legacy upgrade. If automatic managed upgrades are required, arrange an
   explicit adoption/migration implementation and review first; re-running this command
   or recreating an empty managed store does not transfer the existing memory.

A genuinely retired store can be archived only after its owner confirms the retirement,
all writers are stopped, and a complete backup has been verified. Retirement is a
separate data-retention decision, never a prerequisite silently imposed by this upgrade.

## Legacy maintenance procedure

Use an authorized maintenance window for the existing manual procedure:

1. Identify the exact repository, memory state directory, installed prefix, and affected
   bound session supervisors. Record the store UUID, head, floor, consumer cursors and
   binding identities. Include unfinished work and claims when upgrading an existing
   schema-5 store. Keep this inventory private.
2. Stop that memory service and its affected bound session supervisors through their
   normal controls. Leave unrelated sessions alone. Wait for accepted writes to drain
   and confirm the old processes have exited before replacing runtime files.
3. Take a consistent private backup of the stopped memory and affected bridge/notifier
   state, preserving any SQLite journal/WAL files together with their databases. Never
   copy only a live main database. Preserve checkpoints, inboxes, binding observations,
   consumer cursors and owner metadata; do not commit backups or inventories to Git.
4. Rerun the normal installer with the same prefix, target and selected paths. Preserve
   state intact. A different target thread needs its own state directory. If
   `CLAUDE_CONFIG_DIR` is used, keep it consistent for both bridge and notifier services.
   Do not silently reset checkpoints or rewrite work selections.
5. Start memory explicitly with the upgraded runtime and its existing repository/state
   selection. Startup validates identity and the complete source catalog before
   write-capable configuration. Migration runs individual DDL statements in one
   transaction and changes the schema version last. A failed migration rolls back;
   investigate the reported refusal rather than changing metadata by hand.
6. Restart only the affected session supervisors with matching code. Verify schema 5,
   both advertised capabilities, healthy explicit bindings, and unchanged store identity
   for a schema-4 upgrade. Schema 3 receives a new durable store UUID. Verify unchanged
   head/floor, notes, replay records, frozen snapshot payloads and acknowledged cursors.
   Resume any old snapshot using the new CLI; it accepts existing note payloads and the
   tagged work shapes without resetting cursors. Perform a delivery check only when
   communication with its exact recipient is authorized.

Extra or malformed tables, indexes and triggers are refused rather than adopted.
An identity-free file containing work records is data, not an unfinished empty start.
Startup verifies SQLite schema format 4 after the owner's WAL reset before enabling
work accounting. A near-full upgrade may refuse explicitly while preserving the
original schema and records; deleting active state is not an upgrade recovery method.

Rollback requires a compatible, consistent backup and an explicit recovery plan. An old
runtime refuses a schema-5 store; changing its schema number is not rollback. Preserve
post-upgrade work before any restoration decision: restoring an older backup would
otherwise discard it. See [capacity and recovery limits](INSTALL.md#work-item-capacity-planning)
and [explicit guidance opt-in](WORK-ITEMS-POLICY.md) before first adoption.

For first adoption, exercise two synthetic consumers first: one writer creates and starts
an item, the other observes the conflict and reviews without taking its writer claim.
Checkpoint progress, finish the item, and consume its structured events. Configure live
participants only with authorization for their explicitly selected guidance files.
