# Chunk 03 — production discovery and coalesced wake

Completes decision criteria 6–7/9; depends on chunk02. Current Go's `OnMemoryChange` callback
is not a production wake subscription. Wire the required path; do not claim historical parity.

Audience rules: a proposal/change wakes the current new assignee (and a previous assignee
whose assignment was removed); a report wakes the item's creator; a verdict wakes the
reported worker's participant, or its recorded native actor when no participant exists.
The creator can also discover every item transition through its current queries/catch-up.
Resolve a role audience through the same store's current participant holder; holder changes
retarget pending notices without transferring a work claim. Never resolve a native target
by parsing a claimed consumer label as a session ID.

Add creator/current-assignee query filters derived from the caller's validated consumer/role,
not guessed native IDs. Combine the chunk01 paged current inventory with frozen memory
snapshots/deltas and existing retention floors. A reset uses current queries even when its
continued participant cursor already passed an old proposal/report; never reset that cursor.
Process deltas through the announced head before acknowledging them. Report a retention gap
instead of claiming processing of records that no longer exist.

Use the durable work-event stream as the source of notice reconciliation. Persist bounded
audience watermarks and one pending notice reference per repository/role or creator consumer,
with at most 256 audience rows per store (128 items' assignee/creator pairs). Reconstruct
pending relevant heads from retained events/current items on startup, registration/holder
change and after post-commit `OnMemoryChange`. Never guess which native session holds an
unheld/conflicting role. Use current participant registry/cursor actor provenance to resolve
eligible targets; a custom creator consumer without a verified active actor is shown unresolved
and remains discoverable by query. Clean audience state only when no retained relevant item
or pending notice needs it; use existing ordinary storage/logical-byte limits for these rows.

Reuse the existing inbox/provider wake/read/ack lifecycle, with an explicitly marked daemon
work notice, never a maintainer-authored message. Its body is only a repository work-head/query
pointer, no title, criteria, reference commands or peer content. One pending notice coalesces
all later changes for that audience; after it is handled, a changed head schedules the next.
Transport acceptance is not receiving-model proof. Preserve existing task-failure retries,
native exact targeting, read/ack separation and bounded notification behavior.

Notice enqueue/advance is ordinary work after the event commits; refusal cannot invalidate
a funded report/verdict. Retained events remain the recovery source and reconciliation retries
later. Derive audience identity from stored role/creator provenance, not peer text or a socket
path supplied by a model. Keep provider socket paths unresolved. No new per-transition notice
or model-activity inference is introduced.

Test commit-before-wake, loss, saturation/recovery, coalescing, duplicate handling, task failure,
holder succession, unheld roles, custom consumers and cross-repository isolation with private
provider stubs. Update protocol, usage, guide, storage/schema accounting and Unreleased.
