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
- **Date:** pending; board card BOARD-LINK-746, revision 1.
- **Context:** the maintainer requires that an agent on a paired computer sees a card that
  names it and gets the card's verdicts. The options were: the card's computer sends a
  read-only copy of the card, its changes and its verdicts over the link; or the agent's
  computer reads the card from the card's computer on request, and only the verdict notices
  cross.
- **Decision:** pending. The card recommends the first option.
- **Reason and scope:** recorded with the verdict.
- **Review when:** recorded with the verdict.

The verdict is recorded here, in PDR 011, Decision 2 and in PDR 007, Decision 4, in the pull
request of chunk 01, with the maintainer's words.

## Observable acceptance

1. A card that names `NAME@COMPUTER` for a paired computer shows that recipient as resolved,
   with its computer, not as `unknown_name`. A name for a computer that is not paired is
   refused at create and edit, with a coded reason, as an unknown local name is flagged today.
2. The named agent, on its own computer, sees the card in `decision_list --filter mine` and
   reads every field with `decision_get`, also while the card's computer is offline after the
   copy arrived. The copy shows the computer that it came from and is read-only there.
3. Each new revision of the card updates the copy, as a local card changes for a local
   recipient. Each verdict reaches that agent with its ordinary wake and is in its
   `decision_pending` with its entry and snapshot, as for a local recipient (PDR 007,
   Decision 4).
4. The agent acknowledges with `decision_ack` on its own computer. The card's computer then
   shows that delivery as acknowledged.
5. A withdrawal, a close, an archive and a recipient's removal reach the copy. An unpair
   leaves the copy readable and marked so.
6. A paired computer that runs an earlier release, or a recipient that the other computer
   does not know, gives the card's computer a visible delivery outcome with a reason. It never
   blocks the direct messages between the computers.
7. Retries, restarts and repeated deliveries never duplicate a card, a verdict or a delivery,
   and never move a copy to an earlier revision.
8. Nothing gives the other computer an edit, a verdict, or the maintainer's or overwatch's
   authority on the card's computer.
