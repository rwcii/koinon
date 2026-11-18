# Chunk 01 — Capture at the origin

Cites `../decision.md` (acceptance 1, 9, 10 at the origin) and `../definition-of-done.md`
(Authority and identity, origin side; Recipients and upgrade; Visibility at the origin, in
`decision_get` and `decision_history`; Schema; Documentation and records, for the records).

## Outcome

On the card's computer, a recipient `NAME@COMPUTER` for a paired computer resolves as
`remote`. Each create, edit, withdrawal, close, archive, restore, removal and verdict queues
one board entry for each remote recipient, in the same transaction, and the card shows each
remote delivery as `queued`. The schema migration reconciles cards that already name such
recipients. An unpair marks that pair's waiting entries `unpaired`. Nothing is sent yet.

## Work

1. Records: BOARD-LINK-746 with the maintainer's words in `../decision.md`, PDR 011,
   Decision 2 (items 3 and 5) and PDR 007, Decision 4.
2. Schema, complete for the sprint, one version: the remote recipient kind with its pair ID;
   the board entries at the origin with their per-recipient state; the board streams per pair
   (`link_streams` gets a stream kind); the receiver's copies, remote verdict deliveries and
   their numbers; the queued acknowledgements. Upgrade test from a v0.13.1 store.
3. Upgrade reconciliation as the definition of done says, in the migration.
4. Origin capture in `DecisionCreate`, `DecisionEdit`, `DecisionWithdraw`, `DecisionClose`,
   `DecisionRestore`, the archive path and `deliverVerdict`: one entry per remote recipient,
   each with the whole card at that revision, and the verdict with its snapshot and entry.
5. `decision_get` and `decision_history` show remote recipients with computer and delivery
   state. An unpair marks the pair's waiting entries `unpaired`.
6. PROTOCOL.md for the remote recipient kind, the states and the reason codes; CHANGELOG.md.

## Tests

Recipients and upgrade, the origin-side identity tests, and each capture point with one and
two remote recipients.

## Out of this chunk

The link operations and the receiver (02). Retention, the dashboard view and the live check
(03).
