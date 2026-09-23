# Sprint 2026-09-22 · Chunk 01 — Foundation

> References: `decision.md`, `definition-of-done.md`, `sprint.md` (shared names). Not restated here.

## Scope

The helper, the watchdog runner, the CI change, their tests, and the wait inventory with its
guard. No existing test file is converted here, except where a test of this chunk needs it.

## Approach

**Helper, `tests/waiting.py`.** Standard library only.

- `timeout(seconds=None)`: returns `seconds` or the default of 30, multiplied by
  `KOINON_TEST_TIMEOUT_SCALE`. An unset scale is 1; a value that is not a number, or is below
  1, raises at import with a message naming the variable.
- `async wait_until(condition, what, *, seconds=None, task=None, observe=None, interval=.01)`:
  polls `condition()` until it is true and returns its value. Before each poll, when `task` is
  done, awaits it so that its exception propagates. On timeout it raises `AssertionError` with
  `what`, the budget used, and `observe()` when given (the last observed state).
- `wait_until_sync(condition, what, *, seconds=None, process=None, observe=None,
  interval=.05)`: the same for blocking code. When `process` has exited, it raises with the exit
  status and the process's captured standard error, if any.
- `async settle(awaitable, what, *, seconds=None)`: awaits one awaitable under the budget, for
  teardown gathers; a timeout raises as above.

A wait that covers a longer interval passes `seconds` explicitly with a comment naming that
interval.

**Watchdog, `tests/run.py`.** Runs `unittest` discovery on `tests/` with a result class that
records the current activity:

- `startTest` and `stopTest` record the running test's id and restart the timer.
- A background thread checks the time since the last recorded progress. At the bound (300
  seconds times the scale, overridable by `KOINON_TEST_WATCHDOG_SECONDS` for the tests of this
  chunk) it prints the running test's id; when no test is running, it walks the main thread's
  frames for `setUpClass`, `tearDownClass`, `setUpModule` or `tearDownModule` and prints that
  fixture with its class or module. It then calls `faulthandler.dump_traceback(all_threads=True)`
  and `os._exit(3)`.
- `faulthandler.dump_traceback_later` is armed at a longer bound as a backstop for a thread that
  holds the interpreter lock.
- Other arguments pass through to `unittest`, so `python tests/run.py -v` and
  `python tests/run.py -v test_session` both work.

`python3 -m unittest discover -s tests` keeps working, without the watchdog.

**CI.** `.github/workflows/tests.yml` runs `python tests/run.py -v` and sets
`timeout-minutes: 30` on the test job and `KOINON_TEST_TIMEOUT_SCALE` to 3 on macOS jobs. The
root `AGENTS.md`, `CONTRIBUTING.md` and the `check` skill name `python3 tests/run.py -v` as the
test command.

**Inventory and guard, `tests/wait_inventory.py`.** Built by listing every `asyncio.timeout(`,
`asyncio.wait_for(` and `monotonic() +`/`time() +` deadline under `tests/`, and every
`for _ in range(n)` loop that polls with a sleep, then reviewing each one: a coordination wait gets `pending-02` or `pending-03` by the file split in chunks 02 and
03; a product-deadline assertion gets `product-deadline` and a one-line reason. A test in
`tests/test_wait_inventory.py` fails when one of the three text patterns occurs in a test other
than an inventory entry, or when an entry no longer matches its test. Polling loops are in the
inventory but not in the guard; the inventory review, not the guard, proves their migration.

## Done-criteria (this chunk's slice)

- Criteria covered: 2, 3, 4, 5, 6, 7; the inventory for criterion 1.
- Tests: the helper tests, the parameterized watchdog subprocess test with its eight blocking
  places, the stalled-worker test and the failed task and process tests, all in
  `definition-of-done.md`; the guard test.
- Gate: `check`, the six-job `Tests` run on the pull request.
