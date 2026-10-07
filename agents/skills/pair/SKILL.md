---
name: pair
description: Pair program in one shared checkout with one driver and one observer, committed handoffs, fixed acceptance criteria and bounded review. Use when the maintainer asks agents to pair or trade the driver role.
---

# Pair programming

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first.

Complete the maintainer's agreed increment, then swap or finish. A possible improvement is not
another assignment. Pairing grants no permissions beyond the maintainer's authorization.

## 1. Fix the scope

Before the first edit, the driver records observable acceptance criteria and exclusions in
the issue, one line each. The observer agrees or objects once; settle that response before
editing. Both work from this scope. Only the maintainer can expand it. Existing repository security
and preservation requirements remain mandatory boundaries, even when not repeated in the issue.

Agree on the shared work branch and checkout, the first driver, and the next complete
increment. Swap at a committed, test-passing increment rather than by elapsed time.

## 2. Drive and observe

Only the driver edits the shared checkout, stages changes, commits or runs commands that
change its state. The observer reads committed checkpoints with `git show` or
`git diff <base> <hash>` and reproduces findings in a separate scratch worktree or temporary
location. Do not inspect or modify the driver's changing working tree.

The observer stays focused on the agreed increment and sends no running commentary while
the driver works. Interrupt only to stop an imminent destructive or security mistake.
Send all other findings together at the committed checkpoint.

## 3. Review once

Make one review pass per increment. A blocker needs a demonstrated, reachable failure and
either a violated written acceptance criterion or a concrete security or data-loss defect
against the repository's mandatory boundaries. State the failing scenario, evidence and
criterion or boundary. A speculative hazard, style preference or optional improvement does
not block completion.

The driver gives each finding one triage response: blocker or dropped, with a one-line
reason. Drop optional improvements without fixes, new issues or another work queue. Only
the maintainer can reopen a dropped item. If a material disagreement about a criterion or mandatory
boundary remains, take it to the maintainer once; do not continue a peer debate or suppress a
demonstrated mandatory-boundary violation.

## 4. Verify and hand over

Fix accepted blockers together. Follow-up review verifies those fixes, their affected
behavior and required checks; carry forward the review of unchanged content. A new commit
does not start another general audit. A newly exposed blocker must meet the same evidence
test; otherwise the increment is finished.

When the criteria pass and no accepted blocker remains, hand over or finish. The driver
commits under the repository's signing and sign-off rules and sends `wheel to you at <hash>`
with the check results. The new driver verifies the hash and clean checkout before editing;
the former driver becomes the observer. Do not swap while fixes or checks are unfinished.

At the final checkpoint, use `check`, `peer-review` and `ship` within the maintainer's authorized
scope. The review verdict names the current hash; a fix-only commit uses the incremental
review rule. Required checks and merge gates still apply.
