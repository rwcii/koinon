# Sprint 2026-10-12 — Grok agent family — Chunks

Each chunk is one pull request from `develop`, built with the `ship` skill after the chunks it
depends on have merged, with one pull request in flight at a time. Each chunk passes its part
of `definition-of-done.md` and updates the documents for the behavior it changes in the same
pull request. All chunks reuse the work item of #254.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Evidence](chunks/01-evidence.md) | `spike.md` records Grok Build's MCP spawn topology, per-call identity, launch-fixed session IDs, reset/resume, subagents and wake paths from real sessions in the container, and the plan files agree with it. | 1 (versions), 2, 3, 5 (facts only) | — |
| 02 Adapter | Grok identity, launcher admission, setup, guide and permissions, with the identity, admission, reset, messaging and edge-case tests. | 1, 2, 3, 4, 6, 7 | 01 |
| 03 Wake | The exact-session content-free wake, or the reported limitation, with its tests and the final live check. | 5, 6 | 02 |

Chunk 01 is specified now. Chunks 02 and 03 depend on facts that only chunk 01 can establish,
so their specifications are written in chunk 01's pull request from its results, and the
plan passes gate B again before chunk 02 starts. If chunk 01 shows that no exact per-call
identity exists, chunk 01 records that, chunks 02 and 03 are not built, and #254 stays open
with the evidence.
