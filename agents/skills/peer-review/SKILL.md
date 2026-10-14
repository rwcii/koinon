---
name: peer-review
description: "Review another agent family's frozen commit and record a commit-bound verdict with reproduced findings."
---

# Peer review

Koinon's procedure source is [WDR 003](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/003-pairing-and-independent-review.md). The adopting project's instructions govern its
application. Invoking this skill or its task authorizes the work steps; ask only for necessary
missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

The author and reviewer are different agent families. Bind one exact hash and read committed
content in a separate checkout, preserving the author's working tree. Compare the change with
its written criteria, authoritative project standards, security boundaries and platform contract.
Report only reachable failures reproduced by a test/command or shown in the committed code.
Name the scenario, evidence, violated requirement and severity. Drop speculation and style.
Run checks appropriate to the change and reuse the author's verified results for identical
content. Report the limits of that evidence; transport acceptance is not receiving-model proof.
For paired work use one batch. Follow-up reviews cover accepted fixes, affected behavior and
required gates, carrying forward unchanged content. Only a newly exposed material failure can
block again; a hash change alone does not authorize another general audit.
Record the durable verdict at the project's agreed review checkpoint, bound to the full hash.
Use its declared verdict format and extra review gates; only the reviewer records its gate.
Read back the status and evidence link. A failed or mismatched read-back is not approval.
Never approve with an accepted blocker open. A review verdict grants no permission beyond
the maintainer's task and the receiving agent's native settings.

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
  "filename": ".koinon/skills/peer-review.md",
  "fields": [
    {
      "heading": "Extra review gates",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Verdict format",
      "required": false,
      "default": "project policy/agreed checkpoint"
    }
  ]
}
```
