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

Verified in an interactive `agy` 1.3.0 session (Gemini 3.8 Flash) with a scratch workspace hook,
following `agy`'s built-in `agy-customizations/docs/hooks.md`:

- Hooks live in `<workspace>/.agents/hooks.json`, keyed by hook name; a `Stop` handler is
  `{"type": "command", "command": "...", "timeout": N}`. `agy` loaded a new file without a
  restart.
- The `Stop` handler fired after a text-only reply (`terminationReason: NO_TOOL_CALL`,
  `fullyIdle: true`) with `conversationId`, `workspacePaths`, `transcriptPath`, `modelName` and
  `executionNum` on stdin.
- Returning `{"decision": "continue", "reason": "..."}` made the agent take one more turn and act
  on the reason (it answered the test word). The next `Stop` call had `executionNum: 1`;
  returning `{}` let it stop.
- `PreInvocation` handlers can inject `{"userMessage": "..."}` before a model call (documented,
  not tested).
- The hook's environment has `ANTIGRAVITY_CONVERSATION_ID`, not the language-server address or
  token.

## 5. Antigravity language-server RPC

`agy` runs a language server in its own process on loopback (one HTTP Connect port, one HTTPS
gRPC port, random each start), with methods such as
`LanguageServerService/SendUserCascadeMessage` and `StartCascade`, protected by an
`x-codeium-csrf-token` header. Its built-in plugin documentation tells the agent to call this
endpoint. Results:

- The agent's own command shell has `ANTIGRAVITY_LS_ADDRESS`, `ANTIGRAVITY_CSRF_TOKEN`,
  `ANTIGRAVITY_CONVERSATION_ID` and `ANTIGRAVITY_TRAJECTORY_ID` (names checked, values not read).
- The token is in no file and not in the `agy` process environment; MCP servers (`agy -p`) and
  hook commands do not receive the address or the token.

So only a command that the agent itself runs can hand the address and token to the daemon, with
the agent's command approval. That makes an idle wake possible but not automatic. It stays out
of this sprint: Antigravity delivery is at the turn boundary, as criterion 6 allows.

While approving the test command by typing into the terminal, an `Enter` moved the selection
instead of choosing it, and the dialog saved a permanent allow rule (since reverted). This
confirms the decision not to type into agent terminals.

## 6. OpenCode

Verified with OpenCode 1.18.35 on a scratch `opencode serve` (loopback, own workspace
`opencode.json`, no user configuration changed); the user's own OpenCode session was not used.

- **Server API.** `opencode serve` and the terminal UI with `--port` expose a documented HTTP API
  (OpenAPI at `/doc`): `POST /session/{id}/prompt_async`, `GET /session/status`, `GET /event`,
  and also permission and question reply endpoints. Without `OPENCODE_SERVER_PASSWORD` the API
  has no authentication; with it, a request without basic authentication gets 401.
- **Wake.** `prompt_async` on an idle session returned 204, and the session took a turn on the
  message (called the requested MCP tool, then answered). Status went `busy`, then idle.
- **MCP.** The local MCP server ran unsandboxed (`Seccomp 0`; loopback TCP connect succeeded) and
  inherited the server's environment. Its `tools/call` `_meta` held only `progressToken`: no
  session identifier.
- **Session shell.** A session's command shell has `OPENCODE`, `OPENCODE_PID` and
  `OPENCODE_SERVER_PASSWORD`, and no session identifier. An agent can therefore reach its own
  server's API; this stays inside the same-user boundary.

Chunk 05 proved the per-call source with OpenCode 1.18.35 on a scratch `opencode serve`
(loopback, password, own workspace): a plugin in `.opencode/plugins/` whose `tool.execute.before`
sets `output.args.koinon_session = input.sessionID` for the MCP server's tools. OpenCode passes the
same argument object to the hook and to the MCP call, so the field reaches the server. Two
sessions on one server each called a probe tool and arrived with their own session IDs; the MCP
client names itself `opencode` in `initialize`. Why the spike's first attempt left the arguments
unchanged is not known; the directory is not the cause, since OpenCode loads both `plugin/` and
`plugins/`.

Consequence: the wake path is the supported API, with a loopback server and a password that the
Koinon launcher sets. Session identity for MCP calls is not given by OpenCode. Chunk 05 must
prove a per-call source, such as an OpenCode plugin that adds the calling session's identifier to
Koinon tool calls; a first attempt with a `tool.execute.before` plugin in
`.opencode/plugin/` did not change the MCP arguments. Without a proven source, OpenCode scope
returns to the user.

## Plan changes

None to the criteria or the chunk order. Chunk 05 takes identity per call and checks the MCP
client before trusting an environment identity (fact 1). Chunk 10 clears inherited `CLAUDE_*`
variables. Chunk 04 uses the verified `Stop` hook for Antigravity and
verifies the Claude wake on macOS (fact 3). Fact 2 stays open with the
fallback stated above. OpenCode (fact 6) adds one wake adapter (chunk 04), one setup with the
identity plugin (chunk 05) and one launcher (chunk 10).
