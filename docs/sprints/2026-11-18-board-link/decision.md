# Board link — decision

## Problem

Robert Capps, 2026-11-18, terminal: "agents from separate koinon instances can't see cards they
are tagged in, on other machines." and "they should be able to see the card they are tagged in,
and they should be able to receive alerts/updates when cards are decided." Filed as
[#746](https://github.com/rwcii/koinon/issues/746).

A decision card can name a recipient as `NAME@COMPUTER`, the address form of an agent on a
paired computer (PDR 011, Decision 2). Today the card's daemon looks up each recipient only in
its own names, so that recipient is stored with reason `unknown_name`, and each verdict
delivery to it is `skipped`. On 2026-11-18 a linked computer at v0.13.1 held two cards with
three such recipients; all three recipient rows had `unknown_name`, and all three verdict
deliveries were `skipped`. PDR 011, Decision 2, items 3 and 5 keep the decision board on each
computer; its review condition is "agents need shared memory, work, rooms or decisions across
computers".

## Scope decision

### BOARD-LINK-746: decision cards for agents on another computer

- **Decided by:** Robert Capps.
- **Date:** pending; board card BOARD-LINK-746, revision 5.
- **Context:** the maintainer requires that an agent on a paired computer sees a card that
  names it and gets the card's verdicts. Revisions 1 to 3 proposed a read-only copy of the
  card on the agent's computer. Feedback on revision 2, 2026-11-18: "send a read only card,
  notify the remote agent and allow the agent to read its card remotely? also, the local card
  shows an error that it can't resolve a remote name. the local card should be able to resolve
  the remote name tagged. this seems to indicate that the remote system can't receive the tag
  alert, and likely can't ack the card." Asked whether "read its card remotely" means a live
  read from the card's own computer, the maintainer answered, 2026-11-18, terminal: "you would
  be correct", and added: "the card lives in one place. spreading read only cards around to
  other nodes is a problem. they will QUICKLY fall out of sync."
- **Decision:** pending. Revision 5 recommends: the card lives only on its own computer; the
  tagged agent gets a short notice with no card text, and reads and acknowledges the live
  card on the card's computer over the link.
- **Reason and scope:** recorded with the verdict.
- **Alternatives considered:** a read-only copy on the agent's computer (revisions 1 to 3):
  copies fall out of sync.
- **Review when:** recorded with the verdict.

The verdict is recorded here, in PDR 011, Decision 2 and in PDR 007, Decision 4, in the pull
request of chunk 01, with the maintainer's words.

## Observable acceptance

1. A card that names `NAME@COMPUTER` for a paired computer shows that recipient as resolved,
   kind `remote`, with its computer. A name for a computer that is not paired is stored
   unresolved with reason `link_computer_unknown`, as an unknown local name is today. A paired
   computer that is offline is still resolved; its notices show `queued`.
2. The card and all its state stay only on the card's computer. The agent's computer stores no
   card, no card text and no verdict snapshot.
3. A new card, each change (a new revision, a withdrawal, a close, an archive, a restore, the
   recipient's removal) and each verdict put one short notice in the named agent's inbox, with
   its ordinary wake. The notice holds the reference, the computer, the event, the verdict
   kind for a verdict, and the revision; no card text.
4. On its own computer, the agent reads the live card with `decision_get REFERENCE@COMPUTER`,
   lists the cards of one paired computer that name it with `decision_list --filter mine` and
   that computer, and lists its waiting verdicts there with `decision_pending` and that
   computer. Each answer comes from the card's computer at that moment.
5. The agent acknowledges a remote verdict with `decision_ack` and that computer. The card's
   computer records it, and shows that delivery as acknowledged.
6. While the card's computer is offline or unpaired, these tools answer with a coded error and
   change nothing; the notices stay in the inbox.
7. A paired computer that runs an earlier release gives the card's computer a visible
   `unsupported` state; notices resume by themselves after that computer is updated. A remote
   read of a computer at an earlier release answers `unsupported`. Direct messages never wait.
8. Retries, restarts and repeated deliveries never duplicate a notice or an acknowledgement.
9. After the upgrade, each card that is not archived and already names `NAME@COMPUTER` for a
   paired computer resolves that recipient, and gets a notice for its current verdict when
   the delivery to that name was `skipped`. No change or verdict made during or between the
   sprint's releases loses its notice.
10. A remote read or acknowledgement reaches only cards that name that agent on the asking
    computer. Nothing gives the other computer an edit, a verdict, or the maintainer's or
    overwatch's authority on the card's computer.
