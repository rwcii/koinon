# Sprint 2026-09-22 — test reliability — Observed hangs

One bounded reproduction attempt for each observed hang (decision criterion 10), run under the
watchdog from #108. Each run is bounded by the watchdog (300 seconds times the scale).

## Attempts

| Hang | Command | Revision | Platform | Runs | Result |
| --- | --- | --- | --- | --- | --- |
| `test_work_activation` (#63) | `python3 tests/run.py -v test_work_activation` | 80516b6 | Linux, Python 3.14, local host | 1 | 14 tests passed in 0.2 s; not reproduced |
| `test_work_activation` (#63) | `python tests/run.py -v test_work_activation` | 80516b6 (PR #109, abea0ea) | macOS, Python 3.13, CI run 35805735138 | 1 | 14 tests passed in 0.2 s; not reproduced |
| `test_upgrade_probe` with `test_upgrade_manual` | `python3 tests/run.py -v test_upgrade_probe test_upgrade_manual` | 80516b6 | Linux, Python 3.14, local host | 1 | 14 tests passed in 0.02 s; not reproduced |
| `test_upgrade_probe` with `test_upgrade_manual` | `python tests/run.py -v test_upgrade_probe test_upgrade_manual` | 80516b6 (PR #109, abea0ea) | macOS, Python 3.13, CI run 35805735138 | 1 | 14 tests passed in 0.02 s; not reproduced |

Four runs in total; none reproduced a hang, so none stopped the attempt early.

## Outcomes

- **`test_work_activation`:** not reproduced. The cause stays open in #110. #63 closed on its
  own acceptance with #108.
- **`test_upgrade_probe` with `test_upgrade_manual`:** not reproduced on committed code. The
  observed run differed from these attempts: it ran in the Codex Linux sandbox, on a
  feature-branch checkout, after the same command had edited `test_upgrade_probe.py` locally. In
  that sandbox the filesystem root is owned by uid 65534, and a test that publishes a service
  artifact fails in `setUp` with `RegistrationPathError`; that failure is reproduced but is not
  established as the cause of the hang. The cause stays open in #111.

If either hang recurs, the runner ends the run within its bound and prints the running test or
fixture, every thread stack and every asyncio task stack; that output belongs in its issue.
