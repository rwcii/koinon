# Portable procedures — review

## Criteria review

codex-koinon reviewed the chunk 01 criteria before the plan, 2026-11-17.

| Correction | Disposition |
| --- | --- |
| A Koinon fact file is required for each skill with required facts; Koinon has no tools for the new ones | Accepted: the seven are offered, not adopted; a missing fact file is allowed for them by name, and present files are still checked |
| `TestTeamSkills` and `TestOpenCodeCommandSkills` pin the skill lists | Accepted: both list the seven |
| The decision must be recorded in full, with the board card only as a label | Accepted: PDR 008 Decision 5 holds the words, date, question, answer, reason and scope |
| Record both copyright answers and the relayed words as relayed | Accepted |
| Embedding the skills changes runtime inputs: full suite, native vet, four builds and exact-head CI | Accepted |
| Pin each final file's hash in a committed test | Accepted: the test hashes only the public `agents/skills/NAME/SKILL.md` against constants, so CI never reads the drafts; a later skill change updates its constant in the same pull request. The pull request also lists each draft hash and the one-line diff |
| Replace the whole backticked placeholder, so the citation renders as a link | Accepted |

## Plan review

codex-koinon reviewed a0edec6, 2026-11-17: two P2 findings and two notes.

| Finding | Disposition |
| --- | --- |
| PLAN-F1 (P2): the plan can end with no released archive that the installer can use | Accepted: a delivery check after the release that carries chunk 02; #732 stays open until it passes. It authorizes no merge to main, tag or publication |
| PLAN-F2 (P2): no plan file pins the seven approved draft hashes | Accepted: chunk 01 lists them, as privately supplied inputs |
| N1: use the required full-suite command | Accepted: `go test -race -timeout 30m ./...` |
| N2: test symbolic links in parent and root folders, and an archive entry that escapes | Accepted, as fixture tests with no host installation |

## Changes found while building chunk 01

`TestSkillTextHasNoLimitPhrases` (#487) refused three draft sentences ("Do not guess", "not
proof" twice, "adds no approval step") and the drafts' opening, which lacked "(WDR 002,
Decision 1)". The maintainer saw the exact diff and approved it on 2026-11-17 ("Approve the
changes"); the plan's "one change" became these changes.
