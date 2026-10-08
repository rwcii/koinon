# Chunk 01 — Launched sessions only

Criteria 1 (sub-agents), 6, 9 and 10 of `decision.md`. Delivers #247.
Facts: `live-checks.md`, F1, F2 and F5.

## Current state

- `internal/launcher/launcher.go` records a launch (`core.LaunchTarget`; `HostPID` is the
  launcher's own PID, which `platform.Exec` keeps for the CLI), exports `KOINON_LAUNCH_ID`,
  `KOINON_STATE_DIR` and `KOINON_DAEMON_ADDRESS` for every family and, for Codex,
  `KOINON_CODEX_HOST`, and runs `codex -c shell_environment_policy.set.KOINON_CODEX_HOST="<pid>"`.
- `register` (`internal/mcp/tools.go`) registers every caller, launched or not. For Claude it
  ignores the launch ID and records `claude_pid`.
- Codex 0.161 passes an MCP server only the variables that `mcp_servers.<name>.env_vars` lists,
  so a launched Codex session's `koinon mcp` sees no launch variable (#247). A Claude background
  job inherits the Claude background service's environment (F5).
- `identity` (`internal/mcp/identity.go`) ignores `x-codex-turn-metadata.thread_source`.

## Change

1. **Launch everywhere.** For Codex the command line becomes
   `codex --no-daemon -c shell_environment_policy.set.KOINON_CODEX_HOST="<pid>"
   -c 'mcp_servers.koinon.env_vars=[<names>]' <args>`. `<names>` is one constant shared with
   `internal/mcp`: every variable that `koinon mcp` reads and the launcher sets or inherits
   (`KOINON_LAUNCH_ID`, `KOINON_STATE_DIR`, `KOINON_DAEMON_ADDRESS`, `TMUX`, `TMUX_PANE`); a test
   asserts that every non-identity `Getenv` name in `koinon mcp` is in it. A user argument that
   already contains `--no-daemon` is not repeated. Claude, OpenCode and Antigravity keep the
   environment path.
2. **Claude background jobs.** `koinon claude --bg [args]` records a launch whose target marks a
   background job, writes a private settings file in the launch's state directory with
   `{"env": {<launch variables>}}`, runs `claude --bg <args> --settings <file>`, prints the job ID
   that Claude returns, and records it with the launch. The settings file is removed when the
   launch record is retired.
3. **Islanded direct starts.** `koinon mcp` registers a caller only when its environment carries a
   launch ID that the daemon knows for that family and directory. Without one, every tool call
   returns `not_launched` with the launcher command for the family, and nothing is registered or
   listed. `tools/list` still lists the tools, so the agent can read the refusal. For Claude, the
   launch record supplies the host; the `claude_pid` check stays as a second condition.
4. **Sub-agents.** `identity` reads `x-codex-turn-metadata.thread_source`. A call with `subagent`
   registers its session with `subagent` set. The store gives it a peer name and never makes it an
   alias holder. It is listed with `subagent: true`.

## Done

- Launcher argument tests for every family, `--bg` settings file content, mode and removal (no
  CLI started), the `Getenv` coverage test, `not_launched` for each family with no launch ID and
  with an unknown launch ID, identity tests for `thread_source`, store tests for a sub-agent.
- Existing tests that register sessions without a launch are moved to launched registrations;
  none is deleted without a replacement.
- `docs/INSTALL.md` and `docs/USAGE.md`: start every agent with `koinon <family>`; a direct start
  is an islanded instance. `PROTOCOL.md`: `not_launched`, the `subagent` field.
  The installed `koinon guide` text. `CHANGELOG.md`, with the upgrade note of `decision.md`.
