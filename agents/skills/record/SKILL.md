---
name: record
description: "Write a new decision record, or change one, when a decision with real alternatives needs one, or when a record seems wrong. Route it to its kind and repository, number it, write it and run the project's record checks."
---

# Record

Koinon's procedure source is [PDR 008](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/008-portable-skills-explicit-adoption.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Load the Record kinds and folders, Index and numbering rule, Reserved bands, Routing procedure,
Record checks and Commit subject pattern facts. When the Routing procedure fact names a document,
read it in full. If this skill and that document differ, follow the document.

## 1. Decide whether a record is needed

1. Write the decision in one sentence. Name each option that was really open.
2. A point with one possible option is a fact, not a decision. A tip or a working convention is
   not a record either. Put it in the project's instruction file.
3. Search the existing records. When one of them already settles the point, cite it and stop.
4. When a record seems wrong, collect the evidence first: the fact that changed, the failure that
   the record causes, or the later record or direction that conflicts with it.

## 2. Route the decision

1. Find the kind of record under the Routing procedure fact. The Record kinds and folders fact
   lists each kind and where its records live.
2. Find the repository that owns the decision. Read its record process and its index before you
   write, and route the work there. That process says how its records are written, changed and
   cited from other repositories.
3. Keep each point under one authority. When the owner's process has a form for adopting a
   decision from another repository, use it, and do not write a second record of the same point.
4. Keep distinct decisions distinct. When one decision holds parts with different owners, give
   each part its own record in its own owner's home.

## 3. Number the record

1. Read the index of the kind that the Index and numbering rule fact names.
2. Take the next free number under that rule.
3. Read the Reserved bands fact. A number inside a reserved band is never free, also when the
   index shows a gap there.
4. A change to an existing record keeps its number. A record that replaces another gets a new
   number.

## 4. Write the record

1. Use the template and the frontmatter of the kind. Fill each required field. Leave no
   placeholder.
2. Give the context, the decision, each alternative with the reason that it lost, and the
   consequences.
3. Cite each source so that a reader can find it again, in the form that the owner's process
   asks for. Do not point to a local scratch file.
4. Set the lifecycle status. A replacing record names the record that it replaces, and the old
   record points forward to the new one.
5. Update the index and each reciprocal link. Add the history entry that the kind asks for.

## 5. Check and commit

1. Run each command of the Record checks fact. Read the whole output. Fix each failure, then run
   all the checks again.
2. Write the commit subject with the Commit subject pattern fact. Fill in the kind, the number and
   the title.
3. Send the record through the owner's review and acceptance process. Set its status only as
   that process says.
4. From the plan or the specification that relies on the record, cite it by name and revision.
   Do not copy its text there.

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
  "filename": ".koinon/skills/record.md",
  "fields": [
    {
      "heading": "Record kinds and folders",
      "required": true
    },
    {
      "heading": "Index and numbering rule",
      "required": true
    },
    {
      "heading": "Record checks",
      "required": true
    },
    {
      "heading": "Reserved bands",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Routing procedure",
      "required": false,
      "default": "section 2 of this skill"
    },
    {
      "heading": "Commit subject pattern",
      "required": false,
      "default": "project policy"
    }
  ]
}
```
