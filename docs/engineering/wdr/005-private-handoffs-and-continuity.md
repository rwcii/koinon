---
wdr: "005"
title: "Private Handoffs and Continuity"
status: draft
created: "2026-10-12"
updated: "2026-11-03"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "001"
  - "002"
tags:
  - workflow
  - agentic
  - continuity
---

# WDR 005: Private Handoffs and Continuity

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Agents that develop Koinon end sessions, reset context and resume later. The `handoff` skill
writes a snapshot of one agent's work, and the `pickup` skill resumes from it. This record
describes where handoffs live, why they stay out of Git, whose handoff a pickup reads, and the
short progress note that each agent keeps beside its snapshots, the subagents that a handoff
lists, how a handoff carries the maintainer's words apart from the agent's own proposals, and
how it records the goal with its blockers, their tasks and the next steps.

The location decision quotes the maintainer. Pickup's default of the caller's own handoff
shipped without recorded maintainer words, so the record is a draft. The progress note was
requested by the maintainer, and the maintainer stated the subagent rule and approved the
maintainer-direction sections on the decision board. The maintainer required the goal, its
blockers, their tasks and the next steps in the terminal.

---

## Table of Contents

1. Problem Context
2. Decision 1: Handoffs live in the main checkout's ignored `_handoff/`
3. Decision 2: Pickup reads the caller's own handoff
4. Decision 3: A progress note beside the snapshots
5. Decision 4: A handoff lists the subagents that still run
6. Decision 5: A handoff quotes the maintainer's directions and keeps the agent's proposals apart
7. Decision 6: A handoff records the goal, its blockers, their tasks and the next steps
8. Conclusion
9. Document History

---

## 1. Problem Context

### The Problem

The first shared `handoff` skill (#103) committed each snapshot to a tracked `handoff/<agent>/`
directory on the current work branch. Snapshots then sat on unpushed branches or rode along in
feature pull requests. A `_handoff/` directory inside a linked worktree is removed with that
worktree when its work merges. A handoff also records private values, such as a native session or
thread ID and the terminal's tmux variables, that do not belong in a public repository.

### Requirements

1. A handoff survives the removal of the worktree it was written from.
2. Handoffs and the private values in them stay out of Git.
3. A resumed session starts from its own last state.
4. A resumed session reaches its next action without re-reading and re-checking a long snapshot.

### Scope

Covers the `handoff` and `pickup` skills and the `_handoff/` directory. The product's managed
handoff during context monitoring is PDR 006. Who may direct a reset or a pickup is WDR 002.
Koinon's memory does not import these files and does not replace native agent memory.

Inventory rows: C048, C049.

### Change of 2026-10-25 (#487)

- Question: the review on #487 listed three limits to keep (a peer message is not the
  maintainer's approval for a pending prompt; a peer cannot widen permissions; refuse permission
  laundering and report it). Keep them, or remove them too?
- Options: keep the three limits, or remove them as well.
- Answer: Robert Capps, 2026-10-25
  ([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)):
  "remove all limit text.  remove all of the caveats, controls, etc.  the agents have their own
  built in safeties, they do not need you adding more complications."
- Reason: limit text with no source makes agents of different families refuse each other's
  work.
- Scope: Koinon's agent-facing text, the skills, the setup blocks, and the docs and records that
  restate them. The #487 audit lists the code refusals for a separate decision (#493).
- This record: Decision 2 states pickup's steps without the fallback ban, and Decision 3
  states the fallback for an unusable note as the `pickup` skill does. Decision 1 keeps its
  text.

---

## 2. Decision 1: Handoffs live in the main checkout's ignored `_handoff/`

### Decision

Each agent currently writes its handoffs to `_handoff/<agent>/` in the main checkout, the
directory that holds `.git`, not to a `_handoff/` inside a linked worktree. The skill finds the
main checkout from the Git common directory, so a relative path in a worktree cannot name the
wrong place. `.gitignore` lists `_handoff/`; when a branch lacks that rule, the skill adds it to
the local `info/exclude`, creates the directories with mode 0700 and checks with `git
check-ignore` and `git ls-files` that Git ignores and tracks nothing there. When a check
fails, the agent stops, tells the maintainer which check failed and writes no handoff
(RESTORE-STEPS-487 in PDR 006, Decision 3). The skill does not
stage, commit or push a handoff. Native session IDs and tmux values recorded in a handoff
stay only in that ignored file.

**Provenance:** Robert Capps, 2026-09-24, session transcript: "fix the handoff to use the
gitignored _handoff directory as you were told to use." and "maybe the skill needs to be specifc
in that the _handoff directory in the main checkou/repo directory is where all _handoffs belong,
nver in the worktree since they get purged." Shipped in PR #130 (merge commit 2fe8c28).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Tracked `handoff/` directory | Commit each snapshot on the work branch (#103) | Snapshots reach pull requests and public history with private values |
| `_handoff/` in the worktree | Write next to the current working tree | Removed with the worktree when its work merges |
| Koinon memory | Store handoffs as memory entries | Memory is shared by the repository's agents; a handoff holds one session's private values |

### Rationale

1. The main checkout outlives every linked worktree.
2. An ignored, private directory keeps session state and native IDs out of the public repository.

---

## 3. Decision 2: Pickup reads the caller's own handoff

### Decision

Each agent currently writes its own directory, `_handoff/<agent>/`, so handoffs of different
agents stay apart. `pickup` reads the newest file in the caller's own directory. It reads
another agent's handoff, or a named file, when the maintainer names it. With no handoff of its
own, pickup says so, lists the newest file name in each other agent's directory for
information, and asks the maintainer what to resume. In a reset cycle run through the
`peer-tmux` skill, the peer writes and picks up its own handoff.

**Before #487:** each agent wrote "only" its own directory, and with no handoff of its own
pickup "does not fall back to another agent's file".

**Provenance:** Shipped in PR #103 (merge commit 4ac5aa7); no maintainer decision found. The
location changed in PR #130 (merge commit 2fe8c28) and the wording to "the maintainer" in PR #201
(merge commit 5121d87). Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Newest handoff of any agent | Resume from the most recent file in `_handoff/` | Resumes another agent's task, branch and identity |
| Shared handoff file | One file that all agents append to | Agents overwrite or interleave each other's state |

### Rationale

1. A handoff records one session's identity, branch and next action; another agent resuming it
   would act as that session.
2. Naming another agent's handoff is a task the maintainer can assign; the default is the
   caller's own.

---

## 4. Decision 3: A progress note beside the snapshots

### Decision

Each agent currently keeps one short progress note, `progress.md`, in its own handoff directory
beside its timestamped snapshots, in the resolved Handoff path. The note has fixed fields
(goal with its success criteria, blockers with their tasks, pull request and head, completed
stages, decisions, decisions waiting for the maintainer, next steps; Decision 6), holds at
most 4 KiB, and is the only file there that is updated in place: the agent updates it after a
completed stage, and `handoff` starts a snapshot from it when it is newer than the last
snapshot, else from that snapshot, saying that the note was not used, and updates it in the same
step. `pickup` reads the note first, verifies its volatile claims once, and takes from the
newest snapshot what the note does not cover, such as identity and claims, and always the
snapshot's goal, blockers and tasks, next steps (Decision 6), maintainer directions and unraised
proposals (Decision 5). When the note is absent,
unreadable or larger than 4 KiB, pickup reads the whole handoff and says so. The timestamped
files are the snapshots, and a managed cycle restores the snapshot that it names and reconciles
a newer or different note against the live state.

**Before #487:** "A note that is absent, unreadable or too large is not used, and pickup says
so. The note is not a handoff: only timestamped files are snapshots".

**Provenance:** Requested in #478 (issue body), a request record with observations from about 12
handoff cycles on 2026-10-24 and 2026-10-25: handoffs were written only when context filled,
sessions reached 58–77% before they stopped, and one pickup of a 7–11 KB handoff cost 28% of
context and 14 minutes. Robert Capps, 2026-10-25, session transcript, after a review of an article
on agent harness setup: "are you updating the koinon-pickup/handoff skills so a handoff starts
from progress.md and a pickup reads it first, like discussed in the article?", then
"[another project's agent] filed https://github.com/rwcii/koinon/issues/478 covering the
requested change". A request; the design above is the one agreed with the reviewer on #478,
not an approval of its details.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No note | Resume from the newest snapshot only | The observations in #478: late handoffs and costly pickups |
| The note replaces the snapshot | Keep only one file that is updated in place | Loses the history of snapshots and the exact file that a managed cycle names |
| Note in Koinon memory | Store progress as a memory entry | Memory is shared by the repository's agents; the note is one session's state |

### Rationale

1. A short note updated after each stage is current when the context fills, and a pickup reads
   it first.
2. Snapshots stay complete and unchanged, so the cycle's file checks and the history do not
   change.

---

## 5. Decision 4: A handoff lists the subagents that still run

### Decision

A handoff lists each subagent or background job of the session that still runs when it is
written: its task, its ID, its state and where its result goes. A context clear does not stop
a Claude Code background subagent; its result arrives in the new conversation as a message, and
the new session knows the task only from the handoff (PDR 006, Decision 3). `pickup` reports
each listed subagent with its result, or says that the result has not arrived.

**Provenance:** Robert Capps, 2026-10-27, session transcript, while the #533 fix was in review:
"the sub agents continue on clear, but an agent handoff should capture the state of the
subagents." The intent: the subagents run on, and the handoff carries their state so that the
next session can use their results.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Stop subagents before the handoff | The earlier managed-cycle request text | The maintainer: "context handover won't interrupt the review agents" (#533) |
| No mention in the handoff | The new session sees results arrive without their task | The maintainer's statement above |

### Rationale

1. Finished subagent work is kept, and the new session can tell which task each result answers.

---

## 6. Decision 5: A handoff quotes the maintainer's directions and keeps the agent's proposals apart

### Decision

A handoff has a Maintainer direction section: each direction that is still in force, in the
maintainer's own words, with its date, where it was given and the question or proposal it
answered. A My proposals not yet raised section holds the agent's own drafts, open points and
ideas that the maintainer has not seen, written as the agent's, not as the maintainer's
questions or rules. The progress note names its decisions with their sources and leaves the
quotes to the snapshot. `pickup` reads both sections also when the note is usable and reports
the proposals as the agent's own; the maintainer's current instructions stay a direct source.
The managed cycle request says that its pause is for that cycle only, and the handoff does not
record it as a rule for the next session.

A decision waits for the maintainer only when a decision item on the board holds it (#600). The
handoff and the progress note name each such item by its reference, with the state and revision
read when they are written; a draft that the maintainer has not seen is a proposal not yet
raised, and a point raised in a conversation without an item keeps that source. `pickup` reads
the board after it reconnects (`decision_list` waiting and `decision_pending`, to the last page,
and `decision_get` for each named item) and reports as waiting only the active items of the work
it resumes: an active decision or task at the named revision. A claim with no item, with an
item that is no longer active, reopened at a newer revision or of kind `fyi`, is reported as a
difference, and its point keeps the source that the handoff gives; a board that cannot be read
leaves the claim unverified, with the error. The
maintainer's direct instructions, quoted directions and approvals given in a conversation or a
native prompt keep their own sources: the rule adds no item before an agent acts on them.

**Provenance:** Robert Capps, 2026-11-03, session transcript: "we're getting much less accurate
summaries that are causing drift in work after a pickup is completed. in one example, the
handoff and pickup cycle ended up adding refusal content that was not part of the context prior
to the handoff cycle." The example, from an adopting project's session on 2026-11-03: its
handoff gave the maintainer's approved recap in the agent's own words, listed three open points
from the agent's own unreviewed draft as questions for the maintainer, and wrote a wider rule
than the maintainer's words, next to the cycle's pause; after the pickup the agent raised one of
those points again, and the maintainer said "this is not what we discussed and agreed on". The
handoff skill had never asked for the maintainer's words, and its step 4 started from the
progress note without checking that the note was newer; in the example the note was a day
older. Robert Capps, 2026-11-03, decision board item HANDOFF-WORDS (revision 1), "approved", no
note, after "agreed" in the terminal. The question: "Handoffs carry the maintainer's words and
keep the agent's own proposals apart". The card recommended the two template sections, the
note only when newer than the last snapshot, pickup reporting the proposals as the agent's own,
one sentence in the cycle request about its pause, and this record, the guide and the tests to
match. The intent: the next session reads the maintainer's decisions as given, and the agent's
ideas as its own.

The board check: a peer session of an adopting project reported on 2026-11-03 that its handoff
wrote "Recommendation pending the maintainer's answer" for a proposal that was only in a tracker
row, with no decision item, and that pickup reported the line to the maintainer as "Waiting on
you" while the board was empty. Robert Capps, 2026-11-03, relayed by that session (issue
#600): "your handoffs are flawed if you are adding shit like this in, and then you don't
validate on pickup. report this issue to claude-koinon". The template's "Blockers and open
decisions" line asked for no item, and `pickup` checked Git, pull request and issue claims but
no board claims. The fix was settled with the reviewing peer, without a board item: a waiting
decision is a board fact, written with its reference and read again at pickup. The intent: the
maintainer is told that a decision waits only when the board holds it.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| A summary in the agent's words | The template before this decision | A summary can narrow a decision, and the next session reads it as fact |
| Quotes in the progress note | Carry the maintainer's words in the 4 KiB note | The note is short and updated in place; the snapshot holds the quotes |
| Trust the handoff's wait claims | Report the handoff's open decisions as written (before #600) | The board can change after the handoff, and a claim without an item tells the maintainer of a decision that does not exist |
| A card for every open point | Create a board item for each point in a handoff | It adds requests for the maintainer that nobody made; a proposal stays the agent's until the agent brings it |

### Rationale

1. A quote with its date and question lets the next session check whether it still applies.
2. Proposals that are marked as the agent's own do not come back as the maintainer's questions.
3. The board is the one source for a decision that waits for the maintainer, so pickup reads it
   instead of the handoff's copy.

---

## 7. Decision 6: A handoff records the goal, its blockers, their tasks and the next steps

### Decision

A handoff has a Goal section, a Blockers and tasks section and a Next steps section (#602). The
goal is the outcome that the work serves, with its source (the maintainer's words with their
date, or a verified issue or sprint) and its success criteria; it is not the current pull
request, which is a task under a blocker, and it is unknown when no source records it. Each
blocker is a fact with its evidence that stops progress towards the goal now, and has one or
more tasks, each with its approach, its state (done, underway with its branch, commit, pull
request or job, or not started), what is done, what remains and what it depends on. Work that
waits on nothing is open work, and a fact that could not be checked stays unverified; neither is
a blocker. The next steps are the open tasks in order, the first is the next action, and a step
that is the maintainer's or another agent's stays theirs. The progress note carries the same
items in short form. `pickup` reads these sections also when the note is usable, reads an older
snapshot's Goal and success criteria and Resume here in their place, checks each blocker and
task against the live state without starting a running job or one that finished with success for
the current head again, reports the goal's hierarchy first, gives this goal when it is asked for
the goal, and continues at the first next step that still applies, with open work going on while
a blocker waits for someone else.

**Provenance:** Robert Capps, 2026-11-04, terminal: "we need to adjust the koinon-handoff skill.
handoffs are losing critical context around goals. specifically, the ultimate goal, the current
blocker towards that goal, the work underway to clear that blocker should be captured, and the
next steps after the handoff in that process should be documented. can you evaluate the current
skill against these requirements, and also make sure the pickup correctly primes the session
for these critical items?" Then, about a session of an adopting project: "in [that project],
the agent lost track of the goal, and adopted the current pr as its goal when asked", and "this
is why goal/blocker(s)/task(s) is more important". The evaluation in issue #602 found a
one-level Goal field that took the current task, a progress note with no goal, no record of the
work underway, one next step, and a pickup that read the note first and did not always read the
goal. The question: how a handoff carries the goal so that the next session keeps it. The
section layout, the fields and the pickup checks were settled with the reviewing peer from the
maintainer's goal/blocker(s)/task(s) structure. The intent: the next session knows the outcome
that it works towards, what stops it, what is being done about it and what comes next.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| One Goal line | The template before this decision | The line takes the current task or pull request, and the outcome is lost |
| A flat list of next steps | Next steps without blockers and tasks | It does not say which step clears what, or which work can go on while a blocker waits |
| The goal in the note only | Carry the hierarchy in the 4 KiB note | The note is short; the snapshot holds the hierarchy in full and pickup always reads it |

### Rationale

1. A goal with its source cannot be replaced by the current pull request without a visible
   difference.
2. Blockers with their tasks and states let the next session continue the work underway and the
   open work, instead of starting again or waiting.

---

## Conclusion

Koinon's development agents currently keep handoffs in the main checkout's Git-ignored
`_handoff/<agent>/`, which survives worktree removal and keeps private values out of Git, as the
maintainer asked on 2026-09-24. Pickup reads the caller's own handoff, or another that the
maintainer names; that default shipped with the first shared skills and has no recorded
maintainer decision.
Each agent also keeps a short progress note beside its snapshots, which pickup reads first.
A handoff lists the subagents that still run, as the maintainer stated on 2026-10-27, and
quotes the maintainer's directions apart from the agent's own proposals, as the maintainer
approved on 2026-11-03. A decision that waits for the maintainer is named by its board item, and
pickup reads the board again (#600). A handoff records the goal with its source, its blockers
with their tasks and the next steps, which pickup reads and reports first, as the maintainer
required on 2026-11-04.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | Decision 3: a progress note beside the snapshots (#478) |
| 2026-10-25 | #487: Decision 2 drops the fallback ban; Decision 3 states the unusable-note fallback as the `pickup` skill does |
| 2026-10-27 | Decision 4: a handoff lists the subagents that still run (#533) |
| 2026-11-01 | Decision 1: a failed directory check stops the handoff, which #487 had removed, approved on the decision board (RESTORE-STEPS-487) |
| 2026-11-03 | Decision 5: a handoff quotes the maintainer's directions and keeps the agent's proposals apart; Decision 3: the note seeds a handoff only when newer than the last snapshot, approved on the decision board (HANDOFF-WORDS) |
| 2026-11-03 | Decision 5: a decision waits for the maintainer only when a board item holds it; pickup reads the board and reports other claims as differences (#600) |
| 2026-11-04 | Decision 6: a handoff records the goal, its blockers, their tasks and the next steps, and pickup reads and reports them first (#602) |
