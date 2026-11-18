# Board link — definition of done

Every chunk cites `decision.md` and this file. Tests are synthetic and run in CI on Linux and
macOS (WDR 004, Decision 3). Each guarantee is proven once, in the chunk that adds it, at the
boundary where Koinon enforces it. Two daemons in one test use separate private state
directories and the link test harness of #670, with real TLS between them. No test reaches a
real agent, a real board or another computer.

## Authority and identity

- The card's computer (the origin) is the only authority for the card. Create, edit,
  withdraw, close, restore and verdicts happen only there. The other computer stores a
  read-only copy; no tool, CLI command or dashboard action there edits it, gives it a verdict
  or closes it. A copy gives no maintainer, overwatch or local-agent authority on either
  computer (PDR 011, Decision 2, item 3 as amended).
- A copy is keyed by the pair ID of the origin and the origin's item ID, never by the
  reference alone or by a computer name, which the maintainer can change. A remote verdict is
  keyed by the pair ID and the origin's verdict ID. A local card and a copy with the same
  reference, and a local verdict and a remote verdict with the same number, never collide in
  `decision_list`, `decision_get`, `decision_pending` or `decision_ack`.
- Agents name a copy as `REFERENCE@COMPUTER`, with the origin's current effective name. Each
  answer that shows a copy or a remote verdict has `origin` with the computer name.
- Tests: same reference and same verdict number on both computers; a rename of the origin
  computer; a forged copy write from a local caller is refused.

## Recipients

- A recipient `NAME@COMPUTER` on create or edit resolves when COMPUTER is the effective name
  of a paired computer: kind `remote`, with the pair ID stored on the recipient row. Otherwise
  it is unresolved with reason `link_computer_unknown`, as an unknown local name is today.
  Whether the other computer knows NAME is known only after delivery (next section).
- Tests: a paired computer; an unpaired name; a name that was paired and is now unpaired; a
  rename between create and verdict.

## Transport

- Card copies, verdicts and acknowledgements cross on their own ordered stream per pair,
  separate from the direct-message stream, with the durable outbox, ordering, retry, backoff
  and stored result of #670 chunk 03. A refusal or a stall on the board stream never blocks a
  direct message, and the reverse.
- A new link operation carries them. A computer that does not know the operation answers
  `link_operation_unknown`; the sender records each waiting entry for that pair as
  `unsupported` with that reason, does not retry it until the pair reconnects with a new
  release, and the direct-message stream continues.
- Receipt means that the receiving daemon committed the copy, or the verdict with its pending
  delivery, before it answers and before the recipient's wake. A repeat of a committed entry
  answers its stored result and writes nothing.
- A copy never moves to an earlier revision. A verdict keeps its own snapshot. An older
  verdict that is not acknowledged stays pending after a reopen, as for a local recipient.
- The operation frame is bounded at 1 MiB, as the maintainer operation is
  (`link.MaxMaintainerFrameBytes`). A card with every field at its limit and the worst JSON
  escaping fits; nothing is cut short.
- Tests: disconnected recipient computer, then reconnect; origin offline after receipt (the
  copy and its pending verdict stay readable and ackable); a duplicate and a reordered retry;
  a lost answer after commit; a receiver at an earlier release (unknown operation); a card at
  every field limit; the direct-message stream continues while the board stream waits.

## Delivery to the agent

- On the receiving computer, NAME resolves as a direct message from the link does: a peer
  name, or a participant address so that a holder change or a context cycle keeps it. An
  unknown name, an unheld address or an inactive recipient answers `refused` with the reason,
  and the origin shows it on that delivery.
- `decision_list --filter mine` lists copies that name the caller or the participant it holds,
  beside local cards, in a stable order with paging. `decision_get REFERENCE@COMPUTER` returns
  every field, the state, the revision, the origin and the current verdict with its snapshot.
- Each verdict puts one typed inbox message in the recipient's inbox with its ordinary wake,
  and one entry in its `decision_pending` with the card text, as for a local recipient.
- Tests: peer name; participant address across a holder change; unknown name; holder change
  between the copy and the verdict.

## Acknowledgement

- `decision_ack` of a remote verdict records the acknowledgement on the agent's computer at
  once, and queues it to the origin on the board stream. The origin marks that delivery
  `acknowledged` with the time; a repeat changes nothing. An acknowledgement survives a
  restart of either daemon while it waits.
- Tests: ack while the origin is offline, then reconnect; a lost answer; a restart with the
  ack queued; a repeat.

## Lifecycle

- A new revision, a withdrawal, a close, an archive and a restore at the origin update each
  copy. A recipient removed by an edit gets a last update that marks its copy `unnamed`, and
  its later revisions and verdicts are not sent.
- An unpair retires the board stream of that pair as #670 does for messages. Copies from that
  pair stay readable with state `unpaired`; their pending verdicts can be acknowledged
  locally, and the acknowledgement is not sent.
- A copy leaves the store 30 days after it is archived, unpaired or unnamed, and after each of
  its verdict deliveries on that computer is acknowledged. The origin keeps the record and its
  deep archive.
- Tests: each transition; an unpair with queued entries; the retention sweep.

## Visibility at the origin

- `decision_get`, the dashboard card and `decision_history` show each remote recipient with
  its computer and its delivery state: `queued`, `received`, `refused` with the reason,
  `unsupported`, `acknowledged`, or `unpaired`.

## Documentation and records

- PDR 011, Decision 2 and PDR 007, Decision 4 record BOARD-LINK-746 with the maintainer's
  words, in chunk 01. PROTOCOL.md describes the new operation, the copy fields, the remote
  recipient kind, `origin` and the reason codes. docs/USAGE.md and the installed guide
  (`koinon guide --agent FAMILY`, Decisions) say how to name a remote recipient and how a copy
  appears. CHANGELOG.md has each chunk's entry.

## Checks and evidence

- Each chunk: the check skill (`go vet`, gofmt, the race suite, `git diff --check`, the four
  `CGO_ENABLED=0` builds), CI on Linux and macOS at the exact head, Codex peer review and
  refusal review bound to the head.
- Schema: one new version for the sprint's tables, with the upgrade test from the current
  release's store.
- Live evidence after the last chunk merges, before the issue closes: two of the maintainer's
  paired computers at the new build. A synthetic test card on one names a test session on the
  other; the copy, a verdict, the wake, the pending entry and the acknowledgement at the
  origin are observed and recorded in `verification.md`. Which computers, and the release, are
  the maintainer's choice (no real agent is named on another computer without the
  maintainer's say).
