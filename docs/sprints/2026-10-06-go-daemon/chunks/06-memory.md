# Chunk 06 — memory

Read `../decision.md`, `../definition-of-done.md`, `PROTOCOL.md` and
`docs/PARITY-MEMORY-DESIGN.md` first.

## Outcome

The daemon holds one memory store per Git common directory, with record format 2, snapshot
pages, deltas, consumer cursors, acknowledgement rules, head-change notices and the error codes
of `PROTOCOL.md`. MCP tools: `memory_status`, `memory_sync`, `memory_ack`, `memory_record`. A
head change wakes subscribed sessions through chunk 04's adapters when 04 has merged, and is
otherwise visible at the next sync.

## Tests

The memory cases of the definition of done.

## Done

Gate passes.
