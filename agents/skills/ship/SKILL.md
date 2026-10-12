---
name: ship
description: "Take one authorized change through the adopting project's branch, check, review, PR and merge policy."
---

# Ship

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Load the required Work target, Commit policy, Required checks, and PR and merge policy
facts. Keep project rules authoritative. Work on the assigned branch in an isolated
checkout; hold the assigned advisory work claim when the project's runtime supplies one.
Read its checkpoint before writing, report progress and renew its lease. Conflicts grant no takeover.
Commit under the declared policy without automated attribution. Bring the branch current
with its work target, preserving published history. Run the check skill and its required gates.
Respect the project's repository-wide PR limit and coordinate the publication turn through
the installed runtime. Publish one complete increment; unrelated follow-ups use another branch.
Freeze the exact current head and obtain the independent peer-review verdict and any other
review gates the project declares. On fixes, carry
forward unchanged review and verify accepted findings and affected behavior. Collect failures
before fixing them together; do not restart a general audit on each hash.
Merge only within existing maintainer authorization, with exact-head CI and review gates
satisfied. Apply the declared merge policy, including auto-merge where required. Read back
actual merge and issue states; a successful-looking command is not proof. Close only delivered
issues, record the evidence, and finish the issue's work item; release it with a checkpoint
when a later chunk or another driver still has work. Do not publish a release under this skill.

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
  "filename": ".koinon/skills/ship.md",
  "fields": [
    {
      "heading": "Work target",
      "required": true
    },
    {
      "heading": "Commit policy",
      "required": true
    },
    {
      "heading": "Required checks",
      "required": true
    },
    {
      "heading": "PR and merge policy",
      "required": true
    },
    {
      "heading": "Extra checks",
      "required": false,
      "default": "none"
    }
  ]
}
```
