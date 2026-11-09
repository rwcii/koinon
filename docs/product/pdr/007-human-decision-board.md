---
pdr: "007"
title: "Human Decision Board"
status: draft
created: "2026-10-12"
updated: "2026-11-09"
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

The board is built (#206; PRs #353, #358 and #360). Its plan
(`docs/sprints/2026-10-12-decision-board/`) was reviewed, and the maintainer approved it (Gate B)
on 2026-10-13 (#206 comment 6150911355). The decisions below are what
#206 requests. #206 says its settled points "were settled with the requester"; that sentence
is agent-written and the requester's original words were not located.

---

## Table of Contents

1. Problem Context
2. Decision 1: A dashboard board of daemon-owned rows and agent-written items
3. Decision 2: Only a maintainer action records a verdict; a later text change reopens the item
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
6. Loopback dashboard only (#206); since PDR 009, also a maintainer client through `koinon mcp
   --maintainer`. No credentials, keys, personal data, session IDs or peer bodies.
7. Since #537: feedback on a decision item, Acknowledge on an `fyi` item, and a task kind for
   the maintainer's own work, which waits with Completed, Declined and Provide feedback.

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
active (waiting for a verdict), returned (the maintainer's feedback is back with the author;
#537), in progress (approved; the agent does the work and closes it), archive (closed,
declined, completed, acknowledged, dismissed or withdrawn; readable by reference) and deep
archive (after 30 days, moved to a log file; nothing is deleted, and it can be restored). An
agent withdraws an active or returned item that no longer needs the maintainer (the work is done, a newer item
supersedes it, it was answered in chat, or it is no longer valid): the item's author, a session
of the author's participant or a named recipient, at the revision it read, with a reason. The
withdrawal is not a verdict; the item keeps it in its history, and a restore reopens the item. The board does
not fail at a fixed number of items. An item is one of three kinds (#537): a `decision` item,
a `task` (work that the maintainer must do) and an `fyi` item (information). A waiting query
lists active decision items and tasks, and the board shows tasks first. An `fyi` item has only
Acknowledge, which archives it, tells its recipients that the maintainer read it, and is not an
approval; an fyi that needs an answer is a decision item or a task. The `ACTION FOR YOU` title
marker on an `fyi` item, from the existing board design, is retired: the upgrade makes each open
one a task. Card text (context, recommendation and comments) is rendered from
Markdown, code and commands appear in code blocks, and agents write card prose in ASD-STE100
Simplified Technical English. Agents use one card form, which the guide's Decisions section
gives (#562): the title is the action; the context is short facts, each with its source and
date; the recommendation has the heading "Recommendation:", then What, Why, Fast or complete,
Fix loop and On track, in that order, then, each only when it has relevant data, Corrections from the peer,
Open disagreements, Other options and A separate choice, which you can add to any option
(#582). Fix loop starts with "yes" or "no", then the number of fix rounds on the deliverable so
far and the references of its earlier cards; "yes" when the card asks for another fix, retry or
exception because an earlier fix of the same deliverable did not close it. On track starts with
"on track" or "off track", then the task, goal or deliverable that the work belongs to and where
it was assigned or approved; "off track" when the card asks for, or the agent already started,
work that nobody assigned or approved, and the card then names that work, says that nobody
approved it and where the agent looked for an approval. On track is about scope, not delay
(CARD-LOOP-SCOPE, #638). ASD-STE100 applies to the sentences, and the form stays.
The tool field text and the command usage give the form at each write, and the daemon checks the
text of each `decision` item that an agent creates or edits. The reply has `form_notes` when the
context is not a list or is over 2500 characters, when the heading or a label is missing or out
of order, when Fix loop or On track does not start with one of its answers, or when the
recommendation is over 3000 characters. A note refuses nothing and changes
no state; the author corrects the card with `decision_edit` (CARD-FORM-AT-WRITE-635, #635).

**Provenance:** Requested in #206 (issue body, requirements 1, 2, 5, 6, 8, 10, 11, 12 and
the settled point on rows); a request record. Agent withdrawal: requested in #504 (issue body,
2026-10-26: stale cards "pile up", and "The maintainer had to sort them by hand"); Robert Capps,
2026-10-26: "resolve 504 and 505". Intent: the board shows only what still needs the
maintainer, and an agent removes a card it no longer needs without moving that work back to
the maintainer. Kinds and actions: Robert Capps, 2026-10-27: "the decision board needs an option to approve, decline, or 'provide feedback'. provide feedback providing guidance where approve or decline would be misleading. the FYI also is not very useful as it stands. I get tasks placed here, where I have to 'dismiss' is the only option to close or acknowledge an issue. valid options should be acknowledge. tasks should be in the waiting category with, completed, decline, or 'provide feedback' as options." ([#537](https://github.com/rwcii/koinon/issues/537));
the design, with the answers to its four questions, approved on the decision board
(BOARD-VERDICTS-537). The answers: feedback returns the card to its author ("agreed, your
recommendation is correct"); a task kind replaces the "ACTION FOR YOU" prefix ("I agree");
Acknowledge on an fyi tells the author ("yes"); the other uses keep their current behavior, and
the full design goes on one card ("agreed"). Intent: each
action says what the maintainer means; work for the maintainer waits as a task with its own
actions, and an fyi item is acknowledged, not dismissed. Using the existing board's UI design:
#206 comment 6065771626, 2026-10-08: "let's also apply the current decision board skill UI
design to the dashboard." (posted from the maintainer's account without agent framing;
authorship not otherwise verified). Card text: Robert Capps, 2026-10-12: "card comments should be
formattable in markdown. code should be displayed in code blocks so it's readable, and all
prose should be in asd-ste100" (#206 comment 6119203856). Card form: Robert Capps, 2026-10-29,
relayed by a session of another project: "we need to add some guidance to agents using koinon, not leave it up
to the agent to make it up." (#562); the form, with its guide text, approved on the decision
board (CARD-FORM-562). Context: the guide gave no card form, and the cards of another
project changed their headings and sentence form between revisions. The other option, a catalog
skill for the form, was not chosen: a second copy can drift. Intent: every agent
writes a card in the same form, so the maintainer reads each card the same way. Labels after
Fast or complete: approved on the decision board, Robert Capps, 2026-10-31, with the note "add
them as standard options, but they don't all need to be present in ALL cards if there is no
relevant data." (CARD-LABELS-582, #582).
Intent: each card gives these parts under the same labels, and a card leaves out a label that
has nothing to say. Card form at the write:
- **Decided by:** Robert Capps, 2026-11-07, on the decision board, card CARD-FORM-AT-WRITE-635,
  revision 3, "approved", with no note.
- **Context:** cards had drifted from the form, and the form text had not changed. The form was
  only in the guide, which a session reads once and can lose at a compaction. Robert Capps: "the
  point of koinon is to allow agents to run semi-autonomously. I won't be there to prompt it to
  re-read every time it compacts." The question: how the form reaches an author with no prompt.
  The card's separate choice, a message after each compaction to read the guide again, was not
  taken, so its default applies: no message.
- **Decision:** the form goes into the tool field text and the command usage, and the replies
  carry `form_notes`.
- **Reason and scope:** the tool text and the reply come with each write, in each family. The two
  sizes come from the cards with a list context since 2026-10-31: 75% have a context under 2280
  characters and a recommendation under 3114. A note does not refuse, under "remove all limit
  text" (#487), so an agent can leave a note unanswered. Scope: `decision` items; tasks and fyi
  items get no notes.
- **Review when:** agents leave notes unanswered and cards drift again.

Fix loop and On track:
- **Decided by:** Robert Capps, 2026-11-09, on the decision board, card CARD-LOOP-SCOPE,
  revision 2, "approved", with no note.
- **Context:** Robert Capps: "I see claude consistently get stuck in an infinite fix loop." Many
  cards on a local board asked for one more fix, retry or exception after earlier fixes of the
  same deliverable. On the meaning of on track: "I'm mostly concerned that the agent is working
  on something that is not on its current set of tasks/goals/deliverables, or if it has decided
  on its own to take on additional unsanctioned/unapproved work and is trying to justify it
  through opaquely worded decision records. While I'd love to know if it's delayed in meeting its
  goals, that's not my intention for the on track question." The question: how a card shows a
  fix loop and work outside the assigned or approved scope. Not taken: a dashboard highlight of
  the answers.
- **Decision:** two required labels after Fast or complete, Fix loop and On track, each starting
  with a fixed answer, and `form_notes` for a missing label or an answer that is not one of them.
- **Reason and scope:** each card states both facts in a fixed place, where the maintainer sees
  them first. The answers are the author's statement: the daemon checks that they are present and
  in the fixed words, not that they are true. A note refuses nothing. Existing cards do not
  change. Scope: `decision` items.

The `ACTION FOR YOU` marker: Proposed in the #206 plan; approved with the plan at Gate B (#206 comment 6150911355); retired by #537. Lifecycle: Robert Capps, 2026-10-13:
"we don't build systems that fail at some arbitrary limit. closed items should be archived.
after 30 days, archived items should be moved to a log file in case an old decision needs to
be resurrected. nothing gets deleted, but the lifecycle becomes active > in progress >
archive > deep archive" (#206 comment 6150321526); the meaning of each state was confirmed
in #206 comment 6150362521. Intent: the board keeps working as it grows, and no decision is
lost.

**Provenance (spoken alert, SPEAK-SCOPE-553):**

- **Decided by:** Robert Capps, on the decision board (revision 1), "approved".
- **Date:** 2026-10-28.
- **Context:** #553. Robert Capps asked: "can we make it distinct per project?", and chose that
  the speech runs in the browser. The alert speaks when a decision or task starts to wait, and
  names its repository. The question: one switch for all repositories, or one for each.
- **Decision:** one switch on the Settings page for every repository on that Koinon instance;
  the browser keeps it. The note: "One switch for all repositories running on that Koinon
  instance. We may change this later, but let's keep it simple for now."
- **Reason and scope:** each alert names its repository, so it is distinct for each project; a
  switch for each repository adds a list to maintain. The decision covers the switch only.
- **Review when:** Robert Capps wants to mute one repository.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Keep the single-family board | Relay verdicts by hand | The problem #206 names |
| A title marker for manual work | `ACTION FOR YOU` on an `fyi` item (the #206 plan) | Retired by #537: an `fyi` item has one action, and the maintainer's work needs Completed, Declined and Provide feedback |

### Rationale

1. The dashboard already serves every family and has login, CSRF and audit.

### User Impact

The maintainer sees all agents and open decisions on one page in the dashboard.

---

## 3. Decision 2: Only a maintainer action records a verdict; a later text change reopens the item

### Decision

A verdict is `approved`, `declined` or `feedback` on a decision item, `completed`, `declined`
or `feedback` on a task, and `acknowledged` on an `fyi` item (#537); an earlier `dismissed`
verdict on an `fyi` item stays in the history. It has its time (`at`; #206 calls it
`verdictAt`), a note, an audit record, and a `snapshot` (#206's `verdictOn`): a copy of the
title, context, recommendation and commands the human saw. The note is optional, except for
feedback, where it is the maintainer's guidance. Approve moves the item to in progress.
Feedback approves and declines nothing: it returns the item to its author, who changes it into
a new revision, which waits for a new verdict, or withdraws it; an item can get feedback more
than once. Each other verdict archives the item.
No agent interface (MCP, CLI or bearer API) writes a verdict field. A maintainer action does:
an authenticated dashboard action, or a `decision_verdict` call of a maintainer client through
`koinon mcp --maintainer` with its maintainer token (PDR 009, Decisions 3 and 4), with no
further check under the same-user trust model (PDR 005, Decision 3). Each verdict records its
client as `via`: `dashboard` or `mcp:NAME`. Decided text
stays unchanged in ordinary use. When the facts around a decision change, the text can change:
the change reopens the item for a new verdict, and the record keeps every earlier verdict with
the text it was given on. A concurrent edit cannot make a verdict apply to unseen text. This
covers the maintainer's verdicts on this board, not agent-to-agent work verdicts (PDR 004).

**Provenance:** Requested in #206 (issue body, requirements 3 and 4, and the settled point
on verdict writes); a request record, original words not located. The concurrent-edit
guarantee: Proposed in the #206 plan (criterion 4); approved with the plan at Gate B (#206 comment 6150911355).
Text changes after a verdict: Robert Capps, 2026-10-13, on the statement "A decision's text
can't change after your verdict.": "it can, if the facts surrounding the decision dictate a
change.  but in general use, they do not." Asked whether the item then reopens for a new
verdict: "yes, reopen it for a new verdict.  history of decisions is carried on the record."
(#206 comments 6148016252 and 6148194337). Intent: a verdict always matches the text the
maintainer saw, and the history stays on the record. The verdicts of each kind and the return
of an item with feedback: Robert Capps, 2026-10-27, on #537 (quoted in Decision 1), and the
answer "agreed, your recommendation is correct" on the state after feedback
(BOARD-VERDICTS-537). Intent:
feedback hands the item back to its author for a revision; it is not a verdict that closes the
question.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Agents relay verdicts | A peer writes the verdict it was told | #206: the record is the authority, not a peer's relay |
| A separate human-identity check | Extra authentication beyond the dashboard login | #206 settled point: no further check under the same-user model |
| Editable after verdict, verdict kept | Update the item and keep the verdict | The verdict would no longer match the text the human saw |
| Frozen after verdict | New facts need a new item | Robert Capps, 2026-10-13: the text can change when the facts change |

### Rationale

1. A verdict written only by the maintainer, in the dashboard or a maintainer client (PDR 009),
   on the revision the human saw, means what the human saw; a later change asks for a new
   verdict.

### User Impact

The verdict actions exist only for the maintainer, in the dashboard or a maintainer client
(PDR 009); agents read verdicts but cannot set them.
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

**Provenance:** Requested in #206 (issue body, requirement 7). Robert Capps, 2026-10-12: "yes,
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

Each new verdict, an `fyi` acknowledgement included, reaches each named recipient (#206's
`relayTo`) and the item's author (#537) once, with no loss and no
duplicate record; the notice can repeat. A recipient that does not resolve is flagged, and
its delivery is recorded as skipped with the reason. Each recipient has a durable delivery record with an
acknowledgement. The content-free wake notice carries the verdict ID; it can repeat after a
crash, and the receiver ignores an ID it has handled. Each agent reads the verdict record
itself through an interface every family can use. The records are stored data that agents
query in bounded pages through Koinon; agents get decision status from those queries, not
by reading or parsing the dashboard HTML. A reopen does not cancel an unacknowledged delivery.
A read of a decided item, the pending list and the verdict notice also tell the agent how to
record the decision, other than an `fyi` acknowledgement: in the ADR, PDR, WDR or sprint
document that holds the rule, in the pull request that does the work, with the decider's name,
the date, the context, the decision, the reason and scope, and the condition for a review
(#623; WDR 001, Decision 7). Koinon writes no record file. The item read gives the verdict with
its card text. `koinon decision export PATH` writes every item, live and deep-archived, as JSON
lines to a new private file outside a Git work tree, which a person or CI can read without the
daemon (#589). Repository text names a decision by its reference, never by its verdict ID,
which exists only in this machine's store. How a project cites its decisions is that project's
own rule; Koinon's own repository follows WDR 001, Decision 7 (#580).

**Provenance:** Requested in #206 (issue body, requirement 9 and the settled point on
delivery); a request record. Stored, queryable
records: Robert Capps, 2026-10-13: "are agents reading the HTML, or are the decision records a
dataset they can query efficiently?  I ask, because this was an issue in the original
incarnation, the page grew unbounded and each agent had to pull down a full html file and parse
it to get status of decisions." (#206 comment 6148016252). Intent: an agent's cost to read
decision status does not grow with the board.

Before #487 this decision also said "Delivery or acknowledgement does not show that an action
was done.", from the plan's criterion 8 ("Not proof that an action was performed"), which Gate B
approved with the plan (#206 comment 6150911355); no maintainer words on it were found. The
#487 review listed three limits and proposed to keep them; the question was whether to keep
them or to remove them too. Robert Capps, 2026-10-25: "remove all limit text.  remove all of the
caveats, controls, etc.  the agents have their own built in safeties, they do not need you
adding more complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit lists the code
refusals for a separate decision (#493). The command boxes (Decision 3) and ASD-STE100 card prose
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
| 2026-10-26 | Decision 2, requirement 6, Rationale and User Impact: a maintainer client also records a verdict, with its `via`, as accepted PDR 009 Decisions 3 and 4 decide (#470) |
| 2026-10-26 | Decision 1: agents withdraw active items with a reason, and a restore reopens them (#504) |
| 2026-10-28 | Decision 4: a verdict read asks the agent to record the verdict in the repository's record file (#536; WDR 001, Decision 7) |
| 2026-10-28 | Decisions 1, 2 and 4, requirement 7: the task kind, feedback that returns an item to its author, and Acknowledge on an `fyi` item, delivered to the recipients; the `ACTION FOR YOU` marker is retired (#537; BOARD-VERDICTS-537) |
| 2026-10-29 | Decision 1: the card form in the guide's Decisions section (#562; CARD-FORM-562) |
| 2026-10-31 | Decision 1: the card form's labels after Fast or complete (#582; CARD-LABELS-582) |
| 2026-10-31 | Decision 4: the product text names a decision by its reference and leaves the citation rule to the project (#580) |
| 2026-11-03 | Decision 4: `decision_record` applies the repository's record mode, and the board has an export (#589, RECORD-MODE-589; WDR 001, Decision 7) |
| 2026-11-05 | Decision 4: a verdict read gives the record form; Koinon writes no record file, and `decision_record` is removed (#623; WDR 001, Decision 7) |
| 2026-11-05 | Decision 1: the spoken-alert decision (SPEAK-SCOPE-553), moved into this record (#623) |
| 2026-11-07 | Decision 1: the card form at each write, and `form_notes` in the replies (#635, CARD-FORM-AT-WRITE-635) |
| 2026-11-09 | Decision 1: the card labels Fix loop and On track (#638, CARD-LOOP-SCOPE) |
| 2026-11-09 | Executive summary: the board is built (#353, #358, #360) |
