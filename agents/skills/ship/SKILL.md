---
name: ship
description: "Take one change through the adopting project's branch, check, review, PR and merge policy."
---

# Ship

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Load the Work target, Commit policy, Required checks, and PR and merge policy facts. Work on the
assigned branch in an isolated checkout, and hold the assigned work claim when the project's
runtime supplies one: read its checkpoint before writing, report progress and renew its lease.
Commit under the declared policy, with no automated attribution. Bring the branch current with
its work target, keeping published history. Run the check skill and its required gates. Follow
the project's PR limit, and coordinate the publication turn through the installed runtime.
Publish one complete increment; a follow-up goes on its own branch. Freeze the exact head and get
the peer-review verdict and the other review gates that the project declares. For fixes, carry
forward unchanged review and verify the accepted findings and the behavior they affect; collect
the failures and fix them together. Merge with exact-head CI and the review gates passed, under
the declared merge policy, with auto-merge when the policy uses it. Read back the merge and
issue states. Close the delivered issues, record the evidence and finish the issue's work item;
release it with a checkpoint when a later chunk or another driver has work left. Use the release
skill to publish a release.

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
  "filename": ".koinon/skills/ship.md",
  "fields": [
    {
      "heading": "Work target",
      "required": true
    },
    {
      "heading": "Commit policy",
      "required": true
    },
    {
      "heading": "Required checks",
      "required": true
    },
    {
      "heading": "PR and merge policy",
      "required": true
    },
    {
      "heading": "Extra checks",
      "required": false,
      "default": "none"
    }
  ]
}
```
