# Chunk 03 — Remote read and acknowledgement

Cites `../decision.md` (acceptance 2, 4, 5, 6, 7, 10) and `../definition-of-done.md` (Remote
read and acknowledgement; One place for the card, agent side; Lifecycle at the origin, for the
dashboard; Documentation and records; Checks and evidence).

## Outcome

On its own computer, the agent reads the live card, the cards of one computer that name it and
its waiting verdicts there, from the card's computer over the link, and acknowledges a verdict
there. Nothing is stored on the agent's computer. The dashboard card shows each remote
recipient's state. The guide and USAGE describe it. The live check is recorded.

## Work

1. Link: the read, list, pending and acknowledge request operations, each on its own
   connection with a time bound, and the 1 MiB answer bound in `link.FrameLimit`.
2. Origin: answer only for remote recipient rows of the asking pair with the agent's peer
   name or held participant address; record an acknowledgement before the answer.
3. Tools: `decision_get REFERENCE@COMPUTER`; `computer` on `decision_list --filter mine`,
   `decision_pending` and `decision_ack`; MCP schemas, CLI flags and the coded errors.
4. Dashboard: the card's recipient list shows remote recipients with computer and state.
5. `koinon guide` (Decisions), docs/USAGE.md, PROTOCOL.md; CHANGELOG.md.
6. Live check: `verification.md` with the steps and results on two of the maintainer's paired
   computers, with a synthetic card and test sessions, on the computers and build that the
   maintainer names.

## Tests

The Remote read and acknowledgement tests and the agent-side One place tests in the
definition of done, and a dashboard test for the recipient states.

## Out of this chunk

Nothing of #746.
