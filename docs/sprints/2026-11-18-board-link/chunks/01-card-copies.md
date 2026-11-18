# Chunk 01 — Card copies

Cites `../decision.md` (acceptance 1, 2, 3 for revisions, 5 for withdraw and close, 6, 7, 8)
and `../definition-of-done.md` (Authority and identity; Recipients; Transport; Delivery to the
agent, for copies; Visibility at the origin, in `decision_get` and `decision_history`;
Documentation and records, for the records).

## Outcome

A card on computer A that names `NAME@B` reaches B as a read-only copy. Each later revision,
withdrawal, close, archive and restore at A updates the copy. On B, NAME sees the copy in
`decision_list --filter mine` and reads it with `decision_get REFERENCE@A`. On A, the card
shows the remote recipient as resolved, and each remote delivery with its state.

## Work

1. Records: BOARD-LINK-746 with the maintainer's words in `../decision.md`, PDR 011,
   Decision 2 (items 3 and 5) and PDR 007, Decision 4.
2. Schema: one new version. The remote recipient kind with the pair ID on
   `decision_recipients`; a per-recipient copy-delivery state at the origin; the board stream
   per pair, beside the message stream (`link_streams` gets a stream kind); the outbox entry
   kind; the copy table keyed by pair ID and origin item ID on the receiver. Upgrade test from
   a v0.13.1 store.
3. Origin: resolve `NAME@COMPUTER` against the paired computers on create and edit. After each
   committed create, edit, withdraw, close, archive and restore, queue one copy entry per
   remote recipient in the same transaction, with the whole card at that revision.
4. Link: a new operation for board entries on the board stream, with the 1 MiB frame bound.
   The sender's worker for that stream follows the #670 stream rules. `link_operation_unknown`
   from the receiver marks the pair's waiting board entries `unsupported`, as the definition
   of done says.
5. Receiver: resolve NAME as `linkInsert` does, store or update the copy in one transaction
   with the stream's high water, refuse an earlier revision as a stored no-op, and answer
   `refused` with the reason for an unknown, unheld or inactive name.
6. Read tools on the receiver: copies in `decision_list --filter mine` and in
   `decision_get REFERENCE@COMPUTER`, with `origin`. Every write tool refuses a copy with
   `decision_remote_read_only`.
7. PROTOCOL.md for the operation, the fields and the reason codes; CHANGELOG.md.

## Tests

The tests of Authority and identity, Recipients, Transport and Delivery to the agent (copies)
in the definition of done, with two daemons over the link test harness.

## Out of this chunk

Verdicts over the link (02). Recipient removal, unpair, retention and the dashboard view (03).
