---
wdr: "001"
title: "Decision Records Govern Constraints"
status: modified
created: "2026-10-12"
updated: "2026-10-28"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related: []
tags:
  - workflow
  - agentic
  - enforcement
---

# WDR 001: Decision Records Govern Constraints

**Date:** 2026-10-12
**Status:** Modified
**Authors:** Robert Capps

---

## Executive Summary

Koinon is written almost entirely by agents. Agents recorded single decisions as broad rules
in skills, sprint plans, guide text, documentation, tests and code, and later agents applied
those rules as constraints that the maintainer never set. This record adopts the ADR, PDR and
WDR record system from rwcii/engineering-standards so that every significant constraint has a
record with its source, scope, alternatives and status, and it sets how existing and new
constraints are routed into records.

A decision is stated once, in its record, as "Koinon currently does X because Y". Other text
cites the record instead of restating it as a general rule. A verdict on the decision board is
recorded in the repository's `DECISIONS.md` and cited there, never by its verdict ID.

---

## Table of Contents

1. Problem Context
2. Decision 1: Adopt the record machinery
3. Decision 2: Significance first, then route by subject
4. Decision 3: Carriers cite records
5. Decision 4: Code-enforced blocks follow their PDR (deprecated)
6. Decision 5: A record covers the case it answered
7. Decision 6: A separate refusal review gate
8. Decision 7: Board verdicts are recorded in DECISIONS.md
9. Conclusion
10. Document History

---

## 1. Problem Context

### The Problem

The maintainer's words, 2026-10-12:

- "you and your peer need to review the audit findings and make a recommendation as to how we
  remove all of these silently intrduced constraints."
- "the issue is that you will take a decision and then extrapolate it in to a very vague
  "rule" which is then interpreted by a later agent."
- On stating engineering choices as "Koinon currently does X because Y": "yes!!! this is the
  correct framing.  do we not have decision records in place for thie project?  in line with
  rwcii/engineering-standards?"
- On the jointly reviewed method that this record states: "approved, proceed with step 1."

Two audits of the repository on 2026-10-12 found approval gates, prohibitions and code-enforced
refusals with no recorded source. Examples: a sprint non-goal "Nothing types into a
terminal" that later work read as a product ban (removed in #305), and new approval and stop
gates added to skills while they were moved into a catalog (#145, chunk 01 review). Koinon's
sprint `decision.md` files have context and decisions but no alternatives, status or
supersession, so a sprint's scope choice reads as a permanent rule and nothing marks it
replaced.

### Requirements

1. Each significant constraint has one record with its source, scope, alternatives, reason
   and status.
2. A maintainer decision is recorded with the maintainer's words and date, scoped to the case
   it answered.
3. Routine work proceeds under the existing mandate, with no record and no new approval.
4. A change of decision is visible: the old record is superseded, not silently edited.

### Scope

Covers the record system, how constraints become records, and how other text refers to them.
Does not decide any product behavior; those decisions are PDRs. Tracked in issue #306.

The first records cite rows (C001 to C086) of a constraint inventory made for #306 on
2026-10-12. Each row holds a constraint's exact text, every copy found, its source and its
actual effect. The inventory quotes private session history, so it is kept outside the
repository; each record states the provenance it relies on.

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
  restate them. The #487 audit listed the code refusals for a separate decision; #493 decided
  them (Change of 2026-10-26).
- This record: Decision 4 is deprecated. Decision 5 states the procedure without its block
  rules. Decision 3 drops the skill's question limit, as the skills did in #487.

### Change of 2026-10-26 (#493)

- Question (decision board item 493-5, 2026-10-26): keep or remove the requirement that a pull
  request to `develop` merges only when the reviewer's `peer-review` and `refusal-review` commit
  statuses pass at the exact head?
- Options: keep both statuses required, or remove them.
- Answer: Maintainer, 2026-10-26: approved ([DECISIONS.md#493-5](../../../DECISIONS.md#493-5)), with no note, on the card's
  recommendation: "Keep both."
  ([#493 comment 6406505885](https://github.com/rwcii/koinon/issues/493#issuecomment-6406505885)).
- Intent and scope: both statuses stay required on `develop`, beside the four CI jobs. The
  ruleset does not change. The reason for the `refusal-review` gate is its 2026-10-12
  provenance (Decision 6).
- This record: Decision 6 cites this verdict. WDR 004 (Decision 3) records it for
  `peer-review`.

---

## 2. Decision 1: Adopt the record machinery

### Decision

Koinon vendors the ADR, PDR and WDR processes, the PDR template, the directory guides and the
indexes from rwcii/engineering-standards at
425b2efc7d829c1f61c9f44df671c4525d5d9158, adapted to Koinon's review skills and commit style.
Upstream records, including its accepted baseline, are not Koinon decisions; a Koinon record
can adopt one by citing it. Each series is numbered from 001 in this repository. No setup
script or repository setting is part of this adoption. Record checks are part of the existing
`sprint`, `peer-review` and `check` skills; the refusal review is its own gate (Decision 6).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Sprint files only | Keep decisions in `docs/sprints/*/decision.md` | No alternatives, status or supersession; scope choices read as permanent rules |
| Rules in AGENTS.md | State constraints as instructions to agents | The failure this record addresses: ungoverned guidance that later agents extrapolate |
| Full template adoption | Import the upstream baseline records, review command and setup script | Imports decisions the maintainer has not made for Koinon and a parallel review path |

### Rationale

1. The maintainer named this standard as the expected practice.
2. Status and supersession make a change of mind visible to later agents.
3. Vendoring keeps the records in the repository that they govern, with no runtime dependency.

---

## 3. Decision 2: Significance first, then route by subject

### Decision

A record is written only for a significant choice with real alternatives. Routine fixes,
mechanics, runbooks and conventions continue under the existing mandate with no record and no
new approval. Each significant constraint is routed by its subject, not by its source:

| Kind | Route |
| ---- | ----- |
| Maintainer decision with recorded words | Record with status `accepted`, quoting the words and date, scoped to the case answered |
| Product behavior choice | PDR, `draft` until the maintainer decides |
| Architecture choice | ADR |
| Workflow choice | WDR |
| Native permission boundary (the maintainer's own agent instructions and settings) | Stated once where agents read it; not copied as a product rule |
| Technical invariant | Kept in its record with the reachable failure it prevents |
| Current implementation fact | Plain description with its version |
| Unsourced policy with no reason | Deleted |

A retrospective record of shipped behavior describes what shipped and where, and does not
claim an endorsement the maintainer did not give. A disputed or new consequential behavior
choice stays a proposal until the maintainer settles it; an established prior acceptance or
delegation counts. A quote is evidence of where a decision came from, not a permission
mechanism. A missing quote is an unresolved provenance result; it does not show that a decision was
never approved; a significant item without one becomes a record, not a silent deletion.
Quotes taken from agent memory are checked against their original source.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Record everything | One record per constraint, however small | Buries decisions in records of mechanics and adds approval steps to routine work |
| Delete everything unsourced | Remove each constraint without a quote | Removes real invariants and shipped behavior whose reason was never written down |
| Route by source | Maintainer words become WDRs, agent text becomes PDRs | A maintainer decision can be about architecture, product or workflow |

### Rationale

1. The significance test is the upstream standard's own "When NOT to write" guidance.
2. Routing by subject keeps a product behavior and its mechanism in separate records that
   cite each other.

---

## 4. Decision 3: Carriers cite records

### Decision

Sprint plans, skills, PROTOCOL.md, docs/USAGE.md, docs/INSTALL.md, `koinon guide` text and skill
sidecars cite records by type and number (PDR 001, WDR 001; the series reuse numbers) and
describe behavior as "Koinon currently does X because Y". A skill is a list of work steps:
invoking the skill or the task is the authorization for those steps. Old sprint non-goals carry
a header that marks them historical. Root AGENTS.md holds pointers to the records.

**Before #487:** the skill sentence went on "and a skill asks only for missing information or
for authority beyond that task", and AGENTS.md also held "the boundaries the maintainer stated".

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Restate in each carrier | Each document carries its own copy of the rule | Copies drift and broaden as later agents edit them |

### Rationale

1. One statement per decision; a change edits one place.
2. A citation lets a later agent read the decision's scope instead of guessing it.

---

## 5. Decision 4: Code-enforced blocks follow their PDR (deprecated)

### Decision

**Deprecated 2026-10-25 by #487** (Problem Context). A change to a refusal or restriction in
code is an ordinary code change (WDR 004). It has positive tests that the intended operation
works. Records and documents describe each refusal that the code returns as current behavior.

**Before #487:** "A refusal or restriction in code that is a disputed consequential behavior
changes only after the maintainer decides its PDR. The change has positive tests that the
intended operation works, and tests that wrong-target and data-corruption cases stay handled."
No maintainer decision set this rule.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Remove blocks with the wording | Change code in the same pass as the documents | Changes product behavior before the maintainer decides it |

### Rationale

1. A disputed consequential block is product behavior, and the maintainer decides product
   behavior. A routine fix to code-enforced behavior, such as a parser that refuses ordinary
   input, continues under the existing mandate (Decision 2).

---

## 6. Decision 5: A record covers the case it answered

### Decision

A decision changes or adds its record, and other text cites that record (Decision 3). A record
covers the case and scope that it answered. Agents judge another case on its own facts, and
record it when it is itself significant. A change of mind supersedes the record, and later
agents read its status. The refusal review (Decision 6) reads the actual effect of new text,
tests and code conditions.

**Before #487:** the title was "No extrapolation". The decision also said that a decision "is
not restated as a general rule elsewhere", that "a behavior block is justified by its record's
observed failure, affected scope, alternatives and reason; an incident or concern alone does
not justify a block", and that reviewers "treat an unrecorded new block as a finding"; its gate
was merge-time. The extrapolation failure is the maintainer's (Problem Context); these rules
had no maintainer source.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Keyword scan | Reject new text that contains words such as "never" or "approval" | Misses positively phrased requirements, tests and code conditions; blocks legitimate text |

### Rationale

1. Extrapolation from one decision to a vague rule is the failure the maintainer named.

---

## 7. Decision 6: A separate refusal review gate

### Decision

Every pull request into `develop` gets a refusal review beside its peer review. A different
agent family reviews the exact head for added or changed refusals, gates, caps, exclusions and
approval or stop steps, in text, tests and code conditions, and finds a source that covers each
case (Decision 5). It records its own verdict comment and `refusal-review` commit status on the
full hash. The `refusal-review` skill describes the steps.

The same review checks each maintainer decision that the change records or cites as a source.
The record states the question the decision answered, the options that were on the table, the
maintainer's answer quoted with its date, and the reason with its scope: what the decision
covers and what it does not. A bare quote, or one without its question or reason, is a finding,
as is a decision applied beyond its stated scope.

**Provenance:** Maintainer, 2026-10-12, approving Gate B for #140: "make sure you and your
peer didn't slip more refusal language in these two projects. if you have spare time, build a
gate for identifying this same refusal language in future PRs to run alongside our code
review, security review, and standards review gates." Intent: an audit of the #140 and #217
plans had to find unsourced refusals after they were written; the gate finds them in each
pull request. This replaces Decision 1's earlier "not a separate review command" for this
check.

**Provenance (decision context):** Maintainer, 2026-10-14, in the stratovera.io repository.

- What happened: an agent recorded "don't log the privacy setting, leave it off" as a bare quote,
  then read it as a ban on all consent logging and rejected a plugin for it. The maintainer had
  answered a question about one plugin's log, which stored very little and capped its entries.
- Raised by the maintainer, not in answer to a question: "this is the problem with you storing
  limited snippets and no context for capturing decisions", then "go ahead and fix the other
  records too - this will require some modifications to the prompt used for doing refusal
  reviews", and "refusal checks can be completed by any agent, not just codex".
- Question then asked: where to change the refusal-review skill, given that the stratovera.io
  copy is byte-for-byte from Koinon. Options: change Koinon first, then copy it (recommended:
  every project using Koinon gets the rule); or change the stratovera.io copy only for now, so
  the copies drift. Answer: "Koinon first, then copy (Recommended)".
- Reason: the maintainer's first message above. Scope: how the refusal review checks recorded
  maintainer decisions, for any agent family that runs it.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Records lens inside peer review | Keep the check as one item of the general review | The #140 and #217 plans passed peer review with unsourced refusals |
| Keyword scan in CI | Fail a build on words such as "never" or "only" | Decision 5: misses positively phrased limits and code, and blocks legitimate text |

### Rationale

1. A separate verdict makes the check visible on every head, as the maintainer asked.

**Provenance (2026-10-26, #493):** the maintainer kept the required `refusal-review` status on
the decision board ([DECISIONS.md#493-5](../../../DECISIONS.md#493-5), [#493 comment 6406505885](https://github.com/rwcii/koinon/issues/493#issuecomment-6406505885); Problem Context).

**Gate crossed:** merge-time. The `develop` ruleset requires the `refusal-review` status.

---

## 8. Decision 7: Board verdicts are recorded in DECISIONS.md

### Decision

Each verdict that the maintainer gives on the decision board is recorded in `DECISIONS.md` at
the root of the repository that the item was created in, and only there. Entries are only
appended: one per verdict, with the card text that the maintainer saw and the maintainer's note
or feedback. The agent that handles the verdict records it with `decision_record` (`koinon
decision record`), corrects the spelling and typing errors in the maintainer's words without
changing their meaning or wording, and commits the file with the work. A commit, a pull request
or a document cites the decision as `DECISIONS.md#REFERENCE`, never by its verdict ID, which
exists only in one machine's store. A recorded entry is public, so agents write every card as
public text. The tool refuses an item from another repository, an item with no recorded
repository, and a `DECISIONS.md` that is not a Koinon record; the agent then asks the
maintainer on the board and changes nothing. A contributor test checks the tracked documents,
the `decision-citations` check checks the pull request text in CI, and the `commit-msg` hook
checks commit messages.

The verdicts that Koinon's documents cited before this decision were recorded on 2026-10-28 as
public rewrites of their cards, written once; the Unify card that PDR 008 cites stays out of
this repository.

**Provenance:** [DECISIONS.md#DECISION-RECORDS-536](../../../DECISIONS.md#decision-records-536)
records the design that the maintainer approved on 2026-10-27, with the maintainer's words. The
maintainer then set these requirements while the change was built (#536):

- On a Unify card that the backfill was about to record here: "why would you write a unify
  card to koinon?" Intent: an item is recorded only in the repository that it decides for.
- On verdict IDs in pushed commit messages and pull request bodies: "yeah, that's what pointed
  me at the issue." Intent: the rule covers commit and pull request text.
- "old cards need to be rewritten for public consumption, PRIOR to moving them to the decisions
  record." Asked whether each rewrite should come back for a new verdict: "they need to be
  rewritten in the record once and then we move on." Intent: one public rewrite of each old
  card, with no new verdict.
- On a repository whose `DECISIONS.md` is not a Koinon record: "in such a case, the agent
  should bring this to the attention of the maintainer." Intent: the tool refuses, and the
  maintainer decides.
- "make sure that text entered by the human is spell and typo checked. we don't need to
  memorialize a typo forever more in the repo", then "this should be part of the agent
  instructions". Intent: the maintainer's words are spelling-corrected wherever an agent puts
  them into the repository; the installed guide says so.
- Asked whether a CI check and a commit-message hook should refuse verdict IDs in pull request
  and commit text: "approved as recommended. continue".

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| An export command into `.koinon/decisions/` | One file per item, written on request | The maintainer asked for MCP and one visible append file |
| The daemon writes the file at the verdict | No agent step | An item names no repository, and the write would land outside any branch and review |
| A comment on the GitHub issue per verdict | As for #493 | Not in the repository |
| A new verdict on each rewritten old card | Every entry is text the maintainer saw | The maintainer: "they need to be rewritten in the record once and then we move on." |

### Rationale

1. A citation resolves for every contributor, clone and CI run.
2. The record changes with the work, in the same branch and review.

---

## Conclusion

Every significant constraint lives in one record with its source, scope and status. Routine
work needs no record. Other text cites records. A record covers the case it answered, and a
refusal review checks the source of each new constraint. Board verdicts are recorded in
`DECISIONS.md` and cited there.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Decision 6: separate refusal review gate |
| 2026-10-14 | Decision 6: the refusal review also checks that each recorded maintainer decision states its question, options, quoted answer and reason with scope |
| 2026-10-25 | #487: Decision 4 deprecated; Decision 5 states the procedure without its block rules; Decision 3 drops the skill's question limit and the AGENTS.md boundaries |
| 2026-10-26 | #493: Decision 6 cites the maintainer's verdict that keeps the required `refusal-review` and `peer-review` statuses ([DECISIONS.md#493-5](../../../DECISIONS.md#493-5)) |
| 2026-10-28 | Decision 7: board verdicts are recorded in `DECISIONS.md` and cited there (#536); Decision 6's quotes are spelling-corrected |
