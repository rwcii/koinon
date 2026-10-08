# Chunk 03 — Participant state and fencing

Criteria 4, 5 and 10 of `decision.md`. Depends on chunk 02.

## Current state

- Messages belong to a native session: `messages(recipient_family, recipient_id, seq)`, with
  `sessions.last_seq` and `sessions.acked_through`. A send to an alias resolves the holder and
  stores the message in the holder's own inbox; the address used is not recorded.
- A memory cursor belongs to a consumer string (`memory_cursors(repository, consumer)`), by
  default the session's peer name (`ResolveMemoryCaller`).
- A work claim belongs to a consumer string (`claim_bundles.consumer`), by default `FAMILY:ID`
  (`ResolveWorkCaller`). A caller can name any consumer.
- `Register` clears `retired_at`, so a retired session that calls again is active again.

## Change

1. **Participant key.** Each participant has the consumer key `participant:<address>`. Native
   session IDs that start with `participant:` are refused by `validKey`.
2. **Defaults.** When the caller holds a participant and names no consumer, memory and work
   calls use the participant key. A caller that does not hold a participant keeps today's
   defaults.
3. **Participant inbox.** The participant's `names` row gains `last_seq` and `acked_through`. A
   send to an address stores the message under recipient `(family, participant:<address>)` with
   the participant's next sequence; a send to a peer name is unchanged. The wake loop notifies the
   current holder with a content-free notice that names the participant inbox. `inbox` returns
   the caller's session messages and, while it holds a participant, the participant's messages;
   each message carries `inbox` (`session` or `participant`). `inbox` takes `after` and
   `participant_after`; `ack` takes `through` and `participant_through`. A message that the holder
   sends carries the address as `sender_name`, so a reply reaches the participant.
4. **Fencing.** Every call that acts for a participant checks, in its own write transaction,
   that the caller is the current holder; otherwise it is refused with `stale_holder` and writes
   nothing. These calls are: a send whose sender name would be the address, participant inbox
   read and ack, memory sync and ack with the participant key, and every work operation and
   claim renewal with the participant key, also when the key is named explicitly as a custom
   consumer, also through HTTP and the command line. The check runs before a keyed replay
   returns a stored result.
5. **Holder change.** Every change of holder (chunk 02's rules, the maintainer's choice, chunk
   04's succession) retires the former holder in the same transaction, when it is still active.
   The change also records a **fence**: a persistent row (participant, fenced native session,
   time, reason) that survives daemon restarts and repeated registration. A retired session that
   registers again is active again with its own peer name, but it is not the holder, and a fenced
   session never counts as a qualifier in chunk 02's rules. Only the maintainer's dashboard choice
   lifts a fence and makes the session the holder again (`decision.md`, gate B decision); it
   removes the fence row in the holder-change transaction.
6. **Provenance.** `work_events`, the participant inbox acknowledgement and the participant's
   memory cursor record the native session (`FAMILY:ID`) that made each change, next to the
   participant key.
7. **Checkout roles (#83).** A writer whose claim consumer is `participant:<address>` is
   addressable: `CheckoutStatus` resolves a participant consumer of the same repository to its
   current holder's peer name, and to no peer when the participant has no active holder (never a
   guess for any other custom consumer). `RequestCheckout` refuses a request from the current
   holder of that participant as a self-request, and re-reads the holder inside the send
   transaction, so a request never reaches a fenced former holder. A handoff by `work_release`
   with `handoff_to` from the holder works as before; the requester's `work_start` creates a new
   generation.
8. **Upgrade.** Messages, claims and cursors that exist before the upgrade keep their current
   owners, as `decision.md` requires: a session's existing inbox, its `FAMILY:ID` claims and its
   peer-name cursor stay its own. Participant ownership applies to what is written after the
   upgrade. The upgrade note in `CHANGELOG.md` says so.

## Done

- The tests of `definition-of-done.md`, "Participant state" and "Fencing", for every path in
  item 4, including MCP, HTTP and the command line, a keyed retry made before a holder change, a
  custom consumer, re-registration of a fenced session and the injected failure inside the holder
  change; the persistent fence tests; and the "Checkout roles (#83) with participants" tests.
- `PROTOCOL.md` (participant inbox, `inbox` and `ack` fields, `stale_holder`, sender name),
  `docs/USAGE.md`, `docs/WORK-ITEMS-POLICY.md` (participant claims replace the successor waiting
  rule), the installed `koinon guide` text, `CHANGELOG.md`.
