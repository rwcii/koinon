# Orca integration (#795) — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Target release:
v0.13.4. Scope review in `review.md`.

| Chunk | Complete outcome | Definition of done | Depends on | Owns |
| --- | --- | --- | --- | --- |
| [01 Parallel sprint chunks](chunks/01-parallel-chunks.md) | Each chunk declares its dependencies and owned items; `sprint.md` has a parallel view; a contributor test checks it | Sprint plans with parallel chunks | none | `agents/skills/sprint/`, `internal/contributor/sprint_plan_test.go`, WDR 002 |
| [02 Orca launch](chunks/02-orca-launch.md) | Launchers run as Orca terminal commands, tested; the setup doc; the observer's scratch copy in `pair` | Orca launch | none | `internal/launcher/`, `agents/skills/pair/`, USAGE "Run Koinon members from Orca" |
| [03 Captain](chunks/03-captain.md) | `coordinate` dispatches through work items, finds loops, checks goal and drift, records disagreements | `coordinate` as the Captain | 01 | `agents/skills/coordinate/`, PDR 002 |
| [04 Merge gates](chunks/04-merge-gates.md) | Required `preflight` and `board-verdict` on `develop`; `release` stops before `main` | Merge gates | none | `agents/skills/ship/`, `agents/skills/release/`, `agents/skills/preflight/`, `.koinon/skills/`, `scripts/preflight-run.sh`, `scripts/preflight-publish.sh`, `scripts/board-verdict.sh`, `scripts/setup-repo.sh`, WDR 004, PDR 008 |
| [05 Fleet Decisions](chunks/05-fleet-decisions.md) | The fleet role, its key behind the admin password, the Fleet navigation and Fleet Decisions | Fleet Decisions | none | `internal/fleet/`, `cmd/koinon/fleet.go`, the `fleet` entry of `cmd/koinon/main.go`, `internal/core/dashboard_fleet.go`, `internal/core/web/fleet_decisions.html`, the navigation in `internal/core/web/layout.html`, the key scope in `internal/core/maintainer_keys.go`, PDR 011 |
| [06 Fleet Sessions](chunks/06-fleet-sessions.md) | Signed session operations and Fleet Sessions | Fleet Sessions | 05 | `internal/core/signed_sessions.go`, the session operations' entries in `internal/core/signed_actions.go`, `internal/core/dashboard_fleet_sessions.go`, `internal/core/web/fleet_sessions.html` |

## Parallel view

| Group | Chunks | Reason |
| --- | --- | --- |
| 1 | 01, 02, 04, 05 | No dependencies; their owned items do not overlap |
| 2 | 03, 06 | 03 needs 01's chunk format; 06 needs 05's fleet role and key |

Shared files are not owned: CHANGELOG.md, README.md, docs/USAGE.md, PROTOCOL.md, CONTRIBUTING.md
and the skills README. Each chunk adds its own lines or section there, and a chunk merges
develop before its head is frozen. An owned directory owns every file under it.

With one open pull request at a time, the chunks merge in the order 01, 02, 04, 03, 05, 06.
The groups say which chunks two pairs can build at the same time on separate branches, as
chunk 01 describes.

The sprint is complete, and #795 closes, when chunk 06 merges, every live check in
`verification.md` has passed (chunk 02's check in a real Orca terminal, chunk 05's check on two
nodes), and v0.13.4 is released. A chunk can merge with its synthetic tests before its live
check; the open live check then stays a listed item of #795.
