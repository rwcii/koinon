# Chunk 02 — Notices over the link

Cites `../decision.md` (acceptance 2, 3, 7, 8) and `../definition-of-done.md` (Notices;
Lifecycle at the origin, for removal).

## Outcome

The notices that chunk 01 queues reach the other computer: each one is a short typed message
in the named agent's inbox, with its ordinary wake and no card text. The origin shows each
notice as `received`, `refused` with the reason, or `unsupported`. A computer at an earlier
release resumes by itself after its upgrade.

## Work

1. Link: the probe operation and the notice operation. One notice worker per pair, with its
   own connection and stream, under the #670 stream rules; the probe on each new connection;
   `unsupported` and the re-probe.
2. Receiver: resolve NAME as `linkInsert` does; insert the typed notice with the stream's high
   water in one transaction, before the answer and the wake; answer `refused` with the reason
   for an unknown, unheld or inactive name.
3. The `removed` notice for a recipient removed by an edit.
4. PROTOCOL.md for the operations and the notice fields; CHANGELOG.md.

## Tests

The Notices tests in the definition of done, with two daemons over the link test harness and
the v0.13.1 codec for the probe.

## Out of this chunk

Remote read, acknowledgement, the dashboard view and the live check (03).
