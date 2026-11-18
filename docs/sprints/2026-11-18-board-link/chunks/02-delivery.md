# Chunk 02 — Delivery over the link

Cites `../decision.md` (acceptance 2 to 5, 6 for unpair, 7, 8, 10) and
`../definition-of-done.md` (Authority and identity; Transport; Delivery to the agent;
Acknowledgement; Lifecycle and retention, for removal and unpair).

## Outcome

The board entries that chunk 01 queues reach the other computer. Each recipient there has its
own read-only copy with a copy number, gets a typed inbox message with its wake for the copy,
each change and each verdict, and sees each verdict in `decision_pending`. `decision_ack`
there reaches the origin. A computer at an earlier release shows `unsupported` and resumes
after its upgrade. Removal and unpair reach the copies.

## Work

1. Link: the probe operation and the board operation, with the board frame bound of 1 MiB in
   `link.FrameLimit`. One board worker per pair, with its own connection and stream, under the
   #670 stream rules; the probe on each new connection; `unsupported` and the re-probe.
2. Receiver: resolve NAME as `linkInsert` does; store the copy, the change or the verdict
   with its pending delivery and inbox message, with the stream's high water, in one
   transaction, before the answer and the wake; an earlier revision is a stored no-op; answer
   `refused` with the reason for an unknown, unheld or inactive name.
3. Read tools on the receiver: copies in `decision_list --filter mine`; `decision_get` by
   reference, `REFERENCE@COMPUTER` or copy number; remote deliveries in `decision_pending`
   with the remote cursor; remote-verdict numbers in `decision_ack`. Every write tool refuses
   a copy with `decision_remote_read_only`. MCP schemas, CLI flags and the guide's tool list.
4. Acknowledgement: queue it on the board stream to the origin; the origin marks the
   recipient's delivery `acknowledged` once.
5. Removal (`unnamed`) and unpair (`unpaired`) on the receiver.
6. PROTOCOL.md for the operations, fields, numbers and cursors; CHANGELOG.md.

## Tests

Transport, Delivery to the agent, Acknowledgement and the removal and unpair tests of
Lifecycle and retention in the definition of done, with two daemons over the link test
harness, and the v0.13.1 codec for the probe.

## Out of this chunk

Retention, the dashboard view and the live check (03).
