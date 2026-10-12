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

## Acceptance criteria

1. The board shows daemon-owned agent name, observed busy/idle/unknown state, current
   claimed work, context used and limit or “not reported”, and waiting decision refs.
   Agents cannot write these rows. Rows have no role, group or agent-defined order.
2. Agents can create and update decision items with ref, item order, title, where, agent
   text, context, recommendation, open/done status, decision/fyi kind, optional commands,
   and named recipients. Unknown or missing recipients are visibly flagged.
3. Only an authenticated dashboard action can approve or decline, with its time, optional
   note, audit record and exact snapshot of the title, context, recommendation and commands
   the human saw. MCP, CLI and bearer API cannot write verdict fields.
4. Item text is immutable after a verdict. New facts require a new item referring to the
   earlier one. A concurrent edit cannot make a verdict apply to unseen text.
5. An fyi item offers Dismiss only. Dismissal records done and its time, and grants no
   approval. Manual work sorts before other items.
6. Each command appears in its own copy box and contains printable ASCII only. Line breaks,
   zero-width, bidirectional and non-ASCII look-alike characters are rejected on write and
   hidden if encountered on read. Hidden commands cannot be approved as displayed.
7. Context and recommendation retain visible line breaks. All other author text is inert
   and escaped; no displayed content becomes code or terminal input.
8. Each accepted verdict has a durable delivery record for each valid recipient. A crash
   cannot lose one; repeated content-free wakes carry the same verdict ID. Recipients read
   the actual record and acknowledge after handling it, ignoring an already handled ID.
   Delivery or acknowledgement is not proof that an action was performed.
9. Decided items enter an archive unchanged and remain readable. The current list stays
   bounded and readable. Current and archived records can be found by reference, read-only.
10. A complete paged waiting query returns exactly open decision items without a verdict,
    excluding fyi, decided and archived items. Every supported agent family can use it and
    read its own verdict records through its existing authenticated interface.
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
- One same-user trust boundary. Dashboard origin records provenance; it does not override
  the receiving agent's native permissions or turn a peer relay into maintainer authority.
  No additional human-identity service is part of this deliverable.
- The verdict and its audit record commit atomically with durable recipient records.
  Refused or uncertain writes must be reconciled without inventing an accepted verdict.
  No new queue may consume capacity reserved for existing controls without a storage proof.
- Reads, pagination, archive lookup and wake delivery never acknowledge unrelated inbox
  messages or memory cursors. Unknown telemetry stays unknown, including after restart.
- Public plans, tests and fixtures use synthetic data. The supplied private board is not
  imported, mirrored, published or contacted. Only its static UI design informs this work.
- Existing native identities and exact-session authentication govern recipient access;
  addresses are display/resolution inputs, never caller identity supplied by a model.
- No chat rooms (#217), context resets (#196), work-verdict replacement (#140), new agent
  roles, extra publishing workflow, installation, service changes or release promotion.
- Plan approval precedes implementation. Each implementation chunk is independently
  reviewable, passes its acceptance tests and the shared checks, and uses squash auto-merge
  to develop. The repository permits only one open PR per target branch.

## Framing status

This decision restates the issue's settled requirements and the supplied UI reference.
The native delivery, bounded storage and exact verdict snapshot specifications remain to
be written and reviewed. No implementation or new permission is implied by this document.
