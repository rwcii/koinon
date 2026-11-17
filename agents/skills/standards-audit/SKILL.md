---
name: standards-audit
description: "In a fresh context, audit a repository or a named path for decisions that have no record. Run the project's deterministic checks, read the enforcement surface as a source of decisions, and report each candidate record with its evidence."
---

# Standards audit

Koinon's procedure source is [PDR 008](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/008-portable-skills-explicit-adoption.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Load the Audit procedure, Record series and index, Deterministic checks, Decision sources and
Reserved bands facts. When the Audit procedure fact names a document, read it in full. If this
skill and that document differ, follow the document.

The audit only reads. It finds decisions and sends each one to its route. Their owners make the
decisions. The audit does not edit guidance to match a preference.

## 1. Set up

1. Start a fresh session that carries no work of an open task.
2. Take the path that the request names as the scope. With no path, audit the whole repository.
3. Record the full hash of the commit that you audit.

## 2. Run the deterministic checks

1. Run each command of the Deterministic checks fact.
2. Read the whole output. A finding in that output is evidence for the audit. It is not a reason
   to stop.
3. Record each command, its exit status and what it reported.
4. When a check cannot run, record its command, its status, the cause and the evidence that is
   missing, as a coverage limit of the audit. It is not a finding. Report a finding only when
   evidence shows a defect in the repository, its wiring or its rules.

## 3. Read the decision sources

1. Read the guidance in scope: instruction files, procedures and plans.
2. Read each source that the Decision sources fact lists. A rule that code enforces is a decision
   as much as a rule in a document.
3. For each rule or practice, look for the record that settles it. Cite the record at an explicit
   repository and revision.
4. Read the history too: the commit log, review findings, and issues, open or closed. Search for
   marks of deferred work, such as `TODO` and `FIXME`.
5. When a document names a gate and nothing runs that gate, report it as a finding.

## 4. Sort the candidates

1. Keep a candidate only when it had real alternatives. Merge each duplicate into one candidate.
2. Put each finding in one group:
   - a rule that no record decides;
   - guidance that conflicts with a record;
   - a workflow, a gate or a code path that conflicts with a record;
   - a record that applies here but that the repository does not cite.
3. List apart, as guidance that needs no record, each tip, how-to or point with one option. Say
   where each one belongs. Keep each rejected candidate in that list with its reason.
4. Rank the candidates by their impact.

## 5. Report each candidate

1. Give each candidate these items:
   - its record type, from the Record series and index fact;
   - a proposed number: the next free number in the index of its series, outside each band of
     the Reserved bands fact;
   - a title;
   - the decision in one line;
   - the alternatives that appear to exist;
   - the evidence, with the path and the revision;
   - a priority.
2. Route each candidate through the project's record procedure. Do not write the record during the
   audit.
3. Add a list of the sources that you read, with their revisions and the rules that you checked
   against them, and the list of coverage limits from section 2.
4. Before you commit the report or file an item from it, scan it for private data under the
   project's rules.

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
  "filename": ".koinon/skills/standards-audit.md",
  "fields": [
    {
      "heading": "Record series and index",
      "required": true
    },
    {
      "heading": "Deterministic checks",
      "required": true
    },
    {
      "heading": "Decision sources",
      "required": false,
      "default": "CI workflows, hooks, gates, scripts, tools and agent settings"
    },
    {
      "heading": "Reserved bands",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Audit procedure",
      "required": false,
      "default": "this skill"
    }
  ]
}
```
