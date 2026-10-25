---
wdr: "003"
title: "Pairing and Independent Review"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "001"
  - "002"
  - "004"
tags:
  - workflow
  - agentic
---

# WDR 003: Pairing and Independent Review

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon is built by agent sessions that pair in one checkout and review each other's commits.
This record describes how that currently works: one driver changes the shared checkout while
the observer reads committed checkpoints, the pair trades roles at a committed increment, the
other agent family reviews a frozen commit and records the verdict, and each change is built in
its own worktree.

The driver and observer split comes from the maintainer's own words. The other choices shipped
in the shared skills without a recorded maintainer decision, so this record is a draft.

---

## Table of Contents

1. Problem Context
2. Decision 1: One driver, one observer
3. Decision 2: Trade roles at a committed increment
4. Decision 3: The other family reviews and records the verdict
5. Decision 4: Findings carry evidence and a triage
6. Decision 5: Each change has its own worktree
7. Conclusion
8. Document History

---

## 1. Problem Context

### The Problem

Two agent sessions working in one directory can overwrite each other's changes, and a reviewer
that reads a working tree while it changes reviews something that never gets committed. An
author that reports on its own work gives no independent check. Before PR #262, #255 and #260
merged before their peer review finished, because nothing on GitHub showed that the sign-off
was missing.

### Requirements

1. One session at a time changes the shared checkout.
2. A review covers a fixed commit, and its verdict names that commit.
3. The review is done by a session other than the author.
4. Review findings are bounded by evidence, so a review ends.

### Scope

Covers the pair roles, the review of a frozen commit and where change work happens. Branch
policy, required checks and merge order are in WDR 004. What a peer request can authorize is in
WDR 002. Inventory rows: C030, C031, C032, C033, C034.

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
- This record: Decisions 3, 4 and 5 state the review and worktree steps without their limits.
  Decisions 1 and 2 are unchanged.

---

## 2. Decision 1: One driver, one observer

### Decision

Koinon currently pairs in one shared checkout with one driver and one observer. Only the driver
edits, stages, commits or runs commands that change the checkout. The observer reads committed
checkpoints with `git show` or `git diff`, and reproduces findings in a separate scratch
worktree or temporary location. The observer sends findings together at the committed
checkpoint, and interrupts earlier only to stop an imminent destructive or security mistake.
This governs writes to the shared checkout; it does not stop the observer from running tests
in its own scratch location. The `pair` skill describes the steps.

**Provenance:** Maintainer, 2026-10-06, session transcript: "work in a pair programming
framework. share the checkout, with one driving, and one observing.  trade off from time to
time." Shipped in PR #177 (merge commit 0d0caed). An earlier statement, 2026-09-18: "you are
both active in the same directory, so you need to negotiate who has write access. i woudl
suggest delegating a driver and an observer role." (memory copy only; original not located).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Both sessions write | Each session edits the checkout when it needs to | Concurrent edits overwrite each other and leave no known state to review |
| Separate checkouts per session | Each session builds in its own worktree and they merge | Not what the maintainer asked for; the pair stops sharing one increment |
| Observer reads the live tree | Observer watches uncommitted changes | Reviews text that may never be committed; findings cannot name a hash |

### Rationale

1. The maintainer asked for a shared checkout with one session driving and one observing.
2. Committed checkpoints give both sessions the same fixed state to discuss.

---

## 3. Decision 2: Trade roles at a committed increment

### Decision

The pair currently trades the driver role at a committed increment whose checks pass, not by
elapsed time. The driver commits, sends `wheel to you at <hash>` with the check results, and
the new driver verifies the hash and a clean checkout before it edits. The pair agrees the
first driver and the next increment before the first edit.

**Provenance:** Shipped in PR #177 (merge commit 0d0caed); no maintainer decision found for the
hand-off point. The maintainer's 2026-10-06 words above ask the pair to "trade off from time to
time"; no source was found for the `pair` skill's sentence "Do not swap while fixes or checks
are unfinished" as a ban on an earlier, agreed swap.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Swap on a timer | Trade roles after a fixed time | The new driver can start from a broken, uncommitted state |
| Never swap | One session drives the whole task | The maintainer asked the pair to trade off |

### Rationale

1. A committed, tested hash is a known state that the new driver can verify.
2. The check results travel with the hand-off, so the new driver does not repeat them.

---

## 4. Decision 3: The other family reviews and records the verdict

### Decision

Koinon's pairs are of different agent families, and the partner family's session reviews the
change. The reviewer reads one frozen commit in a checkout of its own and sends its findings to
the author. It posts its verdict as a pull request comment that names the commit, and records
the `peer-review` commit status on that full hash, linked to the comment. A commit status
belongs to one commit, so each new head gets its own review; a follow-up review covers the
accepted fixes and the behavior they affect, and carries forward unchanged content.

Every agent acts under one GitHub account. GitHub does not let a pull request's author approve
it, so a formal approving review is not available; the comment is the durable verdict and the
commit status is what the `develop` ruleset checks (WDR 004). The status identifies the GitHub
user, not the agent family; the reviewer records it as part of the review.

**Before #487:** the sign-off was posted "When none is open", "The author does not set
`peer-review` on its own pull request", and "A new commit has no status until its own review".
The status "holds the merge until the review when every agent follows this practice; it does
not authenticate the reviewer."

**Provenance:** Shipped in PR #104 (merge commit e3b908f) for the other-family review and the
commit-bound sign-off comment, and in PR #262 (merge commit ce75054) for the `peer-review`
commit status; no maintainer decision found. Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| GitHub approving review | Require one approving review on the pull request | The single account authors every pull request and cannot approve its own |
| Comment only | Sign-off comment with no status | #255 and #260 merged before review; GitHub could not see the missing sign-off |
| Author records the status | Author sets `peer-review` after it reads the comment | Removes the separation between the author and the review |
| Separate GitHub accounts | One account per agent family | Not set up; the maintainer commits under one account (WDR 004) |

### Rationale

1. A verdict bound to one hash cannot be carried to a commit that nobody reviewed.
2. A required status makes a missing sign-off visible to GitHub before the merge.

---

## 5. Decision 4: Findings carry evidence and a triage

### Decision

A review currently reports each failure that a test or a command reproduces, or that the
committed code shows, with its scenario, its evidence, the requirement and a severity. The
author triages each finding as accepted or dropped, with a reason, and fixes the accepted ones.
A material disagreement goes to the maintainer. The reviewer records its verdict (Decision 3).

**Before #487:** the title was "Sign-off waits for accepted blockers". A review reported "only"
reproduced findings; "A blocker is a demonstrated, reachable failure of a written acceptance
criterion, or a concrete security or data-loss defect against the repository's boundaries";
the author triaged each finding "once"; the reviewer posted "the sign-off when no accepted
blocker is open"; and "A sign-off records a review and grants no permission beyond what the
maintainer already gave."

**Provenance:** Shipped in PR #104 (merge commit e3b908f) for the sign-off and the sentence
"Never approve with an open high-severity finding", and in PR #177 (merge commit 0d0caed) for
the blocker test and the single triage; no maintainer decision found. Changed by #487 (Problem
Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Severity label as the gate | Any finding labeled high blocks, whatever its evidence | A label without a reachable failure keeps reviews open on speculation |
| Blocker test before sign-off | The verdict waits until no accepted blocker is open | A gate with no maintainer source (#487) |

### Rationale

1. Findings with evidence let the author fix real failures and let the review end.
2. A triage with a reason tells the reviewer what changed and why.

---

## 6. Decision 5: Each change has its own worktree

### Decision

Koinon currently builds each change on a `feature/*`, `fix/*` or `chore/*` branch from
`develop`, in its own worktree. A pair shares that worktree, with one driver (Decision 1). A
reviewer reads the frozen commit in a checkout of its own. A fix to an open pull request
continues on that pull request's branch and worktree.

**Before #487:** the worktree was one "that no unrelated work writes to", and the driver was
"its only writer".

**Provenance:** Shipped in PR #104 (merge commit e3b908f) for "a worktree that no other agent
writes to", and in PR #207 (merge commit 10ea1e4) for "in an isolated worktree" in the root
`AGENTS.md`; no maintainer decision found. Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Branch switching in one checkout | All work shares the main checkout and switches branches | A switch by one task changes the files under another |
| One worktree per session | Each session, paired or not, has its own worktree | Conflicts with the shared checkout that pairing uses |

### Rationale

1. Several tasks and sessions run in one repository at once; each task has its own files in
   its own worktree.
2. The pair shares one worktree, so both partners work on the same increment.

---

## Conclusion

Koinon currently pairs with one driver per checkout, trades roles at a committed and checked
increment, and has the other agent family review a frozen commit, with the verdict recorded as
a commit-bound comment and a `peer-review` status. Findings carry evidence, and the author
triages each one with a reason. The driver and observer split is the maintainer's choice; the
rest shipped in the shared skills, and #487 removed their limits.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | #487: Decisions 3, 4 and 5 state the review and worktree steps without their limits |
