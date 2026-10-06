# Chunk 04 — wake

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

First settle the open spike facts 3, 4 and 5 (`../spike.md`): the Claude wake on macOS, the
`agy` `hooks.json` schema with a scratch plugin (including a text-only reply), and whether a
hook command receives `ANTIGRAVITY_LS_ADDRESS` and `ANTIGRAVITY_CSRF_TOKEN`. Record the results in
`../spike.md` in this pull request.


One adapter per family turns a `waiting` message into a content-free notice and records the
result:

- **Codex:** `codex queue` for the session's thread, with the configured CLI path.
- **Claude:** the Unix socket protocol of `PROTOCOL.md`, through the daemon's registry entry as
  settled in the spike.
- **DeepSeek:** the harness loopback RPC (`session/prompt`, `mode: "queue"`), with the existing
  credential and loopback checks of `README.md`.
- **Antigravity:** a stop hook that asks the daemon for waiting messages and continues the turn
  with the notice; plus, only if a hook receives the language-server address and token, a
  session-start hook that hands them to the daemon so it can wake an idle conversation.

A notice names only the inbox and the sequence range. A busy or unreachable receiver leaves the
message `waiting` and retries with backoff; a handled sequence is never notified again.

## Tests

The wake cases of the definition of done, each adapter against a synthetic receiver. Live check
2 with the user's authorization.

## Done

Gate passes; live check 2 recorded on the pull request.
