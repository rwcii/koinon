# Dashboard rooms — decision

Issue [#217](https://github.com/rwcii/koinon/issues/217) requests the problem and nine criteria.
The maintainer selected a dashboard-configured allowance, default 32, and dashboard posts
as the maintainer's instruction and approval (PDR 005, Decisions 2 and 3). The maintainer
approved the membership, tools, storage and conversation mechanisms at Gate B (Decision 1;
[#217 comment 6129177109](https://github.com/rwcii/koinon/issues/217#issuecomment-6129177109)).
The paired planning boundary is recorded in its
[scope checkpoint](https://github.com/rwcii/koinon/issues/217#issuecomment-6115488464).
Codex drives the plan and Claude observes.

## Problem

The dashboard sends to one session at a time and separates replies in the inbox view.
The maintainer needs one conversation with several agents, where every member sees the
others' replies and the maintainer can follow delivery and acknowledgement.

## Acceptance criteria

1. The maintainer creates named rooms and adds/removes active sessions of any family.
   One session may belong to several rooms; membership changes during a conversation work.
2. One maintainer post reaches every current active member's inbox and uses ordinary wakes.
3. A current member replies to the room through native-identity MCP or DeepSeek's existing
   command path. The reply reaches all other current active members and the room view.
4. Members see each other's room replies in the same conversation.
5. The dashboard shows an ordered transcript with sender, time, body, and each selected
   recipient's delivery/acknowledgement state; it refreshes in place.
6. Members can list the roster and discover that they were added or removed.
   A current member can page retained posts from its current membership's start, including
   posts missed while it was inactive or between holders; retention gaps are explicit.
7. Wakes remain content-free, peer text remains inert data, native identity is validated,
   dashboard changes require login/CSRF, and membership changes/posts are audited.
8. Room messages follow the actual merged #216 retention rules; unacknowledged inbox
   copies are preserved, and removal/expiry alone never purges conversation data.
9. Linux/macOS tests cover changing membership, mixed-family fan-out, removed senders and
   members that expire during the conversation.
10. A room starts with no agent posts allowed. Each successful maintainer post resets the
    allowance to its dashboard-configured refill value, default 32 (PDR 005, Decision 2).
    A peer cannot replenish it or impersonate a dashboard maintainer post.

## Constraints

- The existing daemon/private store and native caller source remain as in ADR 001,
  Decisions 1 and 2. Existing wake and inbox/read/ack semantics remain; notices are
  content-free (Decision 3).
- The maintainer selects an active session by its verified peer name. A session that holds
  a participant address joins as that participant. Its
  membership, membership start and room copies continue with the participant's verified
  holder, as its address state does (PDR 001, Decision 6). A session without a
  participant joins as its exact native session.
- A direct message or room post from the built-in maintainer session in the dashboard is
  the maintainer's instruction and approval (PDR 005, Decision 3; #306 comment 6118947404).
  Typed stored origin comes from that path. Member posts and bodies quoting the maintainer
  remain peer data (WDR 002, Decision 3); native session policies apply.
- Koinon fans out posts and notices; agents produce replies under the maintainer's task
  (WDR 002, Decision 1). This deliverable adds no context-cycle or service-management code.
- #206's decision-board UI is separate. A room may discuss a decision but records no
  decision-board verdict.
- Ordinary tests use synthetic sessions, temporary state and ephemeral ports/private
  provider stubs (WDR 006, Decision 2); native service evidence uses the guarded CI workflow
  (Decision 1).

## Inputs and issues

This plan delivers #217. #216 is closed/merged, and internal/core/retention.go is the baseline,
not a proposed dependency. Read README.md, PROTOCOL.md, CONTRIBUTING.md, docs/USAGE.md,
internal/core/messages.go, retention.go, audit.go and the dashboard action/auth code.
