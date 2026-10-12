---
pdr: "007"
title: "Human Decision Board"
status: draft
created: "2026-10-12"
updated: "2026-10-12"
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

The board is not built. Its plan (the unpublished #206 plan draft (d325228)) is a reviewed framing; the
technical chunks are not written and Gate B has not been given. The decisions below are what
#206 requests. #206 says its settled points "were settled with the requester"; that sentence
is agent-written and the requester's original words were not located.

---

## Table of Contents

1. Problem Context
2. Decision 1: A dashboard board of daemon-owned rows and agent-written items
3. Decision 2: Only a dashboard action records a verdict, and the text then freezes
4. Decision 3: Commands are shown in printable ASCII only
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
3. Human-only verdicts with a snapshot of what the human saw, frozen afterwards.
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
context, recommendation, status, kind, optional commands, `relayTo`). An `fyi` item has only
Dismiss, which sets `done` and is not an approval. Decided items move unchanged to a readable
archive. A waiting query lists open decisions with no verdict. The plan marks manual work as
an `fyi` item whose title starts with `ACTION FOR YOU`, taken from the existing board design,
and sorts those first.

**Provenance:** Requested in #206 (issue body, requirements 1, 2, 5, 6, 8, 10, 11, 12 and
the settled point on rows); a request record. Using the existing board's UI design:
#206 comment 6065771626, 2026-10-08: "let's also apply the current decision board skill UI
design to the dashboard." (posted from the maintainer's account without agent framing;
authorship not otherwise verified). The `ACTION FOR YOU` marker: Proposed in
the unpublished #206 plan draft (d325228); not decided.

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

## 3. Decision 2: Only a dashboard action records a verdict, and the text then freezes

### Decision

A verdict is `approved` or `declined`, with `verdictAt`, an optional note, an audit record,
and `verdictOn`: a copy of the title, context, recommendation and commands the human saw.
No agent interface (MCP, CLI or bearer API) writes a verdict field; only an authenticated
dashboard action does, with no further check under the same-user trust model. Item text can
change before the verdict and not after it; new facts make a new item that refers to the old
one. The plan adds that a concurrent edit cannot make a verdict apply to unseen text. This
covers the maintainer's verdicts on this board, not agent-to-agent work verdicts (PDR 004).

**Provenance:** Requested in #206 (issue body, requirements 3 and 4, and the settled point
on verdict writes); a request record, original words not located. The concurrent-edit
guarantee: Proposed in the unpublished #206 plan draft (d325228) (criterion 4); not decided.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Agents relay verdicts | A peer writes the verdict it was told | #206: the record is the authority, not a peer's relay |
| A separate human-identity check | Extra authentication beyond the dashboard login | #206 settled point: no further check under the same-user model |
| Editable after verdict | Update the item and keep the verdict | The verdict would no longer match the text the human saw |

### Rationale

1. A verdict written only from the dashboard, on frozen text, means what the human saw.

### User Impact

Approve and Decline exist only in the dashboard; agents read verdicts but cannot set them.
To change a decided item, an agent opens a new item that cites the old one.

---

## 4. Decision 3: Commands are shown in printable ASCII only

### Decision

Each command appears in its own copy box and holds printable ASCII only. The page does not
show an entry with a line break, a zero-width, bidirectional or look-alike character, so the
human pastes what they see. The plan rejects such commands on write, hides them on read, and
does not let a hidden command be approved as displayed. A valid command that needs non-ASCII
text cannot be shown in a command box under this rule.

**Provenance:** Requested in #206 (issue body, requirement 7); a request record. Reject on
write and hide on read: Proposed in the unpublished #206 plan draft (d325228) (criterion 6); not decided.

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

Each new verdict reaches each `relayTo` session once, with no loss and no duplicate. An item
without a valid `relayTo` is flagged. Each recipient has a durable delivery record with an
acknowledgement. The content-free wake notice carries the verdict ID; it can repeat after a
crash, and the receiver ignores an ID it has handled. Each agent reads the verdict record
itself through an interface every family can use. Delivery or acknowledgement does not show
that an action was done.

**Provenance:** Requested in #206 (issue body, requirement 9 and the settled point on
delivery); a request record. "Not proof that an action was performed": Proposed in
the unpublished #206 plan draft (d325228) (criterion 8); not decided.

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
verdicts, decided text stays frozen, commands are plain ASCII, and every family reads each
verdict once. The plan adds a manual-work marker and write-time rules; none is decided yet.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
