---
pdr: "004"
title: "Coordinated Work Results and Role Routing"
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
  - work-items
---

# PDR 004: Coordinated Work Results and Role Routing

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently has v1 work items: proposed to a consumer key, started by an explicit claim,
and finished by the current owner as a terminal record. Issue #140 asks for three additions
for a coordinator and worker pair: route a proposal to a role across session resets, let a
different consumer accept or reject a reported result, and let the assignee and creator
discover the transitions that concern them.

None of this is built. Gate B for #140 has not been given ("the rest come after", #145
comment 6116403867). This record states what #140 requests and what the reviewed plan
proposes. The maintainer decided on 2026-10-12 that only the reporting consumer is refused
the verdict, as #140 asks; the plan's wider exclusion is not adopted.

---

## Table of Contents

1. Problem Context
2. Decision 1: Route a proposal to a role without claim inheritance
3. Decision 2: A reported state and a verdict by a different consumer
4. Decision 3: The reporting consumer cannot record the verdict
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
7. None of these records grants approval or permission.

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
gets no claim or identity from its predecessor. The plan proposes an optional
`proposed_role` (family and launcher role), exclusive with `proposed_assignee`, resolved
through the participant registry. An unheld or conflicting role is reported as such. A claim
started from a role proposal belongs to the accepting native session for its whole
generation. Existing consumer proposals and claims keep v1 behavior.

**Provenance:** Requested in #140 (issue body, gap 1 and acceptance criteria); a request
record, no maintainer quote found. The `proposed_role` mechanism: Proposed in
the unpublished #140 plan draft (742a6f2) (chunk 01); not decided.

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
keeps the report and both scopes. Accept finishes the item as completed. The verdict is
same-user reported data and grants no authority. The plan proposes a `reported` lifecycle
state between active and finished, with `work_report`, `work_accept` and `work_reject`.
Reported items do not expire; the 30-day finished retention starts at acceptance. A report
reserves storage for its later verdict, with at most 16 pending reservations per store and
32 per daemon. Unreported v1 finish and withdrawal stay available.

**Provenance:** Requested in #140 (issue body, gap 2 and acceptance criteria), which
recommends the `reported` state and names an alternative; a request record, no maintainer
quote found. State choice and funding: Proposed in the unpublished #140 plan draft (742a6f2) (decision
and chunk 02); not decided.

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

## 4. Decision 3: The reporting consumer cannot record the verdict

### Decision

The consumer that reported an item cannot accept or reject it; any other consumer can. The
verdict is a second look by a session other than the one that did the work; without this
rule, "accepted" means only that the worker says it is done. The plan's wider exclusion of
every session in the reporter's logical worker participant is not adopted.

**Provenance:** Requested in #140 (owner cannot verdict its own reported item). Maintainer,
2026-10-12: "a, next item", choosing the #140 rule over the plan's logical-participant
exclusion and over no rule, after asking "why is any agent refusing to accept the work of a
peer session?" ([#306 comment 6119167156](https://github.com/rwcii/koinon/issues/306#issuecomment-6119167156)).

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
all outstanding work. Today the Go daemon has the post-commit memory-change hook
(`OnMemoryChange`) but no production caller binds it to work wakes.

**Provenance:** Requested in #140 (issue body, gap 3); a request record, no maintainer quote
found. Audience rules and bounds: Proposed in the unpublished #140 plan draft (742a6f2) (chunk 03); not
decided.

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
maintainer decided the verdict rule (Decision 3); the other mechanisms are undecided.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's decision on who records the verdict (#306) |
