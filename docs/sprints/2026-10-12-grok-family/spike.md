# Sprint 2026-10-12 — Grok agent family — Evidence (chunk 01)

Recorded 2026-10-12 by Claude in the development container that `definition-of-done.md`
describes: Grok Build 1.0.50 (`grok 1.0.50 (c58f321264ba)`, npm `@xai-official/grok`, the
`latest` tag), signed in with `grok login --device-auth`, Grok 4.7 model, UID 1000, all
capabilities dropped, `no-new-privileges`, no host mounts and no route to the host's loopback
services (a connection to the host daemon's port was refused on 127.0.0.1 and timed out on
the Docker gateway). Session IDs below are shortened; they belonged to throwaway sessions.

The probe MCP server logged its PID, parent PID, working directory, `GROK_SESSION_ID`,
`TMUX_PANE` and every JSON-RPC message, and answered one tool, `probe_echo`, with its own PID
and `GROK_SESSION_ID`. It was registered with `grok mcp add probe -- node <probe>`. Sessions ran
in tmux with `--always-approve`.

## 1. Spawn topology

**Default (no leader).** Each TUI session starts its own stdio MCP server as a direct child of
the `grok` TUI process. Two TUIs in two panes gave two servers (parents 946 and 1097), each
with its own `GROK_SESSION_ID`, the TUI's working directory and its own `TMUX_PANE`. The
client identifies itself in `initialize` as `clientInfo.name = "grok-shell-<server>"`,
`version = "1.0.50"`, protocol `2025-11-25`.

**Leader mode** (`[cli] use_leader = true` in `~/.grok/config.toml`, or `--leader`; off by
default per Grok's `02-authentication.md`). The first TUI starts `grok-native agent leader
--no-exit-on-disconnect --relay-on-demand --grok-ws-url wss://code.grok.com/ws/code-agent` and
`~/.grok/leader.sock` (socket mode 0755 in the owner's `~/.grok`). Each session still gets its
own MCP server, but as a child of the leader, not of its TUI. Every server inherits the first
TUI's working directory and `TMUX_PANE`: the second session, in pane `%1` and directory `w5`,
had a server with `cwd` `w4` and `TMUX_PANE=%0`.

## 2. Per-call identity

A `tools/call` carries only `params._meta.progressToken`, the tool name and the arguments. No
session marker reaches the server in either mode. Each server received only its own session's
calls, and its `GROK_SESSION_ID` named that session, in both modes:

- default: `from-g1` reached the server of `01a12e19-1c8d…`, `from-g2` the server of
  `01a12e19-2b44…`;
- leader: `from-L2` reached only the second session's server (`01a12e1d-57ae…`).

So the server process's `GROK_SESSION_ID` identifies the calling session. In leader mode the
server's parent process, working directory and `TMUX_PANE` do not identify its session.

## 3. Launch-fixed identity

`grok --session-id 7f3c2a10-0000-4000-8000-00000000c0de` started a session whose MCP server
had exactly that `GROK_SESSION_ID`. A launcher can choose the ID of the first session.

## 4. Reset and resume

- `/clear` ("Start a new session") in a default TUI started a new MCP server (PID 1352) under
  the same TUI process and pane, with a new `GROK_SESSION_ID` (`01a12e1b-899d…`). The next call
  reached only the new server. **The old server (PID 1198) kept running with stdin open**, so a
  Koinon MCP server in that position would keep renewing the cleared session.
- `grok --resume <id>` in a new TUI process started a server with the resumed session's
  original `GROK_SESSION_ID` and the new process as parent.

## 5. Subagents

A subagent ran as its own Grok session (`01a12e1a-8444…`, its own directory under
`~/.grok/sessions`), but its `probe_echo` call went to the **parent session's** server and
carried the parent's `GROK_SESSION_ID`, with no subagent marker. A server cannot tell a
subagent's call from its parent's. (Claude Code subagents also call through the parent's
server under the parent's identity.)

## 6. Wake

- Default mode: no external interface to a running TUI session was found; the TUI runs its
  backend in process and opens no socket.
- Leader mode: a local ACP client (`grok agent --leader stdio`) sent `initialize`, then
  `session/load {sessionId, cwd}` and `session/prompt {sessionId, prompt}` for the second
  session. The prompt appeared in that session's TUI pane, the turn ran, and its tool call
  reached only that session's server; the first session's pane showed nothing. The leader
  also reports `_x.ai/queue/changed` with the queued prompt. This is an exact-session prompt
  path; a Koinon wake through it would carry only Koinon's fixed content-free notice.

## 7. Permissions

Grok merges allow/ask/deny rules from its own `config.toml` scopes, `--allow`/`--deny` and
Claude-style `.claude/settings*.json`. MCP tools are named `server__tool`; the narrowest allow
rule for Koinon's tools is `MCPTool(koinon__*)` (or `mcp__koinon` in the Claude spelling).
Models reach MCP tools through Grok's `search_tool`/`use_tool` meta-tools.

## Consequences for the plan

- Per-call identity exists: one MCP server per session with the session's `GROK_SESSION_ID`.
- Host, pane and directory evidence from process ancestry and environment holds only in the
  default mode. In leader mode it names the leader and the first TUI for every session.
- An exact-session wake exists only in leader mode.
- `/clear` leaves the previous session's MCP server running.

Chunks 02 and 03 are specified from these facts in `chunks/02-adapter.md` and
`chunks/03-wake.md`.
