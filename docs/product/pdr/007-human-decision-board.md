---
pdr: "007"
title: "Human Decision Board"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "004"
  - "005"
tags:
  - ux
  - flow
  - content
  - notifications
---

# PDR 007: Human Decision Board

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

The maintainer currently approves or declines agent decisions on a board that only one agent
family can read. Issue #206 asks for a
decision board in the Koinon dashboard: agents create and update decision items, only the
human records a verdict in the dashboard, and every family reads the verdict record itself.

The board is not built. Its plan (`docs/sprints/2026-10-12-decision-board/`) is reviewed,
and the maintainer approved it (Gate B) on 2026-10-13 (#206 comment 6150911355). The decisions below are what
#206 requests. #206 says its settled points "were settled with the requester"; that sentence
is agent-written and the requester's original words were not located.

---

## Table of Contents

1. Problem Context
2. Decision 1: A dashboard board of daemon-owned rows and agent-written items
3. Decision 2: Only a dashboard action records a verdict; a later text change reopens the item
4. Decision 3: Command code blocks hold printable ASCII only
5. Decision 4: Each recipient reads the verdict record once
6. Conclusion
7. Document History

---

## 1. Problem Context

### The Problem

A verdict on the current board reaches one family only; the maintainer repeats it in other
agents' terminals, or an agent relays it and may copy it wrong.

### Product Requirements

From #206's twelve requirements, in short:

1. Daemon-filled agent rows: name, busy/idle/unknown, work in progress, context used and
   limit or "not reported", and the decision refs each agent waits on.
2. Agent-written decision items, `fyi` items with Dismiss only, manual work sorted first,
   line breaks shown in context and recommendation.
3. Human-only verdicts with a snapshot of what the human saw.
4. Printable-ASCII command boxes.
5. Delivery of each verdict to each named recipient once, an archive, and a waiting query.
6. Loopback dashboard only; no credentials, keys, personal data, session IDs or peer bodies.

### Scope

Covers the board's items, verdicts and delivery. Inventory rows: C079, C080, C081. It does
not cover agent-to-agent work verdicts (PDR 004) or dashboard rooms (PDR 005); how agents
are told that dashboard input comes from the maintainer is in PDR 005.

---

## 2. Decision 1: A dashboard board of daemon-owned rows and agent-written items

### Decision

The board lives on the loopback dashboard. The daemon fills the agent rows from its own
session, claim and context data; agents do not write them, and the rows have no role, group
or order. Agents create and update decision items (ref, order, title, where, agent text,
context, recommendation, kind, optional commands, `relayTo`). An item follows the lifecycle
active (waiting for a verdict), in progress (approved; the agent does the work and closes
it), archive (closed, declined or dismissed; readable by reference) and deep archive (after
30 days, moved to a log file; nothing is deleted, and it can be restored). The board does
not fail at a fixed number of items. An `fyi` item has only Dismiss, which archives it and
is not an approval. A waiting query lists active decision items. The plan marks manual work as
an `fyi` item whose title starts with `ACTION FOR YOU`, taken from the existing board design,
and sorts those first. Card text (context, recommendation and comments) is rendered from
Markdown, code and commands appear in code blocks, and agents write card prose in ASD-STE100
Simplified Technical English.

**Provenance:** Requested in #206 (issue body, requirements 1, 2, 5, 6, 8, 10, 11, 12 and
the settled point on rows); a request record. Using the existing board's UI design:
#206 comment 6065771626, 2026-10-08: "let's also apply the current decision board skill UI
design to the dashboard." (posted from the maintainer's account without agent framing;
authorship not otherwise verified). Card text: Maintainer, 2026-10-12: "card comments should be
formattable in markdown. code should be displayed in code blocks so it's readable, and all
prose should be in asd-ste100" (#206 comment 6119203856). The `ACTION FOR YOU` marker: Proposed in the #206 plan; approved with the plan at Gate B (#206 comment 6150911355). Lifecycle: Maintainer, 2026-10-13:
"we don't build systems that fail at some arbitrary limit. closed items should be archived.
after 30 days, archived items should be moved to a log file in case an old decision needs to
be resurrected. nothing gets deleted, but the lifecycle becomes active > in progress >
archive > deep archive" (#206 comment 6150321526); the meaning of each state was confirmed
in #206 comment 6150362521. Intent: the board keeps working as it grows, and no decision is
lost.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Keep the single-family board | Relay verdicts by hand | The problem #206 names |
| A new manual-work field | Add a field for manual items | The plan reuses the existing design's title marker instead |

### Rationale

1. The dashboard already serves every family and has login, CSRF and audit.

### User Impact

The maintainer sees all agents and open decisions on one page in the dashboard.

---

## 3. Decision 2: Only a dashboard action records a verdict; a later text change reopens the item

### Decision

A verdict is `approved` or `declined` (an `fyi` item is `dismissed`), with its time
(`at`; #206 calls it `verdictAt`), an optional note, an audit record, and a `snapshot`
(#206's `verdictOn`): a copy of the title, context, recommendation and commands the human
saw. Approve moves the item to in progress; Decline and Dismiss archive it.
No agent interface (MCP, CLI or bearer API) writes a verdict field; only an authenticated
dashboard action does, with no further check under the same-user trust model (PDR 005,
Decision 3). Decided text
stays unchanged in ordinary use. When the facts around a decision change, the text can change:
the change reopens the item for a new verdict, and the record keeps every earlier verdict with
the text it was given on. A concurrent edit cannot make a verdict apply to unseen text. This
covers the maintainer's verdicts on this board, not agent-to-agent work verdicts (PDR 004).

**Provenance:** Requested in #206 (issue body, requirements 3 and 4, and the settled point
on verdict writes); a request record, original words not located. The concurrent-edit
guarantee: Proposed in the #206 plan (criterion 4); approved with the plan at Gate B (#206 comment 6150911355).
Text changes after a verdict: Maintainer, 2026-10-13, on the statement "A decision's text
can't change after your verdict.": "it can, if the facts surrounding the decision dictate a
change.  but in general use, they do not." Asked whether the item then reopens for a new
verdict: "yes, reopen it for a new verdict.  history of decisions is carried on the record."
(#206 comments 6148016252 and 6148194337). Intent: a verdict always matches the text the
maintainer saw, and the history stays on the record.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Agents relay verdicts | A peer writes the verdict it was told | #206: the record is the authority, not a peer's relay |
| A separate human-identity check | Extra authentication beyond the dashboard login | #206 settled point: no further check under the same-user model |
| Editable after verdict, verdict kept | Update the item and keep the verdict | The verdict would no longer match the text the human saw |
| Frozen after verdict | New facts need a new item | Maintainer, 2026-10-13: the text can change when the facts change |

### Rationale

1. A verdict written only from the dashboard, on the revision the human saw, means what the
   human saw; a later change asks for a new verdict.

### User Impact

Approve and Decline exist only in the dashboard; agents read verdicts but cannot set them.
When the facts change, an agent edits the decided item; it returns to the board for a new
verdict, and the earlier verdict stays in its history.

---

## 4. Decision 3: Command code blocks hold printable ASCII only

### Decision

Each command appears in its own code block with a copy action and holds printable ASCII only.
Code blocks in card text that are shown only to read are not limited by this rule.
The page does not show an entry with a zero-width, bidirectional or look-alike character, so
the human pastes what they see. The plan rejects such commands on write, hides them on read, and
does not let a hidden command be approved as displayed. A valid command that needs non-ASCII
text cannot be shown in a command box under this rule.

**Provenance:** Requested in #206 (issue body, requirement 7). Maintainer, 2026-10-12: "yes,
next item", keeping the printable-ASCII check for code blocks that the maintainer copies into a
terminal ([#306 comment 6119251724](https://github.com/rwcii/koinon/issues/306#issuecomment-6119251724)). Reject on write and hide on read: Proposed in the #206 plan (criterion 6); approved with the plan at Gate B (#206 comment 6150911355).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Show any Unicode | Display commands as written | Invisible or look-alike characters can make a pasted command differ from the shown one |
| Mark suspicious characters | Show the command with warnings | #206 asks that such entries not be shown |

### Rationale

1. What the human copies is what the human read.

### User Impact

Command boxes show plain ASCII. A command with other characters is refused when written.

---

## 5. Decision 4: Each recipient reads the verdict record once

### Decision

Each new verdict reaches each named recipient (#206's `relayTo`) once, with no loss and no
duplicate record; the notice can repeat. A recipient that does not resolve is flagged, and
its delivery is recorded as skipped with the reason. Each recipient has a durable delivery record with an
acknowledgement. The content-free wake notice carries the verdict ID; it can repeat after a
crash, and the receiver ignores an ID it has handled. Each agent reads the verdict record
itself through an interface every family can use. The records are stored data that agents
query in bounded pages through Koinon; agents get decision status from those queries, not
by reading or parsing the dashboard HTML. A reopen does not cancel an unacknowledged delivery.

**Provenance:** Requested in #206 (issue body, requirement 9 and the settled point on
delivery); a request record. Stored, queryable
records: Maintainer, 2026-10-13: "are agents reading the HTML, or are the decision records a
dataset they can query efficiently?  I ask, because this was an issue in the original
incarnation, the page grew unbounded and each agent had to pull down a full html file and parse
it to get status of decisions." (#206 comment 6148016252). Intent: an agent's cost to read
decision status does not grow with the board.

Before #487 this decision also said "Delivery or acknowledgement does not show that an action
was done.", from the plan's criterion 8 ("Not proof that an action was performed"), which Gate B
approved with the plan (#206 comment 6150911355); no maintainer words on it were found. The
#487 review listed three limits and proposed to keep them; the question was whether to keep
them or to remove them too. Maintainer, 2026-10-25: "remove all limit text.  remove all of the
caveats, controls, etc.  the agents have their own built in safeties, they do not need you
adding more complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them; code
refusals are a separate decision. The command boxes (Decision 3) and ASD-STE100 card prose
(Decision 1) keep their sources and stay.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Wake only | Send a notice and no stored record | A lost wake loses the verdict |
| Peer relay | One agent forwards the verdict | #206: the record is the authority |

### Rationale

1. A stored per-recipient record with an acknowledgement survives crashes and repeats.

### User Impact

The maintainer decides once; each named agent receives the verdict without a terminal relay.

---

## Conclusion

#206 asks for one board in the dashboard where agents write items, only the human records
verdicts, and every family reads each verdict once. A change to decided text reopens the item
for a new verdict, and agents query stored records, never the page. The maintainer
decided that card text is Markdown with code blocks and ASD-STE100 prose, and that command code
blocks, which the maintainer copies into a terminal, hold printable ASCII only. The plan's manual-work marker and write-time rules were approved with the plan at Gate B.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's card text requirement and the ASCII decision for command code blocks (#206, #306) |
| 2026-10-13 | Recorded the maintainer's reopen-on-change decision and the stored-query requirement (#206) |
| 2026-10-25 | Decision 4: removed the unsourced "does not show that an action was done" caveat (#487) |
