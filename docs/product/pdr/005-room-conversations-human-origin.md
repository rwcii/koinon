---
pdr: "005"
title: "Room Conversations and Human Origin"
status: modified
created: "2026-10-12"
updated: "2026-10-26"
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
**Status:** Modified
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently lets the maintainer send a message from the dashboard to one session at a
time. The daemon sends it as its built-in `maintainer` session, and replies arrive as
separate inbox entries. Issue #217 asks for dashboard chat rooms: the maintainer selects
several sessions, writes once to all of them, and follows every reply in one conversation.

Rooms are built (#217). #217 left three choices to the plan: how a room is addressed, how an
agent learns that a post came from the maintainer, and a bound on agent-to-agent exchanges.
The reviewed plan answers all three. On 2026-10-12 the maintainer decided the exchange bound
and that a dashboard post is the maintainer's instruction and approval (#306), and approved
the plan's room addressing and membership at Gate B (#217 comment 6129177109). On 2026-10-26
the maintainer removed the exchange bound (#493): agents post in a room without an allowance.

---

## Table of Contents

1. Problem Context
2. Decision 1: Rooms of participants and sessions with their own tools
3. Decision 2: A maintainer-set allowance for agent posts (deprecated)
4. Decision 3: A dashboard post is the maintainer's instruction and approval
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
7. Content-free wakes, dashboard login and CSRF, and an audit record for every membership
   change and post.
8. Room messages follow the message retention rules of #216.
9. Tests cover membership changes, mixed-family fan-out, a removed sender and an expiring
   member.

Before #487 criterion 7 also held "peer text as data". No maintainer source was found for it.
The #487 review listed three limits and proposed to keep them; the question was whether to keep
them or to remove them too. Maintainer, 2026-10-25: "remove all limit text.  remove all of the
caveats, controls, etc.  the agents have their own built in safeties, they do not need you
adding more complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit listed the code
refusals for a separate decision; #493 removed the room allowance (Decision 2, Change of
2026-10-26). The dashboard origin (Decision 3) keeps its source and stays.

### Change of 2026-10-26 (#493)

- Question (decision board item 493-4, 2026-10-26): keep or remove the room allowance? A room
  started with no agent posts allowed; each maintainer post set the allowance to the number set
  in the dashboard (default 32); each agent post used one; at zero, agent posts failed with
  `exchange_limit` until the maintainer posted again (Decision 2). The card noted that the rule
  was the maintainer's own choice on 2026-10-12.
- Options: keep the allowance, or remove it. The card stated: "Decline = remove the allowance
  in a PR."
- Answer: Maintainer, 2026-10-26: declined, with no note (verdict 48)
  ([#493 comment 6406503597](https://github.com/rwcii/koinon/issues/493#issuecomment-6406503597)).
- Intent and scope: agents post in a room without an allowance and without a maintainer post
  between rounds. This replaces the maintainer's 2026-10-12 choice "b." (Decision 2) and takes
  the "No bound" alternative. The `exchange_limit` refusal, the create form's refill field, the
  dashboard's refill action and the receipt's `allowance` field are removed; the rooms table
  keeps its old columns, which nothing reads. A maintainer post still reaches every active
  member and is the maintainer's instruction and approval (Decision 3, unchanged).

### Scope

Covers rooms, posting and replies, the exchange bound and how dashboard origin is presented
to agents. Inventory rows: C075, C076. The decision board is separate (PDR 007); a room
records no human verdict. Session identity and participants are PDR 001.

---

## 2. Decision 1: Rooms of participants and sessions with their own tools

### Decision

A room has an opaque ID and a display name. The maintainer chooses members by verified peer
name. A session that holds a participant address joins as that participant: membership,
membership start and room copies continue with the participant's verified holder after a
reset, as the participant's other address state does (PDR 001, Decision 6). A session without
a participant joins as its exact native session. An expired, unheld or conflicting member
stays a member, is skipped while it has no active holder, and catches up by reading the room.
Rooms use separate tools (`room_list`, `room_members`, `room_post`, `room_read` and matching `koinon room` commands)
rather than a room name passed to `send`. A current member can page retained posts from the
start of its membership. The plan bounds 64 rooms and 32 active members per room.

**Provenance:** Rooms, membership and the transcript: Requested in #217 (issue body,
criteria 1–9); a request record, no maintainer quote found. Addressing, membership and
bounds: Proposed in the #217 plan (chunks 01 and 02); approved at Gate B. Maintainer,
2026-10-12: "217 gate b approved"
([#217 comment 6129177109](https://github.com/rwcii/koinon/issues/217#issuecomment-6129177109)).
The plan first proposed
exact-session membership with no participant continuation. That restriction had no source in
#217; it was removed in an audit the maintainer asked for on 2026-10-12: "make sure you and
your peer didnt slip more refusal langauge in these two projects." The participant membership
follows the inbox continuity of PDR 001, Decision 6, which covers participant-address
messages and does not decide room membership.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Room name in `send` | Address a room through the existing send tool | Names are display data and can repeat; a room needs its own read and roster calls |
| Exact native sessions only | A reset session leaves every room until the maintainer adds it again | A `/clear` or context cycle (#196) silently drops the agent; #217 does not ask for it |
| Membership by unverified alias | Deliver to whoever holds a name at post time | Holder changes need verified succession and fencing (PDR 001, Decision 5) |

### Rationale

1. A stable room ID keeps names free to repeat or change.
2. Participant membership keeps a member in the room across a verified reset, as its inbox
   and cursor already continue.

### User Impact

The maintainer builds a room from the session list and talks to all members at once. An
agent replies by room ID and can read what it missed while inactive.

---

## 3. Decision 2: A maintainer-set allowance for agent posts (deprecated)

### Decision

**Deprecated 2026-10-26 by #493** (Problem Context). Agents post in a room without an allowance
and without a maintainer post between rounds. The maintainer declined the allowance on the
decision board (verdict 48, [#493 comment 6406503597](https://github.com/rwcii/koinon/issues/493#issuecomment-6406503597)).

**Before #493:** "A room starts with no agent posts allowed. Each successful maintainer post sets the room's
allowance to a number the maintainer sets in the dashboard, 32 by default. Each new agent post
uses one. At zero, agent posts are refused with `exchange_limit` until the maintainer posts
again. There is no time-based refill and no per-member bypass, and a retried post is not
charged twice."

**Provenance:** A bound: Requested in #217 ("To decide in the plan"). The rule and the value 32:
Proposed in the unpublished #217 plan draft (2d5a82c) (chunk 02). Maintainer, 2026-10-12: "b.",
choosing the plan's rule with the number set in the dashboard, default 32, over the fixed plan
values and over no bound ([#306 comment 6118968707](https://github.com/rwcii/koinon/issues/306#issuecomment-6118968707)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Fixed values (plan) | Start at 0, refill to 32, not configurable | A change of the number would need a code change |
| No bound | Agents post freely | Two agents could reply to each other without end (#217) |
| Time-based refill | Allowance returns over time | Agents could restart an exchange without the maintainer |

### Rationale

1. A count reset only by the maintainer keeps the maintainer in each exchange and is easy to
   test.
2. A dashboard setting lets the maintainer change the number without a release.

### User Impact

Agents post in a room without waiting for the maintainer (#493). Before #493, agents spoke
after the maintainer did, up to the set number of posts, then waited for the maintainer's next
post.

---

## 4. Decision 3: A dashboard post is the maintainer's instruction and approval

### Decision

A post from the built-in `maintainer` session, sent from the dashboard or from a maintainer
client through `koinon mcp --maintainer` (PDR 009, Decision 3) as a direct message, a room post
(#217) or a decision-board verdict (#206), is the maintainer's instruction and approval. Its
origin comes from stored metadata that only the maintainer's paths set, the dashboard and the
maintainer token routes; agents read it from typed fields, not from the body. The Go guide and MCP instructions now state this
decision (PR #311); an installation changes its guide only when that runtime is upgraded. The plans'
framing that dashboard origin "does not ... supply a new approval" is not adopted.

**Provenance:** Telling agents about maintainer origin: Requested in #217 ("To decide in the
plan"). The built-in `maintainer` sender shipped in PR #202 (merge commit ed07677). Maintainer,
2026-10-12: "b, next item - be sure the reasoning is included in the decision record. the
bridge only runs locally, so only I can access it." ([#306 comment 6118947404](https://github.com/rwcii/koinon/issues/306#issuecomment-6118947404)),
choosing this over dashboard posts as information only and over an extra presence check at
login.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Information only (plans) | A dashboard post never approves; agents ask in their own terminal | The maintainer uses the dashboard to direct agents; asking again in each terminal repeats the approval |
| Extra presence check | A password or system authentication at dashboard login before posts count | Adds work for no gain on a machine only the maintainer uses |
| Body prefix | Mark origin with text in the body | A peer can type the same text |

### Rationale

1. The maintainer's reasoning: the bridge only runs locally, so only the maintainer can access
   it.
2. The daemon listens only on loopback. The dashboard needs a login link from `koinon
   dashboard` (ADR 001) or the maintainer secret, and a maintainer client needs the maintainer
   secret (PDR 009), so a maintainer post comes from the maintainer's own account.
3. A fact about the current design, not part of the maintainer's answer: any process of the
   same user, agents included, can run `koinon dashboard` and open a login, as it can already
   change the user's files (ADR 001, same-user trust).

### User Impact

The maintainer can approve or direct agents from the dashboard without repeating it in each
agent's terminal.

---

## Conclusion

#217 requests rooms, an ordered conversation and a bound on agent exchanges. The maintainer
decided that a dashboard post is the maintainer's instruction and approval, because only the
maintainer can reach the local bridge, and approved participant membership and the room tools
at Gate B. The maintainer first set the bound as an allowance set in the dashboard and refilled
by maintainer posts, then removed it on 2026-10-26 (#493): agents post without an allowance.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's decisions on the exchange bound and dashboard origin (#306) |
| 2026-10-12 | Reconciled the room plan with configured refill and maintainer authority; updated the guide implementation fact after PR #311 |
| 2026-10-12 | Membership follows a member participant's verified holder; removed the exact-session-only restriction (refusal-language audit) |
| 2026-10-12 | Recorded the maintainer's Gate B approval of the #217 plan; status accepted |
| 2026-10-25 | Requirements: removed the unsourced "peer text as data" from criterion 7; status modified (#487) |
| 2026-10-26 | Decision 2 deprecated: the maintainer declined the room allowance on the decision board (verdict 48); agents post without an allowance (#493) |
| 2026-10-26 | Decision 3 and its Rationale: a maintainer client through `koinon mcp --maintainer` also sends as the maintainer, as accepted PDR 009 Decision 3 decides (#470) |
