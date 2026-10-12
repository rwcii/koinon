# Chunk 01 — evidence

Read `../decision.md` and `../definition-of-done.md` first.

## Outcome

`docs/sprints/2026-10-12-grok-family/spike.md` records, for each fact below, what was tested,
the exact commands, the Grok Build version and build hash, and the result. Probe code stays out
of the repository except as short excerpts in the record. The same pull request writes the
specifications of chunks 02 and 03 from the results, or records that they are not built, and
corrects any plan file that a result contradicts. The plan then passes gate B again.

## Method

All live probes run in the development container that `../definition-of-done.md` describes.
Its recipe is outside the repository: a `node:22.20.0-bookworm-slim` image with `tmux`, `git`
and `@xai-official/grok@1.0.50` installed by npm, run as the maintainer's UID. 1.0.50 is
npm's `latest` tag on 2026-10-12. A probe MCP server is a short script that logs its PID,
parent PID, environment and every JSON-RPC message it receives to a tmpfs, and answers
`initialize`, `tools/list` and `tools/call` with one echo tool. A probe of the HTTP transport
does the same on the container's own loopback. Sessions are started in tmux inside the
container.

Already recorded without a provider connection (2026-10-12, in the container, no network):

- `grok --version` prints `grok 1.0.50 (c58f321264ba)`. No public `xai-org/grok-build` commit
  in the newest ten syncs names that build in `SOURCE_REV`; the newest public sync is
  2026-09-29. Source lines are therefore leads, and the facts below come from the binary.
- The binary contains `GROK_SESSION_ID`, `X-Grok-Agent-ID` and `XAI_API_KEY`.
- `grok --help` lists `--session-id <UUID>` for a new conversation, `--resume`, `--continue`,
  `--fork-session`, `--leader-socket` (default `~/.grok/leader.sock`), `--agents`, and the
  `leader`, `agent`, `sessions` and `login` (`--oauth`, `--device-auth`) commands.
- Without a provider connection the TUI stops at "Connecting..." and starts no MCP server, so
  every fact below needs a signed-in session.

## Facts to settle

1. **Spawn topology.** Whether each session gets its own stdio MCP server process, or the
   leader shares one across sessions; the parent process of each server; whether this
   changes with separate `--leader-socket` paths.
2. **Per-call identity.** Whether a `tools/call` carries any session marker (`_meta`, request
   fields, or `X-Grok-Agent-ID` on the HTTP transport); whether one server process receives
   calls from two sessions; what `GROK_SESSION_ID` holds in each case.
3. **Launch-fixed identity.** Whether `--session-id <UUID>` fixes the ID that the MCP server
   and every call see, so that a Koinon launch can bind it.
4. **Reset and resume.** Grok's reset command(s) in the TUI, and whether a reset, `--resume`,
   `--continue` or `--fork-session` changes the session ID that a server and a call see, or
   restarts the server.
5. **Subagents.** Which session ID a subagent's tool call presents, and whether a server can
   tell it from the top-level session.
6. **Wake.** Whether a prompt can reach an idle, exact session from outside: the leader socket,
   ACP, `grok agent` or another documented interface; what it carries; who can reach it.
7. **Permissions.** Which approval a Koinon tool call needs, and the narrowest allow rule.

## Tests

None in the suite. Every probe runs in the container with synthetic repositories.

## Done

Every fact has a recorded result. The plan files agree with the results, and gate B has
approved them again.
