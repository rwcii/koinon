# Sprint 2026-09-22 — test reliability — Review

The reviewer is the Codex agent; the author is the Claude agent. Each finding is listed with
its disposition.

## Decision and definition of done (reviewed at 7fef046)

| Finding | Disposition |
| --- | --- |
| The watchdog regression named only `setUp`, `tearDown` and `setUpClass`; the `select`/`epoll` case was an edge case without proof; outside a test the active fixture was not named. | Fixed in a4c5bc8: one parameterized subprocess test over eight blocking places, including `select` on an unwritten pipe; criterion 5 names the test or fixture. |
| A text scan cannot prove criterion 1; file-wide exceptions hide waits. | Fixed in a4c5bc8: a reviewed inventory proves migration; the scan is a regression guard; exceptions name the test and the expression. |
| Criterion 7 claimed that a never-finishing worker proves criterion 4. | Fixed in a4c5bc8: criterion 7 proves 5; criterion 4 has failed task and process tests. |
| Three full macOS runs per converting chunk were disproportionate. | Fixed in a4c5bc8: one bounded macOS repeat of the three modules with earlier timing failures, at the last converting chunk. |
| Criterion 10 required diagnostics for historic hangs that may not reproduce. | Fixed in a4c5bc8 and 54c1670: one bounded attempt each, recorded; an unknown cause gets a new issue; #63 closes on its own acceptance. |
| The problem stated a 2-to-10-second range (a wait uses 15); it cited unverified failures; it implied a known cause; it cited planned launchd work that is complete. | Fixed in 54c1670: evidence limited to #48 and its same-commit comment, cause stated as open, the 24-hour hang labelled as observed, the launchd reason removed. The author also withdrew two `test_session` failures that failed on all macOS jobs of one run, which points to branch defects. |
| Criterion 1 did not separate coordination budgets from product deadlines. | Fixed in 54c1670. |

Concurrence on `decision.md` at 54c1670 and on `definition-of-done.md` at a4c5bc8. Gate A was
approved by the user at 54c1670.

## Chunk plan (reviewed at e360f1d)

| Finding | Disposition |
| --- | --- |
| High: the `faulthandler` backstop did not reset with progress or exit, and had no numeric bound. | Fixed in a093d38: re-armed at every progress event with `exit=True`, at the watchdog bound plus 120 seconds; 900 and 1020 seconds on macOS, below the 1800-second job limit. |
| Subprocess waits (`communicate(timeout=…)` and others, 21 files) were outside the inventory; chunk 02 kept custom `asyncio.timeout` wrappers that the guard rejects. | Fixed in a093d38: the inventory lists subprocess waits, unassigned files go to 03, and wrappers are rewritten on `wait_until`. |
| `wait_until_sync` failed on any process exit, even an expected one, and could read inherited pipes. | Fixed in a093d38: the condition is checked first; only a premature exit fails; pipes are never read by the helper; asynchronous code in 03 uses `wait_until`. |
| Chunk 04 had no total bound, and could edit a file owned by 03. | Fixed in a093d38: four bounded runs, stop at the first reproduction, the chunk changes only its record, and test fixes go to the owning chunk. |
| Minor: the table said 03 empties the inventory, although 02 may merge later. | Fixed in a093d38: the last of 02 and 03 to merge confirms the inventory. |

**Verdict:** approved at a093d389bdab5a007569c7bda37aa19e4038aab4, no open findings.

## Build

| Finding | Disposition |
| --- | --- |
| The chunk-01 inventory scan read single lines and a few call names. It missed multi-line `subprocess.run(timeout=…)`, `join(…)`, `future.result(timeout=…)` and `to_thread(event.wait, …)` waits. | Fixed in #112 and #113: a broader sweep during review found the misses, and the owning chunk converted them. The inventory at 28f2e0d holds 30 entries, all `product-deadline`. |
| #113 merged with the temporary macOS repeat job still in `.github/workflows/`, contrary to the definition of done, and its pull request holds no record of the run. | The job ran only for branch `fix/test-process-waits`, so it was inert on `develop`. The close-out pull request removes it, and #48 records the run. |

## Ownership

Codex builds chunk 03 after chunk 01 merges. Claude builds chunks 01, 02 and 04. Edits to
`tests/wait_inventory.py` and to temporary CI jobs are coordinated between the two.
