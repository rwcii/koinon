---
name: recover-team
description: "Diagnose a team outage, prepare an exact restart plan and carry it out."
---

# Recover team

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the installed `koinon guide --agent FAMILY`; its Team operations section has the runnable
steps. Use outage notices, the private, ignored team manifest and checkpoints as evidence. Check
the daemon's health and current native discovery with the supported read interfaces. Tell apart
lost transport, expired registration, retired or fenced ownership, an unavailable CLI and an
unresolved terminal target. Prepare the exact recovery and restart plan, with the known
outcomes, the dependencies and the open facts, and use the declared project recovery checks.
The recovery task authorizes its steps (WDR 002, Decision 1); earlier approval counts. Use the
supported interface, and resolve the actual native target before the dependent step. After each
operation, verify the actual registration, participant holder, pickup and readiness. Keep
existing leases and checkpoints. Reconcile an uncertain result before a retry, and record the
completed stages in the private manifest.

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
  "filename": ".koinon/skills/recover-team.md",
  "fields": [
    {
      "heading": "Project recovery checks",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Manifest",
      "required": false,
      "default": "_handoff/<agent>/team.json in main checkout, private and ignored"
    }
  ]
}
```
