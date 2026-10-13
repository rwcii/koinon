# Chunk 02 — verdicts, reopen and delivery

Depends on chunk 01. Cites `decision.md` (criteria 3, 4, 5, 8, 9, 10) and
`definition-of-done.md` (Verdicts, Delivery, Storage).

## Storage

The next free schema version adds:

- `decision_verdicts`: ID; item ID; item revision; verdict (`approved`, `declined` or
  `dismissed`); optional note (Markdown, 4 KiB); time; snapshot (JSON of title, context,
  recommendation and commands of that revision); `superseded` time. Index: (item ID, ID).
- `decision_deliveries`: verdict ID, ordinal, recipient kind and target, outcome
  (`queued`, `skipped`, `acknowledged` or `superseded`), reason, inbox message ID,
  acknowledgement time. Indexes: the primary key, and (recipient kind, target, outcome,
  verdict ID) for `decision_pending`.
- `messages.decision_verdict`, the typed verdict ID of a `decision_verdict` system message.

## Dashboard verdicts

Approve and Decline on a decision item, and Dismiss on an fyi item, are dashboard actions
through `run` with the existing login, Host, Origin and CSRF checks. The form sends the
reference, the displayed revision and the optional note. One ordinary write checks the
revision, stores the verdict and snapshot, sets the item done, resolves the recipients,
stores one delivery and one typed inbox message per resolved recipient (skipped with a reason
otherwise), and commits with its audit record. Dismiss stores no delivery. The audit target
holds the reference and verdict only, not card text. No `/v1` route or MCP tool reaches
these actions.

## Reopen

`decision_edit` on a decided item now increments the revision, sets the item open, sets
`superseded` on the current verdict, and marks that verdict's unacknowledged deliveries
`superseded`, in one transaction. History keeps every verdict with its snapshot.
`decision_get` returns the history. A verdict always names the revision it was given on.

## Delivery

The typed inbox message's body is fixed: `Decision <reference> has a verdict
(<verdict ID>). Read it with decision_get.` The existing wake path delivers it. A
participant recipient's message goes to the participant inbox, so the current holder reads
it.

- `decision_pending`: the caller's queued deliveries (its session and held participant),
  paged by verdict ID, each with reference, verdict, revision and time.
- `decision_ack`: verdict IDs (at most 32). A fixed-size control update sets
  `acknowledged` and its time. Repeats change nothing.

Pending deliveries do not depend on the inbox message: after it fails or ages out, the
delivery stays pending until acknowledged or superseded.

## Archive and retention

Done items are archived. Archive retention runs in the existing sweep, as bounded control
deletions, under the bounds in the definition of done.

## Docs

PROTOCOL (verdicts, reopen, delivery, pending and acknowledgement), USAGE, the guide (the
dashboard verdict is the maintainer's instruction and approval, PDR 005, Decision 3; a
peer's relay or quote is not), tool descriptions and CHANGELOG. PDR 007 Decisions 2 and 4
match the shipped behavior.
