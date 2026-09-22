# Sprint 2026-09-22 — test reliability — Definition of done

## Tests

- The shared wait helper has its own tests: it returns as soon as the condition holds; on
  timeout its failure message contains the description and the last observed state; it
  re-raises the exception of a failed task or reports the exit status of a failed process
  instead of timing out; the scale factor multiplies the default and an explicit per-call
  deadline.
- The watchdog has one parameterized subprocess test. For each blocking place, a synthetic
  test module blocks forever there and runs under the CI command with a short bound. The places
  are the test body, `setUp`, `tearDown`, `setUpClass`, `tearDownClass`, `setUpModule` and
  `tearDownModule`, and a test body blocked in `select` on a pipe that is never written (the
  `select`/`epoll` case). For each, the child exits nonzero within the bound plus a small
  margin, its output names the running test or, outside a test, the fixture and its class or
  module, and it contains a stack for every thread.
- The stalled-worker test (criterion 7) uses a synthetic asynchronous worker that never
  completes, awaited the way the service tests await real workers, and runs in the same
  subprocess form.
- Migration completeness (criterion 1) is established by a reviewed inventory: the first
  converting chunk lists every coordination wait in `tests/` by file and test, and each later
  chunk marks the entries it converts. The inventory is complete when every entry is converted
  or excepted.
- A repository test is the regression guard for the known patterns. It fails when a file under
  `tests/` other than the helper module contains `asyncio.timeout(`, `asyncio.wait_for(`, or a
  `monotonic() +` or `time() +` deadline, unless that exact expression in that test is on an
  exception list with its reason (criterion 8). Exceptions name the test and the expression,
  never a whole file.

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
- At the head of the last converting chunk, one macOS job runs the modules with earlier
  timing failures (`test_notification_runtime`, `test_session`, `test_session_supervisor`)
  five times in one bounded invocation, with no timing failure. A full suite is repeated only
  to investigate an actual unresolved failure.
