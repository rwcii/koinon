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
  or closes it (`decision_remote_read_only`). A copy gives no maintainer, overwatch or
  local-agent authority on either computer (PDR 011, Decision 2, item 3 as amended).
- One copy for each recipient: the receiver keys a copy by the pair ID, the origin's item ID
  and the local recipient name, and a remote verdict delivery by the pair ID, the origin's
  verdict ID and the local recipient name. Two recipients on one computer never share a copy,
  a delivery, an acknowledgement or a retention state.
- Each copy has a local copy number, which never changes and is never reused. Each remote
  verdict delivery has a local remote-verdict number, with the same rule. These numbers are
  the stable locators: they stay valid after an unpair, a re-pair and a computer rename.
- Agents also name a copy as `REFERENCE@COMPUTER`, the convenience form: it finds the copy
  from the pair that now has that effective name, and only that one. A copy from an earlier
  pairing is reached by its copy number.
- Each answer that shows a copy or a remote verdict has `origin` (the computer name at
  receipt and now, or `unpaired`), the copy number and the remote-verdict number.
- Tests: same reference and same verdict number on both computers; a rename of the origin
  computer; unpair, re-pair under the same name, the same reference and verdict number again,
  then access and acknowledgement of the older copy by its number; a forged copy write from a
  local caller is refused.

## Recipients and upgrade

- A recipient `NAME@COMPUTER` on create or edit resolves when COMPUTER is the effective name
  of a paired computer: kind `remote`, with the pair ID stored on the recipient row.
  Otherwise it is unresolved with reason `link_computer_unknown`, as an unknown local name is
  today. Whether the other computer knows NAME is known only after delivery.
- Every change that the card's recipients must see is captured in the same transaction as
  the change: one board entry per remote recipient for a create, an edit, a withdrawal, a
  close, an archive, a restore, a removal and a verdict. No entry is lost while the computer
  is offline, unsupported, or between the sprint's releases.
- Upgrade reconciliation, once, in the schema migration: for each card that is not archived
  and names `NAME@COMPUTER` for a computer that is paired at that time, the recipient row
  becomes `remote` with its pair ID, one copy entry is queued, and the current verdict, when
  its delivery to that name is `skipped` with `unknown_name`, gets a new remote delivery and an
  entry. Archived cards and their skipped deliveries stay as they are.
- Tests: a paired computer; an unpaired name; a name that was paired and is now unpaired; a
  rename between create and verdict; the migration from a v0.13.1 store seeded with
  `unknown_name` recipients and `skipped` deliveries (active, with verdict, archived).

## Transport

- Board entries cross on their own ordered stream per pair, with its own worker and its own
  connection, separate from the direct-message stream. It uses the durable outbox, ordering,
  retry, backoff and stored result of #670 chunk 03. A refusal, a stall or a closed connection
  on the board stream never blocks a direct message, and the reverse.
- Capability probe: before its first board entry on each new connection, the board worker
  sends a small probe operation (well under 128 KiB). A computer that does not know it answers
  `link_operation_unknown`; the worker then shows that pair's waiting entries as
  `unsupported` (not final), sends no board entry, and probes again on the next connection
  after the next backoff, so delivery resumes by itself after an upgrade, with no edit and no
  new pairing. Test with the v0.13.1 codec and a board entry above 128 KiB.
- Receipt means that the receiving daemon committed the copy, or the verdict with its pending
  delivery and inbox message, before it answers and before the recipient's wake. A repeat of a
  committed entry answers its stored result and writes nothing.
- A copy never moves to an earlier revision. A verdict keeps its own snapshot. An older
  verdict that is not acknowledged stays pending after a reopen, as for a local recipient.
- The board operation's frame bound is 1 MiB on both sides (`link.FrameLimit`), as the
  maintainer operation's is. A card with every field at its limit and the worst JSON escaping
  fits; nothing is cut short.
- Tests: disconnected recipient computer, then reconnect; origin offline after receipt (the
  copy and its pending verdict stay readable and ackable); a duplicate and a reordered retry;
  a lost answer after commit; a receiver at v0.13.1; an upgrade of the receiver while entries
  wait; a card at every field limit; the direct-message stream continues while the board
  stream waits.

## Delivery to the agent

- On the receiving computer, NAME resolves as a direct message from the link does: a peer
  name, or a participant address so that a holder change or a context cycle keeps it. An
  unknown name, an unheld address or an inactive recipient answers `refused` with the reason,
  and the origin shows it on that delivery.
- Each copy entry (first copy and each change) puts one typed inbox message in the
  recipient's inbox with its ordinary wake. It is not in `decision_pending`.
- Each verdict entry puts one typed inbox message with its ordinary wake, and one entry in
  `decision_pending` with the card text and the snapshot, as for a local recipient.
- `decision_list --filter mine` lists the caller's copies beside its local cards.
  `decision_get` takes a reference, `REFERENCE@COMPUTER` or a copy number, and returns every
  field, the state, the revision, `origin` and the current verdict with its snapshot.
  `decision_pending` lists remote deliveries beside local ones; `decision_ack` takes local
  verdict IDs and remote-verdict numbers in separate fields. Local integer verdict IDs and
  the existing fields keep their meaning.
- Paging: `decision_list` and `decision_pending` keep their local cursor and add a separate
  remote cursor, as `inbox` keeps `after` and `participant_after`.
- Tests: peer name; participant address across a holder change; unknown name; holder change
  between the copy and the verdict; two recipients on one computer for one card, one removed,
  one acknowledging; paging across local and remote with both cursors.

## Acknowledgement

- `decision_ack` of a remote verdict records the acknowledgement on the agent's computer at
  once, and queues it to the origin on that computer's board stream to the origin. The origin
  marks that recipient's delivery `acknowledged` with the time; a repeat changes nothing. An
  acknowledgement survives a restart of either daemon while it waits.
- Tests: ack while the origin is offline, then reconnect; a lost answer; a restart with the
  ack queued; a repeat.

## Lifecycle and retention

- A recipient removed by an edit gets a last entry that marks its own copy `unnamed`; it gets
  no later entry. Other recipients' copies are not changed.
- An unpair retires the board streams of that pair with the message stream, as #670 does.
  Copies from that pair stay readable with state `unpaired`; their pending verdicts can be
  acknowledged locally, and the acknowledgement is not sent. Entries waiting at the origin for
  that pair become `unpaired`.
- Retention of copies (BOARD-LINK-746): a copy leaves the store 30 days after it became
  archived, unpaired or unnamed, and only when each verdict delivery of that copy is
  acknowledged. An unacknowledged delivery or an active recipient keeps it. The origin keeps
  its card and deep archive as today.
- Tests: each transition; an unpair with queued entries in both directions; the retention
  sweep with one acknowledged and one unacknowledged copy.

## Visibility at the origin

- `decision_get`, `decision_history` and the dashboard card show each remote recipient with
  its computer and each delivery's state: `queued`, `unsupported`, `received`, `refused` with
  the reason, `acknowledged`, or `unpaired`.

## Schema

- One schema version for the sprint, created complete in chunk 01: every table, column and
  index that later chunks use. Later chunks change no schema. The upgrade test runs from a
  v0.13.1 store.

## Documentation and records

- PDR 011, Decision 2 and PDR 007, Decision 4 record BOARD-LINK-746 with the maintainer's
  words, in chunk 01. PROTOCOL.md describes the operations, the copy and remote-verdict
  fields, the remote recipient kind, `origin`, the numbers, the cursors and the reason codes.
  docs/USAGE.md and the installed guide (`koinon guide --agent FAMILY`, Decisions) say how to
  name a remote recipient and how a copy appears. CHANGELOG.md has each chunk's entry.

## Checks and evidence

- Each chunk: the check skill (`go vet`, gofmt, the race suite, `git diff --check`, the four
  `CGO_ENABLED=0` builds), CI on Linux and macOS at the exact head, Codex peer review and
  refusal review bound to the head.
- Live evidence after the last chunk merges, before the issue closes: two of the maintainer's
  paired computers at the new build. A synthetic test card on one names a test session on the
  other; the copy, a change, a verdict, the wakes, the pending entry and the acknowledgement at
  the origin are observed and recorded in `verification.md`. The maintainer names the
  computers and the build.
