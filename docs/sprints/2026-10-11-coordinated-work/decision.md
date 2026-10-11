# Coordinated work — decision

Issue [#140](https://github.com/rwcii/koinon/issues/140) supplies the approved outcomes and
non-goals. This plan proposes the reported-state model and its ownership/storage mechanics
for Gate B. It grants no permission to perform a task or act on a coordinator verdict.

## Problem

A coordinator cannot route an assignment to the next native consumer of a worker role,
record a verdict on a reported result, and recover the conversation from the work item.
Reusing a stable claim consumer conflates routing with ownership. The Go runtime has a
post-commit memory-change callback, but its daemon does not bind it to production work wakes;
the historical subscription description is not evidence that this path already works in Go.

## Acceptance criteria

1. A proposal to a repository's family/role is found by its current native holder after
   succession. An unheld or conflicting role is reported explicitly, never guessed.
2. New role-assigned claims belong to the accepting native session. A successor cannot
   update, renew, release, finish or replay its predecessor's ownership; start conflicts
   while that lease is live and succeeds after release/expiry with a fresh generation.
3. Existing participant inboxes/cursors and legacy participant-consumer claim continuation
   remain unchanged. A context clear that retains the native identity remains the same
   native session; a genuine successor uses its own native identity, never a guessed ID.
4. The worker reports a result, then a different consumer records accepted or rejected.
   The reporter cannot self-verdict by changing consumer labels, including after native
   succession within the same worker participant.
5. Rejection returns the item to open with a new scope revision; the report, verdict and old
   and new scopes remain readable. Acceptance yields finished/completed and starts the
   existing finished-item retention period. Reported work is not finished or time-pruned.
6. Assignee and creator discover relevant proposals/reports/verdicts through bounded current
   queries and retained catch-up after lost/coalesced wakes, reconnect or restart. Duplicate
   handling and fixed-key retries have no additional effect.
7. Every wake is content-free and targets verified native recipients. No per-transition
   model notice is promised; coalescing never loses the underlying retained work record.
8. Migration preserves old claims/records. Existing funded end operations remain possible
   at saturation; a committed report reserves its later verdict, so full ordinary storage
   cannot leave every reported item unable to finish.
9. Native ownership, retries, migration, reports/verdicts, scope history, capacity and wake
   recovery pass synthetic tests on Linux/macOS, with protocol/command/guide documentation.

## Constraints

- Same-user reported data throughout. An accepted result is a coordinator's assertion,
  not human approval or a permission grant. No task text, reference or notice is executed.
- Keep the existing repository store, advisory lease engine, memory stream, storage boundary
  and provider wake path. No scheduler, automatic assignment, forced takeover, budget
  enforcement or dependency tracking. Consuming-project setup is outside this deliverable.
- Role routing is separate from ownership. Use the existing launcher-declared family/role
  participant; add no model-supplied native identity or role-registration operation.
- Legacy explicit consumer proposals and claims are not silently converted to role claims.
  DeepSeek keeps its native-session command path; role routing is available only for families
  with verified participant roles. All supported transports can use report/verdict operations.
- Use `reported` between active and finished, as the issue recommends. This preserves v1's
  terminal finished state and gives rejection a natural return to open, without reopening
  an already completed item. Unreported v1 completion/withdrawal stays available.
- Preserve loopback, private state, native caller validation, participant fencing and fixed
  retry deadlines. Claims and notifications confer no authorization.

## Issues and inputs

Delivers #140 only. #83's checkout claims remain advisory resources of the same engine.
#196's reset cycle, #206's human decision board and #217's rooms are separate; this verdict
is deliberately not a human-decision API.

Inputs: PROTOCOL.md (participants, memory/work, MCP/wakes), docs/WORK-ITEMS-V1.md,
docs/WORK-ITEMS-COMMANDS.md, docs/WORK-ITEMS-GO-STORAGE.md and root AGENTS.md.
