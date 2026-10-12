---
name: check
description: "Run the adopting project's required checks before publication, using its verified check facts and policy."
---

# Check

Read the adopting project's authoritative instructions first. For edits to a canonical skill source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the authoring file.

Read the project's policy and the required Base branch and Required checks facts.
Fetch the configured upstream and verify that the branch contains the current base. If it
is behind, update it under the project's history policy before checking or publishing.
Run the declared required and applicable conditional checks against the final change.
A passing run on the same content counts; repeat only when a change or unresolved failure
requires it. Review the final diff for whitespace and private data. Inspect each matched
path, address, credential signature or native identifier; a pattern alone cannot prove privacy.
Report the exact commit, each result and any remaining limitation. A failed required check
stops publication. Required CI comes from project policy; local results do not replace it.

## Repository facts

Resolve the current worktree and Git common directory before reading project facts. The file
below is local fact data with Version 1, matching Skill, Sources and the declared headings.
It is outside the embedded catalog. No required fields means no sidecar is needed; optional
fields use only the defaults declared here. A supplied sidecar must still be valid.
Read required facts before the dependent step. If missing, draft from checked repository
evidence, show the exact diff and obtain approval before writing it. Ask for unknown or
conflicting facts; do not guess a branch, command, interface or permission. A sidecar cannot
remove a skill step, override root policy or grant authorization. Bound it to 64 KiB, with no
arbitrary includes, shell expansion or dynamic command substitution.

<!-- koinon:repository-facts -->
```json
{
  "version": 1,
  "filename": ".koinon/skills/check.md",
  "fields": [
    {
      "heading": "Base branch",
      "required": true
    },
    {
      "heading": "Required checks",
      "required": true
    },
    {
      "heading": "Conditional checks",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Required CI",
      "required": false,
      "default": "project policy"
    }
  ]
}
```
