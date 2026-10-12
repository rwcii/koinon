---
wdr: "005"
title: "Private Handoffs and Continuity"
status: draft
created: "2026-10-12"
updated: "2026-10-12"
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
describes where handoffs live, why they stay out of Git, and whose handoff a pickup reads.

The location decision quotes the maintainer. The rule that pickup reads only the caller's own
handoff shipped without recorded maintainer words, so the record is a draft.

---

## Table of Contents

1. Problem Context
2. Decision 1: Handoffs live in the main checkout's ignored `_handoff/`
3. Decision 2: Pickup reads the caller's own handoff
4. Conclusion
5. Document History

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

### Scope

Covers the `handoff` and `pickup` skills and the `_handoff/` directory. The product's managed
handoff during context monitoring is PDR 006. Who may direct a reset or a pickup is WDR 002.
Koinon's memory does not import these files and does not replace native agent memory.

Inventory rows: C048, C049.

---

## 2. Decision 1: Handoffs live in the main checkout's ignored `_handoff/`

### Decision

Each agent currently writes its handoffs to `_handoff/<agent>/` in the main checkout, the
directory that holds `.git`, not to a `_handoff/` inside a linked worktree. The skill finds the
main checkout from the Git common directory, so a relative path in a worktree cannot name the
wrong place. `.gitignore` lists `_handoff/`; when a branch lacks that rule, the skill adds it to
the local `info/exclude`, creates the directories with mode 0700 and checks with `git
check-ignore` and `git ls-files` that Git ignores and tracks nothing there. The skill does not
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

Each agent currently writes only its own directory, `_handoff/<agent>/`, so handoffs of
different agents do not compete. `pickup` reads the newest file in the caller's own directory. It
reads another agent's handoff, or a named file, when the maintainer names it. With no handoff of
its own, pickup says so, lists the newest file name in each other agent's directory for
information, and asks the maintainer what to resume; it does not fall back to another agent's
file. In a reset cycle run through the `peer-tmux` skill, the peer writes and picks up its own
handoff.

**Provenance:** Shipped in PR #103 (merge commit 4ac5aa7); no maintainer decision found. The
location changed in PR #130 (merge commit 2fe8c28) and the wording to "the maintainer" in PR #201
(merge commit 5121d87).

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

## Conclusion

Koinon's development agents currently keep handoffs in the main checkout's Git-ignored
`_handoff/<agent>/`, which survives worktree removal and keeps private values out of Git, as the
maintainer asked on 2026-09-24. Pickup reads the caller's own handoff unless the maintainer names
another; that scope shipped with the first shared skills and has no recorded maintainer decision.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
