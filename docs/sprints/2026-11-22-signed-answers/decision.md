# Signed answers and recipient results (#695, #696) — decision

## Problem

Signed maintainer actions (PDR 011, Decision 6) left two items open:

- [#695](https://github.com/rwcii/koinon/issues/695): Koinon Voice asked to queue the
  maintainer's signed `send` and `room_post` for a computer that is off. Today the node answers
  `computer_offline` at once, and the signed answer to a room post gives only the counts of the
  members that it reached and skipped, not who they are or why.
- [#696](https://github.com/rwcii/koinon/issues/696): the node shows the target's answers
  unchecked (`docs/sprints/2026-11-14-signed-maintainer/decision.md`, Decision 5.6). A node
  that lies could show a false result, a false board or a false inbox. A verdict is safe,
  because it signs the card text that the client showed.

## Scope decision

### Real-time messages, with a result for each recipient, and signed answers

- **Decided by:** Robert Capps.
- **Date:** 2026-11-21, decision board, revision 3 approved after feedback on revision 2.
- **Context:** revision 2 recommended a queue at the node for a signed `send` or `room_post`
  to a computer that is off, for at most 24 hours: an end time that the client signs, the
  states queued here, received there, refused, expired, cancelled and may have been
  delivered, and a cancel. It also recommended that the target sign every answer. The other
  options were #696 first and #695 later; a queue in the client, which signs a new envelope
  when the computer is back; no end time, with a new receipt design; and signing only the
  writes and `outcome`. The feedback on revision 2: "Chat and rooms are real time. They
  cannot queue messages for hours or a day. The participants are either in the chat, or they
  are not part of it. Missed messages don't deliver. They fail, and the sent message shows
  that they were not sent to a given participant and why." Revision 3 removed the queue.
- **Decision:** revision 3 approved:
  1. No queue for a signed `send` or `room_post`, at the node or in the client. The 5-minute
     window and the once-only receipts stay.
  2. The target's answer gives the result for each recipient: sent (a copy is in its inbox;
     read and acknowledgement are separate), or not sent with the reason. For a room post,
     each member of the roster at the post. The target stores the result with the once-only
     receipt, so a later `outcome` gives the same list.
  3. A computer that is off: the node answers `computer_offline` at once. This is the node's
     claim, not the target's. For a room post the node cannot read the current roster: the
     client shows the room as not sent, and a member list only as last known.
  4. A lost answer is not a failure: the client shows the outcome as unknown, reads the
     outcome of that request, and never sends a new one. "Not sent" shows only a confirmed
     result.
  5. The client shows the list with the sent message.
  6. Room history stays: `room_read` still shows a member the posts that it missed. Nothing
     delivers or wakes for them later.
  7. The target signs every answer with its link key, over the digest of the request
     envelope, its pin and the full answer, the recipient list included. The client checks
     that key against the pin that it enrolled. No new enrollment step.
  8. The node signs its own claims (`computer_offline`, `outcome_unknown`); the client shows
     them as the node's claims.
  9. A bad signature: the client shows "answer not verified" and no content. A write then
     stays "outcome unknown".
  10. A new envelope version; an earlier computer refuses it. The sprint settles the contract
      and the test vectors with the Koinon Voice agents, and records them in PDR 011.
- **Reason and scope:** chat is real time: a participant who is not there does not get the
  message, and the sender sees who did not get it and why. A node cannot forge a target's
  answer; it can still hold an answer back or claim that a computer is off. This covers the
  maintainer's signed `send` and `room_post` and every signed answer. Agent messages to
  another computer keep their queue (PDR 011, Decision 4).
- **Alternatives considered:** a queue at the node for at most 24 hours, with an end time that
  the client signs (revision 2; declined: chat and rooms are real time); a queue in the
  client; no end time; #696 first and #695 later; `room_read` showing only the posts that
  reached the member; counts only, as before; signing only the writes and `outcome`.

PDR 011 records this decision in the pull request of chunk 01.

## Observable acceptance

1. A signed `room_post` answers, for each member of the room's roster at the post other than
   the poster, its name and `sent`, or `not_sent` with its reason. A member session that is
   not active gives the reason that liveness found (`retired`, `ended`, `lapsed`,
   `no_liveness_evidence`); a participant member gives `unheld` or `conflicting`.
2. A signed `send` answers `sent` for its recipient, or a refusal whose code and reason say
   why it was not sent (`peer_not_found`, `alias_unheld`, `recipient_inactive` with its
   reason).
3. The same request, sent again or read with `outcome`, gives the same recipient list, also
   after members change, after a restart and after a lost answer.
4. Nothing that was not sent is delivered, or wakes anyone, later. `room_read` still shows a
   member the posts of the room since it joined.
5. A computer that is off gives `computer_offline` at once, signed by the node.
6. Every answer to a version 2 envelope is signed: by the target with its link key, or by the
   node for the node's own claims. A client that checks the signature, the pin, the request
   digest and the signer finds every change of the answer, its status, its request, its
   signer or its target.
7. A computer at an earlier release refuses a version 2 envelope; a version 1 envelope still
   works as before, with an unsigned answer.
8. Shared test vectors, produced in Go and checked by the Koinon Voice client, cover the
   envelope, a target answer, a refusal and a node claim.
9. Evidence: Linux and macOS in CI; a live check with a Koinon Voice build and two Koinon
   nodes.
