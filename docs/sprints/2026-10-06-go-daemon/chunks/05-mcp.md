# Chunk 05 — MCP and agent setup

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

- `koinon mcp` is a stdio MCP server that the agent starts. It takes the session identity per
  call where the family sends it (Codex `_meta.threadId`, `agy`
  `_meta["antigravity.google/conversation_id"]`) and from `CLAUDE_CODE_SESSION_ID` for Claude,
  which it trusts only when `initialize` names the client `claude-code` and its parent process
  is that Claude process (`../spike.md`, fact 1). It registers or renews the session and
  forwards tool calls to the daemon over loopback with the secret.
- Tools: `peers`, `send`, `inbox`, `ack`, `delivery` (outcome of a sent message). Chunks 06 and
  07 add their tools.
- `koinon setup <claude|codex|deepseek|agy|opencode>` adds the MCP server to that agent's configuration
  through the agent's own command (`claude mcp add`, `codex mcp add`, `agy mcp add`, `opencode mcp`, or the
  path the spike recorded for DeepSeek), installs the `agy` stop hook, and installs an OpenCode plugin
  that adds the calling session's identifier to Koinon tool calls. Prove that per-call
  identity first (`../spike.md`, fact 6); if it cannot be proven, stop and take the OpenCode
  scope back to the maintainer before building OpenCode setup. It preserves all
  other configuration and reports what it changed.
- `koinon guide --agent <family>` replaces `session.py guide`: it tells each family to use the
  Koinon tools for every agent message, Claude included, and keeps the existing rules on peer
  content, permissions and notices.

## Tests

The MCP cases of the definition of done; setup against temporary agent configuration
directories, including preservation of unrelated content and a repeated run.

## Done

Gate passes; live checks 1 and 3 recorded on the pull request.
