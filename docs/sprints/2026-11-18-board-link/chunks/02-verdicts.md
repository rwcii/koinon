# Chunk 02 — Verdicts and acknowledgement

Cites `../decision.md` (acceptance 3 for verdicts, 4, 7, 8) and `../definition-of-done.md`
(Delivery to the agent, for verdicts; Acknowledgement; Transport).

## Outcome

A verdict on a card at computer A reaches each remote recipient on computer B: one typed inbox
message with its ordinary wake, and one entry in its `decision_pending` with the card text and
the snapshot. `decision_ack` on B records the acknowledgement at once and sends it to A, where
that delivery shows `acknowledged`.

## Work

1. Origin: `deliverVerdict` queues one board entry for each remote recipient in the verdict's
   transaction, with the verdict, its snapshot and the entry text. The delivery row holds the
   remote state.
2. Receiver: store the remote verdict keyed by pair ID and origin verdict ID, with its pending
   delivery and the inbox message, before the answer and before the wake. A reopen or a
   withdrawal supersedes as for a local recipient.
3. `decision_pending` lists local and remote deliveries in one stable order with paging;
   `decision_ack` takes both, with the remote ones named so that they never collide with a
   local verdict ID.
4. Acknowledgement: B queues the acknowledgement on its board stream to A. A marks the
   delivery `acknowledged` with the time, once.
5. PROTOCOL.md for the verdict and acknowledgement entries; CHANGELOG.md.

## Tests

The tests of Delivery to the agent (verdicts) and Acknowledgement in the definition of done,
and the Transport tests that touch verdicts: origin offline after receipt, a lost answer, a
restart with an acknowledgement queued, a repeat.

## Out of this chunk

Recipient removal, unpair, retention, the dashboard view and the live check (03).
