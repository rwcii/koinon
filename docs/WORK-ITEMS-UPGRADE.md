# Upgrading repository memory for work items

The current memory runtime creates schema-5 stores and upgrades schema 3 or 4 on
startup. Transport protocol remains 1. It advertises `work_items_v1` and
`memory_record_format_2`; every sync and acknowledgement requires integer
`record_format: 2`, including stores that contain only notes. The current CLI supplies
this field. Older readers receive `client_upgrade_required` before maintenance or
cursor mutation. Upgrade bridge, session, notifier and memory runtime files together:
bindings reject memory schema versions that do not match their runtime.

Installing code and configuring work guidance are distinct from starting a repository
memory service. Memory remains optional; the installer neither starts it nor creates
bindings automatically. Use an authorized maintenance window for an existing service:

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
