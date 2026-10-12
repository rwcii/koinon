# Sprint 2026-10-12 — Grok agent family — Chunks

Each chunk is one pull request from `develop`, built with the `ship` skill after the chunks it
depends on have merged, with one pull request in flight at a time. Each chunk passes its part
of `definition-of-done.md` and updates the documents for the behavior it changes in the same
pull request. All chunks reuse the work item of #254.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Evidence](chunks/01-evidence.md) | `spike.md` records Grok Build's MCP spawn topology, per-call identity, launch-fixed session IDs, reset/resume, subagents and wake paths from real sessions in the container, and the plan files agree with it. | 1 (versions), 2, 3, 5 (facts only) | — |
| [02 Adapter](chunks/02-adapter.md) | `koinon grok` in Grok's default mode: launch-fixed first session, per-call identity from `GROK_SESSION_ID` under the proven host, direct start as a plain peer, sub-agent calls as the parent's, leader mode refused, reset and resume through existing succession, setup, guide, permissions and docs. | 1, 2, 3, 4, 6, 7 | 01 |
| [03 Wake](chunks/03-wake.md) | A content-free wake typed into the session's proven, idle tmux pane; the leader-mode path filed as a follow-up; the live check in the development container. | 5, 6 | 02 |

Chunk 01's results are in `spike.md`; chunks 02 and 03 were specified from them. Gate B
approved the plan at 156f9ab with parent-scoped sub-agent attribution in place of the deny hook
(#254 comment 6118290944). Chunk 02 applies that and the decision records of the same date.
