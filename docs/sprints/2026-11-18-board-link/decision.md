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

### BOARD-LINK-746: decision cards and verdicts over the link

- **Decided by:** Robert Capps.
- **Date:** pending; board card BOARD-LINK-746, revision 3.
- **Context:** the maintainer requires that an agent on a paired computer sees a card that
  names it and gets the card's verdicts. The options were: the card's computer sends a
  read-only copy of the card, its changes and its verdicts over the link; or the agent's
  computer reads the card from the card's computer on request, and only the verdict notices
  cross. Feedback on revision 2, 2026-11-18: "send a read only card, notify the remote agent
  and allow the agent to read its card remotely? also, the local card shows an error that it
  can't resolve a remote name. the local card should be able to resolve the remote name
  tagged. this seems to indicate that the remote system can't receive the tag alert, and
  likely can't ack the card." Revision 3 answers yes, and puts the name resolution first.
- **Decision:** pending. The card recommends the first option.
- **Reason and scope:** recorded with the verdict.
- **Review when:** recorded with the verdict.

The verdict is recorded here, in PDR 011, Decision 2 and in PDR 007, Decision 4, in the pull
request of chunk 01, with the maintainer's words.

## Observable acceptance

1. A card that names `NAME@COMPUTER` for a paired computer shows that recipient as resolved,
   kind `remote`, with its computer. A name for a computer that is not paired is stored
   unresolved with reason `link_computer_unknown`, as an unknown local name is today. A paired
   computer that is offline is still resolved; its deliveries show `queued`.
2. The named agent, on its own computer, sees the card in `decision_list --filter mine` and
   reads every field with `decision_get`, also while the card's computer is offline after the
   copy arrived. The copy shows the computer that it came from, has a stable locator that
   stays valid after an unpair or a rename, and is read-only there.
3. The first copy and each later change to the card (a new revision, a withdrawal, a close, an
   archive, a restore, the recipient's removal) put one typed inbox message in that agent's
   inbox with its ordinary wake. Each verdict does the same, and is also in its
   `decision_pending` with its entry and snapshot, as for a local recipient (PDR 007,
   Decision 4). A change that is not a verdict is not in `decision_pending`.
4. The agent acknowledges a verdict with `decision_ack` on its own computer. The card's
   computer then shows that delivery as acknowledged.
5. Each recipient on the other computer has its own copy, deliveries and acknowledgements: a
   removal or an acknowledgement for one never changes another's.
6. An unpair leaves the copies readable and marked `unpaired`. A copy leaves the agent's
   computer 30 days after it is archived, unpaired or unnamed, and only when each of its
   verdicts is acknowledged there. The card's computer keeps the card.
7. A paired computer that runs an earlier release gives the card's computer a visible
   `unsupported` state with a reason; delivery resumes by itself after that computer is
   updated. It never blocks direct messages between the computers.
8. Retries, restarts and repeated deliveries never duplicate a card, a verdict or a delivery,
   and never move a copy to an earlier revision.
9. After the upgrade, each card that is not archived and already names `NAME@COMPUTER` for a
   paired computer resolves that recipient, and its copy and current verdict go to it. No
   change or verdict made during or between the sprint's releases is lost.
10. Nothing gives the other computer an edit, a verdict, or the maintainer's or overwatch's
    authority on the card's computer.
