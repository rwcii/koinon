# Decision board — decision

## Problem

The maintainer's current decision board is available to only one agent family. Other
families need the maintainer to repeat decisions in their terminals. The local Koinon
dashboard already serves every supported family, but it has no common decision record,
frozen human verdict, or durable verdict delivery.

This deliverable implements [#206](https://github.com/rwcii/koinon/issues/206).
Its boundary is recorded in the [paired scope](https://github.com/rwcii/koinon/issues/206#issuecomment-6115978004).
Codex drives the plan; Claude observes. The maintainer supplied the current board's static
UI design as a reference on 2026-10-12. Its generic appearance and interactions apply;
its external provider, private data and repository policies do not.

The maintainer's later text-format choices are in PDR 007, Decisions 1 and 3
([Markdown and ASD-STE100](https://github.com/rwcii/koinon/issues/206#issuecomment-6119203856),
[terminal-copy commands](https://github.com/rwcii/koinon/issues/306#issuecomment-6119251724)).
Dashboard input from the maintainer is instruction and approval under PDR 005, Decision 3.
This reconciliation applies those choices; it does not ask for them again.

## Acceptance criteria

1. The board shows daemon-owned agent name, observed busy/idle/unknown state, current
   claimed work, context used and limit or “not reported”, and waiting decision refs.
   Agents cannot write these rows. Rows have no role, group or agent-defined order.
2. Agents can create and update decision items with ref, item order, title, where, agent
   text, context, recommendation, decision/fyi kind, optional commands, and named
   recipients, and close an approved item when its work is done. The item's state follows
   the lifecycle in criterion 9. Unknown or missing recipients are visibly flagged.
3. Only an authenticated dashboard action can approve or decline, with its time, optional
   note, audit record and exact snapshot of the title, context, recommendation and commands
   the human saw. MCP, CLI and bearer API cannot write verdict fields.
4. Decided text stays unchanged in ordinary use. When the facts change, an agent edit of a
   decided item reopens it for a new verdict. The record keeps every earlier verdict with the
   exact text it was given on. A concurrent edit cannot make a verdict apply to unseen text
   ([maintainer, 2026-10-13](https://github.com/rwcii/koinon/issues/206#issuecomment-6148194337)).
5. An fyi item offers Dismiss only. Dismissal archives the item with its time, and grants no
   approval. Manual work uses the supplied design's existing item convention: kind fyi
   and a title starting with the exact ASCII marker `ACTION FOR YOU`. Those items sort
   before the rest, then by item order and reference. No separate agent-row order is added.
6. Each terminal-copy command appears in its own code block with a copy action and contains
   printable ASCII only (PDR 007, Decision 3). Line breaks,
   zero-width, bidirectional and non-ASCII look-alike characters are rejected on write and
   hidden if encountered on read. Hidden commands cannot be approved as displayed.
7. Card context, recommendation and comments render from Markdown. Code appears in code
   blocks, and agents write all card prose in ASD-STE100 Simplified Technical English
   (PDR 007, Decision 1). Paragraphs and line breaks remain visible. Code shown only for
   reading is outside the command-box ASCII rule. The renderer keeps author-supplied HTML
   and scripts inert under the existing dashboard content boundary (ADR 001, Decision 4).
8. Each accepted verdict has a durable delivery record for each valid recipient. A crash
   cannot lose one; repeated content-free wakes carry the same verdict ID. Recipients read
   the actual record and acknowledge after handling it, ignoring an already handled ID.
   Delivery or acknowledgement is not proof that an action was performed.
9. Items follow the lifecycle active, in progress, archive, deep archive. A closed item is
   archived unchanged and remains readable by reference. After 30 days in the archive, it
   moves to a log file; nothing is deleted, and an old decision can be restored. The board
   does not fail at a fixed number of items
   ([maintainer, 2026-10-13](https://github.com/rwcii/koinon/issues/206#issuecomment-6150321526),
   [states](https://github.com/rwcii/koinon/issues/206#issuecomment-6150362521)).
10. A complete paged waiting query returns exactly open decision items without a verdict,
    excluding fyi, decided and archived items. Every supported agent family can use it and
    read its own verdict records through its existing authenticated interface. Decision
    records are stored data that agents query in bounded pages through Koinon; agents get
    decision status from those queries, not by reading or parsing the dashboard HTML, and an
    agent's cost does not grow with the board
    ([maintainer, 2026-10-13](https://github.com/rwcii/koinon/issues/206#issuecomment-6148016252)).
11. The board remains on the loopback dashboard with existing login, Host/Origin and CSRF
    checks. Known key formats and forbidden control characters are refused; personal data,
    credentials, private session IDs and peer message bodies are excluded by the author.
    The daemon cannot reliably detect every form of personal data.
12. The UI follows the supplied design: responsive decision cards, count indicators,
    agent table, context meters, optional verdict note, disabled pending actions, inline
    errors, decided work and read-only reference lookup, system light/dark colors, and
    separate readable command boxes. It loads no remote resources.

## Constraints

- Linux and macOS, one CGO-free binary, existing SQLite store and storage limits.
- One same-user trust boundary (ADR 001, Decisions 1 and 2). An authenticated dashboard
  verdict is the maintainer's instruction and approval (PDR 005, Decision 3); the receiving
  agent applies its native permissions. A peer relay cannot create that origin. No further
  human-presence check is required by this deliverable (PDR 007, Decision 2).
- The verdict and its audit record commit atomically with durable recipient records.
  Refused or uncertain writes must be reconciled without inventing an accepted verdict.
  No new queue may consume capacity reserved for existing controls without a storage proof.
- Reads, pagination, archive lookup and wake delivery never acknowledge unrelated inbox
  messages or memory cursors. Unknown telemetry stays unknown, including after restart.
- Public plans, tests and fixtures use synthetic data (WDR 006, Decisions 1 and 2).
  The supplied private board is not
  imported, mirrored, published or contacted. Only its static UI design informs this work.
- Existing native identities and exact-session authentication govern recipient access;
  addresses are display/resolution inputs, never caller identity supplied by a model.
- No chat rooms (#217), context resets (#196), work-verdict replacement (#140), new agent
  roles, extra publishing workflow, installation, service changes or release promotion.
- Plan approval precedes implementation. Each implementation chunk is independently
  reviewable, passes its acceptance tests and the shared checks, and uses squash auto-merge
  to develop. The repository permits only one open PR per target branch.

## Framing status

The maintainer approved this decision (Gate A) on 2026-10-13 with the correction in
criterion 4 and the storage requirement in criterion 10
([record](https://github.com/rwcii/koinon/issues/206#issuecomment-6148016252)). The
definition of done, `sprint.md` and the chunks specify storage, delivery and snapshots.
Implementation waits for Gate B.
