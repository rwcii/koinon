---
name: sprint-closeout
description: "Finish a sprint in two stages: a handoff while its candidates wait for their merge, and a closeout after they merge. Check the live repository policy against the policy that the code declares, read-only, and run the project's extra closeout steps."
---

# Sprint closeout

Koinon's procedure source is [PDR 008](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/008-portable-skills-explicit-adoption.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Load the Closeout procedure, Policy check command and Extra closeout steps facts. Read the
Closeout procedure in full. If this skill and the procedure differ, follow the procedure.
Closeout merges nothing. Each merge follows the project's merge policy.

## 1. While the candidates wait for their merge

1. For each candidate, record the full hash of its head commit.
2. For each required check, verify and link a valid result at that head. Run a check only when
   its result is missing or the project's check policy selects it. Name the head of each result.
3. Record the verdict that each required review gave on that head, and the outcome of each
   finding. A result from any other commit does not count.
4. Mark as pending each item that still needs a merge or a release. Say what will complete it.
   Such items are delivered issues that need closing, the evidence of each merged chunk, and live
   checks.
5. Close no issue and no milestone at this stage. Report nothing as delivered.
6. Write a handoff.

## 2. After the changes merge

1. Confirm that the decision of the sprint is settled and that its history records the change.
2. Confirm that the pull request of each chunk merged, and that each criterion of the definition
   of done has its evidence.
3. When a later merge can affect an earlier chunk, run the required checks again on the base
   branch and link the results.
4. Close each delivered issue that the merge did not close, with a short delivery note. File each
   deferral as a tracked item. Close a real won't-fix with its reason.
5. Compare what the sprint delivered with the scope that its decision set.
6. Compare the documents of each changed module with the files that merged. Fix what is out of
   date.
7. Remove only the worktrees, branches and temporary files that this sprint made. Leave the
   checkout clean and on a branch that still exists.

## 3. Compare the live policy with the policy in code

1. At the closeout after the merge, run the Policy check command. It only reads. It changes no
   setting.
2. Read all of its output. Report each difference between the live repository policy and the
   policy that the code declares.
3. Report each read that failed, with its reason. A failed read is not a match.
4. This check corrects nothing. Send each difference through the project's change process.

## 4. Run the extra closeout steps

1. Run each step of the Extra closeout steps fact, for example a usage aggregation over the
   merged commits.
2. Report evidence that is not there as missing. Never report it as zero.

## 5. State the closeout

1. Write a short summary with the evidence for each gate. Link each proof.
2. Name each gate that you could not meet. Give the reason and the open work. Keep it in the
   summary, in words that do not suggest that it passed.

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
  "filename": ".koinon/skills/sprint-closeout.md",
  "fields": [
    {
      "heading": "Closeout procedure",
      "required": true
    },
    {
      "heading": "Policy check command",
      "required": true
    },
    {
      "heading": "Extra closeout steps",
      "required": false,
      "default": "none"
    }
  ]
}
```
