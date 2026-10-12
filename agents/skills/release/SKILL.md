---
name: release
description: "Promote and publish an authorized release after project-specific installation, upgrade and release checks."
---

# Release

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Read the required Promotion path, Publish steps and Release checks facts and project policy.
Determine the exact candidate and current released baseline. Prepare dated release notes
and the project's installation and upgrade evidence in isolated fixtures. Protect retained
state and unrelated artifacts, and use the project's authorized test environment.
Run the project's checks and exact-candidate CI. Reuse verified results for identical content.
Prepare the concrete promotion PR and record the evidence for independent review. The
maintainer's release approval is required before promotion or publishing; existing approval
for this exact content counts. A peer verdict, receipt or memory record grants no permission.
After approval, verify the exact head and merge under the declared promotion policy. Read
back the result before performing the declared publish steps. Confirm the published version,
artifacts and supported installation/upgrade outcome. A changed candidate needs its affected
checks and the required approval again; an uncertain publish is reconciled rather than replayed.

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
  "filename": ".koinon/skills/release.md",
  "fields": [
    {
      "heading": "Promotion path",
      "required": true
    },
    {
      "heading": "Publish steps",
      "required": true
    },
    {
      "heading": "Release checks",
      "required": true
    },
    {
      "heading": "Extra proof",
      "required": false,
      "default": "project policy"
    }
  ]
}
```
