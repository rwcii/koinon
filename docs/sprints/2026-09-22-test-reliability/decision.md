# Sprint 2026-09-22 — test reliability — Decision

## Problem

The test suite coordinates with asynchronous services, subprocesses and background tasks by
waiting for a condition under a fixed wall-clock deadline. About 70 `asyncio.timeout` or
`asyncio.wait_for` sites and about 60 deadline loops in about 30 files each choose their own
duration. Such deadlines have expired in CI: #48 records `test_notification_runtime` failing on
a macOS job, and its comment records one commit whose `test (macos-latest, 3.11)` context
failed in one run (`test_session`) and passed in another. The cause of these expirations is not
established; runner load and a delivery defect both remain open. A timeout usually reports only
`TimeoutError` or a fixed message, so a failure does not show which of these it was.

The opposite failure also occurs. A test that waits with no bound can hang: #63 records
`test_work_activation` blocked for about nine hours, and on 2026-09-22 a run of
`test_upgrade_probe` with `test_upgrade_manual` was observed on this machine still blocked
after 24 hours. The required `Tests` job has no `timeout-minutes`, so a hang holds a runner up
to the six-hour platform limit and produces no diagnostics.

Both failures cost a CI run, and both teach contributors to rerun instead of investigating.

## Acceptance criteria

1. Every wait that coordinates a test with asynchronous work goes through one shared helper,
   and no test file chooses its own coordination deadline. A coordination deadline bounds how
   long the test waits; it is not a product deadline that the test asserts (criterion 8).
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
    recorded in a new issue with the watchdog's diagnostics. A hang that does not reproduce is
    recorded as not reproduced, and a new issue keeps its cause open. #63 closes on its own
    acceptance text (criteria 5, 7 and 9), not on a root cause.

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
