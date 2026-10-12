# Coordinated work — decision

Issue [#140](https://github.com/rwcii/koinon/issues/140) requests the outcomes and non-goals.
The maintainer selected the reporting-consumer verdict rule (PDR 004, Decision 3;
[#306 comment 6119167156](https://github.com/rwcii/koinon/issues/306#issuecomment-6119167156)).
This plan proposes the role, reported-state, funding and discovery mechanisms for Gate B;
those mechanisms remain undecided (PDR 004, Decisions 1, 2 and 4). Work records and verdicts
remain reported data within the maintainer's task (WDR 002, Decisions 1 and 3).

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
3. Existing/default participant inboxes/cursors and participant-consumer claim continuation
   remain unchanged. A context clear that retains the native identity remains the same
   native session; a genuine successor uses its own native identity, never a guessed ID.
4. The worker reports a result, then a different consumer records accepted or rejected.
   The reporting consumer and reporting native session are excluded, including when that
   session changes consumer labels. A different native session using a different consumer
   is eligible, including a later session in the same worker role (PDR 004, Decision 3).
   Normal caller, repository and participant-consumer validation applies.
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

- Work records and coordinator verdicts are reported data; task authority comes from the
  maintainer's instructions (WDR 002, Decisions 1 and 3). Koinon's runtime carries the records
  and content-free notices rather than executing their text (ADR 001, Decision 3).
- Keep the existing repository store, advisory lease engine, memory stream, storage boundary
  and provider wake path. No scheduler, automatic assignment, forced takeover, budget
  enforcement or dependency tracking. Consuming-project setup is outside this deliverable.
- Role routing uses the existing launcher-declared family/role participant (PDR 001,
  Decision 3). Native identity comes from the transport (ADR 001, Decision 1); routing and
  the explicitly native-bound role-claim proposal are separate (PDR 004, Decision 1).
- Legacy explicit consumer proposals and claims are not silently converted to role claims.
  DeepSeek keeps its native-session command path; role routing is available only for families
  with verified participant roles. All supported transports can use report/verdict operations.
- Use `reported` between active and finished, as the issue recommends. This preserves v1's
  terminal finished state and gives rejection a natural return to open, without reopening
  an already completed item. Unreported v1 completion/withdrawal stays available.
- The loopback daemon/private store and native caller validation remain as in ADR 001,
  Decisions 1 and 2. Participant fencing remains as in PDR 001, Decision 5; default
  participant state continues across succession as in Decision 6. Fixed retry deadlines
  and existing v1 claims remain unchanged. Claims and notices are data (WDR 002, Decision 3).

## Issues and inputs

Delivers #140 only. #83's checkout claims remain advisory resources of the same engine.
#196's reset cycle, #206's human decision board and #217's rooms are separate; this verdict
is deliberately not a human-decision API.

Inputs: PROTOCOL.md (participants, memory/work, MCP/wakes), docs/WORK-ITEMS-V1.md,
docs/WORK-ITEMS-COMMANDS.md, docs/WORK-ITEMS-GO-STORAGE.md and root AGENTS.md.
