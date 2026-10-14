---
wdr: "001"
title: "Decision Records Govern Constraints"
status: modified
created: "2026-10-12"
updated: "2026-10-14"
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
cites the record instead of restating it as a general rule.

---

## Table of Contents

1. Problem Context
2. Decision 1: Adopt the record machinery
3. Decision 2: Significance first, then route by subject
4. Decision 3: Carriers cite records
5. Decision 4: Code-enforced blocks follow their PDR
6. Decision 5: No extrapolation
7. Decision 6: A separate refusal review gate
8. Conclusion
9. Document History

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
mechanism. A missing quote is an unresolved provenance result, not proof that a decision was
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
invoking the skill or the task is the authorization for those steps, and a skill asks only for
missing information or for authority beyond that task. Old sprint non-goals carry a header that
marks them historical. Root AGENTS.md holds pointers to the records and the boundaries the
maintainer stated.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Restate in each carrier | Each document carries its own copy of the rule | Copies drift and broaden as later agents edit them |

### Rationale

1. One statement per decision; a change edits one place.
2. A citation lets a later agent read the decision's scope instead of guessing it.

---

## 5. Decision 4: Code-enforced blocks follow their PDR

### Decision

A refusal or restriction in code that is a disputed consequential behavior changes only after
the maintainer decides its PDR. The change has positive tests that the intended operation
works, and tests that wrong-target and data-corruption cases stay handled.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Remove blocks with the wording | Change code in the same pass as the documents | Changes product behavior before the maintainer decides it |

### Rationale

1. A disputed consequential block is product behavior, and the maintainer decides product
   behavior. A routine fix to code-enforced behavior, such as a parser that refuses ordinary
   input, continues under the existing mandate (Decision 2).

---

## 6. Decision 5: No extrapolation

### Decision

A decision changes or adds a record and is not restated as a general rule elsewhere. A record
covers the case and scope that it answered; another case is judged on its own facts under the
existing mandate, or recorded if it is itself significant. A behavior block is justified by
its record's observed failure, affected scope, alternatives and reason; an incident or concern
alone does not justify a block. Reviewers judge the actual effect of new text, tests and code
conditions, including positively phrased requirements, and treat an unrecorded new block as a
finding. A change of mind supersedes the record, and later agents read its status.

**Gate crossed:** merge-time (the `refusal-review` skill, Decision 6).

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
peer didnt slip more refusal langauge in these two projects.  if you have spare time, build a
gate for identifying this same refusal language in future PRs to run alongside our code
review, security review, and standrds review gates." Intent: an audit of the #140 and #217
plans had to find unsourced refusals after they were written; the gate finds them in each
pull request. This replaces Decision 1's earlier "not a separate review command" for this
check.

**Provenance (decision context):** Maintainer, 2026-10-14, in the stratovera.io repository.

- What happened: an agent recorded "don't log the privacy setting, leave it off" as a bare quote,
  then read it as a ban on all consent logging and rejected a plugin for it. The maintainer had
  answered a question about one plugin's log, which stored very little and capped its entries.
- Raised by the maintainer, not in answer to a question: "this is the problem with you storing
  limited snipets and no contect for capturing decisions", then "go ahead and fix the other
  records too - this will require some modifications to the prompt used for doing refusal
  reviews", and "refusal checks can be completed by any agnet, not just codex".
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

**Gate crossed:** merge-time. The `develop` ruleset requires the `refusal-review` status.

---

## Conclusion

Every significant constraint lives in one record with its source, scope and status. Routine
work needs no record. Other text cites records instead of restating them. A disputed consequential
code-enforced behavior changes only after its PDR is decided; routine fixes do not wait for one.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Decision 6: separate refusal review gate |
| 2026-10-14 | Decision 6: the refusal review also checks that each recorded maintainer decision states its question, options, quoted answer and reason with scope |
