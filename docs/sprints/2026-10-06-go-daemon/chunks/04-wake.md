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

A notice names only the inbox and the sequence range. A busy or unreachable receiver leaves the
message `waiting` and retries with backoff; a handled sequence is never notified again.

## Tests

The wake cases of the definition of done, each adapter against a synthetic receiver. Live check
2 with the user's authorization.

## Done

Gate passes; live check 2 recorded on the pull request.
