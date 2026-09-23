# Sprint 2026-09-23 · Chunk 05 — Codex activity and context

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API. Not
> restated here.

## Scope

Closes #84. `session.py ensure` associates the participant process; the notifier reads the
Codex session log and publishes activity, model and context in `bridge-<pid>.json`.

## Approach

- `platform_support.parent_pid(pid)`: Linux from `/proc/<pid>/stat`, macOS from
  `ps -o ppid= -p <pid>`.
- Association: `session.py ensure` runs in the Codex session's shell. It walks its ancestors
  and records the nearest ancestor process whose executable is the Codex CLI that the
  installation names (`codex` in `install.json`), with its PID and process-start marker, in
  the session's `session.json`. No match: record nothing, and activity is
  `participant_not_associated`. The build verifies this ancestor rule on Linux and macOS
  before relying on it; if the Codex CLI runs the shell through a wrapper process, the chunk
  records the rule it found.
- Log location: the notifier locates the log once, as `koinon/usage_selection.py` does
  (`CODEX_HOME` or `~/.codex`, `sessions/**/*<thread id>*.jsonl`, first line `session_meta`
  with a matching `payload.id`), and keeps the path in its state directory. It does not read
  Codex's `state_5.sqlite`, and it does not connect to the app-server: neither is shown to be
  free of side effects (`docs/DELIVERY.md`).
- Reading: the notifier reads new bytes from the end of the log on each cycle, bounded per
  cycle, and keeps only: the latest `turn_context.model`; the latest `task_started`,
  `task_complete` and `turn_aborted` turn IDs; the latest `token_count` `last_token_usage`
  input tokens and `model_context_window`. It never reads `last_agent_message` or any other
  text field. An unrecognized record shape gives `source_unrecognized`.
- Activity: `busy` while the latest started turn has no matching end, `idle` when it has
  ended; both only while the associated process is live with its recorded start marker;
  otherwise `unknown`. `presence.model_activity` uses source `codex_session_log`.
- Publish `bridge-<pid>.json` through chunk 01's writer after each change.

## Documents

`docs/DELIVERY.md` "Presence and priority" (Codex is no longer unknown), `PROTOCOL.md`,
`docs/NOTIFIER.md`.

## Done-criteria (this chunk's slice)

- Criteria 4 and 5 hold; criteria 6 and 9 hold for the Codex source.
- The Codex activity, Codex context, participant association and allowlist tests of the
  definition of done pass on Linux and macOS.
