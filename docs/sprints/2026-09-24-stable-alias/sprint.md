# Sprint 2026-09-24 — stable alias — Chunks

Delivers #141. Cites `decision.md` (criteria 1–9) and `definition-of-done.md`; it does not
repeat them. Each chunk is one pull request that merges complete. The order is linear.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 host and terminal](chunks/01-host-terminal.md) | `ensure` records the Codex host process and the tmux pane of each registration and reports them. No behaviour change. | 4, 7 (host, terminal) | — |
| [02 alias](chunks/02-alias.md) | Codex registrations take a stable alias when it is free; the holder's registry record publishes it; one holder at every point. | 1, 2, 7 (alias), 8 (alias) | 01 |
| [03 rebind](chunks/03-rebind.md) | `session.py rebind` moves the alias on verified same-pane evidence and stops the predecessor; guide, `pickup` and `peer-tmux` use it. | 3, 4, 5, 8 (rebind, retirement) | 02 |
| [04 tmux name](chunks/04-tmux-name.md) | `ensure` and `rebind` name the agent's own tmux session or pane after the alias. | 6, 8 (tmux) | 03 |

Criterion 9 (Linux and macOS, Python 3.11–3.13) applies to every chunk.

## Done-criteria per chunk

Each chunk passes the gate in `definition-of-done.md` and the tests listed in its own file.
Chunk 02 ends with live check 1, chunk 03 with live check 2, chunk 04 with live check 3; each
live check needs the user's authorization and an authorized runtime upgrade to the merged
commit. The sprint closes #141 after chunk 04 and all three live checks.

## Assumptions to verify in the build

- Claude Code finds a registry record that appears while its process runs, and it ignores
  unknown fields in the record (Koinon already writes `bridgeOwner` and `peerFeatures`). Live
  check 1 establishes the first for a record that carries an alias. No chunk rewrites a live
  record in place: a notifier chooses its published name at start only, and a move restarts
  the new holder's service.
- A Codex process can hold more than one rollout file open at once (observed 2026-09-24: one
  CLI process held two). Rollout file holders are therefore not an active-thread signal, and no
  chunk uses them as one.
