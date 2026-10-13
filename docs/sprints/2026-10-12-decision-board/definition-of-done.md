# Decision board — definition of done

Every chunk cites `decision.md` and this file. The tests are synthetic and run on Linux and
macOS CI. Each guarantee is proven once, in the chunk that adds it, at the boundary where it
is enforced. A later chunk adds only its own cases.

## Records and agent queries

- Agents read the board only through Koinon: MCP tools, `POST /v1/decisions/*` and
  `koinon decision ... --as`. No agent interface returns, needs or parses dashboard HTML. A
  test calls every agent operation and asserts that the replies are JSON records with no
  dashboard markup, and that each reply's work is bounded by its page limit, not by the
  number of stored items. The test fills the store to its item cap and measures this.
- Create, edit, get and list work for every supported family, using synthetic native
  metadata and DeepSeek's command path. Guessed caller identity, `maintainer` as a caller and
  unknown arguments are refused. Arguments that name a verdict, dismissal, audit or agent-row
  field are refused as unknown fields on every interface.
- Lists: `waiting` (open decision items without a current verdict), `open`, `archived`,
  and `mine` (items whose recipients resolve to the caller's session or held participant).
  Each list is keyset-paged by a stable cursor, with at most 100 items and 64 KiB per page,
  and returns `more` and `next_after`. A complete walk returns every match exactly once while
  items are created, edited, decided and archived during the walk. Summaries hold the
  reference, title, kind, status, revision and current verdict, not the full text.
- `decision_get` by reference returns the full item, its current revision and its verdict
  history, for open and archived items. An unknown reference is `not_found`.
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
- An fyi item offers Dismiss only. Dismiss sets done and its time. It is not an approval and
  creates no delivery.
- Reopen: an agent edit of a decided item increments the revision, sets the item open, and
  marks the current verdict `superseded` in the same transaction. The earlier verdict, its
  snapshot and its deliveries stay in the history. A superseded verdict's unacknowledged
  deliveries show `superseded`. The item waits for a new verdict on the new text.

## Delivery

- Recipients resolve to a participant address or a peer name. An unresolved recipient is
  stored with its reason and shown on the card and in agent reads. A verdict on such an item
  records a skipped delivery with that reason.
- Each delivery is a stored record (verdict, recipient, outcome, acknowledgement time) and
  one typed inbox message with `system_kind` `decision_verdict` and the verdict ID. The
  existing wake path delivers that message. A participant's delivery follows its current
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
- Bounds: 256 open items, 10 000 items in all, 32 recipients per item, 4 000 verdicts kept.
  Creating past a bound is refused with a fixed code. Archive retention removes the oldest
  archived items whose deliveries are all acknowledged or superseded, in bounded control
  deletions that allocate no new notice or audit.
- Agent writes and verdicts are ordinary writes. When the store is full, they are refused
  visibly and nothing is half written. Acknowledgement is a fixed-size control update. A
  storage test at the bounds shows that existing funded work and cycle controls keep their
  guarantees.

## Publication

Each chunk uses check and ship, WDR 004 for publication, WDR 003 for exact-head review and
WDR 006 for native evidence. Every PR enables squash auto-merge. PROTOCOL, USAGE, the guide,
the MCP tool descriptions and CHANGELOG change in the chunk that changes the behavior.
