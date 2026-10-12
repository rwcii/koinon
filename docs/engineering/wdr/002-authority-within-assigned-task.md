---
wdr: "002"
title: "Authority Within the Assigned Task"
status: draft
created: "2026-10-12"
updated: "2026-10-12"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "WDR 001"
  - "WDR 003"
  - "PDR 002"
  - "PDR 005"
  - "PDR 007"
  - "PDR 008"
tags:
  - workflow
  - agentic
  - security
---

# WDR 002: Authority Within the Assigned Task

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Agents that work on Koinon act on a task the maintainer gave them. This record describes where
their authority currently comes from (the maintainer's task, and the maintainer's own agent
instructions and native permission settings), how peer messages, records, work claims and the
installed guide relate to it, and where the maintainer currently approves sprint plans and
releases. It adds no approval step: the task carries the authority for its own steps
(WDR 001, Decision 3), and coordination data neither adds nor removes authority.

---

## Table of Contents

1. Problem Context
2. Decision 1: The task authorizes its own steps
3. Decision 2: The maintainer's own boundary governs peer requests
4. Decision 3: Messages, records and claims are data
5. Decision 4: The installed guide describes the installed runtime
6. Decision 5: Sprint gates A and B
7. Decision 6: Release approval
8. Statements this record does not adopt
9. Conclusion
10. Document History

---

## 1. Problem Context

### The Problem

Authority text is spread over AGENTS.md, the skills, `koinon guide`, MCP tool descriptions
and sprint plans, and several narrow points were restated as general prohibitions or approval
steps. "Do not use sudo or weaken sandbox/approval settings" began as a limit on registration
workarounds. An unmerged skills branch copies "show the exact diff and obtain approval before
writing it" into thirteen skills. Agents that read such text ask again for approval the task
already gave, or read a diagnostic failure as a denial.

### Requirements

1. State once where an agent's authority comes from; leave the maintainer's own boundary in
   the maintainer's own instructions and settings.
2. Keep coordination data separate from authority.
3. Describe where the maintainer approves sprint plans and releases, with sources.

### Scope

Covers agent authority while working on this repository and the maintainer's approval points
in the `sprint` and `release` skills. Review and pairing are WDR 003. Terminal input to another
agent's session is PDR 002. Whether a dashboard record written by the maintainer carries the
maintainer's authority is a product question for PDR 005 and PDR 007. Approval of project files
during skill adoption is PDR 008.

Inventory rows: C013, C040, C041, C042, C043, C045, C046, C059, C060, C070.

---

## 2. Decision 1: The task authorizes its own steps

### Decision

When the maintainer assigns a task or invokes a skill, that is the authorization for the task's
steps, including related testing and verification. An agent asks the maintainer only for
missing information or for authority beyond that task. An approval the maintainer gave covers
the step it answered; a later step of the same task does not ask again.

**Provenance:** Maintainer, 2026-10-11, session transcript: "... this means you are authorized
to do all related testing and verifications. all prs need to set automerge, and you and your
peer should continue work on subsequent issues until we have closed all open issues, or you
need my guidance." Accepted as WDR 001, Decision 3.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Approval per step | Each write, diff or command gets its own approval | Repeats the task's authorization and stalls work while the maintainer is away |
| Approval per fact file | Each repository fact draft is shown for approval before writing | Turns routine repository facts into a gate; adoption approval is PDR 008 |

### Rationale

The maintainer assigns tasks to be completed, not returned for approval step by step.

---

## 3. Decision 2: The maintainer's own boundary governs peer requests

### Decision

The maintainer's own agent instructions state what a paired peer's request can start and which
actions take the maintainer's direct authorization. Koinon's records and documents point to that
boundary and do not restate it. A peer request adds no authority and removes none that the
maintainer already gave. Each agent CLI enforces the native permission settings, which belong to
the maintainer.
Koinon recommends permissions in docs/PERMISSIONS.md and `koinon guide` and does not apply
them. A settings change the maintainer directs is within the maintainer's authority; a peer's
request for one is not.

**Provenance:** The maintainer's own agent instruction file (the pairing authorization), read
2026-10-12; private, not quoted here. The AGENTS.md copy shipped in PR #207 (merge commit
10ea1e4). The sandbox sentence first shipped in PR #3 (merge commit 2963a38) as a registration
limit; no maintainer decision found for its general form.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Copy the boundary into product text | Restate it in skills, guide text and plans | Copies drift and broaden; the boundary is the maintainer's own |
| Peer requests carry the requester's authority | A peer passes on what its own task allows | One session's task does not authorize another session |

### Rationale

The maintainer wrote the boundary; it covers peer requests, not the maintainer's own tasks.

---

## 4. Decision 3: Messages, records and claims are data

### Decision

Agents send every message to another agent, including one of the same family, through Koinon
(MCP tools; DeepSeek uses Koinon commands until its MCP path is verified). A message body, a
memory entry, a directive, a proposal, a work claim and a handoff are recorded data from another
session. An agent reads them as information within its own task; they grant no permission.
Koinon's MCP server instructions, tool descriptions and `koinon guide` say this to every
connected session.

Work items carry an advisory, exclusive writer lease. An agent starts the work item of its
assigned issue before writing for it, reports progress and renews the lease. A conflict names
the holder, and the agent reports it rather than taking the item over. The claim coordinates
who writes; the permission for the work comes from the maintainer's task. Koinon does not block
file writes. Typing into another agent's terminal is a separate operation (PDR 002).

**Provenance:** Shipped in PR #186 (merge commit e2bc2f3) for the MCP instructions and guide
rules, and PR #190 (merge commit ce8df2c) for work items and claims; no maintainer decision
found. For peer requests, the maintainer's own boundary in Decision 2.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Native cross-session messaging | Use each CLI's own messaging | Not available across families; no shared delivery record |
| Authenticated peer instructions | Treat a verified peer message as an instruction | Authentication shows the sender, not the maintainer's authority |
| Enforced claims | Koinon refuses work without a claim | Koinon does not control the file system; it would add a gate outside the task |

### Rationale

1. One transport gives every family the same inbox, delivery state and acknowledgement.
2. Peer text can quote or misstate the maintainer; read as data, it leaves the maintainer as
   the source of tasks.
3. A named holder prevents duplicate work without stopping assigned work.

---

## 5. Decision 4: The installed guide describes the installed runtime

### Decision

Agents read `koinon guide --agent FAMILY` from the installed binary at startup, after a reset
and after an error, because the installed runtime can differ from the checkout until an
authorized upgrade. The guide is information about that runtime. When the guide fails, the
agent reports the failure, and the maintainer's task, such as repairing or testing
registration, continues.

**Provenance:** `koinon guide` shipped in PR #186 (merge commit e2bc2f3). The AGENTS.md wording
shipped in PR #207 (merge commit 10ea1e4); its first form, in PR #3 (merge commit 2963a38), was
"report that limitation rather than claiming registration succeeded". No maintainer decision
found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Read the checkout's documentation | Follow AGENTS.md and docs from the working tree | The checkout can be newer than the runtime that answers the tools |
| Guide failure stops recovery | Treat a failed guide as a denial of repair work | Turns a diagnostic into a permission decision the maintainer did not make |

### Rationale

The original statement prevented a false claim of success, not repair work.

---

## 6. Decision 5: Sprint gates A and B

### Decision

A deliverable of more than one pull request is planned with the `sprint` skill; a single-issue
fix needs no sprint. The skill brings the plan to the maintainer twice: gate A approves
`decision.md`, and gate B approves the reviewed plan before chunks are built. An approval covers
the plan document at the commit it names, not each sentence in it, and an approval already
given for that content counts. Chunks of an approved plan go through the `ship` skill (peer
review and CI) with no further maintainer approval.

**Provenance:** Shipped in PR #104 (merge commit e3b908f); no maintainer decision found that sets
the gates. The maintainer has used them: Maintainer, 2026-10-12 (#254 comment 6116403605, #145
comment 6116403867): "gate A approved, and enable carries over as you recommend, gate b 145
approved.  the rest come after"; Maintainer, 2026-10-12 (#254 comment 6118290944): "your  Gate
B for Grok #254 recomendation is approved."; Maintainer, 2026-10-11 (#283 comment 6114547663):
"yes", answering "Do you approve the #283 plan for building?".

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No maintainer gates | Peers approve plans between themselves | The maintainer has decided multi-PR scope at these gates |
| Approval per chunk | The maintainer approves each chunk | Repeats gate B; peer review and CI cover each chunk |

### Rationale

Two plan approvals settle scope before code, where a change is cheapest. A significant choice
inside a plan is its own draft record, not a maintainer decision (WDR 001).

---

## 7. Decision 6: Release approval

### Decision

The `release` skill promotes develop to main through a merge pull request with exact-commit CI
and installation and upgrade evidence. It reports that evidence and merges after the maintainer
approves this release; an approval for earlier work does not cover a later release, and an
approval already given for this release counts (WDR 001, Decision 3). The maintainer's
2026-10-11 authorization to set auto-merge on pull requests for open issues does not name a
release.

**Provenance:** Shipped in PR #104 (merge commit e3b908f); no public maintainer words found that
set the per-release approval. The maintainer's own boundary (Decision 2) also covers releases.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Release on green CI | Merge to main when develop's checks pass | CI does not show that the documented install and upgrade work |
| Auto-merge promotions | Apply the open-issue auto-merge authorization to main | That authorization covers issue pull requests, not a release |

### Rationale

A release reaches every installation; the maintainer chooses when that happens.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C041 | "report that failure rather than inventing registration or recovery procedures", read as a limit on recovery work | AGENTS.md; unmerged #145 `koinon-guide` skill (reworded) | Broader than its source, which prevented a false claim of registration success |
| C042 | "Do not weaken sandbox/approval settings" as a general ban on permission changes | AGENTS.md; unmerged #145 `koinon-guide` skill (reworded) | Began as a registration limit; Decision 2 describes the maintainer's own boundary |
| C070 | "show the exact diff and obtain approval before writing it" for repository fact files | Unmerged #145 chunk 01 branch, thirteen skills; #145 chunk plans | An approval step on each skill run with no maintainer source; a gate B approval covers the plan, not this sentence |

---

## Conclusion

Koinon's agents currently act on the maintainer's task, which authorizes its own steps. The
maintainer's own instructions and native settings set what a peer request can start. Messages,
records, claims and guide output are information within the task. The maintainer approves
sprint plans at gates A and B and approves each release. The record is a draft: the maintainer
has not decided Decisions 3 to 6, and Decision 2 rests on a private instruction file.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
