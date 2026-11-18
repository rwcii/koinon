# Chunk 01 — Capture at the origin

Cites `../decision.md` (acceptance 1, 2, 9 at the origin) and `../definition-of-done.md` (One
place for the card, origin side; Recipients and upgrade; Lifecycle at the origin, for
`decision_get`, `decision_history` and unpair; Schema; Documentation and records, for the
records).

## Outcome

On the card's computer, a recipient `NAME@COMPUTER` for a paired computer resolves as
`remote`. Each create, edit, withdrawal, close, archive, restore, removal and verdict queues
one notice for each remote recipient, in the same transaction, and the card shows each notice
as `queued`. The schema migration reconciles cards that already name such recipients. An
unpair marks that pair's waiting notices `unpaired`. Nothing is sent yet.

## Work

1. Records: BOARD-LINK-746 with the maintainer's words in `../decision.md`, PDR 011,
   Decision 2 (items 3 and 5) and PDR 007, Decision 4.
2. Schema, complete for the sprint, one version: the remote recipient kind with its pair ID;
   the notice entries with their per-recipient state; the notice stream per pair
   (`link_streams` gets a stream kind); the typed inbox notice. Upgrade test from a v0.13.1
   store.
3. Upgrade reconciliation as the definition of done says, in the migration.
4. Capture in `DecisionCreate`, `DecisionEdit`, `DecisionWithdraw`, `DecisionClose`,
   `DecisionRestore`, the archive path and `deliverVerdict`.
5. `decision_get` and `decision_history` show remote recipients with computer and notice
   state. An unpair marks the pair's waiting notices `unpaired`.
6. PROTOCOL.md for the remote recipient kind, the states and the reason codes; CHANGELOG.md.

## Tests

Recipients and upgrade, and each capture point with one and two remote recipients.

## Out of this chunk

The notice stream (02). Remote read, acknowledgement, the dashboard view and the live check
(03).
