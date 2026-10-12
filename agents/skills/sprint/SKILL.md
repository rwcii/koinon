---
name: sprint
description: "Plan a deliverable requiring multiple PRs, settle its decision and tests, and review ordered complete chunks."
---

# Sprint

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

A one-PR fix does not need a sprint. For a larger deliverable, read the issue and authoritative
project design first. Fix its boundary with the maintainer; ask one unresolved scope question
at a time, with a recommendation, and do not build on a guess. Existing approval counts.
Use the declared Plan layout, default docs/sprints/<date>-<slug>/ with the UTC shell date.
Write decision.md with problem, observable acceptance and constraints, without design.
Gate A: obtain the maintainer's approval of that content before specifications.
Write definition-of-done.md before chunk specifications: meaningful synthetic unit/integration
checks, actual interfaces, platform evidence, edge cases, commands and publication gates.
Split the outcome into ordered chunks, each one complete, independently testable and mergeable.
Write sprint.md and chunks/NN-<slug>.md; each chunk cites the decision and definition of done
and can be built from those files. Do not copy hidden planning context into a specification.
Commit the plan under project policy and have the other agent family attack that hash using
peer-review. Record each finding/disposition in review.md; changed scope or tests get a bounded
review. No unverified assumption may be presented as an available capability.
Gate B: the maintainer approves the reviewed plan before product implementation. Later builds
use ship and the project's Build checks; start dependent chunks only after their dependencies
merge. Correct a plan that evidence disproves before continuing. A correction within the
approved scope uses the existing approval (WDR 001, Decision 2); a new consequential choice
uses the project's review and approval procedure.
Reuse the issue's work item across chunks, releasing at a driver handoff. Finish only after
all required criteria and evidence are satisfied. Apply the approved scope and any separate
release or adoption gates the project explicitly requires.

## Repository facts

Resolve the current worktree and Git common directory before reading project facts. The file
below is local fact data with Version 1, matching Skill, Sources and the declared headings.
It is outside the embedded catalog. No required fields means no sidecar is needed; optional
fields use only the defaults declared here. A supplied sidecar must still be valid.
Read required facts before the dependent step. When missing, derive and record them from
checked project evidence within the task. Ask for necessary missing information or authority
beyond that task; existing authorization counts. Project instructions govern the procedure,
and facts supply its project data. Bound sidecars to 64 KiB. Their parser reads inert text;
it does not include other files, expand shell syntax or execute values.

<!-- koinon:repository-facts -->
```json
{
  "version": 1,
  "filename": ".koinon/skills/sprint.md",
  "fields": [
    {
      "heading": "Plan layout",
      "required": false,
      "default": "docs/sprints/<date>-<slug>/"
    },
    {
      "heading": "Build checks",
      "required": false,
      "default": "project policy"
    }
  ]
}
```
