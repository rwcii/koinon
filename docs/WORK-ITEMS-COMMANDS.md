# Staged work commands

This slice implements the [approved work contract](WORK-ITEMS-V1.md) on synthetic
schema-5 stores. Public startup remains schema 4 and refuses these commands with
`schema_too_old`. There is no supported activation flag, live migration, participant
opt-in or background maintenance loop in this slice. The CLI parser is packaged
so the interface and installed dependencies can be tested before activation.

## Interface

The CLI uses `memory.py --consumer NAME work OPERATION` and
`memory.py --consumer NAME claim renew`. Work IDs are positional except on create
and list. Wire names are `work-create`, `work-get`, `work-list`, `work-propose`,
`work-edit`, `work-start`, `work-update`, `work-release`, `work-finish`, and
`claim-renew`. Wire fields use underscores; CLI options use hyphens. Unknown work
request fields are refused. References are inert reported strings.

| Operation | Fields beyond work ID and consumer |
| --- | --- |
| create | Required title, criteria, non_goals, key, deadline; optional proposed_assignee, references, author. |
| get | Optional revision selects a retained scope revision instead of the current view. |
| list | Optional lifecycle, owner, proposed_assignee, stale, blocked, limit (1–100). |
| propose | Required if_revision and proposed_assignee; explicit JSON null clears it, or CLI `--clear-assignee`. |
| edit | Required if_revision and at least one of title, criteria, non_goals; a live claim requires its owner's claim_generation. |
| start | Required if_revision, checkpoint, next_artifact, progress_deadline, key, deadline; optional lease_seconds and resources. |
| update | Required if_revision, claim_generation, progress, checkpoint, next_artifact, progress_deadline; optional active/blocked lifecycle, blocker, references, renew_for. Blocked requires blocker text. |
| release | Required if_revision, claim_generation and final checkpoint. |
| finish | Required if_revision, claim_generation, outcome; completed requires references, withdrawn requires reason. |
| renew | Required claim_generation and if_claim_revision; optional lease_seconds. Work revision is unchanged. |

Other mutations accept optional paired key/deadline and reported author fields.
CLI references repeat `--reference`; start resources repeat `--path-resource` or
`--exact-resource`. JSON resources are `[kind, key]` pairs. Claim generation is a
durable writer token, distinct from the service-instance generation used for
endpoint targeting. Lease duration defaults to 900 seconds and is bounded to
60–3,600. Progress deadlines must be after server time and within one day; retry
deadlines follow the existing 24-hour horizon.

Results contain work ID, revision, sequence and duplicate flag. Start/update/renew
also return claim generation/revision/expiry; renewal's sequence is null. Replay
returns the original bounded result before mutable preconditions or reconciliation.
Transport service generation is excluded from fingerprints so retries can survive
restart; claim generation and all semantic request fields remain bound to the key.

## Transaction and recovery behavior

The memory worker serializes commands. Each observable mutation writes the record,
scope history if applicable, paired stream/event rows, head and replay result in
one Store transaction. Only after commit can the existing content-free wake fire.
Renewal writes no work event and does not report progress. Ordinary writes preserve
remaining credits; release, finish and due transitions consume their own obligations.

After static validation and replay lookup, a mutation may independently reconcile
one due transition for its known target. Lease expiry wins over progress overdue.
A start may separately reclaim that target's inactive bundle under ordinary
admission. A later revision or ownership refusal can therefore follow a committed
older due event; reread the item before retrying. Invalid fields and unknown targets
do not trigger reconciliation. These are bounded target operations, not a background
sweep. Periodic maintenance, diagnostics, shutdown integration and physical finished
item reclamation remain the next slice.

Work mutations also run the existing rate-limited note/snapshot/replay expiry after
validation and retry lookup. That legacy expiry routine is separate from the bounded
work-target transition. Reads and successful replays bypass cleanup writes.

Get/list compute effective ownership and progress at server `observed_at`; reads
never renew, acknowledge, or require writable storage. Finished items become
invisible to new reads/snapshots at their fixed 30-day expiry even before cleanup.
Existing snapshots and replay results retain their separate lifetimes.
Full views expose `observed_lease_expires` from the retained bundle, even when it is
expired. This distinguishes the deadline used for the observation from historical
`last_lease_expires`, which standalone renewal does not rewrite.

## Stream and snapshot readers

Schema-5 sync and ack require integer `record_format: 2`, checked before maintenance,
snapshot issuance or cursor mutation. The CLI supplies it automatically. The guard
also applies to stores containing only notes. Schema-4 behavior stays compatible.

Delta work entries have `type: work-event`, `event_kind`, `work_id` and immutable
`payload`, alongside sequence and provenance. Snapshot work views have
`type: work-item` and `seq` equal to their latest event sequence. A snapshot includes
one frozen current view per retained work ID, ordered by ID after the existing note
selection. Ownership/deadline observations describe snapshot creation, not later page
reads. Legacy frozen note snapshots resume unchanged; newer events follow as deltas.

Work text is excluded from note search and FTS rebuilds. Work-only head increments
keep an already-current note index current without adding documents. Notes cannot
supersede or revoke work events. The shared 5,000-entry ceiling includes work stream
rows; it is not a notes-only allowance. The [storage limits](WORK-ITEMS-STORAGE.md)
and encoded record/scope/event/replay bounds still apply independently.
