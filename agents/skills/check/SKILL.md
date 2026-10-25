---
name: check
description: "Run the adopting project's required checks on a change, using its check facts and policy."
---

# Check

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the project's policy and the Base branch and Required checks facts. Fetch the configured
upstream and make sure that the branch contains the current base; when it is behind, update it
under the project's history policy. Run the declared required checks, and the conditional checks
that apply, against the final change. A passing run on the same content counts. Review the final
diff for whitespace and private data, and inspect each matched path, address, credential
signature or native identifier. Report the exact commit and each result. When a required check
fails, fix the cause and run the check again. The project's required CI runs with the local
checks.

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
