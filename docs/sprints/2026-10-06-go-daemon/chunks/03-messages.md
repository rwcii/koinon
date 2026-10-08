# Chunk 03 — messages

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

- The daemon stores messages per receiving session with gapless sequence numbers, a delivery
  state (waiting, notified, uncertain, failed with reason) and an acknowledgement state.
- Loopback API for: list peers, send by session name or alias, read inbox from a sequence,
  acknowledge through a sequence, read the delivery outcome of a sent message. Every call
  authenticates with the secret and names the caller's session.
- `koinon peers`, `koinon send`, `koinon inbox`, `koinon ack` call the same API.
- Names: a peer name and an alias per family and repository, with the uniqueness rules of the
  current runtime (`docs/sprints/2026-09-24-stable-alias/`), held by the daemon instead of the
  Claude registry.
- No wake yet: a stored message stays `waiting`.

## Tests

The message cases of the definition of done, for all family pairs, with synthetic sessions.

## Done

Gate passes.
