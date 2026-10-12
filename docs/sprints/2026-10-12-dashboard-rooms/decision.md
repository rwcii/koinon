# Dashboard rooms — decision

Issue [#217](https://github.com/rwcii/koinon/issues/217) supplies the maintainer's approved
problem and nine criteria. The paired planning boundary is recorded in its
[scope checkpoint](https://github.com/rwcii/koinon/issues/217#issuecomment-6115488464).
Codex drives the plan and Claude observes. Gate B for product implementation is pending.

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
   posts missed while that same native session was inactive; retention gaps are explicit.
7. Wakes remain content-free, peer text remains inert data, native identity is validated,
   dashboard changes require login/CSRF, and membership changes/posts are audited.
8. Room messages follow the actual merged #216 retention rules; unacknowledged inbox
   copies are preserved, and removal/expiry alone never purges conversation data.
9. Linux/macOS tests cover changing membership, mixed-family fan-out, removed senders and
   members that expire during the conversation.
10. Agent exchanges have a visible finite bound between maintainer posts. A peer cannot
    replenish it or impersonate a dashboard maintainer post.

## Constraints

- One existing same-user daemon/database, loopback listeners, existing native admission,
  wake providers and inbox/read/ack semantics. No guessed native caller or provider socket.
- Membership is an exact native session selected by its verified peer name. A participant
  successor does not silently inherit room membership; the maintainer can add it explicitly.
- Rooms are conversation data, not assignment, approval, execution or budget permissions.
- Dashboard origin proves the message's source, not a new permission grant. Room and
  direct dashboard messages are recorded maintainer conversation data within the session's
  existing authorized task and permission settings; they never override native instructions
  or supply a new approval for a service/configuration/reset action. Member posts and quotes
  remain peer data. This framing is the same for both dashboard message paths.
- No automatic agent response, context reset, terminal input or service/config changes.
- #206's decision-board UI is separate. A room may discuss a decision but records no human
  verdict and does not replace that issue's authority boundary.
- Synthetic sessions, temporary state, ephemeral ports/private provider stubs only locally.
  Real service tests remain guarded disposable Linux/macOS CI; no maintainer-host changes.

## Inputs and issues

This plan delivers #217. #216 is closed/merged, and internal/core/retention.go is the baseline,
not a proposed dependency. Read README.md, PROTOCOL.md, CONTRIBUTING.md, docs/USAGE.md,
internal/core/messages.go, retention.go, audit.go and the dashboard action/auth code.
