---
name: koinon-guide
description: "Read the installed Koinon guide and catalog reference for this agent family."
---

# Koinon guide

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the installed runtime's guidance with `koinon guide --agent FAMILY` at startup, at resume
and after a Koinon error. Use its native identity, configured transport, discovery, inbox and
acknowledgement, work and memory procedures for the maintainer's task. The guide describes the
running installation; an edit to a checkout reaches it at the next install or upgrade. Read the
installed catalog with its list and read interface. When the guide or the daemon is unavailable,
report the command and its output to the maintainer.

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
  "filename": ".koinon/skills/koinon-guide.md",
  "fields": []
}
```
