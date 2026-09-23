# Sprint 2026-09-22 · Chunk 04 — Observed hangs

> References: `decision.md` (criterion 10), `definition-of-done.md`, chunk 01's runner. Not
> restated here.

## Scope

One bounded reproduction attempt for each observed hang:

- `test_work_activation` (#63).
- `test_upgrade_probe` with `test_upgrade_manual` (observed 2026-09-22 on this machine).

## Approach

- On the `develop` commit after chunk 01, run each module 20 times under
  `python3 tests/run.py -v <module>` on Linux, with the default bound. Run the same on macOS
  in a temporary CI job: one commit of this chunk's pull request adds it, the run is recorded,
  and the next commit removes it before merge.
- Record in `docs/sprints/2026-09-22-test-reliability/hangs.md`, for each attempt: the command,
  the revision, the platform, the number of runs and the result.
- A reproduced hang: read the watchdog's output. When it points to a test defect, fix it in
  this chunk with a test. When it points to a runtime defect, open an issue on the milestone
  with the diagnostics; its fix is a separate chunk (decision constraints).
- A hang that does not reproduce: record it as not reproduced and open an issue that holds the
  unknown cause and links the record.

## Done-criteria (this chunk's slice)

- Criterion covered: 10.
- `hangs.md` holds both records; every reproduced or unexplained hang has an issue or a fix with
  a test.
- Gate: `check`. The chunk changes `docs/sprints/` and, only for a test fix, `tests/`.
