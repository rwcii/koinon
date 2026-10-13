# Chunk 02 — verdicts, reopen and delivery

Depends on chunk 01. Cites `decision.md` (criteria 3, 4, 5, 8, 9, 10) and
`definition-of-done.md` (Verdicts, Delivery, Storage).

## Storage

The next free schema version adds:

- `decision_verdicts`: ID; item ID; item revision; verdict (`approved`, `declined` or
  `dismissed`); optional note (Markdown, 4 KiB); time; snapshot (JSON of title, context,
  recommendation and commands of that revision); `superseded` time. Index: (item ID, ID).
- `decision_deliveries`: verdict ID, ordinal, recipient kind and target, outcome
  (`queued`, `skipped` or `acknowledged`), reason, inbox message ID, acknowledgement time. Indexes: the primary key, and (recipient kind, target, outcome,
  verdict ID) for `decision_pending`.
- `messages.decision_verdict`, the typed verdict ID of a `decision_verdict` system message.

## Dashboard verdicts

Approve and Decline on a decision item, and Dismiss on an fyi item, are dashboard actions
through `run` with the existing login, Host, Origin and CSRF checks. The form sends the
reference, the displayed revision and the optional note. One ordinary write checks the
revision, stores the verdict and snapshot, sets the item in progress (Approve) or archived
(Decline, Dismiss), resolves the recipients,
stores one delivery and one typed inbox message per resolved recipient (skipped with a reason
otherwise), and commits with its audit record. Dismiss stores no delivery. The audit target
holds the reference and verdict only, not card text. No `/v1` route or MCP tool reaches
these actions.

## Reopen

`decision_edit` on an in-progress or archived item now increments the revision, sets the
item active and sets
`superseded` on the current verdict, in one transaction. Its deliveries do not change: an
unacknowledged one stays queued until the recipient acknowledges it. History keeps every
verdict with its snapshot. `decision_get` returns the current verdict and the history count;
`decision_history` (reference, `after`, `limit`) pages the verdicts by verdict ID as the
definition of done states. A verdict always names the revision it was given on.

## Delivery

The typed inbox message's body is fixed: `Decision <reference> has a verdict
(<verdict ID>). Read it with decision_get.` The existing wake path delivers it. A
participant recipient's message goes to the participant inbox, so the current holder reads
it.

`wakeNotice` keeps its text for every wake. For a batch whose sequence range holds
`decision_verdict` messages, the wake reads their `messages.decision_verdict` values in one
query on the unique (recipient, sequence) key, limited to the batch range, and appends
`Decision verdicts <ID>, <ID>.`, at most 8 IDs, then `+N more` with the count of the rest. Target selection, coalescing, retry
and inbox read and acknowledgement do not change.

- `decision_pending`: the caller's queued deliveries (its session and held participant),
  paged by verdict ID, each with reference, verdict, revision, time and, for a superseded
  verdict, its superseded time.
- `decision_ack`: verdict IDs (at most 32). An ordinary write sets `acknowledged` and its
  time. Repeats change nothing. When the ordinary band is full it is refused visibly and the
  deliveries stay queued.

Pending deliveries do not depend on the inbox message: after it fails or ages out, the
delivery stays pending until the recipient acknowledges it.

## Close, archive and deep archive

- `decision_close`: reference, `if_revision`, optional outcome note. It moves an in-progress
  item to archived and records the caller's name and the time. Other states are refused
  with `not_in_progress`. A retry with the same key returns the first result.
- `decision_items` gains the closer, the outcome note and the deep-archive mark (file and
  offset).
- The deep-archive sweep, the log file, crash recovery and `decision_restore` /
  `koinon decision restore` are as the definition of done states. The sweep runs in the
  existing daemon sweep and moves at most 64 items per pass.

## Docs

PROTOCOL (verdicts, the lifecycle, reopen, delivery, pending and acknowledgement, close,
deep archive and restore), USAGE, the guide (the
dashboard verdict is the maintainer's instruction and approval, PDR 005, Decision 3; a
peer's relay or quote is not), tool descriptions and CHANGELOG. PDR 007 Decisions 2 and 4
match the shipped behavior.
