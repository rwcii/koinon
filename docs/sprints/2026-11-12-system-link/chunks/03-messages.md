# Chunk 03 — messages

Depends on 02. Cites `decision.md` (LINK-670-WHAT items 1, 3 to 5, LINK-670-OFFLINE) and
`definition-of-done.md` (Messages; Checks and evidence).

## Storage

Schema: the next free version on develop.

- `link_outbox`: local ID, cross-node message ID, pair ID and generation, sender key and name,
  recipient NAME, body, created time, wire sequence (null until head), state (`queued`,
  `received`, `refused`, `cancelled`, `uncertain`), reason, terminal time. Indexes: (pair,
  generation, state, local ID) for the head; (state, terminal time) for retention.
- `link_streams`: pair ID, generation, direction, next sequence (outgoing), high-water, last
  result and digest (incoming).
- `messages` gets a nullable typed origin: pair ID, generation, cross-node ID and remote sender
  name. Local messages keep null.

## Protocol

Link operation `deliver` with sequence, cross-node ID, recipient NAME, sender name, body and
digest; reply `received`, `refused` (reason) or `retry` (busy, full). The stream rules of the
definition of done apply. A delivery worker per connected pair sends the head; backoff on
`retry` and on connection loss.

## Interfaces

- `send` (MCP, CLI, HTTP) accepts `NAME@COMPUTER` and returns the outbox state and ID;
  `delivery` reads the state by ID.
- Inbox entries from another computer show `from` as `NAME@COMPUTER` and `origin` `link`; a
  reply to that address goes back through the stored pair.
- Dashboard Remote settings: the queue for each paired computer with states, and Cancel.
- Retention sweep: finished outbox rows 30 days after their terminal time.

## Evidence

The tests of the Messages section, with two in-process daemons. Then the live pairing and
message round trip between a Linux and a macOS computer on one tailnet, recorded in
`verification.md`, before this chunk merges. The links PDR is written in this chunk.
