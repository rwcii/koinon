# Sprint 2026-09-22 — test reliability — Decision

## Problem

The test suite coordinates with asynchronous services, subprocesses and background tasks by
waiting for a condition under a fixed wall-clock deadline. About 70 `asyncio.timeout` or
`asyncio.wait_for` sites and about 60 deadline loops in about 30 files each choose their own
duration, from 2 to 10 seconds. On macOS runners these deadlines expire before the condition
holds: `test_notification_runtime` (#48), three `test_session` tests, and one
`test_session_supervisor` test have failed this way while the same commit passed on another
job. A timeout usually reports only `TimeoutError` or a fixed message, so a failure does not
show whether the runner was slow or the code under test was wrong.

The opposite failure also occurs. A test that waits with no bound can hang indefinitely:
`test_work_activation` blocked for about nine hours (#63), and on 2026-09-22 a run of
`test_upgrade_probe` and `test_upgrade_manual` was still blocked after 24 hours. The required
`Tests` job has no `timeout-minutes`, so a hang holds a runner up to the six-hour platform limit
and produces no diagnostics.

Both failures cost a CI run of 10 to 15 minutes or more, and both teach contributors to rerun
instead of investigating. Planned macOS supervision tests must wait through launchd's
ten-second restart interval, which the present deadlines cannot cover.

## Acceptance criteria

1. Every wait that coordinates a test with asynchronous work goes through one shared helper,
   and no test file chooses its own coordination deadline.
2. The helper's deadline has one generous default and one scale factor that CI can raise; a
   wait that must cover a longer interval states that interval at the call.
3. When a wait times out, the failure names what the test was waiting for and the last state
   it observed.
4. When the task or process being waited on has already failed, the test reports that failure,
   not a timeout.
5. A test run that stops making progress (in a test, its setup or teardown, or a class or
   module fixture) ends within a stated bound with a nonzero exit status, names the test or
   the fixture that was running, and prints the stack of every thread.
6. The required `Tests` job has a job timeout.
7. A controlled test with a worker that never finishes proves criterion 5: bounded failure
   with the diagnostics retained. Criterion 4 is proved separately, with a task and a process
   that have already failed.
8. Tests that measure a product deadline or expiry keep their timing assertions unchanged.
9. The suite's result is unchanged when nothing hangs: the same tests pass on Linux and macOS.
10. For each of the two observed hangs (#63's `test_work_activation`; `test_upgrade_probe` with
    `test_upgrade_manual`), one bounded reproduction attempt runs under the watchdog, and its
    exact command, revision and result are recorded. A reproduced hang is fixed with a test, or
    recorded in an issue on this milestone with the watchdog's diagnostics. A hang that does
    not reproduce is recorded as not reproduced, and its issue stays open while its cause is
    unknown.

## Constraints

- Test code and CI configuration only. No runtime module changes, unless a hang in criterion 10
  is traced to a runtime defect; that fix is then its own chunk with its own review.
- Tests use synthetic peers and synthetic services only, and never touch live sessions or user
  services (root `AGENTS.md`).
- Standard library only.
- Linux and macOS, Python 3.11 to 3.13, as in `.github/workflows/tests.yml`.
- `python3 -m unittest discover -s tests` keeps working for contributors; the root `AGENTS.md`,
  `CONTRIBUTING.md` and the `check` skill name the command that CI runs, if it changes.
- Waits that exercise a product deadline or expiry are not coordination waits and are out of
  scope (for example `test_subscriptions.test_both_services_outlive_ordinary_request_deadlines`).
- No recovery runner: a hang fails the run; it is not retried or skipped.

## Issues

- Delivers #48 and #63.
- The root cause of each observed hang is investigated only as far as the watchdog's evidence
  reaches (criterion 10). A cause that needs more work gets its own issue on this milestone.
