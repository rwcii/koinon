# Decision board — definition of done

Every chunk cites `decision.md` and this file. The tests are synthetic and run on Linux and
macOS CI. Each guarantee is proven once, in the chunk that adds it, at the boundary where it
is enforced. A later chunk adds only its own cases.

## Records and agent queries

- Agents get decision status only through Koinon: MCP tools, `POST /v1/decisions/*` and
  `koinon decision ... --as`. No agent interface returns, needs or parses dashboard HTML.
  This is about status retrieval; dashboard work and UI testing are not affected. A test
  calls every agent operation and asserts that the replies are JSON records with no
  dashboard markup, and that each reply's work is bounded by its page limit, not by the
  number of stored items or verdicts. The test stores 10 000 items, with one item reopened
  to many verdicts, and measures this. 10 000 is a test size, not a limit.
- Create, edit, get and list work for every supported family, using synthetic native
  metadata and DeepSeek's command path. Guessed caller identity, `maintainer` as a caller and
  unknown arguments are refused. Arguments that name a verdict, dismissal, audit or agent-row
  field are refused as unknown fields on every interface.
## Lifecycle

- An item has one state: `active` (it waits for the maintainer's verdict), `in_progress`
  (approved; the agent does the work), `archived` (closed) or deep-archived (moved to the
  log file). A new item is active. Approve moves it to in progress. Decline, and Dismiss
  of an fyi item, move it to archived. An agent closes an in-progress item with
  `decision_close` when the work is done, which moves it to archived and records the
  caller's name, the time and an optional Markdown outcome note (4 KiB). An agent edit of an
  in-progress or archived item reopens it to active (see Verdicts).
- There is no limit on the number of items or verdicts. Field byte limits and 32 recipients
  per item are input rules for one item. A write fails only for a reason that applies to
  that write (a validation error, a stale revision, or a full store under the daemon's
  existing storage bound), never because the board holds many items.

## Agent lists

- Lists: `waiting` (active decision items), `active`, `in_progress`, `archived`, and `mine`
  (items whose recipients resolve to the caller's session or held participant).
  Each list is keyset-paged on the immutable item ID, with at most 100 items and 64 KiB per
  page, and returns `more` and `next_after`. Items created, edited, decided and archived
  during a walk give this result: no item appears twice; an item that matches for the whole
  walk appears once; an item whose match changes during the walk appears at most once. A test
  edits item order and inserts an item during a walk and checks each case. Summaries hold the
  reference, title, kind, state, revision and current verdict, not the full text.
- `decision_get` by reference returns the full item, its current revision, its current
  verdict and the number of verdicts in its history, for active, in-progress and archived
  items. A deep-archived reference is `deep_archived`, with the restore command.
  `decision_history` pages the item's verdicts by verdict ID, at most 16 verdicts and
  256 KiB per page. An unknown reference is `not_found`.
- Page byte limits count the serialized JSON reply, with its envelope and JSON escaping.
  A page stops before the record that would pass its limit, and always holds at least one
  complete record. The largest record a page can hold fits within the limit: a verdict with
  every snapshot and note field at its byte limit, written only in characters with the
  longest JSON escape, serializes below 256 KiB; a list summary with its title at its limit
  serializes below 64 KiB. A test writes those maximum records and reads each in a page.
- Reads move no inbox read or acknowledgement marker and no memory cursor.

## Content

- Each field has a byte limit. Text is valid UTF-8. Control characters other than newline
  and tab are refused in Markdown fields. Title, reference, where and recipients take no
  control characters.
- Terminal-copy commands: at most 8 entries of at most 512 bytes each, printable ASCII
  (0x20–0x7E) only. Tests refuse line breaks, tab, DEL, zero-width, bidirectional and
  non-ASCII look-alike characters on write. A stored entry that fails the rule on read is
  hidden, and its item cannot be approved as displayed.
- Known credential formats are refused in every text field: private-key blocks, and the
  token prefixes of GitHub, AWS access keys, Slack, OpenAI-style and Anthropic-style API
  keys. The list is fixed in code and in PROTOCOL. The daemon cannot find all personal data;
  the guide says so.
- Markdown in context, recommendation, agent text and verdict notes renders as a fixed
  subset: paragraphs, line breaks, lists, emphasis, inline code and fenced code blocks. Tests
  show raw HTML, scripts, links with `javascript:` and image syntax as inert text. Code
  displayed only for reading may hold non-ASCII text.
- The guide tells agents to write card prose in ASD-STE100. The daemon does not check grammar.

## Verdicts

- Approve, Decline and Dismiss exist only as dashboard actions behind the existing login,
  Host, Origin and CSRF checks. MCP, CLI and bearer requests cannot reach them. Tests cover
  the failure of each check.
- The form carries the item revision that the page showed. A verdict on another revision is
  refused as `stale_revision` and changes nothing. The verdict stores the snapshot of the
  title, context, recommendation and commands of that revision.
- One transaction stores the verdict, its snapshot, its audit record and one delivery per
  resolved recipient, or nothing. A test fails each insert in turn and shows that no part
  remains. A lost reply is resolved by reading the item, which shows the stored verdict or
  none.
- An fyi item offers Dismiss only. Dismiss archives the item and records its time. It is not
  an approval and creates no delivery.
- Reopen: an agent edit of an in-progress or archived item increments the revision, sets the
  item active, and
  marks the current verdict `superseded` in the same transaction. The earlier verdict, its
  snapshot and its deliveries stay in the history. Supersession does not acknowledge a
  delivery: an unacknowledged delivery of a superseded verdict stays pending, shows the
  verdict's superseded time, and stays until the recipient acknowledges it. The item waits
  for a new verdict on the new text.

## Delivery

- Recipients resolve to a participant address or a peer name. An unresolved recipient is
  stored with its reason and shown on the card and in agent reads. A verdict on such an item
  records a skipped delivery with that reason.
- Each delivery is a stored record (verdict, recipient, outcome, acknowledgement time) and
  one typed inbox message with `system_kind` `decision_verdict` and the verdict ID. The
  existing wake path delivers that message. When a wake's sequence range holds
  `decision_verdict` messages, its notice also names their verdict IDs, at most 8, then
  `+N more`. The IDs are integers and carry no card text. Target selection and inbox
  semantics do not change. A test shows that repeated wakes carry the same verdict IDs. A participant's delivery follows its current
  holder through succession.
- `decision_pending` lists the caller's unacknowledged deliveries, also after the inbox
  message has failed or aged out. `decision_ack` acknowledges by verdict ID. It is
  idempotent, and a repeated or late acknowledgement changes nothing. Tests: daemon restart
  before and after commit, repeated wakes, a lost acknowledgement reply, holder change,
  recipient expiry and retirement.
- Delivery and acknowledgement are records only. They are not proof that an action was done.

## Board and agent rows

- Agent rows come from the daemon's sessions, claims and observations only. They show the
  public name, busy/idle/unknown with its reason, current claimed work (native and
  participant claims, on the current holder), context used and limit or "not reported", and
  the references that wait for a verdict and name the agent. Stale and unknown values stay
  unknown after a restart. No native ID, repository path or wake target appears.
- Cards follow the supplied design: counts, kind and status, `ACTION FOR YOU` fyi items
  first and then item order and reference, the optional verdict note, disabled actions while
  a request runs, inline errors, separate command boxes with a copy action, the archive and a
  read-only reference lookup. Light and dark use system colors. No remote resources load.

## Storage

- New tables and indexes are created through the schema migration and pass the catalog
  check. Old-schema fixtures migrate without changing messages, claims, aliases or cursors.
- Deep archive: 30 days after an item was archived, the existing sweep moves its whole
  record (item, recipients, verdicts with snapshots, deliveries) to an append-only log file
  in the daemon's state directory, one JSON line per record, in a file per month
  (`decisions/YYYY-MM.jsonl`, mode 0600). The sweep moves an item only when all its
  deliveries are acknowledged; an item with a pending delivery stays archived until then.
  Each sweep moves a bounded batch. The mark and the delete are ordinary writes; when the
  ordinary band is full, the item stays archived and the next sweep tries again.
- The move loses nothing. The sweep marks the item with the target file and offset,
  appends and syncs the line, then deletes the rows in one transaction. After a crash, a
  marked item is checked against its line at the recorded offset: a complete line finishes
  the delete; a missing or partial line is written again. Tests crash at each step and show
  exactly one complete line and no remaining rows, or the rows and no complete line.
- Nothing is deleted. `decision_restore` (reference) and `koinon decision restore` read the
  monthly files from the newest, find the latest line for the reference, and insert the
  record again as archived, with a new 30-day period. The line stays in the log. A restore
  whose reference an existing item uses is refused with `reference_in_use`. Restore reads
  only the log, so its cost grows with the log; it is a rare recovery action and the guide
  says so.
- Agent writes, verdicts, closes, restores and acknowledgements are ordinary writes under the existing
  ordinary page ceiling, so they never use the reserve that existing controls keep. When the
  ordinary band is full, they are refused visibly and nothing is half written; an
  unacknowledged delivery stays pending. A storage test at a full ordinary band shows the
  refused acknowledgement, the delivery still pending, and a successful acknowledgement after
  space is freed. The same test shows that existing funded work and cycle controls keep their
  guarantees.

## Publication

Each chunk uses check and ship, WDR 004 for publication, WDR 003 for exact-head review and
WDR 006 for native evidence. Every PR enables squash auto-merge. PROTOCOL, USAGE, the guide,
the MCP tool descriptions and CHANGELOG change in the chunk that changes the behavior.
