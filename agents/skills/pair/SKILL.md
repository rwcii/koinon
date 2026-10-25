---
name: pair
description: "Pair as one driver and one observer, with fixed criteria, committed checkpoints and bounded review."
---

# Pair

Koinon's procedure source is [WDR 003](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/003-pairing-and-independent-review.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Write observable acceptance criteria for the agreed task before the first edit. The observer
agrees or proposes corrections once, and the pair settles them before building. Agree on one
work branch and checkout, the first driver and the next complete increment. The driver edits,
stages and commits. The observer reads a named committed checkpoint and reproduces findings in
its own scratch location. At a frozen checkpoint the observer sends one batch of verified
findings, each with its failure and its evidence. The driver triages each finding once as
accepted or dropped, with a reason, and takes a material disagreement to the maintainer once.
Verify the accepted fixes and the behavior they affect; unchanged content keeps its review.
When the increment is committed and tested, hand over the exact hash and the evidence, or
finish. The new driver verifies that hash and the checkout state,
then continues. Use the project's extra gates and checkpoint location when the facts declare
them.

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
