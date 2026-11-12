# Chunk 02 — directory and addressing

Depends on 01. Cites `decision.md` (LINK-670-WHAT items 2 and 3, LINK-670-ADDR) and
`definition-of-done.md` (Directory and addressing).

## Protocol

New link operations: `directory` (request and reply) and `name-update`. A daemon answers
`directory` with its receivers, built from the same session rows as `peers`, with only the
fields that the definition of done lists. Each computer asks for the directory when a
connection opens and at a fixed interval while connected, and keeps the last copy with its
time in memory.

## Addressing

One parser for `NAME@COMPUTER` used by send, peer status and the dashboard: NAME with the local
name rules, COMPUTER with the advertised-name grammar, NAME up to 256 bytes as a local send accepts, total at most 510 bytes. Effective names
of pairs are unique in `link.json`; a collision gets the next free `-N`; a rename is one atomic
write that never changes another pair.

## Interfaces

- `peers` (MCP, CLI, HTTP) adds remote entries with `address` `NAME@COMPUTER`, `remote` true,
  `computer`, `seen_at`, family, role and repository label. Local entries do not change.
- `peer_status` accepts `NAME@COMPUTER` and answers from the cached directory, with its time.
- The dashboard agent list shows remote entries in a separate group for each computer.

## Tests

The tests of the Directory and addressing section, with two in-process daemons.
