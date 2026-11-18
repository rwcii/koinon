# Board link — review

## Scope review

codex-koinon, 2026-11-18, on the options before BOARD-LINK-746 revision 1: favored the copy
option, because it keeps the card readable while the origin is offline after receipt, and
asked for these boundaries, which the plan holds: one authority at the origin; read-only copies
with their origin; references and verdict IDs scoped by a stable pair identity; receipt only
after a durable commit and before the wake; an acknowledgement that reaches the origin; no
duplicate or earlier revision on retry; withdraw, close, recipient removal and unpair
outcomes; queued, offline and unsupported outcomes; a full card that is never cut short. Its
test list (disconnected recipient, origin offline after receipt, duplicate and reordered
retry, holder change, same reference and ID on both computers, acknowledgement lost across a
restart) is in the definition of done.

## Plan review

codex-koinon reviewed 4d0fa1b4d1486da6acbcc34a1339ebe8e3e40d26: needs revision, five P2
findings and three clarifications. Each one is accepted and fixed in the next plan commit.

| Finding | Disposition |
| --- | --- |
| 746-PLAN-F1: an earlier release reads a frame of an unknown operation with the 128 KiB bound, so a large board frame gets `link_frame_too_large` before `link_operation_unknown` (reproduced with a 147,512-byte frame); the handshake has no release version | Fixed: a small probe on each new board connection, `unsupported` as a waiting state, a re-probe on each later connection; own worker and connection per board stream; tests with the v0.13.1 codec and an upgrade while entries wait (Transport) |
| 746-PLAN-F2: one shared copy cannot hold per-recipient removal, delivery, acknowledgement and retention | Fixed: one copy and one delivery per local recipient; a two-recipient test (Authority and identity; Delivery to the agent) |
| 746-PLAN-F3: `REFERENCE@COMPUTER` cannot find a retained copy after unpair or name reuse; no remote-verdict types or merged paging | Fixed: local copy numbers and remote-verdict numbers as stable locators, `REFERENCE@COMPUTER` as a convenience form for the current pair, separate fields in `decision_ack`, a separate remote cursor; an unpair and re-pair test |
| 746-PLAN-F4: change notices were dropped, against BOARD-LINK-746 item 4 and #746 | Fixed: each copy and change puts a typed inbox message with its wake; only verdicts enter `decision_pending` (acceptance 3) |
| 746-PLAN-F5: existing tagged cards and skipped verdicts were not handled; chunk 01 accepted remote recipients before verdicts were captured | Fixed: upgrade reconciliation in the migration (acceptance 9); chunk 01 now captures every change and verdict durably before any transport; unpair at the origin in chunk 01, at the receiver in chunk 02 |
| N1: refuse or store an unpaired computer's name | Stored unresolved with `link_computer_unknown`, as a local unknown name; paired but offline stays resolved and `queued` |
| N2: copy retention is a new choice for the card | Added to BOARD-LINK-746 revision 2 |
| N3: one schema version or several | One version, complete in chunk 01; later chunks change no schema (Schema) |
