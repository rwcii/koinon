---
wdr: "005"
title: "Private Handoffs and Continuity"
status: draft
created: "2026-10-12"
updated: "2026-10-27"
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
short progress note that each agent keeps beside its snapshots, and the subagents that a handoff
lists.

The location decision quotes the maintainer. Pickup's default of the caller's own handoff
shipped without recorded maintainer words, so the record is a draft. The progress note was
requested by the maintainer, and the maintainer stated the subagent rule.

---

## Table of Contents

1. Problem Context
2. Decision 1: Handoffs live in the main checkout's ignored `_handoff/`
3. Decision 2: Pickup reads the caller's own handoff
4. Decision 3: A progress note beside the snapshots
5. Decision 4: A handoff lists the subagents that still run
6. Conclusion
7. Document History

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
- Answer: Maintainer, 2026-10-25
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
([DECISIONS.md#RESTORE-STEPS-487](../../../DECISIONS.md#restore-steps-487)). The skill does not
stage, commit or push a handoff. Native session IDs and tmux values recorded in a handoff
stay only in that ignored file.

**Provenance:** Maintainer, 2026-09-24, session transcript: "fix the handoff to use the
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
(task, pull request and head, completed stages, decisions, open issues, next action), holds at
most 4 KiB, and is the only file there that is updated in place: the agent updates it after a
completed stage, and `handoff` starts a snapshot from it and updates it in the same step.
`pickup` reads the note first, verifies its volatile claims once, and takes from the newest
snapshot what the note does not cover, such as identity and claims. When the note is absent,
unreadable or larger than 4 KiB, pickup reads the whole handoff and says so. The timestamped
files are the snapshots, and a managed cycle restores the snapshot that it names and reconciles
a newer or different note against the live state.

**Before #487:** "A note that is absent, unreadable or too large is not used, and pickup says
so. The note is not a handoff: only timestamped files are snapshots".

**Provenance:** Requested in #478 (issue body), a request record with observations from about 12
handoff cycles on 2026-10-24 and 2026-10-25: handoffs were written only when context filled,
sessions reached 58–77% before they stopped, and one pickup of a 7–11 KB handoff cost 28% of
context and 14 minutes. Maintainer, 2026-10-25, session transcript, after a review of an article
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

**Provenance:** Maintainer, 2026-10-27, session transcript, while the #533 fix was in review:
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

## Conclusion

Koinon's development agents currently keep handoffs in the main checkout's Git-ignored
`_handoff/<agent>/`, which survives worktree removal and keeps private values out of Git, as the
maintainer asked on 2026-09-24. Pickup reads the caller's own handoff, or another that the
maintainer names; that default shipped with the first shared skills and has no recorded
maintainer decision.
Each agent also keeps a short progress note beside its snapshots, which pickup reads first.
A handoff lists the subagents that still run, as the maintainer stated on 2026-10-27.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | Decision 3: a progress note beside the snapshots (#478) |
| 2026-10-25 | #487: Decision 2 drops the fallback ban; Decision 3 states the unusable-note fallback as the `pickup` skill does |
| 2026-10-27 | Decision 4: a handoff lists the subagents that still run (#533) |
| 2026-11-01 | Decision 1: a failed directory check stops the handoff, which #487 had removed, approved on the decision board (RESTORE-STEPS-487) |
