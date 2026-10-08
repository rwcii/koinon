# Chunk 01 — spike

Read `../decision.md` and `../definition-of-done.md` first.

## Outcome

`docs/sprints/2026-10-06-go-daemon/spike.md` records, for each fact below, what was tested,
the exact commands, the agent and CLI versions, and the result. Probe code stays out of the
repository except as short excerpts in the record. When a result contradicts `decision.md` or a
chunk file, the same pull request corrects the plan files, and the corrected plan passes
gate B again before chunk 02 starts.

## Facts to settle

1. **MCP from each sandbox.** A minimal stdio MCP server and a minimal streamable HTTP MCP
   server on loopback, each with one echo tool, are reached from: Claude Code (current
   settings), Codex 0.160 `workspace-write` without network access, the DeepSeek harness, and
   `agy` 1.3.0. Record for each family: whether a stdio server is sandboxed, whether an HTTP
   server on loopback is reachable, which environment variables the server process inherits
   (session identifiers such as `CODEX_THREAD_ID`, `DSH_SESSION_ID`, Claude's session ID), and
   which approval each tool call needs.
2. **DeepSeek MCP support.** Whether the harness supports MCP at all. If not, record the
   command path that criterion 5 then requires.
3. **Claude wake from Go.** A Go program registered once in Claude Code's session registry
   delivers a content-free notice to an idle Claude session over its Unix socket under the
   protocol in `PROTOCOL.md`, on Linux and macOS. Record whether one registry entry for the
   daemon suffices and how `ListAgents` shows it.
4. **Antigravity turn boundary.** The `agy` stop hook: its configuration format and location,
   whether it fires after a text-only reply, and whether its result can continue the turn with
   a prompt.
5. **Antigravity language-server RPC.** Whether `SendUserCascadeMessage` (or a related method)
   on `agy`'s loopback language server can put a notice into an idle conversation, where the
   CSRF token and the ports come from, and whether this is stable across an `agy` update.
   Record it as usable only if the token and ports can be obtained without reading another
   process's memory.

## Tests

None in the suite. Each live probe needs the maintainer's authorization and runs in scratch
directories with scratch agent sessions, never in the maintainer's working sessions.

## Done

Every fact has a recorded result. The plan files agree with the results.
