# Chunk 09 — dashboard actions

Read `../decision.md` and `../definition-of-done.md` first.

## Outcome

Actions: retire a session, release a claim, acknowledge or clear an inbox, send a message to a
session, start a Codex, `agy` or OpenCode session through chunk 10's launcher. Each state-changing request
needs a CSRF token and writes one audit record (time, action, target, result). An audit view
lists the records.

## Tests

The dashboard action cases of the definition of done.

## Done

Gate passes.
