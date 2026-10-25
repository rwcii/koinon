---
name: spin-down
description: "Request each team member's own handoff and carry out the session stop plan."
---

# Spin down

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the installed `koinon guide --agent FAMILY` and use its coordination path; its Team
operations section has the runnable steps. Read the private, ignored team manifest and verify
each live recipient through discovery. Request each session's own handoff, and verify the saved
handoffs. Prepare the exact stop plan, and use the declared project stop checks. The shutdown
task authorizes its steps (WDR 002, Decision 1). Stop each named, verified session through a
supported interface. Reconcile a busy session with the requested shutdown and its handoff.
Report a missing readiness reply, an uncertain delivery or an unavailable stop interface with
its actual outcome, and continue the independent steps; check an uncertain result before you
repeat an action. Update the private manifest and your own handoff with the completed stages and
the remaining work.

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
  "filename": ".koinon/skills/spin-down.md",
  "fields": [
    {
      "heading": "Project stop checks",
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
