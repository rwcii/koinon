---
pdr: "006"
title: "Context Monitoring and Managed Handoffs"
status: draft
created: "2026-10-12"
updated: "2026-10-16"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "001"
  - "002"
  - "008"
tags:
  - flow
  - notifications
  - integration
  - error-handling
---

# PDR 006: Context Monitoring and Managed Handoffs

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently observes context use for some families (the Claude status line and Codex
rollouts) and shows it in `peer_status` and the dashboard. It does nothing when a session
nears its limit. Issue #196 asks for a managed cycle: the daemon watches context against a
threshold, asks the session over Koinon to write its handoff, and after the ready reply enters
the family's reset and pickup commands in the agent's own tmux pane.

The cycle is not built; Gate A and Gate B for #196 are approved. The maintainer's recorded words cover
Enable carry-over across a verified reset and terminal commands as a required part of the
cycle. The rest is requested in #196 or proposed in its plan.

---

## Table of Contents

1. Problem Context
2. Decision 1: Family defaults with a per-session Enable
3. Decision 2: Enable carries over a verified managed reset
4. Decision 3: Ask, wait for ready, then enter commands in the agent's pane
5. Decision 4: Verify the reset, report every step, and fall back to embedded skills
6. Statements this record does not adopt
7. Conclusion
8. Document History

---

## 1. Problem Context

### The Problem

Today the maintainer watches each terminal and runs the handoff, reset and pickup by hand.
A reset before the handoff, in the wrong pane, or without pickup context loses the session's place.

### Product Requirements

1. Compare each session's observed context use with a threshold (#196).
2. For an enabled session, ask it to write its handoff and wait for its ready reply.
3. Then enter the family's reset and pickup commands in its own pane, reading it after each step.
4. Show each step, wait and failure in the dashboard and the audit log, on Linux and macOS.

### Scope

Covers monitoring, enabling, the handoff request, terminal commands for reset and pickup,
verification and reporting. Inventory rows: C077, C078. Pane identity and general terminal
interaction are PDR 002. Handoff and pickup skills and the embedded catalog are PDR 008.

---

## 2. Decision 1: Family defaults with a per-session Enable

### Decision

The dashboard holds family defaults for context mode and threshold. Each session is off
until it is enabled; family defaults enable no session by themselves. A session can have its
own threshold, which replaces its family's threshold while it is set. The family settings, a
session's Enable and its own threshold change in the dashboard (login and CSRF token), with
`koinon context` on the command line, or by an agent session through MCP. Every change is
audited with its origin: the dashboard, `cli`, or `agent` and the calling session's peer name.
The plan proposes that Codex default to its native compaction, and that only fresh,
verified observations from the session start a cycle; unknown or stale context shows a reason.

**Provenance:** Monitoring, the threshold and per-session enabling: Requested in #196 (issue
body, "The cycle the user decided" and "Bounds"); a request record, no maintainer quote in
it. Family defaults with per-session enabling: Maintainer, 2026-10-12, session transcript:
"Family defaults; enable each session (recommended)", the answer to the plan's choice of how
the cycle is enabled (relayed in #196 comment 6115978138). It decides that choice only. The
CLI and agent changes with audit, and the per-session threshold: Maintainer, 2026-10-16,
#391, the request: "I'd like to be able to modify settings like enabling handoff and modifying
handoff type and thresholds from CLI and MCP. Sometimes I am not able to access the
console/dashboard, but I can SSH or ask an agent to enable its session (or one it's managing)
for handoff, and to change the threshold." Then "approved, allow CLI and agents with audit"
(#391 Decision 1), and "ok" to keep the family threshold with an optional per-session
threshold (#391 Decision 2). The
Codex default and freshness rule:
Proposed in the unpublished #196 plan draft (9206a1d); not decided. Current observations: Shipped in PR
#193 (merge commit a19bc2b); no maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Family-wide enable | Turning on a family enables all its sessions | Replaced by the per-session choice recorded on #196 |
| Per-session settings only | Each session sets its own mode and threshold | More dashboard work for the same values; a per-session threshold that replaces the family's is adopted (#391) |
| Dashboard-only changes | Only the dashboard changes the settings and the Enable | The maintainer cannot always reach the dashboard (#391) |

### Rationale

1. Defaults avoid repeating settings; a per-session switch keeps the cycle on only where
   the maintainer turned it on.

### User Impact

The maintainer sets mode and threshold once per family and turns the cycle on per session,
from the dashboard, a shell or an agent; a session can keep its own threshold.

---

## 3. Decision 2: Enable carries over a verified managed reset

### Decision

A session's Enable carries over to the session that follows a managed reset only when the
cycle proves the new session is in the same pane and participant. A failed or uncertain
cycle, or a change of pane or participant, ends the Enable; the maintainer enables again.

**Provenance:** Maintainer, 2026-10-12: "gate A approved, and enable carries over as you
recommend, gate b 145 approved.  the rest come after" (#196 comment 6116403721), answering
the recommendation quoted in that comment.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Enable ends at every reset | The maintainer re-enables after each cycle | Not chosen; the maintainer took the recommendation |
| Carry over on any successor | Follow the participant to any new session | Not recommended; a different pane is not proven to be the same agent |

### Rationale

1. A proven same-pane, same-participant successor continues the same work.

### User Impact

A long-running session keeps its cycle across resets without re-enabling, until a cycle
fails or the session moves.

---

## 4. Decision 3: Ask, wait for ready, then enter commands in the agent's pane

### Decision

At the threshold, Koinon asks the session to start its handoff through `overwatch`, a
reserved system identity like `maintainer`, with its own sender and never maintainer origin.
No native session registers, aliases or holds that name. The request identifies its cycle
and the maintainer's Enable; a peer body claiming that identity carries no system origin.
The agent
finishes or pauses its step, writes its handoff and replies that it is ready; a peer cannot
reply ready for another session. After the reply, Koinon enters the family's commands in the
agent's own pane, proven as in the #156 rename (the pane process is an ancestor of the
agent's host with no other agent between them): `/clear` then `/pickup`, or `/clear` then
`$pickup` for Codex, with no appended task text (PDR 002, Decision 4); for Codex the clear step
also enters the local `/status`, whose card names the new thread for the reset proof (#196
chunk 04). It reads the pane
after each step and handles prompts or alerts shown during the cycle under the maintainer's
existing authorization and native permissions (PDR 002, Decision 2). It reports an outcome
it cannot reconcile rather than counting it as a completed step. Peer text is not entered.
The cycle stops with a shown reason when no ready reply
arrives in time, the pane cannot be proven, or the session is busy. Entering commands in the
tmux session is a required part of the cycle, not an exception.

**Provenance:** Maintainer, 2026-10-12: "it also needs to enter commands in the tmux session
for context management handoffs." (#196 comment 6117338153). The steps, commands and stop
conditions: Requested in #196 (issue body); a request record. The ready-reply form: open in
#196 ("To decide in the plan").

Sender provenance: Maintainer, 2026-10-13, answering the recommendation for a distinct daemon
sender: “I agree, let's call it "overwatch"”
([record](https://github.com/rwcii/koinon/issues/196#issuecomment-6136143253)); then “its a
reserved system identity as well” ([record](https://github.com/rwcii/koinon/issues/196#issuecomment-6136161569)).
Gate B approves the reviewed three-chunk plan with the sender addition
([record](https://github.com/rwcii/koinon/issues/196#issuecomment-6136155493)). Eventual
agentic capabilities are future context, outside this managed-cycle deliverable.


### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Maintainer resets by hand | Koinon only alerts | The manual work #196 removes |
| Reset without a ready reply | Enter commands at the threshold | Can reset before the handoff is written |

### Rationale

1. The ready reply puts the reset after the handoff; pane proof and read-back target the
   right session and catch an unexpected screen.

### User Impact

The maintainer sees a session hand off, reset and pick up without typing in its terminal.

---

## 5. Decision 4: Verify the reset, report every step, and fall back to embedded skills

### Decision

Each step, wait, failure and outcome appears in the dashboard and the audit log. The plan
proposes that native reset, the session's registration and the receiving model's pickup be
verified separately; transport acceptance or a low context value is not enough. A restart
does not repeat terminal commands or count an uncertain reset as done. A session without
installed handoff or pickup skills can use the embedded catalog's skills for this cycle,
shown in the dashboard; if that bootstrap cannot be proven, the cycle stops before reset.
Families without the native proofs tracked in #275 and #199 stay shown as unavailable.

**Provenance:** Reporting: Requested in #196 (issue body, step 4); a request record. Reset
verification is open in #196 ("To decide in the plan"). Separate verification, restart
handling and the embedded fallback: Proposed in the unpublished #196 plan draft (9206a1d); not decided.
The fallback is also named in the planning scope comment (#196 comment 6115978138), which is
not a maintainer quote.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Trust the command send | Count the cycle done once commands are entered | Cannot tell a failed clear from a good one |
| Install missing skills first | Adopt skills before the first cycle | Adoption is a separate, approved step (PDR 008) |

### Rationale

1. A visible, audited outcome lets the maintainer see where a cycle stopped and why.

### User Impact

Every cycle has a visible result. A family that cannot be proven shows why it is unavailable.

---

## 6. Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C078 | Terminal input is an exception to Go sprint decision 6, approved for this cycle only | Earlier #196 issue body ("Bounds"); the original #196 plan draft (9206a1d) decision.md (constraints); both corrected in the reconciliation | Decision 6 was agent-written with no maintainer source and was removed in #305; the maintainer stated the cycle needs terminal commands (#196 comment 6117338153) |
| C078 | Only the bare reset commands may be typed; the skill bootstrap must fit a bare-command restriction | Earlier #196 issue body ("Bounds"); the original #196 plan draft (9206a1d) decision.md (criterion 4, constraints); both corrected in the reconciliation | A limit with no maintainer source; it would block clearing prompts that appear during the cycle, which the agent-written intent in #196 comment 6117338153 counts as part of the same work |

---

## Conclusion

The managed cycle monitors context, asks the agent to hand off, and after its ready reply
enters the reset and pickup commands in its own pane, with visible outcomes. Enable carry-over
and terminal commands have the maintainer's words; the rest is requested or proposed.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-13 | Recorded Gate B and the maintainer's reserved overwatch system sender |
| 2026-10-12 | Reconciled the framing and active issue body with intended terminal commands and prompt handling; retained bare native commands without appended task text |
| 2026-10-14 | Recorded the Codex `/status` read in the clear step (#196 chunk 04) |
| 2026-10-16 | Decision 1: settings and Enable change also from the command line and by agents through MCP, audited with their origin; a per-session threshold (#391) |
