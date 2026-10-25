# Product Decision Records — Process Guide

> Vendored from [rwcii/engineering-standards](https://github.com/rwcii/engineering-standards/tree/425b2efc7d829c1f61c9f44df671c4525d5d9158) at 425b2ef. Koinon changes: the review
> path is the `peer-review` and `check` skills, each series is numbered from 001 in this
> repository, commit titles follow Koinon's sentence style, and upstream records are cited
> by link, not imported, and #487 (2026-10-25) removed the limit wording: this guide and the
> directory guide say where each decision goes and what a PDR cites. The vendored process
> guides, PDR template and directory guides carry the upstream MIT notice in
> [docs/ENGINEERING-STANDARDS-LICENSE](../../ENGINEERING-STANDARDS-LICENSE).

Rules for creating, updating, and referencing PDRs in `docs/product/pdr/`.

---

## What is a PDR?

A PDR documents a **significant product, UX, or behavioral decision** along with its
context, alternatives, and consequences. PDRs help:

- Understand why the product behaves the way it does
- Separate product rationale from architectural rationale
- Preserve the "why" behind user flows, interaction patterns, and feature behavior

### PDRs vs ADRs

PDRs and ADRs use the same template but answer different questions:

| Dimension     | ADR                                          | PDR                                              |
| ------------- | -------------------------------------------- | ------------------------------------------------ |
| **Question**  | How is the system built?                     | How does the product behave?                     |
| **Scope**     | Structure, boundaries, patterns, policies     | User flows, feature behavior, UX contracts        |
| **Stability** | Changes when architecture changes            | Changes when product requirements change          |

**Rule of thumb:** if the decision survives a complete product pivot (new features, new
flows) it's an ADR. If it survives a complete rewrite (new language, new framework) it's
a PDR. The two often pair up: an ADR decides the mechanism, a PDR decides the behavior.

### When to Write a PDR

- **Defines user-facing behavior** that isn't obvious from the code
- **Affects multiple surfaces or features** — a behavior that spans several screens or flows
- **Involves genuine product alternatives with different trade-offs**
- **Captures flow design, state progression, or interaction contracts** — the steps,
  their order, their rationale

### Where Other Decisions Go

- **Architecture or infrastructure decisions** go in ADRs.
- **Bug fixes** go in an issue: a misaligned button or wrong status code is an issue.
- **Implementation details** stay in the code: which component to use, how to structure state.
- **Decisions fully captured in design tools** stay in those tools, the source of truth for
  visual design; PDRs capture the behavioral rationale that design files leave out.

### What PDRs Cite

- **Code locations** — file paths, symbol names, or record numbers in place of line numbers
  or line counts.
- **Human authors** — authors, body and commits name people, as WDR 004, Decision 5 gives
  for commits.
- **Stable behavior** — user-facing behavior and product concepts in place of code snapshots.

---

## Naming Convention

```
NNN-title-in-kebab-case.md
```

- Zero-padded sequential number prefix; kebab-case suffix, 3–6 words.
- Determine the next number from the `README.md` index; increment by one.

---

## Status Lifecycle

```
draft → under_review → accepted → modified
                                     ↓
                              deprecated / superseded
```

| Status         | Meaning                                                              |
| -------------- | -------------------------------------------------------------------- |
| `draft`        | A product insight or preliminary decision; not yet reviewed.          |
| `under_review` | Submitted for formal peer review.                                    |
| `accepted`     | Approved and in effect.                                              |
| `modified`     | An accepted PDR updated with new or amended decisions.               |
| `deprecated`   | No longer relevant (feature retired, flow removed).                  |
| `superseded`   | Replaced by a newer PDR (reference the replacement).                |

---

## PDR Template

A ready-to-copy template lives at [`000-template.md`](000-template.md). The required
structure:

```markdown
---
pdr: "NNN"
title: "Decision Title"
status: draft # draft | under_review | accepted | modified | deprecated | superseded
created: "YYYY-MM-DD"
updated: "YYYY-MM-DD"
authors:
  - "Author Name"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "00X" # architectural underpinnings
related_pdrs:
  - "00Y"
tags:
  - ux
---

# PDR NNN: [Title]

**Date:** YYYY-MM-DD
**Status:** Draft | Under Review | Accepted | Modified | Deprecated | Superseded
**Authors:** [Names]

---

## Executive Summary

[1–2 paragraph overview of the product decision and why it matters to the experience.]

---

## Table of Contents

[Numbered list of all sections.]

---

## 1. Problem Context

### The Problem

[The user-facing problem. Include specific user scenarios, UX observations, or gaps.]

### Product Requirements

[Numbered list of requirements from the user's perspective.]

### Scope

[What this PDR covers and does NOT cover. Cross-reference related PDRs and ADRs.]

---

## 2. Decision 1: [Decision Title]

### Decision

[Clear statement of what was decided about product behavior.]

### Alternatives Considered

| Alternative | Description  | Why Rejected                    |
| ----------- | ------------ | ------------------------------- |
| [Name]      | [What it is] | [Specific reason for rejection] |

### Rationale

[Numbered list — user research, product goals, UX principles.]

### User Impact

[How this changes the user experience. What the user will see.]

### Success Criteria

[How we know it's working. Metrics or observable behaviors. Optional if not measurable.]

---

## [N]. Decision N: [Title]

[Repeat for each additional decision.]

---

## Conclusion

[Summary of key product principles and trade-offs.]

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| YYYY-MM-DD | Initial creation |
```

### Required vs Optional Sections

| Section                       | Required | Notes                                            |
| ----------------------------- | -------- | ------------------------------------------------ |
| YAML Frontmatter              | Yes      | Machine-readable metadata                        |
| Header                        | Yes      | Human-readable duplicate of key fields           |
| Executive Summary             | Yes      | 1–2 paragraphs                                   |
| Problem Context               | Yes      | Include specific user scenarios                  |
| At least one Decision section | Yes      | Each with Decision, Alternatives, Rationale      |
| Alternatives Considered table | Yes      | Per decision                                     |
| User Impact                   | Yes      | How the experience changes                       |
| Success Criteria              | No       | Include when measurable                          |
| Conclusion                    | Yes      | Summarize principles and trade-offs              |
| Document History              | Yes      | At minimum, the creation entry                   |

### Frontmatter Field Reference

| Field           | Required | Type   | Purpose                                              |
| --------------- | -------- | ------ | ---------------------------------------------------- |
| `pdr`           | Yes      | string | PDR number (matches filename prefix)                 |
| `title`         | Yes      | string | Human-readable decision title                        |
| `status`        | Yes      | enum   | See status lifecycle                                 |
| `created`       | Yes      | date   | Date first written                                   |
| `updated`       | Yes      | date   | Date of last modification                            |
| `authors`       | Yes      | list   | At least one human author name                       |
| `reviewers`     | No       | list   | Populated after peer review                          |
| `supersedes`    | No       | string | PDR number this replaces                             |
| `superseded_by` | No       | string | PDR number that replaced this                        |
| `related_adrs`  | No       | list   | ADR numbers for the architectural underpinnings      |
| `related_pdrs`  | No       | list   | Related PDR numbers                                  |
| `tags`          | Yes      | list   | At least one tag (see vocabulary)                    |

### Tag Vocabulary

Standard tags (custom lowercase-kebab tags allowed; prefer standard when applicable):

`ux`, `flow`, `onboarding`, `navigation`, `notifications`, `identity`, `content`,
`voice`, `error-handling`, `empty-states`, `accessibility`, `localization`, `billing`,
`integration`, `mobile`.

---

## Creating, Updating, Referencing

**Create:** pick the next number from the index → copy `000-template.md` → fill all
required frontmatter and sections → add a row to `README.md` → commit
with a title that names the record, for example `Add PDR 001: [title]`.

**Update an accepted PDR:** change `accepted` → `modified`, bump `updated:`, add a
Document History entry, update the index, commit with a title that names the record.

| Change Type                            | Action                                    |
| -------------------------------------- | ----------------------------------------- |
| Typo, clarification, formatting        | Update in place (keep `accepted`)         |
| New decision added to existing context | Update in place (change to `modified`)    |
| Fundamental change to a prior decision | Create new PDR; set old to `superseded`   |
| Decision no longer relevant            | Set `status: deprecated` with explanation |

**Reference** from code with a short comment, from docs with a link, and to the
underpinning architecture via `related_adrs` in frontmatter.
