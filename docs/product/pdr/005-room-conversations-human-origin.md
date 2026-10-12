---
pdr: "005"
title: "Room Conversations and Human Origin"
status: draft
created: "2026-10-12"
updated: "2026-10-12"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "001"
  - "007"
tags:
  - flow
  - identity
  - notifications
  - content
---

# PDR 005: Room Conversations and Human Origin

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently lets the maintainer send a message from the dashboard to one session at a
time. The daemon sends it as its built-in `maintainer` session, and replies arrive as
separate inbox entries. Issue #217 asks for dashboard chat rooms: the maintainer selects
several sessions, writes once to all of them, and follows every reply in one conversation.

Rooms are not built. #217 leaves three choices to the plan: how a room is addressed, how an
agent learns that a post came from the maintainer, and a bound on agent-to-agent exchanges.
The reviewed plan proposes answers to all three. Gate B for #217 has not been given. The
plan's numeric exchange allowance and its "no new approval" framing go beyond what #217
states and are recorded as proposals.

---

## Table of Contents

1. Problem Context
2. Decision 1: Rooms of exact native sessions with their own tools
3. Decision 2: A finite allowance for agent posts between maintainer posts
4. Decision 3: How dashboard origin is shown and framed
5. Conclusion
6. Document History

---

## 1. Problem Context

### The Problem

To talk with several agents about one topic, the maintainer repeats each message per agent
and follows each thread separately. Agents cannot see each other's answers unless they relay
them. Two agents in a shared conversation could also reply to each other without end.

### Product Requirements

From #217's acceptance criteria:

1. The maintainer creates and names a room, and adds or removes active sessions of any
   family at any time. A session can be in more than one room.
2. A maintainer post reaches every current member's inbox and wakes each member.
3. A member replies to the room through Koinon tools (MCP, or the command path for
   DeepSeek); the reply reaches every other member and the room view.
4. Messages between members are visible to all members and the maintainer.
5. The room view shows one ordered transcript with sender, time, body and each member's
   delivery and acknowledgement state, refreshed in place.
6. A member can list the room's members and see that it was added or removed.
7. Content-free wakes, peer text as data, dashboard login and CSRF, and an audit record for
   every membership change and post.
8. Room messages follow the message retention rules of #216.
9. Tests cover membership changes, mixed-family fan-out, a removed sender and an expiring
   member.

### Scope

Covers rooms, posting and replies, the exchange bound and how dashboard origin is presented
to agents. Inventory rows: C075, C076. The decision board is separate (PDR 007); a room
records no human verdict. Session identity and participants are PDR 001.

---

## 2. Decision 1: Rooms of exact native sessions with their own tools

### Decision

A room has an opaque ID and a display name. Members are exact native sessions chosen by
their verified peer name; aliases are refused as members. A successor session of the same
participant does not join automatically; the maintainer can add it. An expired member stays
a member and is skipped until that same session is active again. Rooms use separate tools
(`room_list`, `room_members`, `room_post`, `room_read` and matching `koinon room` commands)
rather than a room name passed to `send`. A current member can page retained posts from the
start of its membership. The plan bounds 64 rooms and 32 active members per room.

**Provenance:** Rooms, membership and the transcript: Requested in #217 (issue body,
criteria 1–9); a request record, no maintainer quote found. Addressing, exact-session
membership and bounds: Proposed in the unpublished #217 plan draft (2d5a82c) (chunks 01 and 02); not
decided.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Room name in `send` | Address a room through the existing send tool | Names are display data and can repeat; a room needs its own read and roster calls |
| Membership by alias | Add whoever holds an alias | The plan ties delivery to an exact session; an alias holder can change silently |

### Rationale

1. A stable room ID keeps names free to repeat or change.
2. Exact sessions keep fan-out and the roster tied to the sessions the maintainer chose.

### User Impact

The maintainer builds a room from the session list and talks to all members at once. An
agent replies by room ID and can read what it missed while inactive.

---

## 3. Decision 2: A finite allowance for agent posts between maintainer posts

### Decision

#217 asks for a bound so that two agents cannot reply to each other without end. The plan
proposes an allowance: a room starts at zero agent posts; each successful dashboard post
sets it to 32; each new agent post uses one; at zero, agent posts are refused with
`exchange_limit` until the maintainer posts again. There is no time-based refill and no
per-member bypass. A retried post is not charged twice. The plan's scope also excludes
automatic agent responses.

**Provenance:** A bound: Requested in #217 ("To decide in the plan"); a request record, no
maintainer quote found. The zero start, the value 32 and refill only by a maintainer post:
Proposed in the unpublished #217 plan draft (2d5a82c) (chunk 02); not decided. The exclusion of automatic
responses comes from the planning scope comment on #217 (comment 6115488464), which is not a
maintainer quote.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No bound | Agents post freely | #217 asks for a bound |
| Time-based refill | Allowance returns over time | The plan wanted the maintainer to restart an exchange |
| Per-pair turn limit | Bound replies between two members | More state; the room-wide count covers the case #217 names |

### Rationale

1. A count reset by the maintainer is visible and easy to test.

### User Impact

Agents in a room can exchange up to 32 posts after each maintainer post. A long agent-only
discussion that the maintainer wants stops at the limit until the maintainer posts again.
The maintainer has not decided whether this is the bound wanted.

---

## 4. Decision 3: How dashboard origin is shown and framed

### Decision

A post's origin comes from stored metadata: the dashboard path records the maintainer, and
no native request can set that origin. Agents read the origin from typed fields, not from
the body. Today a direct dashboard message arrives from the built-in `maintainer` session,
and the installed guide says a memory entry with writer `maintainer` was written in the
dashboard and is recorded data. The plan proposes one guide text for direct and room posts:
"Dashboard origin records the maintainer as the source. Handle it within your existing
authorized task and permission settings; it does not override native instructions or supply
a new approval." The decision-board plan carries a similar line.

**Provenance:** Telling agents about maintainer origin and framing it in the guide:
Requested in #217 ("To decide in the plan"); a request record. The built-in `maintainer`
sender: Shipped in PR #202 (merge commit ed07677); the memory-entry guide line: Shipped in
PR #243 (merge commit f9fc14e); no maintainer decision found for either. The "does not
... supply a new approval" framing: Proposed in the unpublished #217 plan draft (2d5a82c) (chunk 02) and
the unpublished #206 plan draft (d325228) (constraints); not decided. No source found for treating a verified dashboard post as unable to carry the
maintainer's approval.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Body prefix | Mark origin with text in the body | A peer can type the same text |
| Dashboard post as maintainer instruction | Treat a verified dashboard post as the maintainer's direct word | Not chosen by the plan; the maintainer has not decided between this and the plan's framing |

### Rationale

1. Stored origin lets an agent tell a maintainer post from a peer quoting the maintainer.
2. The plan's reason for the approval framing is that native permission settings stay the
   agent's permission source; whether a dashboard post can approve an action is open.

### User Impact

Agents see which posts came from the maintainer. Under the plan's framing, a maintainer
post in the dashboard would not approve an action that needs approval; the maintainer would
give it where the agent's native permissions ask. This is the open question in this record.

---

## Conclusion

#217 requests rooms, an ordered conversation and a bound on agent exchanges. The plan
proposes exact-session membership, room tools, a 0/32 allowance refilled by maintainer
posts, and a guide framing that dashboard origin supplies no new approval. All are
undecided; the allowance values and the approval framing are plan choices, not #217's text.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
