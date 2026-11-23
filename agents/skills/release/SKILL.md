---
name: release
description: "Promote and publish a release after the project's installation, upgrade and release checks."
---

# Release

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the Promotion path, Publish steps and Release checks facts and the project policy. Find the
exact candidate and the current released baseline. Prepare dated release notes and the project's
installation and upgrade evidence in isolated fixtures, in the project's test environment, with
retained state and unrelated artifacts kept as they are. Run the project's checks and the CI of
the exact candidate; reuse verified results for identical content. Account for failed CI runs in
the release scope that the Release checks define. Prepare the promotion PR and record the
evidence for independent review. Verify the exact head and merge it under the declared promotion
policy. Read back the result, then perform the declared publish steps. Confirm the published
version, the artifacts and the supported installation and upgrade outcome. When the candidate
changes, run its affected checks again. When a publish step has an uncertain result, read the
published state and complete the missing parts.

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
