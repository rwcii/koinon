---
name: pair
description: "Pair as one driver and one observer, with fixed criteria, committed checkpoints and bounded review."
---

# Pair

Koinon's procedure source is [WDR 003](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/003-pairing-and-independent-review.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Record observable acceptance criteria and exclusions in the agreed task before edits. The
observer agrees or objects once; settle that response before building. Only the maintainer
can expand scope, and existing authorization and mandatory project boundaries remain in force.
Agree on one work branch/checkout, first driver and next complete increment. Only the driver
edits, stages and commits. The observer reads a named committed checkpoint and reproduces
findings in a separate scratch location; it does not inspect the changing author tree.
Observe the agreed increment without running commentary. Interrupt only for an imminent
destructive or security mistake. At a frozen checkpoint send one verified findings batch.
A blocker needs a reachable failure of written criteria or a concrete security/data-loss
violation of mandatory boundaries. Drop speculation, style and optional improvements
without fixes, new issues or another work queue; only the maintainer can reopen them.
The driver triages each finding once as accepted or dropped, with a reason. A material
unresolved disagreement goes to the maintainer once. Verify accepted fixes and affected
behavior; unchanged content keeps its review. A new hash does not restart a general audit.
When the increment is committed, tested and has no accepted blocker, hand over the exact
hash and evidence or finish. The new driver verifies it and checkout state before writing.
Do not swap during unfinished fixes/checks. Use the project's extra gates and checkpoint
location when declared; pairing does not authorize unrelated operations or policy changes.

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
  "filename": ".koinon/skills/pair.md",
  "fields": [
    {
      "heading": "Extra increment gates",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Checkpoint location",
      "required": false,
      "default": "agreed task"
    }
  ]
}
```
