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
