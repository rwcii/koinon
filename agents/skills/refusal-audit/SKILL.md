---
name: refusal-audit
description: "Audit a whole repository for refusal traps, once and on request. A refusal trap is text, a setting, a hook, a code condition or a test that blocks approved work with no source. Produces one inventory grouped by root cause, and one fix proposal for each root cause. Use when agents refuse legitimate work or ask for the same approval again and again."
---

# Refusal audit

Koinon's procedure source is [PDR 008](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/008-portable-skills-explicit-adoption.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Read the project's root instruction files first, and the Instruction files fact. Follow the
procedures that they name.

In this skill, the **owner** is the person in the Owner fact. By default, that is the human who
is accountable for the project.

## What this audit finds

A **refusal trap** is content that stops, limits or delays work, or asks for approval, where no
source covers the case. Agents read it as a rule and then refuse work that the owner approved.

Most traps start the same way. Someone records a decision for one case. Then the text states it
as a general rule. A later agent cannot see the case or the source, so it applies the rule
everywhere. Copies of the rule spread into other files, and each copy can get wider.

A trap can be in any of these carriers:

- agent instruction files, such as `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.cursorrules` and
  `.github/copilot-instructions.md`;
- skills, prompts, commands and their fact files;
- procedures, runbooks, plans, sprint files, templates, contributing guides and other docs;
- agent settings: permission allow and deny lists, sandbox settings, approval modes;
- hooks: agent hooks, Git hooks and pre-commit configuration;
- CI workflows, branch rules as code, and setup scripts;
- code conditions that refuse, skip or fall back, and their error texts;
- tests that assert a refusal or a limit;
- issue bodies, pull request templates and other text that agents read as instructions.

This is a one-time audit that you run by hand, on request. The agent that receives the request
runs it locally, from start to end. It is not a CI job or a merge check.

The audit applies one rule to the content that is already in a repository: each constraint
needs a source that covers its case. The diff-level partner of this audit is the skill in the
Diff-level review fact. That review checks only what each change adds or changes. If the
project has its own record for the rule, the Rule source fact names it. Cite that record.

## What a trap is not

Keep these. They are not findings:

- A constraint with a source that covers its case (see step 4).
- A routine argument check, such as an argument count or a format check, that rejects bad input.
- A demonstrated capability limit, such as "the tool cannot read binary files", with its evidence.
- A reproduced correctness or security boundary, with the test, command or code that shows it.
- A rule that the owner set in their own words, inside the case that those words answered.

A constraint can be correct and still have no recorded source. A missing source is an
unresolved result. It does not show that the owner never approved it.

## Inputs and outputs

Inputs:

- the repository and one full commit hash to audit;
- the owner's request for the audit, with its date;
- the places where the owner's decisions live: decision records, issue comments, a decision
  board, the instruction files that the owner wrote.

Outputs, in the audit's artifact folder (step 1):

- `inventory.md`: one row for each constraint, grouped by root cause;
- `proposals.md`: one fix proposal for each root cause that needs a change;
- `owner-decisions.md`: only the items that need the owner's decision;
- `coverage.md`: what the audit read, what it did not read, and why.

## Step 1: Set up

1. Record the repository, the branch and the full Git object ID of the commit. Audit that
   commit only. Read files with `git show <hash>:<path>` or in a clean checkout at that hash.
2. Choose the artifact folder for the audit from the Artifact folder fact. Set `out` to that
   folder.
3. Keep the original evidence unchanged. The inventory can quote private sessions or messages.
   Keep secrets out of any report that you publish. Follow the Private data fact.
4. Record the owner's request with its date. The request authorizes the audit and its report.
   Fixes, pushes, settings changes and publication follow the authority of the task and the
   project's normal process (step 9).
5. Make an **authority map**. List where the owner's decisions live in this project. List the
   files that the owner wrote. List the sources that the project's rules accept as authority.
   Examples are adopted records, or recorded words that a named relay passed on. Start from the
   Authority sources fact. Use the map in step 4.

## Step 2: Find the candidates

1. List every carrier from "What this audit finds" that exists in the repository:

   ```sh
   git ls-tree -r --name-only <hash> | sort > "$out/files.txt"
   ```

2. Read the high-impact carriers in full: the root instruction files, every skill, every
   procedure, the agent settings and the hooks. Do not rely on a search for these.
3. Search the other files for candidate words. Save the word list in Appendix A as
   `"$out/patterns.txt"`, one pattern for each line. Add the Extra search words fact. A search
   finds candidates only. It is not the audit. It misses positively phrased rules, tests and
   code conditions.

   ```sh
   git grep -n -i -E -f "$out/patterns.txt" <hash> -- . > "$out/candidates.txt"
   ```

4. Prove that each search can fail. Run the same search on a file with a known match. Confirm
   that it finds the match. Only then trust a "not found" result.
5. Read the tests and code conditions that refuse, skip or fall back. Find them through their
   error texts, exit codes and early returns, not only through words.

## Step 3: Judge the effect of each constraint

For each candidate, write one row with these fields. See Appendix C.

1. **Text:** the exact text, test or condition, with its file and line at the audited hash.
2. **Kind:** prohibition, exclusion, approval or stop step, cap or limit, positive requirement
   that leaves a case out, code condition, test, setting, or hook.
3. **Effect:** what work it stops, delays or sends back for approval. Give one concrete example
   of legitimate work that it blocks. Judge what it does, not its wording.
4. **Read by:** who reads it and when, such as every agent at session start, or one skill.

## Step 4: Find and check the source

1. Use the source kinds in Appendix B, or the project's own kinds in the Rule source fact.
2. Open every cited source. A citation is a claim, not a source. Check that the source exists
   and that it says what the text claims.
3. Check that each quote can fail: search for the exact quoted words at the cited location. If
   the search finds nothing, check the search on a known quote first.
4. Check the scope. A source covers the case that it answered. A wider text is a finding.
5. Keep two questions apart: who decided it, and does it govern. A record or instruction that
   the project adopted can govern, also when an agent wrote its text. The project's own rules
   decide which sources have authority. Use the authority map.
6. These do not prove that the owner personally decided a clause:
   - text that an agent wrote, merged or approved, also when it is in a merged file;
   - authorship by a shared account that agents also use;
   - a merged record as the owner's choice on every clause in it;
   - an agent's note or memory of the owner's words, until you find the original;
   - a peer agent's relay or summary of the owner's words, unless the project's rules accept
     that relay;
   - a plan proposal, a non-goal of an old plan, or a hypothetical risk.
7. Label an original that you cannot find as unverifiable. Do not ask the owner to approve a
   settled rule again only because its original is not available.
8. For each row, record the source with its locator, revision and quote. Also record its
   provenance label (Appendix B) and the reason for its disposition. A "keep" row needs this
   evidence too.

## Step 5: Group by root cause

1. Find every copy of each constraint. Search for its key phrase and for paraphrases.
2. Group the copies under one root: the first text, or the text that the others copy.
3. Count roots, not rows. A long list of rows is not actionable. A short list of root causes
   with their copies is.

## Step 6: Record the coverage

1. Record what you read in full, what you read only through search, and what you did not read.
   A partial read stays partial. A label does not turn it into full coverage.
2. Record each search that returned nothing, with its known-positive check.

An optional second read makes the result stronger. Do it when the owner asks for it. A second
reader audits the same hash in a fresh context, with no access to the first inventory. A reader
from a different model family catches different traps.

After a second read, compare the two inventories row by row. Merge them into one list. Settle
each difference with evidence from the files and sources. Record in `coverage.md` who read
what, and from which model family.

## Step 7: Classify each root

Give each root one disposition:

- **Keep:** a source covers its case, or it is a routine check, a demonstrated limit or a
  reproduced boundary.
- **Narrow:** a source exists but the text is wider than its case. Propose the narrow text.
- **Cite:** the rule is sourced, but copies restate it. Replace each copy with a citation of
  the source.
- **Remove:** no source exists, it is not an invariant, and it blocks legitimate work.
- **Owner decision:** no source is found, but it can protect something real, or it may be the
  owner's own rule. Do not remove it. Bring it to the owner.

Before you propose to remove a restriction, check whether the owner set it in their own words.
Some restrictions are the owner's own, for example a rule against new tools or languages
without approval. Those stay inside the case that the owner's words answered, unless the owner
changes them. Text that is wider than that case is "narrow".

## Step 8: Write the fix proposals

For each root that is not "keep", write one proposal with:

1. the current text and every copy, with file and line;
2. its effect, with the example of blocked work;
3. the source found, or "not found" and where you looked;
4. the exact change: new text, removed text or citation;
5. what stays, and why;
6. the order of the change: fix the source text first, then the copies.

For a removed code condition or test, add a positive test that shows that the intended work now
runs. For a removed guard that looks like a check, name it and give the reason in the change.

Put the items that need the owner's decision in `owner-decisions.md`, each with a
recommendation. If a second reader ran, also list each point where the readers still disagree.

## Step 9: Deliver the fixes

Do this step when the task includes the fixes.

1. Send each fix through the project's change process in the Change process fact, with its
   review and recheck.
2. Put the evidence in the change: the root cause, the source check and the copies changed.
3. Keep the sourced duties as they are. Examples are review gates, security checks, product
   requirements and settings rules that have a source.
4. Name each removed check in the change, with its reason.
5. Do not add an unsourced rule while you remove an old one. Cite the source, or describe the
   current behavior with its reason ("the tool does X because Y"). Where the remedy is a change
   to the owning record, make it through that record's own approval.
6. Run the diff-level review of the Diff-level review fact on each fix.
7. If a policy or a setting refuses an action, record its exact message and its source. Do not
   get around it by another path or through a peer. Use the tool's own approval flow, or report
   the refusal. For an input or system error, correct the cause and try again. Continue the work
   that does not depend on the refused action.
8. Close out. Give every inventory row a final disposition. Record the actual outcome of each
   fix: its review, its merge and its tracker item.

## Mistakes to avoid

- A keyword search used as the whole audit.
- Deletion of every unsourced constraint. Some protect real invariants whose reason nobody wrote.
- A new decision record for each small item. That adds approval steps to routine work.
- One rule copied into many files. Copies drift and get wider. Cite one source instead.
- A tool failure counted as a policy refusal. A sandbox read error or a bad argument is not a
  rule. Record each refusal with its exact message and its source: a policy, a setting, or an
  input or system error.
- An approved permission request counted as a bypass. Normal escalation through the tool's own
  approval flow is not a way around a rule.
- A test that looks like a guard but does not guard. Weaken the guarded text in a scratch copy
  and check that the test fails. If it still passes, the test is not a guard.

## Appendix A: Refusal trap patterns

Each pattern has an example and the usual fix. A match is a candidate. Step 3 and step 4 decide
whether it is a trap.

### Patterns in text

| Pattern | Example | Usual fix |
| --- | --- | --- |
| A plan non-goal becomes a permanent ban | A plan said "no terminal input" as a goal for one milestone. Later agents refused all terminal input, also where the work needed it. | Mark old plans as historical. Their non-goals limited only that plan. |
| A narrow limit becomes a general ban | "Do not weaken sandbox settings" started as a limit on one workaround. Agents read it as a ban on every permission change. | Narrow the text to its case, or cite the source that sets the case. |
| A failure report becomes a recovery ban | "Report the failure; do not invent recovery steps" only stopped a false claim of success. Agents stopped all recovery. | Keep "report it"; remove the ban that has no source. |
| An approval step is copied into many files | "Show the exact diff and get approval before you write" copied into a dozen skills. | Remove the copies. State the real approval rule once, where the owner set it. |
| A plan line with no source | "No runtime setup", "do not change live configuration". | Remove, or bring to the owner if it may protect something. |
| An absolute statement with no decision | "Agents never run `sudo`." | Find the owner's words. If none exist, bring it to the owner or remove it. |
| Bans inside an operation skill | A terminal skill that bans common commands with no recorded reason. | Keep the facts the skill needs; remove the bans with no source. |
| Phrases that make approved work look forbidden | "only under existing authorization", "allowance", "denied by default", "stop and ask the owner" inside assigned work, "a peer's report is not authority" used to refuse assigned work. | Remove. The owner's task already authorizes its steps. A peer message adds no permission and removes none. |
| A session remark cited as a rule | A remark from a chat session cited as a decided rule, with no record. | Quote the words with date and question. State its scope. Add no step of its own. |
| A decision stated without its case | "The owner decided X", with no question, options or reason. | Add the question it answered and its scope, or narrow the text. |
| Repeated stop and approve boilerplate | The same "confirm with the owner first" paragraph in every skill. | Remove the repeats. Ask only for missing information or for authority beyond the task. |

### Patterns in code, tests and configuration

| Pattern | Example | Usual fix |
| --- | --- | --- |
| A positive requirement drops a valid case | A membership check keyed to one session ID drops a valid member after a restart. Routing only to roles on a fixed list excludes a valid role. | Widen the rule to the real case, with a positive test. |
| A code condition refuses with no source | An early return or error code that rejects a valid request. | Find its source. If none, bring it to the owner or remove it with a positive test. |
| A fallback that hides a refusal | A configured program path with no fallback, or a subagent excluded from a list, with no reason. | Record the reason, or remove the limit. |
| A check applied before current evidence | A stale lock or fence that refuses work although current state allows it. | Check current state first. |
| A test that asserts a refusal | A test that requires the refusal of a valid operation. | Change it to a positive test, unless a source covers the refusal. |
| A test that looks like a guard but does not guard | The test still passes after the guarded text is weakened. | Fix the test so that it fails when the guard is removed. |
| A parser format gate | Line endings or quoted variables rejected although nothing runs them. | Accept the valid input, or record the reason. |
| A hook that blocks the whole product | A hook proposed for one case that blocks every session. | Scope the hook to its case, or remove it. |
| A settings deny list entry with no source | A deny rule that blocks a command that approved work needs. | Find its source. Change the setting through the project's own rule for settings changes. |
| A native sandbox limit read as a project rule | "The sandbox cannot write here" restated as "never write here". | Describe the limit as a fact of the tool, not as a rule. |

### Candidate search words

Save the block below as `patterns.txt`, one pattern for each line, and pass it to `git grep -f`.
Add the words of the project's own language from the Extra search words fact. A match is a
candidate. It is not a finding.

```text
never|must not|do not|don't|may not|cannot|forbidden|prohibit|not allowed|disallow
only (if|when|under|after)|unless|except|exclusively|restricted to
approv|permission|authoriz|allowance|sign-off|confirm (with|first)
stop and|wait for|ask (the )?(owner|maintainer|user)|before you (write|push|run|change)
denied by default|deny|refus|reject|block|gate|fence|lock
no (new|runtime|live|setup|change)|out of scope|non-goal
at most|limit|cap|maximum|threshold|timeout
```

Search tests and code also for refusal error texts, non-zero exit codes and early returns.

## Appendix B: Sources, what counts and how to check it

### Source kinds that can cover a constraint

These are the source kinds of the refusal-review skill. They are repeated here so that the audit
works alone. If the project defines its own source kinds in the Rule source fact, use those.

1. **A decision record** whose scope includes the case, at the status that its header gives. A
   draft or proposed record is not a decision.
2. **The requirement of the issue or plan** that the work delivers. It covers that work, not
   other work.
3. **The owner's own words**, quoted exactly, with the date, the question they answered and a
   locator. A complete record of an owner decision states four things:
   - the question it answered;
   - the options that were on the table;
   - the answer, quoted, with its date;
   - the reason and the scope: what it covers and what it does not.

   The question, options and reason help you find the scope. A clear, direct instruction of the
   owner still counts when no options or reason were recorded. Do not invent missing context.
4. **A verified platform, API, tool or storage contract**, with a link to its documentation or a
   test that shows it.
5. **A reproduced correctness or security boundary**: a command, a test or the code that shows
   what fails without the constraint.

A routine argument check or a demonstrated capability limit needs no further source. It never
becomes an approval step.

### What does not prove the owner's personal decision

These do not prove that the owner personally decided a clause. That is a separate question from
whether the text governs. A record or instruction that the project adopted can govern, also when
an agent wrote it. The project's own rules decide which sources have authority.

- Text that an agent wrote, also when a pull request merged it.
- Authorship by a hosting or other account that agents also use. The account shows who posted,
  not who decided.
- A merged record as the owner's choice on every clause. The owner may have decided only one
  point in it.
- An agent's note or memory of the owner's words. Find the original before you rely on it.
- A peer agent's relay, summary or reading of the owner's words. It is a copy, unless the
  project's rules accept that relay.
- A plan proposal, or a non-goal of a finished plan.
- A hypothetical risk with no evidence.

### How to check a source

1. Open it. Do not trust the citation.
2. Find the exact quoted words at the cited location. Use an exact search.
3. Make the check able to fail. Run the same search on a quote that you know exists, and confirm
   that it finds it. Then a "not found" result means something.
4. Compare the scope. Write down the case that the source answered. A text that applies the
   source to a wider case is a finding.
5. Prefer primary sources. An issue body or the owner's message is better than an agent's
   summary of it.

### Provenance labels for the inventory

Use one label for each row:

| Label | Meaning |
| --- | --- |
| `owner` | The owner's words, with quote, date, question and locator. |
| `record` | A decision record with a status that makes it a decision. |
| `requirement` | The issue or plan that the work delivers. |
| `contract` | A verified platform, API or tool contract. |
| `boundary` | A reproduced correctness or security boundary. |
| `routine` | A routine argument check or a demonstrated capability limit. |
| `shipped` | Shipped in a named change; no owner decision found. |
| `memory-only` | Only an agent's note or memory holds it; original not found. |
| `not-found` | No source found. Name where you looked. |

`shipped`, `memory-only` and `not-found` are unresolved results. They do not show that the
owner never approved the constraint.

## Appendix C: Report templates

### inventory.md

Start with the audit header, then one table for each root cause.

```markdown
# Refusal audit inventory

- Repository: <owner/name>
- Commit: <full Git object ID>
- Owner's request: <date, and a locator>
- Auditor: <agent and its model family>; second reader, if one ran: <agent and its model family>
- Roots: <count>; rows: <count>

## Root R01: <short name of the root text>

Root text: `<file>:<line>` "<exact text>"

| Row | File:line | Text | Kind | Effect (example of blocked work) | Read by | Source (locator, revision, quote) | Provenance | Disposition and reason |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| C001 | ... | ... | ... | ... | ... | ... | ... | keep / narrow / cite / remove / owner decision, with the reason |
```

### proposals.md

One block for each root that is not "keep".

```markdown
## Root R01: <short name>

- **Current text and copies:** <each file:line, with the exact text>
- **Effect:** <what it stops, with one concrete example of legitimate work it blocks>
- **Source:** <what you found, with its locator; or "not found", and where you looked>
- **Change:** <the exact new text, removed text or citation, for each copy>
- **What stays:** <the parts that keep a source, and why>
- **Order:** <fix the source text first, then the copies; name the repositories in order>
- **Tests:** <the positive test that shows the work now runs, where code or tests change>
```

### owner-decisions.md

Only the items that need the owner, each with a recommendation. If a second reader ran, also list
each point where the readers still disagree. Use the project's recommendation form if it has
one; otherwise use this form.

```markdown
## Recommendation: <the action, in one line>

**What:** the change, its source, and what gets recorded.

**Why:** the trap it removes, with the example of blocked work.

**What stays:** the part that may protect something, and why we keep it or ask.

**Disagreements:** if a second reader ran, any point where the two readers still disagree.
```

### coverage.md

```markdown
| Reader | Read in full | Read through search only | Not read | Reason |
| --- | --- | --- | --- | --- |
```

Also record each search that returned nothing, with the known-positive check that proves the
search can find a match.

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
  "filename": ".koinon/skills/refusal-audit.md",
  "fields": [
    {
      "heading": "Owner",
      "required": false,
      "default": "the human accountable for the project"
    },
    {
      "heading": "Instruction files",
      "required": false,
      "default": "the root instruction files of each agent family"
    },
    {
      "heading": "Artifact folder",
      "required": false,
      "default": "the ignored task folder that the project names, else a folder outside the repository"
    },
    {
      "heading": "Authority sources",
      "required": false,
      "default": "built at run time in step 1"
    },
    {
      "heading": "Rule source",
      "required": false,
      "default": "the source kinds in Appendix B"
    },
    {
      "heading": "Extra search words",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Diff-level review",
      "required": false,
      "default": "refusal-review"
    },
    {
      "heading": "Private data",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Change process",
      "required": false,
      "default": "project policy"
    }
  ]
}
```
