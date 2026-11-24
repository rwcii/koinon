# Chunk 03 — Captain

Cites `../decision.md` (Decided in the spec and kept: work items, `coordinate`; acceptance 3)
and `../definition-of-done.md` (`coordinate` as the Captain). Depends on chunk 01.

## Outcome

The coordinator of a running team dispatches ready chunks to pairs through work items, finds
work that loops, checks progress against the sprint's goal, and keeps the pairs' escalated
disagreements on their work items.

## Work

1. `agents/skills/coordinate/SKILL.md`, new steps with their sources:
   - Dispatch: read the parallel view; for each chunk whose dependencies have merged and whose
     owned items no running chunk owns, create or update its one work item (`work_create` or
     `work_edit`, with the chunk's outcome, owned items and the pair), then tell the pair.
   - Loop awareness: at each team check, a work item with a loop goes to the maintainer as a
     board card with what failed. Corrected in chunk 03: a loop counts failed attempts, not
     checkpoints, because Koinon sets a checkpoint at each start and release (PDR 002,
     Decision 5).
   - Goal and drift: compare each active work item with `decision.md` and
     `definition-of-done.md`; report work outside them as a card before it grows.
   - Disagreement record: when a pair escalates a disagreement, the driver writes it on the
     item with `work_update` (lifecycle `blocked`, and a blocker that gives both positions and
     the card reference, sent again on each update until the verdict), and the coordinator
     tracks it there. Corrected in chunk 03: a work report releases the claim and waits for a
     verdict (PDR 004, Decision 2), and Koinon keeps a blocker only while the item is
     `blocked`.
2. Contributor tests for the new steps' text, as for the existing coordinate steps.
3. Records and docs: PDR 002, Decision 5; PDR 004 (work results) for work items as the
   source of truth of dispatched chunks; the skills README; CHANGELOG. Added in chunk 03: the
   `sprint` skill reuses the issue's item only for chunks that one pair builds in order, and
   the sprint's Acceptance 3 gets a dated correction note for the loop rule.

## Tests

Contributor tests; a dry run of the steps against this sprint's own plan, in the pull request
evidence.
