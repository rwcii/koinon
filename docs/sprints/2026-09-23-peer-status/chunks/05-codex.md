# Sprint 2026-09-23 · Chunk 05 — Codex activity and context

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API. Not
> restated here.

## Scope

Closes #84. The notifier associates the thread's owner process, reads the Codex session log
and publishes activity, model and context in `bridge-<pid>.json`.

## Approach

- Association: the owner of a thread is the user's process that holds the thread's session
  log open. Observed on 2026-09-23 on Linux with Codex CLI 0.155.1: one `codex` process held
  five session logs open, so one process can host several threads, and an ancestor process
  alone does not identify a thread's owner. The notifier finds the owner with
  `platform_support.open_file_holders(path)`: Linux reads `/proc/<pid>/fd` of the user's
  processes, macOS runs `lsof -t -- <path>`. It records the owner's PID and process-start
  marker. On every observation it checks that this process is live with its start marker and
  still holds the log open (`platform_support.holds_open(pid, path)`: Linux reads that
  process's `/proc/<pid>/fd`, macOS runs `lsof -a -p <pid> -- <path>`). A process that
  unloads the thread while it stays alive for other threads therefore loses the association.
  A lost association publishes `unknown` with `participant_not_associated` and starts a new
  search. No holder, several holders, or a holder whose executable is not the installation's
  Codex CLI gives `participant_not_associated`. Whether an idle, loaded thread keeps its log
  open is not verified; when it does not, that thread reports `unknown`, never `idle` by
  assumption, and the live check of the definition of done records which case holds. The macOS rule is tested with a synthetic process
  that holds a file open. That test proves the mechanism, not compatibility with the Codex CLI
  on macOS, which stays a documented, unverified limit until checked on a macOS host.
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
- Publish `bridge-<pid>.json` through chunk 01's writer after each change, with each value's
  source event time (`timestamp` of the `token_count`, `task_started`, `task_complete` or
  `turn_aborted` record). When the log becomes unreadable or unrecognized, or the owner
  process ends, publish that field group as `unknown` with its reason.

## Documents

`docs/DELIVERY.md` "Presence and priority" (Codex is no longer unknown), `PROTOCOL.md`,
`docs/NOTIFIER.md`.

## Done-criteria (this chunk's slice)

- Criteria 4 and 5 hold; criteria 6 and 9 hold for the Codex source.
- The Codex activity, Codex context, participant association and allowlist tests of the
  definition of done pass on Linux and macOS.
