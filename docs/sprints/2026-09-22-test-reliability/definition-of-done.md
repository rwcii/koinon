# Sprint 2026-09-22 — test reliability — Definition of done

## Tests

- The shared wait helper has its own tests: it returns as soon as the condition holds; on
  timeout its failure message contains the description and the last observed state; it
  re-raises the exception of a failed task or reports the exit status of a failed process
  instead of timing out; the scale factor multiplies the default and an explicit per-call
  deadline.
- The watchdog has a subprocess test: a synthetic test module whose test blocks forever runs
  under the CI command with a short bound. The child exits nonzero within the bound plus a
  small margin, its output names the blocked test, and it contains a stack for every thread.
  The same test covers a block in `setUp`, in `tearDown` and in `setUpClass`.
- The stalled-worker test (criterion 7) uses a synthetic asynchronous worker that never
  completes, awaited the way the service tests await real workers.
- A repository test fails when a test file under `tests/` contains a coordination deadline
  outside the helper: `asyncio.timeout(`, `asyncio.wait_for(`, or a `monotonic() +` or
  `time() +` deadline, except in the helper module and in an explicit, commented allow-list of
  product-deadline tests (criterion 8).

## Integration points

- The CI command in `.github/workflows/tests.yml` runs the suite with the watchdog on
  `ubuntu-latest` and `macos-latest` for Python 3.11, 3.12 and 3.13.
- Subprocess-based tests (`test_session`, `test_session_supervisor`, `test_work_activation`)
  wait on real child processes through the helper.

## Edge cases

- The awaited task fails before the deadline, and after it.
- The condition becomes true in the same poll in which the deadline expires.
- A test blocks in a C call or in `select`/`epoll` without returning to the event loop (the
  observed `ep_poll` hang).
- A test blocks in teardown after its body passed.
- The scale factor is unset, invalid, or less than one.

## The gate

- The `check` skill, run with the CI command.
- `Tests` green on all six jobs of the pull request, and green again on the push to `develop`.
- For chunks that convert waits: the converted files pass three consecutive full CI runs on
  macOS without a timing failure (reruns of the same commit through `workflow_dispatch`).
