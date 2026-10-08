# Sprint 2026-10-08 — participants — Live checks

Facts F1 to F4 of `decision.md`, checked on 2026-10-08 with the maintainer's authorization in
throwaway sessions. Linux (kernel 7.0), Codex CLI 0.161.0, Claude Code 2.1.294, tmux 3.6. Each
scratch agent ran in a scratch Git repository on a private tmux server (`tmux -S`), with a clean
environment and a probe MCP server instead of Koinon. The probe logs its process ID, parent
process ID, selected environment variables and the `_meta` of each `tools/call`. No scratch
session registered with the maintainer's daemon. The Codex configuration file was restored byte
for byte after each check.

## F1. Which process runs a Codex session's MCP servers

| Start | Parent of the MCP servers | `/clear` |
| --- | --- | --- |
| `codex` (direct, as a person starts it) | The shared Codex app-server daemon (`codex app-server --managed-daemon`), not the TUI in the pane. The maintainer's running Codex sessions show the same. | A new MCP server process starts for the new thread. The old thread's server keeps running. |
| `codex -c …` (as `koinon codex` starts it) | The Codex TUI process in the pane. | A new MCP server process starts for the new thread, also a child of the same TUI. |

- The process table holds no link from a thread or its MCP server to the TUI that shows it in the
  direct start. The daemon holds the thread's writer lock (`~/.codex/thread-writer-locks/`).
- An MCP server of a direct start outlives its TUI: after the TUI ended, the servers of its last
  thread kept running under the daemon.
- Codex passes a filtered environment to MCP servers. `TMUX`, `TMUX_PANE` and `KOINON_LAUNCH_ID`
  were absent from the probe, also when the TUI had them. With
  `-c 'mcp_servers.<name>.env_vars=["KOINON_LAUNCH_ID"]'` the probe received
  `KOINON_LAUNCH_ID` (#247).
- Every call carries `_meta.threadId`, `_meta.sessionId` and `x-codex-turn-metadata` with
  `thread_id` and `thread_source`.

Consequence: "the same MCP server serves the holder and the new session" never occurs for an
interactive Codex session. For a launched session, the TUI is the host process: a new user
thread under the same host is the reset evidence. A direct start has no verifiable host.

## F2. Concurrent threads

A sub-agent runs as its own thread with its own MCP server process. Its calls carry
`x-codex-turn-metadata.thread_source` = `subagent`; the user's thread carries `user`. The parent
and the sub-agent called the probe at the same time through different servers. The maintainer's
Codex state database records 202 parent-to-child thread spawns, so sub-agents are in common use.

Consequence: one MCP server serves one thread, so calls from two threads never share a server.
Without a rule, each sub-agent that calls a Koinon tool registers as its own session in the same
repository and competes for the address.

## F3. Claude `/clear`

`/clear` started a new transcript (a new Claude session ID) in the same Claude process. The MCP
server process stayed the same, and its `CLAUDE_CODE_SESSION_ID` kept the first value. Koinon
therefore sees one continuous session across a Claude `/clear`; no succession occurs. Claude
`/resume` inside a running process was not checked.

## F4. Claude background job

- `claude --bg` runs the job's Claude process under `claude bg-pty-host`, under the Claude daemon
  (`claude daemon run`). The job's MCP server has the job's `CLAUDE_CODE_SESSION_ID` and no
  `TMUX` or `TMUX_PANE`.
- `claude attach <short ID>` in a tmux pane is a client process in that pane's process tree. Its
  command line holds the short ID, which is the first eight characters of the job's session ID.
  The `bg-pty-host` command line holds the same ID in its socket path.
- The attached client started no MCP server, so it does not register as a second Koinon session.
  The second peer in #147 was the client's record in Claude Code's own session registry, which
  the Python runtime listed. The Go runtime lists only sessions that register through
  `koinon mcp`.
