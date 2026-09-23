# Sprint 2026-09-22 · Chunk 03 — Process and store waits

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's helper
> interface. Not restated here.

## Scope

Every inventory entry marked `pending-03`. Its files wait on subprocesses, sessions and stores:
`test_memory`, `test_work_activation`, `test_session`, `test_work_storage`,
`test_session_supervisor`, `test_install`, `test_work_foundation`, `test_upgrade_live_memory`,
`test_upgrade_backup`, `test_session_socket_handoff`, `test_session_service`,
`test_notification_source` and `test_memory_service`.

## Approach

- Replace each `deadline = monotonic() + n` loop and each `for _ in range(n)` polling loop
  in blocking code with `wait_until_sync`, passing the child process as `process` where there
  is one, so that a child that exits early reports its status instead of timing out. A wait in
  asynchronous code uses `wait_until`; the file grouping does not choose the helper.
- Give each subprocess wait with a timeout its budget from `timeout()`, keeping the call's
  own semantics.
- Keep the existing failure texts as the `what` description, for example `session failed to
  start`.
- Leave `product-deadline` entries unchanged.
- Remove each converted entry from `tests/wait_inventory.py`.

## Done-criteria (this chunk's slice)

- Criteria covered: 1, 8 and 9 for these files.
- No `pending-03` entry remains, and the guard test passes. Whichever of 02 and 03 merges
  last confirms that the inventory holds only `product-deadline` entries.
- Tests: the converted files pass unchanged in result on all six CI jobs.
- Gate: `check`, the six-job `Tests` run. When this chunk merges after chunk 02, the repeated
  macOS check of the definition of done runs at its head.
