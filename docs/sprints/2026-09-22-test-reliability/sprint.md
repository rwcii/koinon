# Sprint 2026-09-22 — test reliability — Chunks

Realizes [decision.md](decision.md) (gate A approved) and is measured by
[definition-of-done.md](definition-of-done.md). Milestone `2026-09-22-test-reliability`.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Foundation](chunks/01-foundation.md) | The shared wait helper, the watchdog runner, the CI command and job timeout, the stalled-worker and failed-worker tests, and the wait inventory with its regression guard. | 2, 3, 4, 5, 6, 7 | — |
| [02 Asynchronous waits](chunks/02-async-waits.md) | Every coordination wait in the files of the asynchronous services uses the helper. | 1, 8, 9 (its files) | 01 |
| [03 Process and store waits](chunks/03-process-waits.md) | Every coordination wait in the subprocess, session and store files, and in any file not named in 02, uses the helper. | 1, 8, 9 (its files) | 01 |
| [04 Observed hangs](chunks/04-observed-hangs.md) | One bounded reproduction attempt for each observed hang, recorded, with a new issue for any cause found or left unknown. | 10 | 01 |

Chunks 02, 03 and 04 do not depend on each other and may be built at the same time; they change
disjoint files, and chunk 04 changes only its record. The chunk that merges last among 02 and 03
confirms that the inventory holds only `product-deadline` entries, runs the repeated macOS check
of the definition of done, and closes #48. #63 closes when chunk 01 merges (criteria 5, 7
and 9).

## Shared names

These names are fixed here so that chunks 02 to 04 can be written against chunk 01 without
reading its code first.

- `tests/waiting.py`: the helper module. Chunk 01 defines its interface.
- `tests/run.py`: the runner that arms the watchdog; CI runs `python tests/run.py -v`.
- `KOINON_TEST_TIMEOUT_SCALE`: the one scale factor, a number of at least 1, default 1.
- `tests/wait_inventory.py`: the inventory and the regression guard's exception list, one
  entry per coordination wait (file, test, expression, status `pending-02`, `pending-03` or
  `product-deadline` with a reason).
