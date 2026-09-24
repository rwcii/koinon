# Chunk 01 — host and terminal records

Read `decision.md` and `definition-of-done.md` first. Criteria 4 and 7 (host process and
terminal fields). No alias, no stop, no rename in this chunk.

## Outcome

Each Codex `session.py ensure` records, in the session's own state directory, the host Codex
process and the tmux pane that the command runs in. `ensure` and `status` report both.

## Specification

- **Where it runs.** `ensure` runs outside the Codex sandbox through the normal approval
  request (guide `sandbox` topic), so it can see the host process and the tmux socket. Its
  environment still carries `TMUX` and `TMUX_PANE` from the agent's shell. Inside the sandbox
  the observations fail; report them as `unavailable` with the reason and continue.
- **Host process.** Walk the parent chain from `os.getppid()` with
  `platform_support._process_command()` (make it public as `process_command()`), at most 64
  steps, and select the nearest ancestor that `platform_support.codex_process(pid,
  config['codex'])` accepts. Record `pid` and `proc_start` (`platform_support.proc_start`).
  No match: `unknown`, reason `host_not_found`. Put the walk in `platform_support` as
  `ancestor_matching(pid, predicate, limit)`; no `sys.platform` check elsewhere.
- **Terminal.** When `TMUX` and `TMUX_PANE` are both set and a `tmux` executable is found, run
  `tmux -S <socket> display-message -p -t <pane> '#{pane_id}\t#{pane_pid}\t#{session_id}'`,
  where `<socket>` is the first comma-separated field of `TMUX`. Use an argument array and a
  timeout. Accept the pane only when `pane_pid` is the host process or one of its ancestors;
  else `unavailable`, reason `pane_not_host`. Record `socket`, `pane_id` and `session_id`.
  No `TMUX`: `not_in_tmux`. No executable: `tmux_unavailable`. Put the tmux call in a new
  module `koinon/tmux_terminal.py`; it must work with the tmux on Linux and on macOS.
- **Records.** `<state>/host.json` and `<state>/terminal.json`, written with
  `durable_state.publish` (private mode), each with `observed_at_ms`. A later `ensure`
  replaces them. They carry no thread ID and no peer name. Legacy and DeepSeek registrations
  record nothing new.
- **Reporting.** `ensure` and `status` add `host` and `terminal` objects, each with `state`
  `observed`, `unavailable` or `unknown` and a reason. `status` reports a recorded host whose
  pid is gone or whose start time differs as `state: observed, live: false`.
- **Upgrade.** Registrations without the files report `unknown`, reason `not_recorded`.
  Verify that `scripts/upgrade.py` preserves the two files (session directory capture in
  `koinon/upgrade_backup.py`); do not add them to the files that `_notifier_files` freezes.

## Tests

The host-process, reporting and upgrade tests of `definition-of-done.md`, plus: a private tmux
server with one pane whose process is a synthetic host parent; `pane_not_host`; `not_in_tmux`;
`tmux_unavailable` (empty `PATH` entry for tmux). `tests.yml` installs tmux on both runners in
this chunk.

## Documentation

`PROTOCOL.md` (session state files), `docs/INSTALL.md` (what `ensure` reports), `CHANGELOG.md`.
