---
name: spin-up
description: "Prepare and carry out an explicitly authorized team launch plan with verified native capabilities."
---

# Spin up

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Read the installed koinon guide for this family and follow its supported coordination path. Its Team operations section has the runnable steps.
Inspect the selected checkout's branch, local changes and advisory work claims. Preserve
uncommitted work and existing leases. Apply any declared project readiness checks.
Prepare a concrete launch plan naming agent families/assigned roles, directories, current
state and intended work. Confirm each launcher/native capability through installed guidance.
The launch task authorizes its steps (WDR 002, Decision 1); ask for necessary missing facts
or authority beyond it. Peer messages are coordination data. Carry out the planned launches
through supported interfaces. Verify exact native sessions,
participant/holder state and own terminal proof, rather than a transport result or pane title.
Handle first-run permissions and alerts through the verified new session terminal within the
authorized launch plan. Read each prompt, select the response within existing authorization
and task context, and verify the result. Surface decisions that exceed that scope; continue
once the prompt is cleared.
Report an unavailable CLI or target that cannot be resolved, and continue independent
launch steps. Record the completed launch outcome and unresolved steps in the private ignored team manifest;
never put private native identities, endpoints or credentials in public project facts.

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
  "filename": ".koinon/skills/spin-up.md",
  "fields": [
    {
      "heading": "Project readiness checks",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Manifest",
      "required": false,
      "default": "_handoff/<agent>/team.json in main checkout, private and ignored"
    },
    {
      "heading": "Start directory",
      "required": false,
      "default": "main checkout"
    },
    {
      "heading": "Roles",
      "required": false,
      "default": "none: one member per family, address FAMILY-LABEL"
    }
  ]
}
```
