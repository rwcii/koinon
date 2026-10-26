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

## Key Points

- **WDRs capture process.** Use the three-question test (see `WDR_PROCESS.md`): a
  decision that survives **both** a rewrite and a product pivot is a WDR; one that
  changes in a rewrite is an ADR; one that changes in a pivot is a PDR.
- **A WDR is a meaty decision with genuine alternatives** you anchor on and
  validate compliance against. Tips and conventions go in an `AGENTS.md`; tool
  mechanics go in a project's enforcement.
- **Cite file paths, symbols and record numbers; list human authors; cite concepts,
  not snapshots.** Records and commits carry the maintainer's identity and no tool
  attribution (WDR 004, Decision 5).

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

## Steps for a Change

- [ ] Read `WDR_PROCESS.md` for the classification test and template reuse
- [ ] Place the decision in a WDR, an ADR or a PDR with the three-question test
- [ ] Write the decision with its genuine alternatives; tips and how-tos go in an `AGENTS.md`
- [ ] Write it at principle level, with tooling in the Enforcement section
- [ ] Update `README.md` index when adding or changing status
