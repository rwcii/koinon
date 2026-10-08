# Sprint 2026-10-08 — participants — Chunks

Each chunk is one pull request from `develop`, built with the `ship` skill after the chunks it
depends on have merged. One pull request is in flight at a time. Each chunk passes its part of
`definition-of-done.md` and documents the behaviour it changes.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Launched sessions only](chunks/01-codex-launch.md) | Every Koinon session is started with `koinon <family>` (Codex with `--no-daemon`, background jobs with `koinon claude --bg`); a direct start is islanded; a sub-agent never takes an address. | 1 (sub-agents), 6, 9, 10 | — |
| [02 Participants](chunks/02-participants.md) | Participants with optional roles, one holder chosen never by order, the maintainer's choice in the dashboard, upgrade of existing aliases, reporting. | 1, 2, 9, 10 | 01 |
| [03 Participant state](chunks/03-participant-state.md) | The participant owns its address inbox, memory cursor and claims; fencing refuses every stale holder path. | 4, 5, 10 | 02 |
| [04 Succession](chunks/04-succession.md) | Verified succession changes the holder and retires the former holder atomically; the skills describe it. | 3, 10 | 03 |
| [05 Terminal naming](chunks/05-terminal-naming.md) | Names follow the address, a taken name is retried, a Claude background job is named through its attached client's pane. | 7, 8, 9, 10 | 02 |

Chunk 05 needs only 02, but it is built after 04 under the one-pull-request rule. The final live
check of `definition-of-done.md` runs before the last chunk's sign-off.

Work items: chunks 01 to 04 reuse the work item of #228 (01 also delivers #247, 02 delivers the
Claude part of #252 and closes it, 04 delivers #141); chunk 05 delivers #147 and closes #228. Each issue closes when the last chunk that names it
merges.
