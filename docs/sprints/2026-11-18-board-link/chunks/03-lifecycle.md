# Chunk 03 — Lifecycle and evidence

Cites `../decision.md` (acceptance 5, 6) and `../definition-of-done.md` (Lifecycle; Visibility
at the origin, for the dashboard; Documentation and records; Checks and evidence).

## Outcome

A recipient that an edit removes gets a last update that marks its copy `unnamed`. An unpair
retires the pair's board stream and leaves its copies readable as `unpaired`. Copies leave the
store by the retention rule. The dashboard card shows each remote recipient's computer and
delivery state. The guide and USAGE describe remote recipients. The live check on two paired
computers is recorded.

## Work

1. Origin: on an edit that removes a remote recipient, queue its last update; send it nothing
   later.
2. Unpair: retire the board stream with the message stream; mark that pair's copies
   `unpaired`; keep local acknowledgement of their pending verdicts.
3. Retention: the sweep removes a copy 30 days after it is archived, unpaired or unnamed and
   after each of its deliveries on that computer is acknowledged.
4. Dashboard: the card's recipient list shows remote recipients with computer and state.
5. `koinon guide` (Decisions), docs/USAGE.md, PROTOCOL.md for the states; CHANGELOG.md.
6. Live check: `verification.md` with the steps and results on two of the maintainer's paired
   computers, with a synthetic card and test sessions, at the computers and build that the
   maintainer names.

## Tests

The Lifecycle tests in the definition of done, and a dashboard test for the recipient states.

## Out of this chunk

Nothing of #746.
