# Board link — definition of done

Every chunk cites `decision.md` and this file. Tests are synthetic and run in CI on Linux and
macOS (WDR 004, Decision 3). Each guarantee is proven once, in the chunk that adds it, at the
boundary where Koinon enforces it. Two daemons in one test use separate private state
directories and the link test harness of #670, with real TLS between them. No test reaches a
real agent, a real board or another computer.

## One place for the card

- The card's computer (the origin) holds the card and all its state: fields, revisions,
  recipients, verdicts, snapshots, deliveries and acknowledgements. The agent's computer
  stores none of them. Its only stored trace is the notice in the agent's inbox.
- Create, edit, withdraw, close, restore and verdicts happen only at the origin. No link
  operation edits a card, gives a verdict, closes it or acts as the maintainer or overwatch
  (PDR 011, Decision 2, item 3 as amended).
- Tests: after a notice, a read and an acknowledgement, the agent's store has no card row and
  no card text; a forged edit, verdict or close over the link is refused.

## Recipients and upgrade

- A recipient `NAME@COMPUTER` on create or edit resolves when COMPUTER is the effective name
  of a paired computer: kind `remote`, with the pair ID stored on the recipient row.
  Otherwise it is unresolved with reason `link_computer_unknown`, as an unknown local name is
  today. Whether the other computer knows NAME is known only after a notice.
- Each event that the recipient must hear of is captured in the same transaction as the
  event: one notice entry per remote recipient for a create, an edit, a withdrawal, a close,
  an archive, a restore, a removal and a verdict. No entry is lost while the computer is
  offline, unsupported, or between the sprint's releases.
- Upgrade reconciliation, once, in the schema migration: for each card that is not archived
  and names `NAME@COMPUTER` for a computer that is paired at that time, the recipient row
  becomes `remote` with its pair ID, and when the current verdict's delivery to that name is
  `skipped` with `unknown_name`, it becomes a remote delivery with a queued verdict notice.
  Archived cards and their skipped deliveries stay as they are.
- Tests: a paired computer; an unpaired name; a name that was paired and is now unpaired; a
  rename between create and verdict; the migration from a v0.13.1 store seeded with
  `unknown_name` recipients and `skipped` deliveries (active, with verdict, archived).

## Notices

- Notices cross on their own ordered stream per pair, with its own worker and its own
  connection, separate from the direct-message stream. It uses the durable outbox, ordering,
  retry, backoff and stored result of #670 chunk 03. A refusal, a stall or a closed connection
  on the notice stream never blocks a direct message, and the reverse.
- Capability probe: before its first notice on each new connection, the worker sends a small
  probe operation. A computer that does not know it answers `link_operation_unknown`; the
  worker shows that pair's waiting notices as `unsupported` (not final), sends none, and
  probes again on the next connection after the next backoff, so notices resume by
  themselves after an upgrade, with no edit and no new pairing.
- A notice holds the origin's reference, the event, the verdict kind and verdict ID for a
  verdict, and the revision. It holds no title, field or entry text, so its frame stays small.
- On the agent's computer, NAME resolves as a direct message from the link does: a peer name,
  or a participant address so that a holder change or a context cycle keeps it. The notice is
  one typed inbox message with its ordinary wake, committed before the answer. An unknown
  name, an unheld address or an inactive recipient answers `refused` with the reason, and the
  origin shows it. A repeat answers its stored result and writes nothing.
- Tests: disconnected agent computer, then reconnect; a duplicate and a reordered retry; a
  lost answer after commit; a receiver at v0.13.1 with the v0.13.1 codec, then its upgrade
  while notices wait; the direct-message stream continues while the notice stream waits; peer
  name; participant address across a holder change; unknown name.

## Remote read and acknowledgement

- New request operations, from the agent's computer to the origin, each on its own
  connection and bounded in time: read one card, list the cards that name the agent, list its
  waiting verdicts, and acknowledge verdicts. The agent's computer sends the agent's peer name
  and the participant address it holds now; the origin answers only for remote recipient rows
  of that pair with one of those names (acceptance 10).
- Tools on the agent's computer: `decision_get` takes `REFERENCE@COMPUTER`;
  `decision_list --filter mine` and `decision_pending` take an optional `computer`;
  `decision_ack` takes an optional `computer` with the origin's verdict IDs. Without
  `computer`, each tool works as today on the local board. Each remote answer has `origin`.
  Paging uses the origin's cursors.
- The answer frame is bounded at 1 MiB (`link.FrameLimit`), as the maintainer operation's is.
  A card with every field at its limit and the worst JSON escaping fits; nothing is cut short.
  A page of a list or pending answer is cut to fit, with its cursor.
- An acknowledgement is recorded at the origin before the answer. A repeat changes nothing.
  The agent's computer stores nothing and queues nothing for it.
- Offline, unpaired, an earlier release or a timeout: a coded error (`link_computer_offline`,
  `link_computer_unknown`, `link_unsupported`), and no change on either side.
- Tests: read, list, pending and ack across two daemons; the same reference and verdict
  number on both computers; a name that the card does not name gets nothing; a holder change
  between the notice and the read; the origin offline; a computer at v0.13.1; a card at every
  field limit; a repeated acknowledgement; a lost answer after the acknowledgement commits.

## Lifecycle at the origin

- A recipient removed by an edit gets a last `removed` notice, and no later one. Its remote
  reads of that card then get nothing.
- An unpair retires the notice stream of that pair with the message stream, as #670 does.
  Waiting notices for that pair become `unpaired`.
- `decision_get`, `decision_history` and the dashboard card show each remote recipient with
  its computer and each notice's state: `queued`, `unsupported`, `received`, `refused` with
  the reason, `unpaired`; and each verdict delivery as `acknowledged` after the ack.

## Schema

- One schema version for the sprint, created complete in chunk 01: every table, column and
  index that later chunks use. Later chunks change no schema. The upgrade test runs from a
  v0.13.1 store.

## Documentation and records

- PDR 011, Decision 2 and PDR 007, Decision 4 record BOARD-LINK-746 with the maintainer's
  words, in chunk 01. PROTOCOL.md describes the operations, the notice fields, the remote
  recipient kind, `origin`, the `computer` parameters and the reason codes. docs/USAGE.md
  and the installed guide (`koinon guide --agent FAMILY`, Decisions) say how to name a remote
  recipient, what a notice is, and how to read and acknowledge a remote card. CHANGELOG.md
  has each chunk's entry.

## Checks and evidence

- Each chunk: the check skill (`go vet`, gofmt, the race suite, `git diff --check`, the four
  `CGO_ENABLED=0` builds), CI on Linux and macOS at the exact head, Codex peer review and
  refusal review bound to the head.
- Live evidence after the last chunk merges, before the issue closes: two of the maintainer's
  paired computers at the new build. A synthetic test card on one names a test session on the
  other; the notices, the wakes, the remote read, the remote pending list and the
  acknowledgement at the origin are observed and recorded in `verification.md`. The
  maintainer names the computers and the build.
