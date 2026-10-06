# Sprint 2026-10-06 — Go daemon — Spike results

Chunk 01 record. Each fact from `decision.md` ("Unverified facts") with the test, the versions
and the result. Probes ran on Linux (kernel 7.0) on 2026-10-06, in scratch directories, with
scratch agent runs; MCP servers were configured through command-line overrides or, for `agy`,
added and then removed with the configuration files restored byte for byte. The probe was a
minimal MCP server (stdio, or streamable HTTP on loopback) with one tool that reports its
process, environment names, sandbox markers (`/proc/self/status` `Seccomp`, `NoNewPrivs`,
namespaces), a loopback TCP connect, a Unix socket connect and a write outside the workspace.

## 1. MCP from each sandbox

| Family (version) | stdio server sandboxed | loopback HTTP reached | Session identity available to the server |
| --- | --- | --- | --- |
| Codex 0.160.0, `workspace-write`, no network | No: `Seccomp 0`, host namespaces, TCP and Unix connect succeed, write outside the workspace succeeds | Yes (`codex-mcp-client/0.160.0`) | Not in the environment (`CODEX_THREAD_ID` absent). Every `tools/call` carries `_meta.threadId`, `_meta.sessionId` and `x-codex-turn-metadata` (`thread_id`, `turn_id`, `model`, `sandbox_mode`). The stdio server's parent is the Codex process. |
| Claude Code 2.1.292 (`-p`) | No (this host runs Claude without the Bash sandbox) | Yes (`claude-code/2.1.292`), no session header | `CLAUDE_CODE_SESSION_ID`, `TMUX`, `TMUX_PANE` and the working directory in the stdio server's environment. `tools/call` `_meta` carries only `claudecode/toolUseId`. |
| `agy` 1.3.0 (`-p`) | No: `Seccomp 0`, connects and writes succeed | Not tested | Every `tools/call` carries `_meta["antigravity.google/conversation_id"]` and `antigravity.google/artifacts_dir`. The server inherits the launching environment. |
| DeepSeek harness | Not tested: the harness is not installed on the probe host | — | — |

Approvals: Codex `exec` ran both MCP tools under `approval: on-request` without a prompt. `agy -p`
refused the MCP call until a permission rule `mcp(<server>/<tool>)` allowed it; an interactive
session asks instead. Claude needed `--allowedTools` in `-p` mode.

Consequences for the plan:

- An HTTP MCP server cannot tell sessions apart for Claude (no identity in headers or calls), so
  the stdio `koinon mcp` command of chunk 05 is required. It must take identity per call where
  the family sends it (Codex `threadId`, `agy` `conversation_id`), not once at start: one Codex
  process changes thread on `/clear` while its MCP server keeps running. For Claude it takes
  `CLAUDE_CODE_SESSION_ID` from its environment.
- Environment identity is not trustworthy on its own. The default tmux server on the probe host
  carries one Claude session's `CLAUDE_CODE_SESSION_ID`, `CLAUDE_CODE_MESSAGING_SOCKET` and
  `CLAUDE_CODE_MESSAGING_TOKEN` in its global environment. They are in the tmux server process's
  own environment, so the shell that started the server had them. That Claude process has since
  ended. Every tmux session created on the server afterwards inherits the variables, also a
  session a person starts with `tmux new -s <name>`, and also non-Claude agents, so they report
  the identity of a session that no longer exists. `koinon mcp`
  accepts a Claude session identifier only when the MCP client is Claude Code (`clientInfo.name`
  `claude-code` in `initialize`) and its parent process is that Claude process. The launcher of
  chunk 10 removes inherited `CLAUDE_*` variables from the sessions it starts.

## 2. DeepSeek MCP support

Open. The harness is not installed on the probe host. Needs a host with the DeepSeek harness, or
the user's confirmation of its MCP support. Until settled, criterion 5 for DeepSeek uses the
documented command path, run with the agent's approval as today.

## 3. Claude wake from Go

Verified on Linux. A Go program (`CGO_ENABLED=0`, Go 1.26.6) with no Claude registry entry
connected to a Claude session's socket under `/run/user/1000/cc-socks/`, wrote one
`{"msgV":1,"type":"user",...}` frame with a `<cross-session-message from=… from-name="koinon">`
envelope, and the session showed it with the sender name `koinon`. No token was needed for a
same-user sender. The sender needs no registry entry to wake a Claude session; a registry entry
is needed only for Claude sessions to send to Koinon through Claude's own peer feature, which
criterion 4 replaces with MCP. A reply socket path longer than the AF_UNIX limit failed with
`bind: invalid argument`, which the edge case in `definition-of-done.md` covers. macOS is not
verified (no macOS host with Claude Code); the Python runtime's macOS behaviour in `PROTOCOL.md`
applies until chunk 04 verifies it.

## 4. Antigravity turn boundary

Partly verified, from the binary's symbols and the `agy` built-in plugin documentation:

- A user customization stop hook exists (`customization/hooks.NewStopHook`), loaded from a JSON
  hook specification (`jsonhook.JSONHookSpec`); plugins carry it as `hooks.json`.
- The hook receives `FullyIdle`, `ExecutionNum`, `TerminationReason`, `FinalModelOutput` and
  returns `Decision` and `Reason`; the runtime can inject a user message step
  (`injectUserMessageStep`) and limits continuations (`MaxStopHookContinuations`).

Open: the exact `hooks.json` schema and whether the hook fires after a text-only reply. This is
settled at the start of chunk 04 with a scratch plugin before the adapter is written.

## 5. Antigravity language-server RPC

`agy` runs a language server in its own process on loopback (one HTTP Connect port, one HTTPS
gRPC port, random each start), with methods such as
`LanguageServerService/SendUserCascadeMessage` and `StartCascade`, protected by an
`x-codeium-csrf-token` header. Its built-in plugin documentation tells the agent to call this
endpoint and says that `ANTIGRAVITY_LS_ADDRESS` and `ANTIGRAVITY_CSRF_TOKEN` are in the agent's
shell environment. Results:

- The token is in no file and not in the `agy` process environment.
- The MCP server's environment does not contain either variable (`agy -p`).

So the daemon cannot obtain the address and token without reading another process's memory,
and under chunk 01's rule this path is not usable as found. One path remains to test in chunk 04:
whether a hook command receives the two variables, so that a session-start hook could pass them
to the daemon. Until that is shown, Antigravity delivery is at the turn boundary only, as
criterion 6 allows.

## Plan changes

None to the criteria or the chunk order. Chunk 05 takes identity per call and checks the MCP
client before trusting an environment identity (fact 1). Chunk 10 clears inherited `CLAUDE_*`
variables. Chunk 04 starts with the `hooks.json` schema test and the hook-environment test
(facts 4 and 5) and verifies the Claude wake on macOS (fact 3). Fact 2 stays open with the
fallback stated above.
