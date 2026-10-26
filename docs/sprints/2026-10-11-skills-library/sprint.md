# Skills library and adoption — chunks

> Historical sprint plan. Its non-goals and constraints limited this sprint only. Current decisions are in the [decision records](../../engineering/wdr/README.md); see WDR 001.

Each chunk merges complete through ship. Reuse #145's work item across chunks, releasing its
claim at a driver handoff. Close #145 only after all three chunks and required evidence pass.

| Chunk | Complete outcome | Criteria | Dependency |
| --- | --- | --- | --- |
| [01 Canonical skills and catalog](chunks/01-catalog.md) | Generic canonical skills with per-repo fact sidecars, embedded canonical bytes, shared symlinks/wrappers and source-completeness guards | 1, 7, 8, 9; part of 10 | None |
| [02 Queries and offers](chunks/02-query.md) | List/read through supported transports and initialize/guide offer, using embedded bytes without runtime checkout | 2, 3; part of 10 | 01 |
| [03 State and adoption](chunks/03-adoption.md) | Machine/project skill and sidecar state query, exact-change agent adoption procedure, durable decisions, decline/local-edit preservation across upgrades | 4, 5, 6; remaining 10 | 02 |

Chunk01 improves the skills used here and embeds a tested catalog without advertising tools
that do not exist. Chunk02's list/read are useful before state/adoption arrives; chunk03 adds
the remaining guidance. Keep one open PR per target branch; no plan PR while the system-service PR occupies the target.
