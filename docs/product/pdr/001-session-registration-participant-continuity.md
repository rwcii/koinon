---
pdr: "001"
title: "Session Registration and Participant Continuity"
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
  - "002"
  - "004"
tags:
  - identity
  - flow
  - onboarding
  - integration
---

# PDR 001: Session Registration and Participant Continuity

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon registers a native Claude, Codex, OpenCode or Antigravity session as a plain peer;
`koinon <family>` adds participant features. DeepSeek registers by command. It gives a Codex sub-agent a peer name
but no participant, takes roles from the launch record, moves a participant's address only on
host or pane evidence, and lets only the dashboard's **Make holder** choice lift a former
holder's fence.

This behavior shipped with the participants sprint (PRs #263 to #268) and later fixes. On
2026-10-12 the maintainer decided four of these choices (#306): a direct start becomes a plain
peer, Codex sub-agents keep a peer name without a participant, the fence stays, and the
participant owns its address state across a reset. Roles and succession evidence (Decisions
3 and 4) have no maintainer decision, so the record stays `draft`.

---

## Table of Contents

1. Problem Context
2. Decision 1: Launched sessions get participant features; a direct start is a plain peer
3. Decision 2: Sub-agents get a peer name, not a participant
4. Decision 3: Roles come from the launch record
5. Decision 4: Succession needs host or pane evidence
6. Decision 5: Only the dashboard lifts a fence
7. Decision 6: The participant owns its address state across a reset
8. Statements this record does not adopt
9. Conclusion
10. Document History

---

## 1. Problem Context

### The Problem

A peer name changes with each new native session, so people and agents need a stable address
(`codex-koinon`). Before the participants sprint, one alias existed per family and repository,
and two sessions of one family in one checkout could exchange it by renewal order (#228,
#141). Koinon also could not tell which terminal a directly started Codex session ran in: its
MCP servers run in Codex's shared app-server daemon, which removes `TMUX`, `TMUX_PANE` and
`KOINON_LAUNCH_ID` from their environment.

### Product Requirements

These come from issue texts that agents opened from the maintainer's account; they record
requested behavior, not the maintainer's own words.

1. Two sessions of one family work in one repository, each with a stable address (#228,
   criteria 1 and 2).
2. Registration or renewal order does not move an address (#228, criterion 2).
3. A reset session takes its predecessor's address on verified evidence (#228, criterion 3;
   #141, items 2 and 3).
4. A directly started Codex session names its own terminal when its host and pane are
   verified; naming does not depend only on the launcher (#228, criterion 5).

### Scope

Inventory rows: C003, C004, C005, C010, C011, C012, C024. Covers admission, sub-agents, roles,
succession evidence, fences and the participant's inbox, cursor and claims across a reset.
Does not cover terminal naming and wakes (PDR 002), work routing by role (PDR 004) or how
identity is read (ADR 001).

---

## 2. Decision 1: Launched sessions get participant features; a direct start is a plain peer

### Decision

Decided: a Claude, Codex, OpenCode, Antigravity or Grok session that was started without
`koinon <family>` registers as a plain peer. It has a peer name, sends and receives, and uses
the memory and work tools. It gets no participant address, role, succession or terminal
naming; those need the launch record.

Implemented: the MCP server verifies native identity on every call and registers a plain peer
when no launch ID is present. Plain peers renew and recover after an outage with their own
inbox, cursors and claims retained. They neither qualify for nor create a participant. A
retained registration without a launch renews as a plain peer and releases any former
participant address, without deleting the participant's state or lifting its fences.

A supplied launch must still match; invalid associations return `not_launched` and never
fall back to plain mode (`admitLaunch`). A launch matches when it has the caller's family and
directory and its host process is among the caller's ancestors, or, for a background job,
when its recorded job ID begins the session ID. `koinon <family>` creates the launch and
passes it to the session's MCP server.

DeepSeek has no launcher (`launcher.Families` holds the other four families). It registers
with `koinon register --as deepseek:ID`; the daemon refuses a DeepSeek registration with a
launch ID or `subagent`. A DeepSeek session has no role and no host record.

**Provenance:** Full refusal previously shipped in PR #263 (merge commit fec77b1), documented in PR #265 (66cd58d);
no maintainer decision found. The participants plan (PR #258) lists both as 2026-10-08
maintainer decisions and records gate B approval of the plan at c6e5081 in agent-written text;
a gate approval approves a plan document, not each sentence in it. The DeepSeek choice is
recorded "until #199 settles its MCP path"; #199 is open, and no source states a permanent
no-launcher policy. Plain-peer direct start: Maintainer, 2026-10-12: "b, next item", choosing
"let a direct start register as a plain peer" over keeping the full refusal ([#306 comment
6118852008](https://github.com/rwcii/koinon/issues/306#issuecomment-6118852008)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Verified direct start (#228, criterion 5) | A direct Codex start registers and names its terminal when host and pane are verified | The shared Codex daemon hides the pane, and no other verified attribution was found |
| Full refusal (previous behavior) | Every Koinon tool refuses a direct start with `not_launched` | The session cannot send, receive or use memory and work, though its identity is still verified by native metadata |
| Any start registers (v0.1.0) | Every session registers; a launch only adds host data | Host, pane and role then depend on how the session started |
| DeepSeek launcher | `koinon deepseek` with launch records | Deferred until #199 settles DeepSeek's MCP path and live proof |

### Rationale

1. A launch gives every session the host, pane and role that succession and naming need.
2. Identity comes from native per-call metadata (ADR 001), so messaging, memory and work do
   not need the launch.
3. The command path is the only DeepSeek path that works today.

### User Impact

A session started with `claude`, `codex`, `opencode` or `agy` can send, receive and use the
memory and work tools by its peer name. It is not reachable by a participant address, and
its terminal is not renamed. DeepSeek gets no role, succession or
terminal naming.

A Grok session registers only in Grok's default mode. A session served through Grok's leader
process is refused with `grok_leader_mode`, launched or not: its server has the first
terminal's directory and pane, so its repository and host would be wrong. Gate B approved the
default mode only (#254 comment 6118290944); leader mode is a follow-up. A Grok launch chooses
the first session's ID and admits only that ID until a session has registered through it.

Direct Codex and OpenCode starts have no automatic wake in this implementation: the launch
provides Codex's CLI target and OpenCode's wake endpoint. Grok has no wake provider yet. Their waiting messages are stored
and remain readable with the inbox tool. Claude and Antigravity retain their native socket
and turn-boundary paths.

---

## 3. Decision 2: Sub-agents get a peer name, not a participant

### Decision

Koinon currently treats a Codex call whose turn metadata names a `thread_source` other than
`user` as a sub-agent; `koinon mcp` registers that thread again with `subagent: true`, and the
flag stays set. A sub-agent has its own peer name and sends and receives by it. It does not
hold or qualify for a participant: `assignNames` releases any address it holds, succession
refuses it with `subagent`, and `ChooseHolder` refuses it with `not_participant`. Koinon
detects sub-agents only for Codex.

Grok Build runs a sub-agent's tool calls through the parent's MCP server with the parent's
session ID and no per-call marker. Koinon attributes those calls to the parent session, as it
does for Claude Code sub-agents, and documents that in the guide and PROTOCOL; it adds no
refusal of Grok sub-agent calls (#254 chunk 02).

**Provenance:** Shipped in PR #263 (merge commit fec77b1) and PR #266 (merge commit 1eaa7e0);
no maintainer decision found. The plan states it as acceptance criterion 1 after a live check
found that each Codex sub-agent registered as its own session. Grok: Maintainer, 2026-10-12:
"your  Gate B for Grok #254 recomendation is approved." (#254 comment 6118290944), approving
parent-scoped attribution with no default sub-agent ban. Codex sub-agents: Maintainer,
2026-10-12: "a, next item", keeping the own peer name with no participant over counting the
sub-agent's calls as its parent's ([#306 comment 6118909834](https://github.com/rwcii/koinon/issues/306#issuecomment-6118909834)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Sub-agent qualifies | Count it like any session of its participant | It would conflict with, or take the address from, the thread that started it |
| Act for the parent | It calls as its parent's participant | No record found that the plan weighed it |
| No registration | Refuse sub-agent calls | No record found that the plan weighed it; it would remove peer-name messaging |

### Rationale

1. A sub-agent of the holder in the same checkout would otherwise be a second qualifier.

### User Impact

A message to a sub-agent uses its peer name; the dashboard shows "sub-agent; no participant".

---

## 4. Decision 3: Roles come from the launch record

### Decision

Koinon currently takes a session's role only from its launch record, set with
`koinon <family> --role ROLE`, not from a tool argument; the address is
`<family>-<label>-<role>`. A role has 1 to 24 lower-case letters, digits or hyphens and starts
with a letter (`ValidRole`). A role made only of hexadecimal digits and hyphens, such as
`cafe` or `add`, is refused, so an address cannot look like a peer name's hexadecimal suffix.
The code does not check who runs the launcher; PROTOCOL.md, docs/USAGE.md and the guide
describe roles as maintainer-assigned.

**Provenance:** Shipped in PR #266 (merge commit 1eaa7e0); no maintainer decision found. The
plan lists "additional participants of one family get maintainer-assigned role suffixes" as a
2026-10-08 maintainer decision in agent-written text; #228, criterion 2, named it as one
option beside participant slots. No source found for the syntax values.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Participant slots | Numbered participants instead of named roles | Not chosen in the plan; no reason recorded |
| Role as tool argument | A session names its role when it registers | A model-chosen argument would choose the address |
| Free-form roles | Any text as a role | Hexadecimal roles can look like peer-name suffixes |

### Rationale

1. A role in the launch record ties the address to how the session was started.

### User Impact

A second session of one family in a repository needs `--role` to get its own address.

---

## 5. Decision 4: Succession needs host or pane evidence

### Decision

Koinon currently lets a session take its participant from an active holder only through its
own tool call, on one of this evidence (`Store.succeed`): the same host process (PID and start
time) after the holder made no tool call for the guard setting (30 seconds by default), as
`same_host`; a Codex `/clear` that the SessionStart hook reports on the holder's host, at once,
as `host_cleared`; or the same tmux server and pane with the holder's host ended, as
`same_pane`. Otherwise the holder stays and the result names the refusal. Without an active
holder, exactly one qualifier takes the address; several leave it unheld until the maintainer
chooses or one remains.

**Provenance:** Shipped in PR #266 (1eaa7e0), PR #268 (9aef846), PR #293 (978fcb8) and
PR #294 (baa2b02); no maintainer decision found. #141 comment 5821230593 records a 2026-09-24
"user decision" on same-pane succession in agent-written text.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Host process as evidence only (2026-09-24 plan) | The same host process alone never moves the address | Replaced in the participants plan so that a Codex `/clear` can move the address; the guard waits until the holder is quiet |
| Explicit reset link (#141, item 2) | A verified link from old thread to new thread | No supported signal found; the `/clear` hook is the one used |
| Renewal order | The next registration takes a free address | Moved addresses between participants (#228) |

### Rationale

1. A live check on 2026-09-24 (#141) showed that `/clear` keeps the Codex process and
   `/resume` switches it back, so the host process alone does not show which thread is active.

### User Impact

After a reset in the same terminal, the new session takes the address on its first tool call,
or after the guard. `peers` and the dashboard show each result and refusal.

---

## 6. Decision 5: Only the dashboard lifts a fence

### Decision

Koinon currently retires and fences a former holder in the same transaction as each holder
change. A fenced session can register again with its peer name, but it does not qualify for
that participant, and its succession is refused with `fenced`, also after a `/resume`. Only
**Make holder** in the dashboard (`ChooseHolder`) deletes the fence.

**Provenance:** Shipped in PR #267 (merge commit 850cbdd) and PR #268 (merge commit 9aef846);
no maintainer decision found. The plan records it as a gate B decision in agent-written text:
no resume evidence is verified, and a retained old thread that calls after a quiet period
looks the same as an intentional `/resume`. Maintainer, 2026-10-12: "a, next item", keeping
the fence with the dashboard as the way back over letting a resume take the address back
([#306 comment 6118891323](https://github.com/rwcii/koinon/issues/306#issuecomment-6118891323)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Resume takes it back (#141, item 3; 2026-09-24 plan, criterion 5) | A later `/resume` in the same pane takes the address again once the newer holder ended | An old thread that wakes by itself could take the address from the current session |
| Same-host after guard | A fenced session on the holder's host takes it back after the guard | Proposed in the plan's first review; an idle old thread looks like a resume |
| Time-limited fence | The fence ends after a period | No record found that the plan weighed it |

### Rationale

1. A fenced thread that calls again cannot be told apart from an intentional resume.

### User Impact

A `/resume` to a fenced thread gives it only its peer name. Once it has registered again,
the maintainer can restore it with **Make holder**; otherwise a new `koinon <family>` session
takes the address under Decision 4.

---

## 7. Decision 6: The participant owns its address state across a reset

### Decision

Messages sent to a participant address, the participant's default memory cursor and its
default work claims belong to the participant, not to one native session. A session that takes
the address continues the unread messages, the cursor position and the live claim generation
and lease. Messages sent to a peer name stay with that one session.

**Provenance:** Shipped in PR #266 (merge commit 1eaa7e0) and PR #267 (merge commit 850cbdd).
Maintainer, 2026-10-12: "a, next
item", keeping participant ownership over each native session owning its own state, as #228
criterion 4 asked ([#306 comment 6118864620](https://github.com/rwcii/koinon/issues/306#issuecomment-6118864620)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Per-session ownership (#228, criterion 4) | Each native session keeps its own inbox, cursor and claims; nothing moves at a reset | A reset leaves unread messages and the live claim with the old session, and the successor waits for the claim to expire |

### Rationale

1. A reset continues the participant's work instead of stranding it.

### User Impact

After a reset, the new session sees the participant's unread messages and continues its work
item without waiting for a lease to expire.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C024 | "The same host process alone never moves the alias" | 2026-09-24 stable-alias sprint decision, criterion 4 | Python-era plan; same-host succession after the guard replaced it (Decision 4) |

---

## Conclusion

Launched sessions get the participant address, role, succession and naming; a direct start is
a plain peer. An address moves only on host or pane evidence or the maintainer's dashboard
choice, and the participant keeps its messages, cursor and claims across a reset. The
maintainer decided the direct-start, sub-agent, fence and ownership choices on 2026-10-12;
roles and succession evidence remain shipped behavior without a maintainer decision.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's decisions on direct start, sub-agents, the fence and participant ownership (#306) |
| 2026-10-12 | Applied Decisions 1 and 2 to Grok Build: direct start, default-mode scope and parent-scoped sub-agents (#254 chunk 02) |
