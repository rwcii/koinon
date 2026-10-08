# Chunk 01 — Codex launch

Criteria 1 (sub-agents), 6, 9 (naming reason) and 10 of `decision.md`. Delivers #247.
Facts: `live-checks.md`, F1 and F2.

## Current state

- `internal/launcher/launcher.go` records a launch (`core.LaunchTarget`, `HostPID` is the
  launcher's own PID, which `platform.Exec` keeps for the CLI), exports `KOINON_LAUNCH_ID`,
  `KOINON_STATE_DIR`, `KOINON_DAEMON_ADDRESS` and, for Codex, `KOINON_CODEX_HOST`, and runs
  `codex -c shell_environment_policy.set.KOINON_CODEX_HOST="<pid>" <args>`.
- Codex 0.161 passes an MCP server only the variables that `mcp_servers.<name>.env_vars` lists.
  `koinon mcp` reads `KOINON_LAUNCH_ID`, `KOINON_STATE_DIR`, `KOINON_DAEMON_ADDRESS`, `TMUX` and
  `TMUX_PANE`, so a launched Codex session's server sees none of them.
- `internal/mcp/identity.go` takes a Codex session's ID from `_meta.threadId`. It ignores
  `x-codex-turn-metadata.thread_source`.

## Change

1. **Launcher.** For Codex, the command line becomes
   `codex --no-daemon -c shell_environment_policy.set.KOINON_CODEX_HOST="<pid>"
   -c 'mcp_servers.koinon.env_vars=[<names>]' <args>`, where `<names>` lists every variable that
   `koinon mcp` reads from its environment and the launcher sets or inherits (`KOINON_LAUNCH_ID`,
   `KOINON_STATE_DIR`, `KOINON_DAEMON_ADDRESS`, `TMUX`, `TMUX_PANE`). The list is one constant
   shared with `internal/mcp` and a test asserts that every `Getenv` name in `koinon mcp` that is
   not a family identity variable is in it. A user argument that already contains `--no-daemon`
   is not repeated.
2. **Shared daemon.** In `register`, a Codex caller without `KOINON_LAUNCH_ID` whose server's
   parent process is a Codex app-server (`codex app-server` in its arguments) records
   `host: codex_shared_daemon` in the session's observation. Terminal naming returns
   `codex_shared_daemon` (a final result) for such a session, and the naming reason text says that
   the terminal cannot be verified under the shared Codex daemon and recommends `koinon codex`.
   A direct `codex --no-daemon` start (parent is the Codex CLI itself) keeps today's
   unattributed behaviour; it is reported as `pane_not_host` without a launch.
3. **Sub-agents.** `identity` reads `x-codex-turn-metadata.thread_source`. A call with
   `subagent` registers its session with a flag `subagent` in the registration. The store gives a
   sub-agent session its peer name and never makes it an alias holder (`assignNames` skips the
   alias part). A sub-agent session is listed with `subagent: true`. Nothing else changes for it.

## Done

- Launcher argument tests (no CLI started), the `Getenv` coverage test, identity tests for
  `thread_source`, store tests for a sub-agent registration, naming tests for
  `codex_shared_daemon` with a synthetic process tree.
- `docs/INSTALL.md` and `docs/USAGE.md`: start Codex through `koinon codex`; what a plain
  `codex` start gets. `PROTOCOL.md`: the `subagent` session field and the naming result.
  `CHANGELOG.md`.
