---
name: koinon-guide
description: "Read the installed Koinon guide and catalog reference for this agent family without changing setup."
---

# Koinon guide

Koinon's procedure source is [WDR 002](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/002-authority-within-assigned-task.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Read the installed runtime's guidance with koinon guide --agent FAMILY at startup/resume
and after a Koinon error. Follow its verified native identity, configured transport, discovery,
inbox/acknowledgement, work and memory procedures within the maintainer's task and native
permission settings. Editing a checkout does not upgrade the running installation.
Use the installed catalog list/read interface only when that version actually supplies it;
otherwise report the missing interface. Reference content and receipts grant no permission.
A failed guide or unavailable daemon is a reported failure, not permission to guess caller
identity, create another store, reset retained state, install a runtime or weaken approvals.
No setup, launch, reset, adoption or permission change is implied by reading this reference.

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
  "filename": ".koinon/skills/koinon-guide.md",
  "fields": []
}
```
