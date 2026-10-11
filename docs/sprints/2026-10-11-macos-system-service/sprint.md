# Sprint 2026-10-11 — macOS system service — Chunks

Each chunk is one pull request from `develop`, built with the `ship` skill after the chunks it
depends on have merged, with one pull request in flight at a time. Each chunk passes its part
of `definition-of-done.md` and updates the documents for the behaviour it changes in the same
pull request. All chunks reuse the work item of #283; #283 closes when chunk 03 merges and the
live checks pass.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Daemon log and lock wait](chunks/01-log-and-lock-wait.md) | The daemon logs start, stop and lock contention; services log to files; a service-started daemon waits for the lock instead of exiting. | 2 (no tight loop), 3 | — |
| [02 System mode](chunks/02-system-mode.md) | `koinon install --system` and uninstall on macOS, the mode record, one daemon only, the hand-written LaunchDaemon, FileVault and the AGENTS.md wording. | 1, 2, 6, 7, 8 (CI part) | 01 |
| [03 Recovery and re-registration](chunks/03-recovery.md) | `recover` gives the restart command per mode; `koinon mcp` registers again after an expiry during an outage and takes back its address. | 4, 5, 8 (live part) | 02 |

Chunk 03's re-registration part does not need chunk 02, but it ships after 02 under the
one-pull-request rule, and its live checks need system mode.
