---
name: ship
description: "Take one change through the adopting project's branch, check, review, PR and merge policy."
---

# Ship

Koinon's procedure source is [WDR 004](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/004-publication-and-verification.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Load the Work target, Commit policy, Required checks, Extra checks, PR and merge policy, Merge
approval, and Docs and agent files facts. Work on the
assigned branch in an isolated checkout, and hold the assigned work claim when the project's
runtime supplies one: read its checkpoint before writing, report progress and renew its lease.
Commit under the declared policy, with no automated attribution. Bring the branch current with
its work target, keeping published history. Run the docs gate below, then the check skill and its
required gates. Run the steps of the Extra checks fact at the points that it names. Follow
the project's PR limit, and coordinate the publication turn through the installed runtime.
Record each board decision that the change carries out in the ADR, PDR, WDR or sprint document
that holds the rule, in the form that the verdict read gives: decided by (the person's name),
date, context, decision, reason and scope, and review when. Koinon writes no record file. A
facts file can still hold the retired Decision record heading; Koinon ignores it.
Publish one complete increment; a follow-up goes on its own branch. Freeze the exact head and get
the peer-review verdict and the other review gates that the project declares. When the Merge
approval fact names a board approval, put a card on the project's decision board after those
reviews pass: it names the pull request and its full head, with the review evidence. After the
maintainer approves the card, run the fact's status command for that pull request, head and
card. A new head needs a new card or a new revision of the card. For fixes, carry
forward unchanged review and verify the accepted findings and the behavior they affect; collect
the failures and fix them together. Merge with exact-head CI and the review gates passed, under
the declared merge policy, with auto-merge when the policy uses it. Read back the merge and
issue states. Close the delivered issues, record the evidence and finish the issue's work item;
release it with a checkpoint when a later chunk or another driver has work left. Use the release
skill to publish a release.

## Docs gate

Koinon's procedure source for this gate is WDR 004, Decision 7.
Before you freeze the head, compare the files in the Docs and agent files fact with the code at
the head:

1. List the user-visible changes against the base: commands, tools, flags, defaults, limits,
   error codes, routes, paths, versions, features and skills.
2. Read each file in the fact at the head. Check first the parts that describe a changed item,
   then the rest of the file, because a statement can be wrong from an earlier change.
3. Correct each statement that the code at the head contradicts.
4. Describe each new user-visible feature in the file where a reader looks for it.
5. Make the version numbers, release names and skill lists in those files agree with the code
   at the head.
6. Commit the corrections in this change. Name the files that you read and changed in the
   review evidence.

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
      "heading": "Docs and agent files",
      "required": false,
      "default": "the README, the user docs, the agent instruction files such as AGENTS.md and CLAUDE.md, and the project's skills"
    },
    {
      "heading": "Extra checks",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Merge approval",
      "required": false,
      "default": "none"
    }
  ]
}
```
