# Sprint 2026-09-22 · Chunk 04 — Observed hangs

> References: `decision.md` (criterion 10), `definition-of-done.md`, chunk 01's runner. Not
> restated here.

## Scope

One bounded reproduction attempt for each observed hang:

- `test_work_activation` (#63).
- `test_upgrade_probe` with `test_upgrade_manual` (observed 2026-09-22 on this machine).

## Approach

- On the `develop` commit after chunk 01, run each original command once under the watchdog:
  `python3 tests/run.py -v test_work_activation` and
  `python3 tests/run.py -v test_upgrade_probe test_upgrade_manual`. Run them once on Linux
  locally and once on macOS in a temporary CI job: one commit of this chunk's pull request adds
  it, the run is recorded, and the next commit removes it before merge. The watchdog bounds
  each run; four runs in total. Stop at the first reproduced hang.
- Record in `docs/sprints/2026-09-22-test-reliability/hangs.md`, for each attempt: the command,
  the revision, the platform, the number of runs and the result.
- A reproduced hang: read the watchdog's output and open an issue on the milestone with the
  diagnostics. Its fix is not made in this chunk: a test defect is fixed in the chunk that owns
  the file, or after it merges; a runtime defect is a separate chunk (decision constraints).
- A hang that does not reproduce: record it as not reproduced and open an issue that holds the
  unknown cause and links the record.

## Done-criteria (this chunk's slice)

- Criterion covered: 10.
- `hangs.md` holds both records; every reproduced or unexplained hang has an issue or a fix with
  a test.
- Gate: `check`. The chunk changes only `docs/sprints/`, apart from the temporary CI job.
