---
pdr: "011"
title: "Linked Koinon Computers"
status: accepted
created: "2026-11-12"
updated: "2026-11-12"
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
Memory, work, rooms, decisions and context cycles stay on each computer.

Robert Capps approved each decision below on the decision board on 2026-11-12. The decision
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
  list of item 2 (`directory`) and `name-update`; item 5 holds. Items 1, 3 and 4 (messages) are
  pending (chunk 03); until then a send to a paired computer answers `link_messages_pending`.

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
  Delivery to NAME and item 6 come with the messages (chunk 03).

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
- **Shipped:** pending (chunk 03).

## Conclusion

The maintainer pairs their own computers once; agents then reach sessions on them by
`NAME@COMPUTER`, with messages queued at the sender until the other computer answers. Each
computer keeps its own stores and controls.

## Document History

| Date | Change |
| --- | --- |
| 2026-11-12 | Record LINK-670-WHO, WHAT, ADDR and OFFLINE, approved by Robert Capps; chunk 01 (pairing) shipped (#670, #674). |
| 2026-11-13 | Chunk 02: the receiver directory and NAME@COMPUTER addresses shipped; delivery pending (#670). |
