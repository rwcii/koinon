---
name: refusal-review
description: "Review another agent family's frozen commit for new refusals, gates and restrictions that have no source, and for maintainer decisions recorded without their context, and record a commit-bound verdict."
---

# Refusal review

Koinon's procedure source is [WDR 001](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/001-decision-records-govern-constraints.md), Decision 6. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

This review runs beside the code, security and standards reviews and has its own verdict. Bind
one exact hash and read the committed content in a separate checkout of your own.

## 1. List the constraints

Read the whole diff against its base. List every added or changed constraint, in any file:

- prohibitions, refusals and exclusions, however phrased, including positively phrased
  requirements that leave something out;
- approval, stop, wait and permission steps, and new reasons to ask the maintainer;
- caps, limits, scope boundaries and conditions in code that refuse or skip work;
- tests that assert a refusal or a limit;
- maintainer decisions the change records, restates or cites as a source, in any file.

Judge what the text, test or condition actually does, not its wording. A keyword search can help
find candidates; the review is the reading.

## 2. Find each source

Each listed constraint needs a source that covers its case:

- a decision record (ADR, PDR or WDR) whose scope includes the case;
- the requirement of the issue or plan that the change delivers;
- the maintainer's own words, quoted with date and the question they answered;
- a verified native, platform, API or storage contract, or a reproduced correctness or
  security boundary.

A recorded maintainer decision states four things: the question it answered, the options that
were on the table, the answer quoted with its date, and the reason with its scope (what it
covers and what it does not). A bare quote, or one without its question or reason, cannot show
its scope, so a later agent may read it as a wider rule.

A routine argument check or a demonstrated capability limit has its source in the code or the
platform. Text that presents a plan proposal as decided, or a decision wider than its quoted
case, is a finding.

## 3. Record the verdict

Report a finding for each constraint with no source, one wider than its source, and each
maintainer decision recorded without its question, options, quoted answer or reason. Name the
file and the text, test or condition, its effect, the missing or narrower source, and the fix.
Send findings in one batch. A follow-up review covers the accepted fixes and the constraints
they add or change.

Record the verdict at the project's review checkpoint, bound to the full hash, in its declared
verdict format. The reviewer records its own gate. Read back the status and the evidence link.

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
  "filename": ".koinon/skills/refusal-review.md",
  "fields": [
    {
      "heading": "Verdict format",
      "required": false,
      "default": "project policy/agreed checkpoint"
    }
  ]
}
```
