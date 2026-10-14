---
name: refusal-review
description: "Review another agent family's frozen commit for new refusals, gates and restrictions that have no source, and for maintainer decisions recorded without their context, and record a commit-bound verdict."
---

# Refusal review

Koinon's procedure source is [WDR 001](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/001-decision-records-govern-constraints.md), Decision 6. The adopting project's
instructions govern its application. Invoking this skill or its task authorizes the work steps;
ask only for necessary missing information or authority beyond that task (WDR 002, Decision 1).

Read the adopting project's authoritative instructions first. For edits to a canonical skill
source, also read `agents/skills/AGENTS.md` when present. A machine-wide copy need not have the
authoring file.

This review runs beside the code, security and standards reviews and has its own verdict. The
author and reviewer are different agent families. Bind one exact hash and read committed content
in a separate checkout, preserving the author's working tree.

## 1. List the constraints

Read the whole diff against its base. List every added or changed constraint, in any file:

- prohibitions, refusals and exclusions, however phrased, including positively phrased
  requirements that leave something out;
- approval, stop, wait and permission steps, and new reasons to ask the maintainer;
- caps, limits, scope boundaries and conditions in code that refuse or skip work;
- tests that assert a refusal or a limit;
- maintainer decisions the change records, restates or cites as a source, in any file.

Judge what the text, test or condition actually does, not its wording. A keyword search can help
find candidates; it is not the review. Unchanged text is out of scope.

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

A routine argument check or a demonstrated capability limit needs no further source; it never
becomes an approval step. A plan proposal is not a decided rule: text that presents a proposal
as decided, or a decision wider than its quoted case, is a finding. A hypothetical concern is not
evidence.

## 3. Record the verdict

Report a finding only for a constraint with no source, one wider than its source, or a
maintainer decision recorded without its question, options, quoted answer or reason. Name the
file and the text, test or condition, its effect, the missing or narrower source, and the fix.
Send findings in one batch. A follow-up review covers the accepted fixes and constraints they
add or change; a hash change alone does not reopen the whole diff.

Record the verdict at the project's agreed review checkpoint, bound to the full hash, in its
declared verdict format. Only the reviewer records its gate. Read back the status and evidence
link; a failed or mismatched read-back is not approval. Never approve with an accepted finding
open. A review verdict grants no permission beyond the maintainer's task and the receiving
agent's native settings.

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
