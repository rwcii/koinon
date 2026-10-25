---
name: peer-review
description: "Review another agent family's frozen commit and record a commit-bound verdict with reproduced findings."
---

# Peer review

Koinon's procedure source is [WDR 003](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/003-pairing-and-independent-review.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Bind one exact hash and read the committed content in a separate checkout of your own. Compare
the change with its written criteria, the project's standards, its security boundaries and the
platform contract. Report each failure that a test or a command reproduces, or that the committed
code shows: name the scenario, the evidence, the requirement and the severity. Run the checks
that fit the change, and reuse the author's verified results for identical content. State what
the evidence covers. For paired work, send one batch. A follow-up review covers the accepted
fixes, the behavior they affect and the required gates, and carries forward unchanged content.
Record the verdict at the project's review checkpoint, bound to the full hash, in its declared
format, with the extra review gates that the project declares. Read back the status and the
evidence link.

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
