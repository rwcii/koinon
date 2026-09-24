# Sprint 2026-09-24 — participant guidance — Chunks

Realizes [decision.md](decision.md) (gate A: the user approved the joint recommendation on #133,
with the Claude hook) and is measured by [definition-of-done.md](definition-of-done.md).
Milestone `2026-09-24-participant-guidance`.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Identity](chunks/01-identity.md) | Native `ensure` and `status` print `name`, `state_dir`, `inbox_command`; `status` is read-only for an unregistered session. | 3 (status), 4 | — |
| [02 Guide and bootstrap](chunks/02-guide.md) | The catalog, `session.py guide`, the small bootstrap for Codex and DeepSeek, and drift reports at install and upgrade. | 1, 2, 3 (guide), 8, 10 | 01 |
| [03 Revisions and notices](chunks/03-revisions.md) | Runtime and guidance revisions, staleness fields, `guide-ack`, deduplicated notifier notices. | 5, 6, 10 | 02 |
| [04 Claude](chunks/04-claude.md) | The Claude view, the managed `CLAUDE.md` block and the `SessionStart` hook, through install, upgrade and removal. | 7, 8, 10 | 03 |
| [05 Skills](chunks/05-skills.md) | `handoff`, `pickup` and `peer-tmux` point to `guide`. | 9 | 04, #132 |

The chunks run in order: each one uses the interface of the one before. Chunk 01 closes #131.
Chunk 05 records the last live check and closes #133.

## Shared names

- `koinon/guidance.py`: the catalog, the family views, the renderer and the revision digest.
- `session.py guide [--agent FAMILY] [--topic NAME] [--brief] [--json]` and
  `session.py guide-ack REVISION`. `guide` dispatches before any configuration lock,
  registration or manager call.
- Output fields: `guide_revision`, `guide_stale`, `runtime_revision`, and in each observation
  `state` (`observed`, `unavailable`, `unknown`) with `reason`.
- `install.json`: `runtime_revision`, `guidance_revision`, and for each managed bootstrap or hook
  its path and the digest of the text Koinon wrote.
- Acknowledgement, owner-only `{revision, acknowledged_at}`: `<state>/guidance-ack.json` for a
  Codex or DeepSeek session; `guidance-ack-<session id>.json` in
  `participant_status.directory()` for a Claude session, keyed by `CLAUDE_CODE_SESSION_ID`.
