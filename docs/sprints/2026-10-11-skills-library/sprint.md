# Skills library and adoption — chunks

Each chunk merges complete through ship. Reuse #145's work item across chunks, releasing its
claim at a driver handoff. Close #145 only after both chunks and their required evidence pass.

| Chunk | Complete outcome | Criteria | Dependency |
| --- | --- | --- | --- |
| [01 Catalog and query](chunks/01-catalog.md) | Embedded portable catalog, list/read through supported transports, initialize/guide offer, documented reference boundary | 1, 2, 3, 7, 8; part of 9 | None |
| [02 State and adoption](chunks/02-adoption.md) | Scoped read-only state query, exact-change agent adoption procedure, durable decisions, decline/local-edit preservation across upgrades | 4, 5, 6; remaining 9 | 01 |

Catalog list/read are useful before state/adoption is available. Chunk 01 describes those
available commands and does not advertise an unimplemented state or adoption interface.
Chunk 02 adds the remaining guidance. Keep one open PR per target branch.
