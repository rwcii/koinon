# Sprint 2026-10-06 — Go daemon — Chunks

Milestone `2026-10-06-go-daemon`. Cites `decision.md` (criteria 1–12) and
`definition-of-done.md`; it does not repeat them. Each chunk is one pull request that merges
complete. The Python runtime stays untouched and releasable until chunk 12.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 spike](chunks/01-spike.md) | Recorded evidence for every unverified fact in `decision.md`; the plan corrected and gated again if a fact differs. | 5, 6 (facts) | — |
| [02 core](chunks/02-core.md) | Go module, `koinon serve`, loopback listener with the secret, SQLite state, session records, Go CI. | 1, 2, 3 | 01 |
| [03 messages](chunks/03-messages.md) | Send, store, inbox, acknowledge and delivery state through the daemon's API and `koinon` commands. | 4 | 02 |
| [04 wake](chunks/04-wake.md) | Wake adapters for Claude, Codex, DeepSeek and Antigravity. | 6 | 03 |
| [05 mcp](chunks/05-mcp.md) | `koinon mcp`, agent setup commands and the startup guide for all four families; Claude coordination through Koinon. | 4, 5 | 03 |
| [06 memory](chunks/06-memory.md) | Memory stores in the daemon with the current protocol, exposed through MCP. | 7 | 02, 05 |
| [07 work items](chunks/07-work-items.md) | Work items, claims and events in the daemon, exposed through MCP. | 8 | 02, 05 |
| [08 dashboard views](chunks/08-dashboard-views.md) | Login, request protection and the read-only views. | 9 (views) | 03, 06, 07 |
| [09 dashboard actions](chunks/09-dashboard-actions.md) | Administrative actions and the audit log. | 9 (actions) | 08, 04, 10 |
| [10 launcher](chunks/10-launcher.md) | `koinon codex` and `koinon agy`. | 10 | 02 |
| [11 install and upgrade](chunks/11-install-upgrade.md) | Service install, the import of Python-era state, upgrade from `main`, uninstall. | 11 | 04, 05, 06, 07, 10 |
| [12 retire Python](chunks/12-retire-python.md) | Python runtime removed; documentation, `AGENTS.md`, skills and guidance describe the Go runtime. | 12 | 09, 11 |

Criteria 1 and 3 and the Linux and macOS constraint apply to every chunk from 02 on. Chunks
with no dependency between them may be built at the same time by different agents.

## Done-criteria per chunk

Each chunk passes the gate in `definition-of-done.md` and the tests in its own file. Live checks
need the maintainer's authorization: chunk 01 runs live check 1 with probes, chunk 04 live check 2,
chunk 05 live checks 1 and 3 with the real server, chunk 11 live check 4. The release after
chunk 12 uses the `release` skill. The sprint closes its milestone issues with the evidence of
the chunk that delivers each one.

## Assumptions to verify in the build

Chunk 01 owns every assumption listed under "Unverified facts" in `decision.md`. No other chunk
starts until chunk 01 has merged and, where a fact differs, the plan has passed gate B again.
