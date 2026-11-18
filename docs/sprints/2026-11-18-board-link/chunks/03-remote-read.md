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

## Live check decision

- Decided by: Robert Capps
- Date: 2026-11-18
- Context: Work item 6 runs the live check on the computers and the build that the maintainer
  names. Paired with blackhole: nebula (v0.13.1, reached over SSH, with the two cards that name
  `name@blackhole`), neutron and mac-studio (connected), build-server (offline). Options:
  blackhole and nebula; blackhole and mac-studio (also macOS, but real agents and no remote
  shell from here); a release first, then the live check.
- Decision: blackhole and nebula, with the build at the frozen head of the chunk 03 pull
  request, after #749 merges and Codex passes that head. Build it for linux/amd64 and install
  it on both with `koinon install`; check `koinon version` on each. A synthetic card on
  blackhole names a test session on nebula as `NAME@nebula`; no real agent is tagged. On
  nebula the test session gets the notice, reads, lists, gets the verdict and acknowledges it;
  blackhole shows the delivery as acknowledged. Check that nebula stores no card row or card
  text, and that the two old cards on nebula now resolve. Record the steps and results in
  `verification.md` in the chunk 03 pull request.
- Reason and scope: the sprint needs a live check on two real paired computers before #746
  closes. Both computers move to schema 40 and cannot go back to v0.13.x; mac-studio, neutron
  and build-server stay at their releases and show `unsupported` until their upgrade.

## Tests

The Remote read and acknowledgement tests and the agent-side One place tests in the
definition of done, and a dashboard test for the recipient states.

## Out of this chunk

Nothing of #746.
