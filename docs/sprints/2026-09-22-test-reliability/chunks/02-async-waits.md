# Sprint 2026-09-22 · Chunk 02 — Asynchronous waits

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's helper
> interface. Not restated here.

## Scope

Every inventory entry marked `pending-02`. Its files are the asynchronous service tests:
`test_notification_runtime`, `test_subscriptions`, `test_upgrade_gate`, `test_service_workers`,
`test_work_maintenance`, `test_bridge_startup`, `test_notification_delivery`,
`test_memory_target`, `test_memory_bindings`, `test_platform_support`,
`test_notification_provider`, `test_inbox_schema`, `test_delivery_ledger`,
`test_peer_transport` and `test_database_worker`.

## Approach

- Replace each coordination `asyncio.timeout` loop with `wait_until`, passing the running
  service task as `task` and a description of the condition as `what`.
- Replace each teardown `asyncio.wait_for(gather(...), n)` with `settle`.
- Keep a file's own diagnostic helpers, such as `wait_for_delivery_health` in
  `test_notification_runtime`, and move their deadline to `timeout()`; keep their messages.
- Leave `product-deadline` entries unchanged.
- Remove each converted entry from `tests/wait_inventory.py`.

## Done-criteria (this chunk's slice)

- Criteria covered: 1, 8 and 9 for these files.
- No `pending-02` entry remains, and the guard test passes.
- Tests: the converted files pass unchanged in result on all six CI jobs.
- Gate: `check`, the six-job `Tests` run. When this chunk merges after chunk 03, the repeated
  macOS check of the definition of done runs at its head.
