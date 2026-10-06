# Chunk 04 — wake

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

First verify the Claude wake on macOS (`../spike.md`, fact 3) and record it in `../spike.md`
in this pull request.


One adapter per family turns a `waiting` message into a content-free notice and records the
result:

- **Codex:** `codex queue` for the session's thread, with the configured CLI path.
- **Claude:** the Unix socket protocol of `PROTOCOL.md`, through the daemon's registry entry as
  settled in the spike.
- **DeepSeek:** the harness loopback RPC (`session/prompt`, `mode: "queue"`), with the existing
  credential and loopback checks of `README.md`.
- **Antigravity:** a `Stop` handler in the workspace `.agents/hooks.json` (`../spike.md`,
  fact 4) asks the daemon for waiting messages of its `conversationId` and, when there are any,
  returns `{"decision": "continue", "reason": <notice>}`; otherwise it lets the agent stop.
- **OpenCode:** `POST /session/{id}/prompt_async` on the session's loopback server with the
  server password (`../spike.md`, fact 6), only when `GET /session/status` shows the session idle;
  never the permission or question endpoints.

A notice names only the inbox and the sequence range. A busy or unreachable receiver leaves the
message `waiting` and retries with backoff. An outcome the daemon cannot confirm is recorded
as `uncertain` and retried while the sequence is unacknowledged (at least once); an acknowledged
sequence is never notified.

## Tests

The wake cases of the definition of done, each adapter against a synthetic receiver. Live check
2 with the user's authorization.

## Done

Gate passes; live check 2 recorded on the pull request.
