# AGENTS.md — Workflow Decision Records

## What This Directory Contains

Workflow Decision Records (WDRs) that capture **how the work is done** — the
engineering workflow, the agent operating loop, the review and enforcement gates,
and the decision-record system itself. WDRs are the process counterpart to ADRs
(how the system is *built*) and PDRs (how the product *behaves*).

## When You Should Read These

- Before changing how the team or its agents work (the operating loop, review
  gates, autonomy boundaries)
- Before changing the record system or the `peer-review` and `check` skills
- Before adding or relaxing a gate that governs other changes
- When the `peer-review` skill checks a change against accepted WDRs

## Key Constraints

- **WDRs capture process, not the system or the product.** Use the three-question
  test (see `WDR_PROCESS.md`): a decision that survives **both** a rewrite and a
  product pivot is a WDR; one that changes in a rewrite is an ADR; one that changes
  in a pivot is a PDR.
- **A WDR is a meaty decision with genuine alternatives** you anchor on and
  validate compliance against — not a tip. Tips and conventions go in an
  `AGENTS.md`; tool mechanics go in a project's enforcement.
- **No line numbers, no AI attribution, no volatile implementation details.**

## Numbering

Own series, sequential from 001; point versions `NNN.N`. Cross-references always
carry the type prefix (WDR 00X). See `WDR_PROCESS.md` for concurrency-safe
allocation under parallel agent work.

## File Locations

| File             | Purpose                                                  |
| ---------------- | -------------------------------------------------------- |
| `WDR_PROCESS.md` | Process rules, the ADR/PDR/WDR test, template reuse      |
| `README.md`      | Index of all WDRs                                        |
| `NNN-*.md`       | Individual WDR files (`NNN.N-*.md` for point versions)   |

## Checklist Before Modifying

- [ ] Read `WDR_PROCESS.md` for the classification test and template reuse
- [ ] Confirm the decision is a WDR (survives both a rewrite and a pivot), not an ADR/PDR
- [ ] Confirm it is a decision with genuine alternatives, not a tip or how-to
- [ ] Keep it principle-level; push tooling to enforcement
- [ ] Update `README.md` index when adding or changing status
