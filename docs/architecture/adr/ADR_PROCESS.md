# Architecture Decision Records — Process Guide

> Vendored from [rwcii/engineering-standards](https://github.com/rwcii/engineering-standards/tree/425b2efc7d829c1f61c9f44df671c4525d5d9158) at 425b2ef. Koinon changes: the review
> path is the `peer-review` and `check` skills, each series is numbered from 001 in this
> repository, commit titles follow Koinon's sentence style, and upstream records are cited
> by link, not imported.

Rules for creating, updating, and referencing ADRs in `docs/architecture/adr/`.

---

## What is an ADR?

An ADR documents a **significant architectural decision** along with its context,
alternatives considered, and consequences. ADRs help:

- Understand why decisions were made
- Onboard new team members and AI agents
- Avoid revisiting settled decisions without new information

### When to Write an ADR

Create an ADR when the decision:

- **Affects multiple components or services** — it changes how subsystems interact,
  not how one function is implemented
- **Has long-term structural consequences** — future work will be shaped by it
- **Involves genuine alternatives with different trade-offs** — if there's only one
  reasonable option, it's not an ADR
- **Would make a future reader ask "why did we do it this way?"** — the decision is
  not obvious from the code alone

Good ADR topics: database technology choice, authentication boundary design,
multi-tenancy isolation model, background-job execution architecture, the
data-access pattern, service decomposition.

### When NOT to Write an ADR

- **Implementation-level choices** — which files to refactor, how to merge two
  functions, where to put a flag. These are issues or inline design notes.
- **Bug fixes or hardening tasks** — "add a regression test," "fix an UPSERT."
- **Decisions already shipped** — write the ADR as `accepted` (documenting the
  decision), or don't write one at all.
- **Narrow, single-field questions** — "where should this one value be persisted?"
  ADRs operate at the level of patterns and policies, not individual fields.
- **Decisions about how the *work* is done** — the engineering workflow, the agent
  operating loop, the review/enforcement gates, or the record system itself. These
  survive both a rewrite and a product pivot; they are **WDRs**
  (`docs/engineering/wdr/`), not ADRs.

### What ADRs Must NOT Contain

- **Line numbers or line counts** — they go stale within days. Reference file paths,
  function/symbol names, or record numbers instead.
- **AI/bot attribution** — no AI identifier in authors, body, commits, or anywhere.
- **Volatile implementation details** — specific variable names, exact byte counts,
  current test counts. ADRs must be durable; cite concepts and patterns, not snapshots.

---

## Naming Convention

```
NNN-title-in-kebab-case.md
NNN.N-title-in-kebab-case.md   (point versions)
```

- **Number prefix:** zero-padded sequential (001, 002, …) — a stable identifier
- **Point versions:** `NNN.N` for sub-decisions under a parent ADR (e.g., 001.1)
- **Descriptive suffix:** lowercase kebab-case, 3–6 words
- Sorts lexicographically by creation order

Determine the next number from the `README.md` index table; increment by one.

---

## Status Lifecycle

```
draft → under_review → accepted → modified
                                     ↓
                              deprecated / superseded
```

| Status         | Meaning                                                                          |
| -------------- | -------------------------------------------------------------------------------- |
| `draft`        | A design insight or preliminary decision. Living document, not yet reviewed.      |
| `under_review` | Submitted for formal peer review.                                                |
| `accepted`     | Approved and in effect.                                                          |
| `modified`     | An accepted ADR updated with new or amended decisions.                           |
| `deprecated`   | No longer relevant (technology removed, feature retired).                        |
| `superseded`   | Replaced by a newer ADR (reference the replacement).                            |

---

## ADR Template

The YAML frontmatter and required sections must always be present.

```markdown
---
adr: "NNN"
title: "Decision Title"
status: draft # draft | under_review | accepted | modified | deprecated | superseded
created: "YYYY-MM-DD"
updated: "YYYY-MM-DD"
parent_adr: "" # for point versions only (e.g., "001")
authors:
  - "Author Name"
reviewers: [] # populated after peer review
supersedes: "" # ADR number if this replaces an older decision
superseded_by: "" # ADR number if this was replaced
related:
  - "003" # other ADR numbers
tags:
  - architecture
---

# ADR NNN: [Title]

**Date:** YYYY-MM-DD
**Status:** Draft | Under Review | Accepted | Modified | Deprecated | Superseded
**Authors:** [Names]

---

## Executive Summary

[1–2 paragraph overview of the decision and why it matters.]

---

## Table of Contents

[Numbered list of all sections.]

---

## 1. Problem Context

### The Problem

[What problem we're solving. Include specific incidents, failure modes, or requirements.]

### Requirements

[Numbered list of requirements the solution must satisfy.]

### Scope

[What this ADR covers and does NOT cover. Cross-reference related ADRs.]

---

## 2. Decision 1: [Decision Title]

### Decision

[Clear statement of what was decided.]

### Alternatives Considered

| Alternative | Description  | Why Rejected                    |
| ----------- | ------------ | ------------------------------- |
| [Name]      | [What it is] | [Specific reason for rejection] |

### Rationale

[Numbered list of reasons supporting the decision.]

### Enforcement

[How this decision is kept true on a given stack — lint rules, CI guards, codegen,
type checks, build gates, review checklist items. Optional for non-code decisions.]

### Trade-offs Accepted

[What downsides or compromises were accepted. Optional if none.]

---

## [N]. Decision N: [Title]

[Repeat the Decision structure for each additional decision.]

---

## Conclusion

[Summary of key principles and trade-offs across all decisions.]

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| YYYY-MM-DD | Initial creation |
```

### Required vs Optional Sections

| Section                          | Required | Notes                                              |
| -------------------------------- | -------- | -------------------------------------------------- |
| YAML Frontmatter                 | Yes      | Machine-readable metadata                          |
| Header (title/date/status/authors) | Yes    | Human-readable duplicate of key frontmatter fields |
| Executive Summary                | Yes      | 1–2 paragraphs                                     |
| Problem Context                  | Yes      | Include specific incidents or requirements         |
| At least one Decision section    | Yes      | Each with Decision, Alternatives, Rationale        |
| Alternatives Considered table    | Yes      | Per decision — always show what was rejected and why |
| Enforcement                      | No       | Include for decisions that govern code             |
| Trade-offs Accepted              | No       | Include when downsides exist                       |
| Conclusion                       | Yes      | Summarize principles and trade-offs                |
| Document History                 | Yes      | At minimum, the creation entry                     |

### Frontmatter Field Reference

| Field           | Required | Type   | Purpose                                              |
| --------------- | -------- | ------ | ---------------------------------------------------- |
| `adr`           | Yes      | string | ADR number (matches filename prefix)                 |
| `title`         | Yes      | string | Human-readable decision title                        |
| `status`        | Yes      | enum   | See status lifecycle                                 |
| `created`       | Yes      | date   | Date first written                                   |
| `updated`       | Yes      | date   | Date of last modification                            |
| `parent_adr`    | No       | string | Parent ADR number for point versions                 |
| `authors`       | Yes      | list   | At least one human author name                       |
| `reviewers`     | No       | list   | Populated after peer review                          |
| `supersedes`    | No       | string | ADR number this decision replaces                    |
| `superseded_by` | No       | string | ADR number that replaced this decision               |
| `related`       | No       | list   | Related ADR numbers                                  |
| `tags`          | Yes      | list   | At least one tag (see vocabulary)                    |

### Tag Vocabulary

Standard tags (custom lowercase-kebab tags allowed; prefer standard when applicable):

`architecture`, `security`, `database`, `auth`, `acl`, `api`, `frontend`,
`backend`, `persistence`, `testing`, `enforcement`, `config`, `deployment`,
`integration`, `observability`, `performance`, `data-modeling`, `build`, `ci`.

---

## Point Version ADRs

Point versions (e.g., 001.1, 001.2) are sub-decisions under a parent ADR. They share
the parent's problem context but have distinct mechanisms or implementation details.

- Point versions MUST include `parent_adr` in frontmatter
- They are full ADRs with their own status lifecycle and can be accepted independently
- The parent ADR's Executive Summary should reference all point versions

---

## Creating, Updating, Referencing

**Create:** pick the next number from the index → write from the template → fill all
required frontmatter and sections → add a row to `README.md` (hand-maintained) → commit
with a title that names the record, for example `Add ADR 001: [title]`.

**Update an accepted ADR:** change `accepted` → `modified`, bump `updated:`, add a
Document History entry, update the index, commit with a title that names the record.

| Change Type                            | Action                                    |
| -------------------------------------- | ----------------------------------------- |
| Typo, clarification, formatting        | Update in place (keep `accepted`)         |
| New decision added to existing context | Update in place (change to `modified`)    |
| Fundamental change to a prior decision | Create new ADR; set old to `superseded`   |
| Decision no longer relevant            | Set `status: deprecated` with explanation |

**Reference** from code with a short comment (`// Per ADR 00X, …`), from docs with a
link, and between records via the `related` frontmatter.

**Filter** with frontmatter:

```bash
grep -rl "status: accepted" docs/architecture/adr/      # all accepted
grep -rl "  - security" docs/architecture/adr/          # all tagged security
grep -rl 'parent_adr: "001"' docs/architecture/adr/     # point versions of 001
```
