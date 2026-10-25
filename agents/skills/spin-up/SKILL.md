---
name: spin-up
description: "Prepare and carry out a team launch plan with verified native capabilities."
---

# Spin up

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the installed koinon guide for this family and use its coordination path; its Team
operations section has the runnable steps. Inspect the selected checkout's branch, local changes
and work claims, and keep uncommitted work and existing leases. Run the declared project
readiness checks. Prepare a launch plan that names the agent families and their roles, the
directories, the current state and the intended work. Confirm each launcher and native
capability through the installed guidance. The launch task authorizes its steps (WDR 002,
Decision 1). Launch through the supported interfaces. Verify the exact native sessions, the
participant and holder state, and each terminal. Answer first-run permission prompts and alerts
in the new session's terminal: read each prompt, select the response that the launch plan and
the task call for, and verify the result. Report an unavailable CLI or an unresolved target, and
continue the independent launch steps. Record the launch outcome and the open steps in the
private, ignored team manifest, which holds the native identities and endpoints.

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
