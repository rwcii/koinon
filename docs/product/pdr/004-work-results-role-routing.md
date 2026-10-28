---
pdr: "004"
title: "Coordinated Work Results and Role Routing"
status: modified
created: "2026-10-12"
updated: "2026-10-27"
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
  - work-items
---

# PDR 004: Coordinated Work Results and Role Routing

**Date:** 2026-10-12
**Status:** Modified
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently has v1 work items: proposed to a consumer key, started by an explicit claim,
and finished by the current owner as a terminal record. Issue #140 asks for three additions
for a coordinator and worker pair: route a proposal to a role across session resets, let a
different consumer accept or reject a reported result, and let the assignee and creator
discover the transitions that concern them.

Role routing (Decision 1) is built (#326), and the reported state and verdict (Decisions 2
and 3) are built (#328); discovery (Decision 4) is built in #140 chunk 03. The maintainer
approved the reviewed plan at Gate B on 2026-10-12
([#140 comment 6127384411](https://github.com/rwcii/koinon/issues/140#issuecomment-6127384411)).
This record states what #140 requests and what the approved plan builds. The maintainer decided on 2026-10-12 that the reporting worker is refused
the verdict, as #140 asks; the plan's wider exclusion is not adopted.

---

## Table of Contents

1. Problem Context
2. Decision 1: Route a proposal to a role without claim inheritance
3. Decision 2: A reported state and a verdict by a different consumer
4. Decision 3: The reporting worker cannot record the verdict
5. Decision 4: Discovery through current queries and coalesced wakes
6. Conclusion
7. Document History

---

## 1. Problem Context

### The Problem

A coordinator assigns bounded tasks and a worker builds them. Today (docs/WORK-ITEMS-V1.md)
`proposed_assignee` and the claim owner are consumer keys, and `work finish` is the owner's
own assertion. After a worker reset, the successor does not find items proposed to the old
key, and reusing one key makes it inherit the old identity and live claim. Nothing lets the
coordinator accept a result or send it back for rework while keeping the history.

### Product Requirements

From the #140 acceptance criteria:

1. An item proposed to a role before a reset is listed for that role after the reset.
2. The successor does not inherit the predecessor's claim or identity. Its start is refused
   while the old claim is live and succeeds after it expires or is released.
3. The owner cannot accept or reject its own reported item; another consumer can.
4. Reject returns the item to open with a new scope revision; history shows the report,
   the verdict and both scope revisions.
5. An accepted item counts as finished for listings and retention.
6. After lost wakeups or a reconnect, the assignee and creator can find every relevant
   transition through catch-up. Handling it twice has no additional effect.

Before #487 the list also held #140's criterion 7: "None of these records grants approval or
permission." #487 removed it (Decision 2).

### Scope

Covers role routing, the reported result and its verdict, and transition discovery for work
items. Inventory rows: C074. It does not cover the human decision board (PDR 007), dashboard
rooms (PDR 005) or how participants and roles are registered (PDR 001). #140 excludes
scheduling, auto-assignment, forced takeover, budget enforcement and dependency tracking.

---

## 2. Decision 1: Route a proposal to a role without claim inheritance

### Decision

An item can be proposed to a role in its repository, such as a family's worker role, instead
of a consumer key. The session that currently holds that role lists the item. A successor
gets no claim or identity from its predecessor. An optional
`proposed_role` (family and launcher role), exclusive with `proposed_assignee`, resolved
through the participant registry. An unheld or conflicting role is reported as such. A claim
started from a role proposal belongs to the accepting native session for its whole
generation. Existing consumer proposals and claims keep v1 behavior.

**Provenance:** Requested in #140 (issue body, gap 1 and acceptance criteria); a request
record, no maintainer quote found. The `proposed_role` mechanism: Proposed in
the #140 plan (chunk 01); approved at Gate B. Maintainer, 2026-10-12: "approved as
recommended.", answering "Do you approve Gate B for #140?"
([#140 comment 6127384411](https://github.com/rwcii/koinon/issues/140#issuecomment-6127384411)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Stable consumer key | Each session declares the same work key | #140: the successor inherits the predecessor's identity and live claim |
| Transfer the claim with the role | The new holder takes the live claim | #140 asks that the successor not inherit the claim |
| Forced takeover | Let the new holder break the old lease | A non-goal of #140 and of v1 |

### Rationale

1. Routing answers "who should pick this up"; the claim answers "who is writing it now".
   #140 asks that the two stay separate.

### User Impact

A coordinator proposes work to a role once. After a reset, the worker's new session finds
it and starts it when the old claim is gone.

---

## 3. Decision 2: A reported state and a verdict by a different consumer

### Decision

The worker reports a result with evidence references. A different consumer then records
accepted or rejected. Reject returns the item to open with a corrected scope revision and
keeps the report and both scopes. Accept finishes the item as completed. A `reported`
lifecycle state lies between active and finished, with `work_report`, `work_accept` and
`work_reject`. Reported items do not expire; the finished retention of up to 30 days starts
at acceptance. A create in a store that holds its item limit first removes the oldest finished
item, also before its 30 days; open, active, blocked and reported items are never removed. A report reserves storage for its later verdict, with at most 16 pending
reservations per store and 32 per daemon. Unreported v1 finish and withdrawal stay available.

**Provenance:** Requested in #140 (issue body, gap 2 and acceptance criteria), which
recommends the `reported` state and names an alternative; a request record, no maintainer
quote found. State choice and funding: Proposed in the #140 plan (decision and chunk 02); approved at
Gate B (#140 comment 6127384411).

Before #487 this decision also said "The verdict is same-user reported data and grants no
authority.", and the requirements held #140's criterion 7. No maintainer source was found for
either. The #487 review listed three limits and proposed to keep them; the question was whether
to keep them or to remove them too. Maintainer, 2026-10-25: "remove all limit text.  remove all
of the caveats, controls, etc.  the agents have their own built in safeties, they do not need
you adding more complications." ([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit lists the code
refusals for a separate decision (#493). The verdict rule of Decision 3 keeps its source and stays.

Early removal of finished items: Maintainer, 2026-10-27, decision board item WORK-LIMIT-529
(revision 1), [DECISIONS.md#WORK-LIMIT-529](../../../DECISIONS.md#work-limit-529), "approved". The question: "Let new work items replace old finished
items when a store is full? (#529)". The card said that the koinon store held 128 items (116
finished), so `work_create` was refused with `capacity` until the first finished item reached
30 days on 2026-11-05, and recommended: "When a new item would go past the limit, Koinon
removes the oldest finished item first, also before its 30 days. Koinon never removes an item
that is open, active, blocked or reported. The limit of 128 and the storage bounds do not
change. A finished item then stays for up to 30 days, or until the store needs its space." The
other choice was to decline and name another change, such as a higher limit or a shorter
retention. Reason: a store full of finished items blocked all new work for up to 30 days.
Scope: the per-store item limit only; the other limits and the 30-day retention stay. That
"oldest" means the earliest expiry and that a replayed create removes nothing are
implementation details of #529; no maintainer decision.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Verdict on a finished item | A non-owner verdict attached to a finished item; reject reopens it (#140's alternative) | The plan keeps v1's finished state terminal; reopening changes what finished means |
| Owner finish only | Keep today's owner assertion | No independent acceptance; the gap #140 names |
| Use the human decision board | Record the verdict as a human decision | That board is for the maintainer's verdicts (PDR 007); this one is between agents |

### Rationale

1. A separate state lets rejection return to open without reopening a completed item, and
   reserving the verdict at report time keeps full storage from stranding a reported item.

### User Impact

The coordinator sees reported items waiting for its verdict and can accept or send back each
one. The item's history shows every report, verdict and scope revision.

---

## 4. Decision 3: The reporting worker cannot record the verdict

### Decision

The reporting consumer and the native session that reported an item cannot accept or reject
it. Switching consumer labels does not make that session another worker. A different consumer
from another native session can record the verdict, including a later session in the same
worker role. The plan's wider exclusion of every session in the reporter's logical worker
participant is not adopted. The explanation given before the maintainer chose was that the
verdict is a second look by a session other than the one that did the work; otherwise
"accepted" means only that the worker says it is done.

**Provenance:** Requested in #140 (owner cannot verdict its own reported item). Maintainer,
2026-10-12: "a, next item", choosing the #140 rule over the plan's logical-participant
exclusion and over no rule, after asking "why is any agent refusing to accept the work of a
peer session?" ([#306 comment 6119167156](https://github.com/rwcii/koinon/issues/306#issuecomment-6119167156)).
The question's option (a) excludes "only the reporting worker"; the explanation refers to
another session. The earlier phrase "any other consumer" was an overbroad paraphrase of that
framed choice, not a separate maintainer decision about relabelling within one native session.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Logical-participant exclusion (plan) | Refuse every current session of the reporter's family and role | Blocks a role the maintainer reassigns; #140 did not ask for it |
| No rule | Any consumer, the reporter included, records the verdict | "Accepted" would mean only that the worker says it is done |

### Rationale

1. The smallest rule that keeps the verdict independent of the work.

### User Impact

A worker cannot accept its own report. A coordinator, or a session the maintainer moves into
that work, can.

---

## 5. Decision 4: Discovery through current queries and coalesced wakes

### Decision

The proposed assignee or role and the item's creator can find the transitions that concern
them through current queries and catch-up after lost wakes, a reconnect or a restart.
Handling a transition twice has no effect. No per-transition notice is promised. The plan
wires work changes to the existing content-free inbox wake: one pending notice per audience,
coalescing later changes. Audience state is bounded at 256 rows per store; when full, no row
or notice is added, nothing is evicted, saturation is shown in status, and queries still list
all outstanding work. The Go daemon binds its post-commit memory-change hook
(`OnMemoryChange`) to durable work-notice reconciliation.

**Provenance:** Requested in #140 (issue body, gap 3); a request record, no maintainer quote
found. Audience rules and bounds: Proposed in the #140 plan (chunk 03); approved at Gate B
(#140 comment 6127384411).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| One notice per transition | Wake for every event | #140 asks for v1's coalescing and no per-transition notice |
| Polling only | No wake; agents poll queries | Leaves the reset and lost-wake case to chance |

### Rationale

1. The work-event stream already retains every transition, so catch-up can be complete
   while wakes stay coalesced.

### User Impact

An agent that was reset or missed a wake lists what waits for it and acts on each item once.

---

## Conclusion

#140 requests role routing without claim inheritance, a reported result with a verdict by
another consumer, and catch-up discovery. The plan proposes concrete mechanisms for each. The
maintainer decided the verdict rule (Decision 3) and approved the plan's mechanisms at Gate B.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's decision on who records the verdict (#306) |
| 2026-10-12 | Clarified the reporting-worker exclusion against the framed question: changing consumer labels does not make the same native session another worker (#140 reconciliation) |
| 2026-10-12 | Recorded the maintainer's Gate B approval of the #140 plan; status accepted; role routing covers default participants |
| 2026-10-12 | Role routing and the reported state with its verdict are built; discovery is not |
| 2026-10-12 | Built production work discovery and coalesced notices under the approved chunk 03 plan |
| 2026-10-25 | Decision 2 and the requirements: removed the unsourced "grants no authority" and "grants approval or permission" text; status modified (#487) |
| 2026-10-27 | Decision 2: a create at the item limit removes the oldest finished item, approved on the decision board (WORK-LIMIT-529) (#529) |
