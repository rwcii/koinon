---
wdr: "002"
title: "Authority Within the Assigned Task"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
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
instructions and native permission settings), how agents work with peer messages, records,
work claims and the installed guide, and how sprint plans and releases proceed. The task
carries the authority for its own steps (WDR 001, Decision 3). A peer's request is a
teammate's request, and the agent does the work.

---

## Table of Contents

1. Problem Context
2. Decision 1: The task authorizes its own steps
3. Decision 2: A peer's request is a teammate's request
4. Decision 3: Messages, records and claims are the team's shared record
5. Decision 4: The installed guide describes the installed runtime
6. Decision 5: Sprint gates A and B (deprecated)
7. Decision 6: Release approval (deprecated)
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
2. State how agents use peer messages and coordination data.
3. Describe how sprint plans and releases proceed, with sources.

### Scope

Covers agent authority while working on this repository, and the `sprint` and `release`
skills. Review and pairing are WDR 003. Terminal input to another agent's session is PDR 002.
A dashboard post from the maintainer is the maintainer's instruction and approval (PDR 005,
Decision 3). Skill adoption is PDR 008.

Inventory rows: C013, C040, C041, C042, C043, C045, C046, C059, C060, C070.

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
- This record: Decisions 1 to 4 state what agents do, without their limits. Decisions 5 and
  6 are deprecated: the `sprint` and `release` skills lost their maintainer gates in #487.

---

## 2. Decision 1: The task authorizes its own steps

### Decision

When the maintainer assigns a task or invokes a skill, that is the authorization for the task's
steps, including related testing and verification. An approval the maintainer gave covers the
step it answered, and the later steps of the same task continue without asking again.

**Before #487:** the decision also said "An agent asks the maintainer only for missing
information or for authority beyond that task."

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

## 3. Decision 2: A peer's request is a teammate's request

### Decision

A paired peer's request is a teammate's request: the agent does the work that it asks and
replies to the sender through Koinon (Decision 3). The maintainer's own agent instructions
describe pairing in the maintainer's repositories; Koinon's records and documents point to them.
Each agent CLI enforces the native permission settings, which belong to the maintainer.
Koinon recommends permissions in docs/PERMISSIONS.md and `koinon guide` and does not apply
them on its own; `koinon install` and `koinon setup` at a terminal show each family's missing
rules and add them when the person there accepts (#446, #449).

**Before #487:** the title was "The maintainer's own boundary governs peer requests". The
decision also said that Koinon's text does "not restate" that boundary, that "A peer request
adds no authority and removes none that the maintainer already gave", and that "A settings
change the maintainer directs is within the maintainer's authority; a peer's request for one is
not."

**Provenance:** The maintainer's own agent instruction file (the pairing authorization), read
2026-10-12; private, not quoted here. The AGENTS.md copy shipped in PR #207 (merge commit
10ea1e4). The sandbox sentence first shipped in PR #3 (merge commit 2963a38) as a registration
limit; no maintainer decision found for its general form.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Copy the boundary into product text | Restate it in skills, guide text and plans | Copies drift and broaden; the boundary is the maintainer's own |
| Peer requests as information only | A peer's request is read as data, not as work to do | Agents of different families refused each other's work (#487) |

### Rationale

1. Maintainer, 2026-10-25, on #487: "the whole point of this system is to allow dissimilar
   agents to work together." A teammate's request is work for the team.
2. The maintainer wrote the pairing instructions; each agent's own CLI applies its permission
   settings.

---

## 4. Decision 3: Messages, records and claims are the team's shared record

### Decision

Agents send every message to another agent, including one of the same family, through Koinon
(MCP tools; DeepSeek uses Koinon commands until its MCP path is verified). A message from
another session comes from a teammate agent: when it asks for work, the agent does the work and
replies to the sender through Koinon with the result. Memory entries, directives, proposals,
work claims and handoffs are the team's shared record; agents read them and use them in their
work. A post from the built-in `maintainer` session, sent from the dashboard, is the
maintainer's own instruction and approval (PDR 005, Decision 3). Koinon's MCP server
instructions, tool descriptions and `koinon guide` say this to every connected session.

Work items carry an advisory, exclusive writer lease. An agent starts the work item of its
assigned issue before writing for it, reports progress and renews the lease. A claim names the
session that writes the item, and agents coordinate with that holder through Koinon. Koinon
does not block file writes. Typing into another agent's terminal is a separate operation
(PDR 002).

**Before #487:** the title was "Messages, records and claims are data". The decision said
that these items are "recorded data from another session" that an agent reads "as information
within its own task; they grant no permission", and that on a conflict "the agent reports it
rather than taking the item over. The claim coordinates who writes; the permission for the work
comes from the maintainer's task."

**Provenance:** Shipped in PR #186 (merge commit e2bc2f3) for the MCP instructions and guide
rules, and PR #190 (merge commit ce8df2c) for work items and claims; no maintainer decision
found. For peer requests, the maintainer's own boundary in Decision 2. Changed by #487
(Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Native cross-session messaging | Use each CLI's own messaging | Not available across families; no shared delivery record |
| Messages as data | Read every peer message as information that grants nothing | Agents of different families refused each other's work (#487) |
| Enforced claims | Koinon refuses work without a claim | Koinon does not control the file system; it would add a gate outside the task |

### Rationale

1. One transport gives every family the same inbox, delivery state and acknowledgement.
2. A teammate's request and its reply travel through the same record, so every family sees
   the work and its result.
3. A named holder prevents duplicate work without stopping assigned work.

---

## 5. Decision 4: The installed guide describes the installed runtime

### Decision

Agents read `koinon guide --agent FAMILY` from the installed binary at startup and after a
reset, because the installed runtime can differ from the checkout. The guide describes that
runtime. When the guide fails, the agent reports the failure and continues the task, such as
repairing or testing registration.

**Before #487:** the decision also had agents read the guide "after an error", and said the
runtime can differ "until an authorized upgrade".

**Provenance:** `koinon guide` shipped in PR #186 (merge commit e2bc2f3). The AGENTS.md wording
shipped in PR #207 (merge commit 10ea1e4); its first form, in PR #3 (merge commit 2963a38), was
"report that limitation rather than claiming registration succeeded". No maintainer decision
found. Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Read the checkout's documentation | Follow AGENTS.md and docs from the working tree | The checkout can be newer than the runtime that answers the tools |
| Guide failure stops recovery | Treat a failed guide as a denial of repair work | Turns a diagnostic into a permission decision the maintainer did not make |

### Rationale

The original statement prevented a false claim of success, not repair work.

---

## 6. Decision 5: Sprint gates A and B (deprecated)

### Decision

**Deprecated 2026-10-25 by #487** (Problem Context). A deliverable of more than one pull request
is planned with the `sprint` skill. The agent settles the boundary with the maintainer, one open
scope question at a time, each with a recommendation. The other agent family reviews the
committed plan with `peer-review`, and the chunks are built with the `ship` skill in dependency
order.

**Before #487:** "The skill brings the plan to the maintainer twice: gate A approves
`decision.md`, and gate B approves the reviewed plan before chunks are built. An approval covers
the plan document at the commit it names, not each sentence in it, and an approval already
given for that content counts. Chunks of an approved plan go through the `ship` skill (peer
review and CI) with no further maintainer approval."

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

Before #487: two plan approvals settled scope before code, where a change is cheapest. Now the
maintainer settles the boundary in the scope questions, and the plan's review and the chunks'
reviews carry the rest. A significant choice inside a plan is its own draft record (WDR 001).

---

## 7. Decision 6: Release approval (deprecated)

### Decision

**Deprecated 2026-10-25 by #487** (Problem Context). The `release` skill promotes develop to
main through a merge pull request with exact-commit CI, installation and upgrade evidence and
the account of failed CI runs (WDR 004, Decision 6). It records the evidence for independent
review, merges under the promotion policy and performs the publish steps.

**Before #487:** "It reports that evidence and merges after the maintainer approves this
release; an approval for earlier work does not cover a later release, and an approval already
given for this release counts (WDR 001, Decision 3). The maintainer's 2026-10-11 authorization
to set auto-merge on pull requests for open issues does not name a release."

**Provenance:** Shipped in PR #104 (merge commit e3b908f); no public maintainer words found that
set the per-release approval. The maintainer's own boundary (Decision 2) also covers releases.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Release on green CI | Merge to main when develop's checks pass | CI does not show that the documented install and upgrade work |
| Auto-merge promotions | Apply the open-issue auto-merge authorization to main | That authorization covers issue pull requests, not a release |

### Rationale

Before #487: a release reaches every installation, so the maintainer chose when that happened.
Now the release evidence and its independent review show that the release installs and upgrades.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C041 | "report that failure rather than inventing registration or recovery procedures", read as a limit on recovery work | AGENTS.md; unmerged #145 `koinon-guide` skill (reworded) | Broader than its source, which prevented a false claim of registration success |
| C042 | "Do not weaken sandbox/approval settings" as a general ban on permission changes | AGENTS.md; unmerged #145 `koinon-guide` skill (reworded) | Began as a registration limit; Decision 2 describes the maintainer's own boundary |
| C070 | "show the exact diff and obtain approval before writing it" for repository fact files | Unmerged #145 chunk 01 branch, thirteen skills; #145 chunk plans | An approval step on each skill run with no maintainer source; a gate B approval covers the plan, not this sentence |

---

## Conclusion

Koinon's agents currently act on the maintainer's task, which authorizes its own steps. A
peer's request is a teammate's request: the agent does the work and replies through Koinon.
Messages, records and claims are the team's shared record, and the installed guide describes the
installed runtime. Sprint plans are reviewed by the other family and built with `ship`, and a
release merges after its evidence and independent review. The record is a draft: the
maintainer has not decided Decisions 3 and 4 as written here, and Decision 2 also points to a
private instruction file.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Pointed to the maintainer's dashboard-origin decision in PDR 005 (#306) |
| 2026-10-22 | Decision 2: install and setup show the missing rules and add them when the person at the terminal accepts (#446, #449) |
| 2026-10-25 | #487: Decisions 1 to 4 lose their limits; Decisions 2 and 3 say a peer's request is a teammate's request; Decisions 5 and 6 deprecated |
