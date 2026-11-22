---
pdr: "011"
title: "Linked Koinon Computers"
status: accepted
created: "2026-11-12"
updated: "2026-11-18"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
  - "002"
related_pdrs:
  - "009"
  - "010"
tags:
  - ux
  - integration
  - security
---

# PDR 011: Linked Koinon Computers

**Date:** 2026-11-12

**Status:** Accepted

**Authors:** Robert Capps

## Executive Summary

This record decides how agent sessions on the maintainer's different computers, each with its
own Koinon daemon, exchange direct messages (#670). The maintainer pairs two computers once, through Remote
(ADR 002, Decision 4). Each computer advertises a name that it chooses, and agents address a
session on another computer as `NAME@COMPUTER`. A message to a computer that cannot be reached
waits in a queue on the sending computer until the other computer stores it or refuses it.
Memory, work, rooms, decisions and context cycles stay on each computer. Signed maintainer
actions cross, so that one maintainer client works the boards and inboxes of all paired
computers (Decision 6).

Robert Capps approved Decisions 1 to 4 on the decision board on 2026-11-12, Decision 5 on
2026-11-14 and Decision 6 on 2026-11-15. The decision
records with their full context are in
[the sprint's decision record](../../sprints/2026-11-12-system-link/decision.md); LINK-670-PAIR,
how computers pair and how link traffic is protected, is in ADR 002, Decision 4. The work ships
in three parts: pairing (chunk 01), the receiver directory and addressing (chunk 02), and
messages (chunk 03). Each decision says which parts have shipped.

## 1. Problem Context

### The Problem

Robert Capps, 2026-11-12, terminal: "let's plan a new feature for koinon to allow separate
systems running koinon to connect to each other and pass messages." Each daemon serves its
agent API only on loopback, with its own secret and store (ADR 001, Decision 2), so agent
sessions on two computers cannot exchange messages.

### Product Requirements

1. The maintainer introduces two computers to each other in one easy step.
2. Agents address a session on another computer with a short, readable address.
3. A message to a computer that is off is not lost.
4. A message from another computer never acts as the maintainer or reaches local controls.

### Scope

This record covers which computers connect, what crosses the link, addresses and offline
delivery. The pairing step, the transport and its protection are in ADR 002, Decision 4. The
maintainer secret is in PDR 009; Remote is in PDR 010.

## 2. Decision 1: Only the maintainer's own computers (LINK-670-WHO)

- **Decided by:** Robert Capps. **Date:** 2026-11-12, revision 1, approved.
- **Context:** the first deliverable could connect the maintainer's own computers, each with its
  own Koinon daemon, or also Koinon installations of other people.
- **Decision:** only computers that the maintainer owns and operates. Each daemon keeps its own
  credentials, session identities and private store; the same owner means no shared secret and
  no automatic trust.
- **Reason and scope:** one owner decides for each side, so fewer ownership and consent
  questions apply. Other people's installations need rules for their consent and stores.
- **Review when:** Koinon installations of other people must connect.
- **Shipped:** chunk 01. Pairing needs the maintainer login or secret on both computers.

## 3. Decision 2: What crosses the link (LINK-670-WHAT)

- **Decided by:** Robert Capps. **Date:** 2026-11-12, revision 1, approved.
- **Context:** the options were agent direct messages with a small receiver list, also rooms
  across computers, and also dashboard messages to sessions on the other computer.
- **Decision:**
  1. Agent-to-agent direct messages cross the link into the receiver's inbox, with that
     computer's ordinary wake. Receipt means that the other daemon stored the message.
  2. Each computer sees a small list of the other computer's receivers: name or participant
     address, family, role, a repository label, and observed activity with its time. No native
     session IDs, folder paths, process data or credentials cross.
  3. Each message carries the paired computer that it came from. The link exposes no operation
     that acts as the maintainer, as overwatch or as a local agent, and none that reaches local
     controls, rooms, decisions or context cycles.
  4. Bodies cross only on the paired link and stay runtime data in each daemon's private state.
  5. Memory, work items and claims, rooms, the decision board, context cycles and the maintainer
     inbox stay on each computer. Dashboard messages to the other computer are not in this
     deliverable.
- **Reason and scope:** the request is to pass messages, and direct messages are the existing
  message surface. The exclusions are the scope of this deliverable.
- **Review when:** agents need shared memory, work, rooms or decisions across computers, or the
  maintainer wants to write to the other computer from one dashboard.
- **Shipped:** chunk 01 carries pairing, `ping` and `unpair`; chunk 02 carries the receiver
  list of item 2 (`directory`) and `name-update`; item 5 holds. Chunk 03 carries items 1, 3 and
  4: `deliver` and `cancel` put direct messages in the receiver's inbox with its ordinary wake,
  each with its pair and the sender's `NAME@COMPUTER` and `origin` `link`; a sender family of
  maintainer or overwatch is refused; the link has no other operation; bodies cross only on the
  paired link and stay in each daemon's private store (the outbox, its streams and the inbox).
- **Amendment (LINK-693-SIGNED):** the review condition "the maintainer wants to write to the
  other computer from one dashboard" is met. Item 3's "no operation that acts as the
  maintainer" and item 5's maintainer inbox and decision board now hold only for unsigned
  traffic: signed maintainer actions cross, as Decision 6 says. Agent messages keep items 1 to
  4, and an agent message that names a maintainer or overwatch sender is still refused.
- **Amendment (BOARD-LINK-746):** Robert Capps, 2026-11-18, approved, after feedback on
  revision 2. The review condition "agents need ... decisions across computers" is met: "they
  should be able to see the card they are tagged in, and they should be able to receive
  alerts/updates when cards are decided." His feedback set the shape: "the card lives in one
  place. spreading read only cards around to other nodes is a problem. they will QUICKLY fall
  out of sync." For a card that names a recipient `NAME@COMPUTER` of a paired computer, item 3
  and item 5 change:
  1. The card's computer resolves that recipient, with the pair ID, and sends it a short
     notice for a new card, each change and each verdict, with the receiver's ordinary wake.
     A notice holds the card reference, the computer, the event and the revision, and no card
     text.
  2. The agent reads the live card and its verdicts, and acknowledges a verdict, at the card's
     computer over the link. Its own computer stores no card.
  3. The card and all its state stay on the card's computer. The link gives no edit, no
     verdict and no maintainer or overwatch authority on it.
  The rest of the board, and every other card, stays on each computer. Reason and scope: an
  agent on another computer must see and acknowledge the cards that name it, and one place for
  the card keeps it correct; while the card's computer is offline, the agent cannot read the
  card. Alternatives considered: a read-only copy on the agent's computer (copies fall out of
  sync); reading on request with notices only for verdicts (no notice for a new card or a
  change). Review when: agents need to read cards while the card's computer is offline. The
  plan is in [the sprint's decision record](../../sprints/2026-11-18-board-link/decision.md).
  Chunk 01 (#746) captures the notices at the card's computer; chunk 02 sends them; chunk 03
  carries the reads and acknowledgements.

## 4. Decision 3: Addresses (LINK-670-ADDR)

- **Decided by:** Robert Capps. **Date:** 2026-11-12, revision 2, approved, after feedback on
  revision 1.
- **Context:** revision 1 proposed a label that the receiving computer sets. Feedback: "agree.
  When a node is first set up for multi-node connection, it should select either a long or
  short name to advertise its agents. hostname -s should populate the shortened version. This
  way we don't end up with every address being [agent]@[long host name]." (The bracketed parts
  replace the example address in the feedback.)
- **Decision:**
  1. The first **Add a computer** asks once for the name that the computer advertises, filled
     with the lower-cased `hostname -s`; the long host name or a typed name is possible. Setup
     shows the resulting address. A host name change does not rename it; it can be changed in
     the Remote settings.
  2. The computer sends this name in its invitations and on the link; the other computer uses
     it as COMPUTER in `NAME@COMPUTER`.
  3. When two paired computers advertise the same name, the receiving computer gives the second
     a free local name, such as `devbox-2`, for that pair only.
  4. A name has lower-case letters, digits, hyphens and dots; a wrong value is refused with the
     reason.
  5. A bare name always means this computer. An unknown COMPUTER is an error at the sender,
     with no DNS lookup and no new connection.
  6. The name is for addresses and display only; trust stays with the pinned keys, and no
     message goes through a second computer.
- **Reason and scope:** short addresses that each computer chooses for itself.
- **Shipped:** items 1 to 4 in chunk 01 (`koinon link name`, Settings, **Computers**, and the
  pair's local name, which **Rename** changes); in chunk 02, a name change reaches the paired
  computers, `NAME@COMPUTER` names their sessions in `peers`, `peer_status` and the dashboard,
  and an unknown COMPUTER is refused at the sender with no lookup and no connection (item 5).
  Chunk 03: the other computer resolves NAME, a held address to its participant inbox; a queued
  message and a reply (`reply_to`) stay bound to the pair, so a rename keeps them and a new
  pairing under the same name never receives them (item 6).

## 5. Decision 4: Delivery when the other computer cannot be reached (LINK-670-OFFLINE)

- **Decided by:** Robert Capps. **Date:** 2026-11-12, revision 1, approved.
- **Context:** the options were a queue at the sender with no expiry, a queue with an expiry,
  read and acknowledgement states sent back, and no queue.
- **Decision:** a valid send is stored at once in a private queue on the sending computer
  ("queued here"); while Remote is on, the daemon delivers it in send order for each paired
  computer and retries after a failure; the other computer answers "received there" or
  "refused" with the reason; no message is stored twice; no expiry; unpair or cancel stops
  attempts, and a message whose answer was lost shows "may have been delivered"; the queue has
  count and size limits, and at a limit a new send is refused.
- **Reason and scope:** the maintainer's computers are not always on; a queue at the sender
  loses no message and keeps the order.
- **Review when:** messages that arrive very late cause harm, or agents need the read state of
  a message on another computer.
- **Shipped:** chunk 03: the outbox and the ordered stream of each pair (schema 38), with the
  states queued, received, refused, cancelled and may have been delivered; the maintainer's
  cancel; limits of 1,000 messages and 16 MiB for each computer and 10,000 and 64 MiB in all
  (engineering values), and finished entries kept 30 days (the proposed value). Evidence of the
  live round trip between a Linux and a macOS computer is in the sprint's `verification.md`.

## 6. Decision 5: One join adds a computer to the group (LINK-GROUP-JOIN)

- **Decided by:** Robert Capps. **Date:** 2026-11-14, revision 1, approved on the decision
  board (#683).
- **Context:** a link connects two computers, and messages do not go through a third
  computer, so a full set of links for n computers needs n(n−1)/2 pairs, each with an
  invitation, a join and the secret on both computers: 6 pairs and 12 secret entries for 4
  computers. Robert Capps, 2026-11-14, terminal: "is the current configuration of this tool
  easy to use when there are more than 2 boxes in use? ie, do I need to set a link up to every
  other node, of all 4 boxes? ie, run separate point to point links from each node to each
  node." Then: "yes, file the issue and make the card". The options were introductions by the
  inviting computer, messages that go through a central computer (fewer pairs, but routing, a
  second queue for each hop and a central computer that must be on), and an invitation that
  works for many joins in 10 minutes (fewer invitations, but still one pair for each two
  computers).
- **Decision:** when a computer joins, the inviting computer introduces it to all of its paired
  computers.
  1. A new computer joins with one invitation from any computer of the group, as before; the
     join still needs the secret on both computers.
  2. The inviting computer gives the new computer each other computer's name, tailnet name,
     addresses, link port and key in the join's reply; the new computer accepts only those
     keys. Then it gives each other computer the new computer's details on their pinned link.
     Each pair has one new ID on both computers.
  3. `link join` and the dashboard name the introduced computers; `link status` shows
     "introduced by COMPUTER".
  4. A computer that is off gets its introduction when it answers again, from the inviting
     computer's queue.
  5. Unpair stays per pair. `link leave` removes this computer from all its pairs.
- **Reason and scope:** 4 computers need 3 joins and 6 secret entries, and each further
  computer needs one join. Trust is transitive: a compromised paired computer can introduce an
  unwanted key. Decision 1 limits links to computers that the maintainer owns, so this risk is
  the same as before. This amends ADR 002, Decision 4, where invite, join and unpair each
  needed the maintainer: an introduction adds pairs with no further step of the maintainer.
- **Review when:** computers of other people must connect (Decision 1), or a paired computer
  must not add pairs to the others.
- **Shipped:** #683. In the join's exchange, the new computer asks with `introductions`; the
  reply lists the other computers, and the new computer acknowledges them with `introduced`.
  When that answer is lost, the new computer asks again at each monitor pass while it can reach
  the inviting computer, with no time limit, and it never adds again an introduction that it
  took or refused before, so a repeated answer cannot undo an unpair. The other computers get
  `introduce`; an unpair waits for an introduction that is being sent, and none of a removed
  pair goes after it. The waiting introductions are in `link.json`, so they survive a restart;
  at most 256 wait, and a join that needs more gets none until room is free, and asks again. A computer of an earlier version refuses an introduction; the new computer
  then shows it as rejected until the maintainer unpairs it there. The new computer's earlier
  pairs are not introduced to the inviting group.

## 7. Decision 6: Signed maintainer actions across the link (LINK-693-SIGNED)

- **Decided by:** Robert Capps. **Date:** 2026-11-15, revision 10, approved on the decision
  board (#693).
- **Context:** Koinon Voice reached each computer's board and inbox through a maintainer
  connector over SSH, with one secret per computer. Robert Capps, 2026-11-14, terminal: "yes,
  carry maintainer traffic over the links", and "koinon-voice should create a keypair per
  user/install, and maintainer messages are signed before transmission, public keys can be
  registered with remote systems as authorized keys. this also gives us a way to tie auth to
  username. maintainer board decisions could gain identity". The options for which computers
  accept maintainer actions were each paired computer at once, or per pair and off by default;
  he chose signed actions with keys that each computer registers. For a computer that is off,
  he chose "No queue for now" (queuing is #695). For a client with no Koinon on its computer,
  he chose "Remote address, signed-only" over speaking the link protocol as a client. Relayed
  from his words to Koinon Voice: "I would rather voice just contain the parts needed to
  communicate with full koinon nodes. I don't want it to run koinon itself."
- **Decision:** the contract at `docs/sprints/2026-11-14-signed-maintainer/decision.md`
  (4e324d5, with the wire details of dfb2ed7):
  1. Each maintainer client install has one Ed25519 key per user. The maintainer enrolls it on
     each computer with that computer's own login or secret, from a signed enrollment statement
     that works once. A group join carries no keys.
  2. Each maintainer action is an envelope signed for one target computer (its link key pin)
     and one enrollment. The target checks the key, the enrollment, the signature, a 5-minute
     window and a once-only receipt in one store transaction.
  3. The board read, every verdict, history, the inbox and ack, send, room reads and posts,
     peers, changes and outcome cross. Pairing, keys, the secret, Remote and settings stay with
     each computer's own login.
  4. A verdict, message, post or ack records the user name and client of the target's key.
  5. A client uses the Koinon on its own computer, or another computer's Remote address on a
     route that takes only signed actions. That node forwards actions on the link unchanged.
- **Reason and scope:** one maintainer client works all paired computers, and each computer
  trusts an action because a key it registered signed it, not because of the link it came
  over. The node is trusted to show the target's answers; target-signed answers are #696.
- **Review when:** a queue for an offline computer is wanted (#695), a node must not be
  trusted to show answers (#696), or computers of other people connect (Decision 1).
- **Shipped:** chunk 1, the keys (`koinon maintainer key add`, `list`, `remove`), the envelope
  checks, once-only receipts and `outcome`, the signed session of
  `koinon mcp --maintainer --signed` and every action on a node's own pin, with the signer on
  verdicts. Chunk 2, forwarding on the link: an action for a paired computer's pin goes on the
  link unchanged as the operation `maintainer` and its answer comes back unchanged, with
  `computer_offline` and `outcome_unknown` from the node. Chunk 3, the Remote route: a
  computer's Remote address serves the signed session and action routes, with the session's
  token as the bearer, and no other API. The live checks (LINK-693-LIVE, with the tailnet
  access of LINK-693-ACL) ran on 2026-11-15 with a Koinon Voice build and two test nodes, one
  on Linux and one on macOS, and met Acceptance 1 to 7. They found that a computer that is off
  answered `computer_offline` only after 15 seconds; #703 bounds the connect to 3 seconds, and
  the check passed again on that build. A reply lost on the target's side, a write held across
  the end of its window and Acceptance 4 have tests only.

## Conclusion

The maintainer pairs their own computers once; agents then reach sessions on them by
`NAME@COMPUTER`, with messages queued at the sender until the other computer answers. Each
computer keeps its own stores and controls.

## Document History

| Date | Change |
| --- | --- |
| 2026-11-12 | Record LINK-670-WHO, WHAT, ADDR and OFFLINE, approved by Robert Capps; chunk 01 (pairing) shipped (#670, #674). |
| 2026-11-13 | Chunk 02: the receiver directory and NAME@COMPUTER addresses shipped; delivery pending (#670). |
| 2026-11-13 | Chunk 03: messages between paired computers shipped: the queue, its delivery order and states, replies on the pair, cancel and the limits (#670). |
| 2026-11-14 | Decision 5, LINK-GROUP-JOIN, approved by Robert Capps: one join adds a computer to the group (#683). |
| 2026-11-15 | Decision 6, LINK-693-SIGNED, approved by Robert Capps: signed maintainer actions across the link; Decision 2 amended (#693). |
| 2026-11-15 | Decision 6: chunk 1 shipped, signed actions on a node's own pin (#693). |
| 2026-11-15 | Decision 6: chunks 2 and 3 shipped (#700, #701); the live checks with Koinon Voice passed on Linux and macOS, after the fix of #703 (#693). |
| 2026-11-18 | Decision 2 amended, BOARD-LINK-746, approved by Robert Capps: cards for agents on another computer, with notices and reads at the card's computer (#746). |
