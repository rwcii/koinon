---
name: preflight
description: "Run the adopting project's enabled review lenses locally before a pull request, then post one result comment bound to the head."
---

# Preflight

Koinon's procedure source is [PDR 008](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/008-portable-skills-explicit-adoption.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

A preflight is an author check, not an independent review. The project's independent reviews
stay required. Their round and stop rules apply to those reviews, not to preflight runs.

Load the Review run command, Review publish command, Lens policy, Base ref, and Docs and agent
files facts. When the
project declares a Review tool pin, use a review tool built from that exact source revision.

## Before you start

1. Commit your change. The run reviews commits, not the working tree.
2. Make sure that the lens policy exists at the base commit.
3. Get the family route and the worker image under the Route and image rules fact.
4. Make sure that the report folder is ignored by Git.
5. Run the docs gate below. A lens reviews the docs only as they are at the head.

## 1. Run the lenses before the pull request

1. Run the Review run command against the Base ref. Use the pull request's base branch.
2. The command runs each enabled lens once. Each lens gets a fresh worker.
3. Give the run no forge credential. No worker or candidate test gets a credential.
4. Find the reports and usage files in the Report folder for the head and run.
5. Exit status 0 means that each lens approves.
6. Any other status means that a lens rejects or failed.

## 2. Handle the findings

1. Read each lens report in the folder that the command prints.
2. For a lens that failed, read the reason that the command prints.
3. Give each finding its outcome under the Review procedure fact.
4. Apply the fixes that those outcomes require.
5. Send work outside the item to the project's decision owner as a decision.
6. Commit the fixes.
7. Run section 1 again at the new head. Then section 3 has a result for the pull request head.

Each run writes usage files with a stable event key for each entry. Import each usage file with
the Usage import step. When that fact is "none", report the usage files as not imported.

## 3. Post the result after the pull request opens

1. Run the Review publish command for the pull request and its head.
2. Give the author's forge credential to the publish command only.
3. The command posts one comment with the newest result of that head.
4. It refuses when the pull request head differs from the result head.
5. When the result is already posted, it prints the link and posts nothing.
6. A clear forge refusal is a failure. Read the reason and correct its cause.
7. A network or server error can follow an accepted comment. So can an unreadable answer.
8. Read the pull request comments before you retry.
9. Retry only when the result comment is absent.

The comment shows a value that is not available as `unknown`. Never show it as zero.

## Docs gate

Koinon's procedure source for this gate is WDR 004, Decision 7. Before you run the lenses,
compare the files in the Docs and agent files fact with the code at the head:

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

## Standard lenses

A project turns on a standard lens with a `## NAME` section that has no text in its lens
policy. The Review run command gives that lens's worker the brief below, from this skill. The
catalog's setup item `setup.preflight-runner` is a runner that does this; `koinon preflight
brief NAME` prints a brief from the installed release. Koinon's source for the wording lens is
WDR 004, Decision 8.

### wording

Check the content that users see and the data that users can read in the change: UI,
dashboard and website text, command output and messages, error text, and the names of cookies,
local storage keys, events, properties, headers and identifiers in exported or published data.
Report each word or name that frames the work in alarming or pejorative terms when an accurate,
neutral term exists, for example "profile", "tracking" or "fingerprint" where "visitor ID",
"visit" or "site data" is just as accurate. Give the text, its file and line, and an accurate,
neutral replacement. The replacement must not hide what the thing does: wording that misleads
is a finding too. Settled policy texts and fixed third-party API names are not findings. Code
comments, README files, agent files and other repository documentation are outside this lens.
When the change has no such content, approve.

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
  "filename": ".koinon/skills/preflight.md",
  "fields": [
    {
      "heading": "Docs and agent files",
      "required": false,
      "default": "the README, the user docs, the agent instruction files such as AGENTS.md and CLAUDE.md, and the project's skills"
    },
    {
      "heading": "Review run command",
      "required": true
    },
    {
      "heading": "Review publish command",
      "required": true
    },
    {
      "heading": "Lens policy",
      "required": true
    },
    {
      "heading": "Base ref",
      "required": false,
      "default": "the pull request's base branch"
    },
    {
      "heading": "Review tool pin",
      "required": false,
      "default": "none"
    },
    {
      "heading": "Route and image rules",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Report folder",
      "required": false,
      "default": "the folder that the run command prints"
    },
    {
      "heading": "Review procedure",
      "required": false,
      "default": "project policy"
    },
    {
      "heading": "Usage import step",
      "required": false,
      "default": "none"
    }
  ]
}
```
