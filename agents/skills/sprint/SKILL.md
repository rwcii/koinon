---
name: sprint
description: "Plan a deliverable requiring multiple PRs, settle its decision and tests, and review ordered complete chunks."
---

# Sprint

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Use a sprint for a deliverable that needs more than one PR. Read the issue and the project's
design. Settle the boundary with the maintainer, one open scope question at a time, each with a
recommendation. Use the declared Plan layout, by default docs/sprints/<date>-<slug>/ with the
UTC date from the shell. Write decision.md with the problem and the observable acceptance,
without design. Write definition-of-done.md before the chunk specifications: meaningful
synthetic unit and integration checks, actual interfaces, platform evidence, edge cases,
commands and publication gates. Split the outcome into ordered chunks, each one complete,
testable alone and mergeable. Write sprint.md and chunks/NN-<slug>.md; each chunk cites the
decision and the definition of done and can be built from those files alone.

In sprint.md, give the chunks a table with the columns Chunk, Depends on and Owns. Depends on
lists the chunk numbers that must merge first, or "none". Owns lists, separated by commas, the
files, directories, interfaces and records that only that chunk changes: a path in backticks, a
record by its name, such as WDR 002. Name the shared files that each chunk adds lines to, such
as the changelog, below the table as not owned. Under a "## Parallel view" heading, add a table
with the columns Group and Chunks: group 1 holds the chunks with no dependency, and each later
group holds chunks whose dependencies are in earlier groups. Two chunks of one group own no item
in common; two items are common when they are the same path, when one is a directory that holds
the other, or when they name the same record, by its name or by its file. Each chunk is in one
group. A coordinator dispatches a chunk when its dependencies have merged and no chunk in
progress owns an item in common with it (WDR 002, Decision 7). Commit the plan
under project policy, and have the other agent family review that hash with peer-review. Record
each finding and its disposition in review.md; changed scope or tests get a bounded review.
Build the chunks with ship and the project's Build checks, in dependency order. Correct the plan
when evidence disproves it (WDR 001, Decision 2). Reuse the issue's work item across chunks that one
pair builds in order, and release it at a driver handoff; a coordinator gives each chunk that it
dispatches its own item (`coordinate`). Finish when every criterion and its evidence are in place.

## Repository facts

The project's facts for this skill are local data in the file named below, in the current
worktree: Version 1, Skill, Sources and the headings declared here. An optional heading that
the file leaves out takes its default, and a skill with no required heading works without the
file. When a required fact is missing, derive it from the project's evidence and record it in
the file. The parser reads the file as plain text, up to 64 KiB.

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
