# Workflow Decision Records — Process Guide

> Vendored from [rwcii/engineering-standards](https://github.com/rwcii/engineering-standards/tree/425b2efc7d829c1f61c9f44df671c4525d5d9158) at 425b2ef. Koinon changes: the review
> path is the `peer-review` and `check` skills, each series is numbered from 001 in this
> repository, commit titles follow Koinon's sentence style, and upstream records are cited
> by link, not imported. #487 removed the limit text: this guide and the directory guide say
> what a WDR holds and where other decisions go. The vendored process guides, PDR template and
> directory guides carry the upstream MIT notice in [docs/ENGINEERING-STANDARDS-LICENSE](../../ENGINEERING-STANDARDS-LICENSE).

Rules for creating, updating, and referencing WDRs in `docs/engineering/wdr/`.

---

## What is a WDR?

A WDR documents a significant decision about **how the work is done** — the
engineering workflow, the agent operating loop, the review and enforcement gates,
and the decision-record system itself. It is the third record type alongside ADRs
(how the system is *built*) and PDRs (how the product *behaves*).

WDRs exist because the most consequential decisions in agent-driven engineering
are about neither the system nor the product, but the **process that produces and
governs them** — and those are exactly the decisions a part-human, part-agent team
drifts on without an anchor.

### The ADR / PDR / WDR test

Two gates route any decision that warrants a record.

**The altitude gate (shared by all three types).** A record captures a *meaty
decision with genuine alternatives* the team anchors on and validates compliance
against. A rule with no real alternative, or a tip rather than a decision, is not
a record — it belongs in an `AGENTS.md` working-convention or a project's
enforcement.

**The type test (three questions).**

1. Would the decision change in a complete **rewrite** (new language/framework)?
   → it is about how the system is built → **ADR**.
2. Would it change in a complete **product pivot** (new features/flows)?
   → it is about how the product behaves → **PDR**.
3. Does it survive **both** — because it governs how the team and its agents work?
   → **WDR**.

"Every change passes an independent review before merge," "verify by execution,
not assertion," and "the `AGENTS.md`/`CLAUDE.md` layout" all survive both a rewrite
and a pivot — they are WDRs.

### Where other decisions go

- **Tips and conventions** — "prefer small commits," a naming nit → `AGENTS.md`.
- **Tool-specific mechanics** — a particular hook command, a lint rule → a
  project's enforcement.
- **A how-to or runbook** — steps to perform a task → the skill or document for
  that task.
- **A decision with no real alternative** — it is how things are; describe it where
  the work happens.

---

## Relationship to ADRs and PDRs

WDRs **reuse the ADR machinery** rather than inventing their own. This is the DRY
standard ([engineering-standards ADR 001.1](https://github.com/rwcii/engineering-standards/blob/425b2efc7d829c1f61c9f44df671c4525d5d9158/docs/architecture/adr/001.1-dry.md)) applied to the record system:

- **Same template** — the YAML frontmatter and section structure in
  [`ADR_PROCESS.md`](../../architecture/adr/ADR_PROCESS.md) § "ADR Template", with
  the field differences noted below.
- **Same status lifecycle** — `draft → under_review → accepted → modified →
  deprecated/superseded`.
- **Same durability conventions** — human authors, with no tool attribution
  (WDR 004, Decision 5); file paths, symbols and record numbers instead of line
  numbers or volatile details; concrete tooling in the Enforcement section.
- **Same review path** — the `peer-review` skill checks changes against accepted
  WDRs, and the `check` skill runs the project's checks on each change.

### Frontmatter differences from an ADR

| Field        | WDR value                                                   |
| ------------ | ----------------------------------------------------------- |
| `wdr`        | the WDR number (replaces `adr`)                             |
| `parent_wdr` | parent WDR number on point versions (replaces `parent_adr`) |
| `tags`       | include `workflow`; add `agentic` for AI-specific decisions |

### One optional added field

A Decision section in a WDR may carry a one-line **Gate crossed: agent-time |
merge-time** note, stating where in the loop the decision is enforced — in the
agent's inner loop before commit, or at the merge gate. Optional, and specific to
workflow decisions.

---

## Naming, numbering, lifecycle

Identical to ADRs (see `ADR_PROCESS.md`): `NNN-title-in-kebab-case.md`, point
versions `NNN.N-…`, zero-padded sequential, a 3–6 word kebab-case suffix.
Determine the next number from this module's `README.md` index.

WDRs are numbered in their **own series** from **001**, independent of the ADR and
PDR series. Cross-references between series always carry the type prefix (ADR 003,
PDR 002, WDR 001) so a bare number is never ambiguous.

### Concurrency-safe numbering (parallel agent work)

Because parallel and branched agent work is a normal mode here, two workstreams can
both reach for the same number. Allocate to avoid collisions: reserve the number on the
branch that lands first, work in disjoint ranges per workstream, or
detect-and-renumber at merge. A number collision breaks every cross-reference and
compliance claim that points at it.

---

## Creating, updating, referencing

Same as ADRs. **Create:** take the next number from the index → write from the ADR
template (with WDR frontmatter) → fill all required sections → add a row to this
`README.md` (hand-maintained) → commit with a title that names the record, for example
`Add WDR 001: [title]`. **Update an accepted WDR:**
`accepted → modified`, bump `updated:`, add a Document History entry, update the
index, commit with a title that names the record. **Reference** from code with
`// Per WDR 00X, …`, from docs with a link, and between records via the `related`
frontmatter.

**Filter** with frontmatter:

```bash
grep -rl "status: accepted" docs/engineering/wdr/     # all accepted WDRs
grep -rl "  - agentic" docs/engineering/wdr/          # all AI-specific WDRs
```
