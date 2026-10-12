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

Koinon currently registers a Claude, Codex, OpenCode or Antigravity session only when
`koinon <family>` started it, and DeepSeek by command. It gives a Codex sub-agent a peer name
but no participant, takes roles from the launch record, moves a participant's address only on
host or pane evidence, and lets only the dashboard's **Make holder** choice lift a former
holder's fence.

This behavior shipped with the participants sprint (PRs #263 to #268) and later fixes. No
maintainer quote has been found for any of these choices; the sprint's records say only that
the maintainer approved the plan at gate B. The record is `draft`. Two choices replace
earlier requests (#228, criterion 5; #141, item 3), and the maintainer has not decided whether
to keep them.

---

## Table of Contents

1. Problem Context
2. Decision 1: Only launched sessions register; DeepSeek by command
3. Decision 2: Sub-agents get a peer name, not a participant
4. Decision 3: Roles come from the launch record
5. Decision 4: Succession needs host or pane evidence
6. Decision 5: Only the dashboard lifts a fence
7. Statements this record does not adopt
8. Conclusion
9. Document History

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
succession evidence and fences. Does not cover the participant's inbox, cursor and claims
after a holder change (PROTOCOL.md, "Participants and roles"), terminal naming and wakes
(PDR 002), work routing by role (PDR 004) or how identity is read (ADR 001).

---

## 2. Decision 1: Only launched sessions register; DeepSeek by command

### Decision

Koinon currently registers a Claude, Codex, OpenCode or Antigravity session only with its own
launch record, which `koinon <family>` creates and passes to the session's `koinon mcp`. The
daemon refuses, on every path, a registration or renewal without a matching launch with
`not_launched` (`admitLaunch`, `Store.Mutate`). A launch matches when it has the caller's
family and directory and its host process is among the caller's ancestors, or, for a
background job, when its recorded job ID begins the session ID. A direct start is islanded:
it is not registered or listed, and every Koinon tool returns `not_launched` with the launcher
command. A session that an older store holds without a launch expires with its records kept.

DeepSeek has no launcher (`launcher.Families` holds the other four families). It registers
with `koinon register --as deepseek:ID`; the daemon refuses a DeepSeek registration with a
launch ID or `subagent`. A DeepSeek session has no role and no host record.

**Provenance:** Shipped in PR #263 (merge commit fec77b1), documented in PR #265 (66cd58d);
no maintainer decision found. The participants plan (PR #258) lists both as 2026-10-08
maintainer decisions and records gate B approval of the plan at c6e5081 in agent-written text;
a gate approval approves a plan document, not each sentence in it. The DeepSeek choice is
recorded "until #199 settles its MCP path"; #199 is open, and no source states a permanent
no-launcher policy.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Verified direct start (#228, criterion 5) | A direct Codex start registers and names its terminal when host and pane are verified | Replaced by the plan's launched-only choice; the shared Codex daemon hides the pane, and no other verified attribution was found |
| Peer-only direct start | A direct start registers with a peer name but no host record, succession or naming | No record found that the plan weighed it; it keeps two kinds of session, which the plan named as a gap |
| Any start registers (v0.1.0) | Every session registers; a launch only adds host data | Host, pane and role then depend on how the session started |
| DeepSeek launcher | `koinon deepseek` with launch records | Deferred until #199 settles DeepSeek's MCP path and live proof |

### Rationale

1. The plan states that a launch gives every session the host, pane and role that succession
   and naming need (problems 4 and 6), and that isolating an agent is a supported use.
2. The command path is the only DeepSeek path that works today.

### User Impact

A session started with `claude`, `codex`, `opencode` or `agy` cannot send, receive or use the
memory and work tools; its tools name `koinon <family>`. DeepSeek gets no role, succession or
terminal naming.

---

## 3. Decision 2: Sub-agents get a peer name, not a participant

### Decision

Koinon currently treats a Codex call whose turn metadata names a `thread_source` other than
`user` as a sub-agent; `koinon mcp` registers that thread again with `subagent: true`, and the
flag stays set. A sub-agent has its own peer name and sends and receives by it. It does not
hold or qualify for a participant: `assignNames` releases any address it holds, succession
refuses it with `subagent`, and `ChooseHolder` refuses it with `not_participant`. Koinon
detects sub-agents only for Codex.

**Provenance:** Shipped in PR #263 (merge commit fec77b1) and PR #266 (merge commit 1eaa7e0);
no maintainer decision found. The plan states it as acceptance criterion 1 after a live check
found that each Codex sub-agent registered as its own session.

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
looks the same as an intentional `/resume`.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Resume takes it back (#141, item 3; 2026-09-24 plan, criterion 5) | A later `/resume` registers the old thread and takes the address again | Replaced for fenced sessions; no verified resume signal |
| Same-host after guard | A fenced session on the holder's host takes it back after the guard | Proposed in the plan's first review; an idle old thread looks like a resume |
| Time-limited fence | The fence ends after a period | No record found that the plan weighed it |

### Rationale

1. A fenced thread that calls again cannot be told apart from an intentional resume.

### User Impact

A `/resume` to a fenced thread gives it only its peer name. Once it has registered again,
the maintainer can restore it with **Make holder**; otherwise a new `koinon <family>` session
takes the address under Decision 4.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C024 | "The same host process alone never moves the alias" | 2026-09-24 stable-alias sprint decision, criterion 4 | Python-era plan; same-host succession after the guard replaced it (Decision 4) |

---

## Conclusion

Koinon currently admits only launched sessions, and DeepSeek by command, so that each session
has a host, pane and role. It moves an address only on host or pane evidence or the
maintainer's dashboard choice. Each choice shipped from an approved sprint plan without
recorded maintainer words. Two replace earlier requests: #228's verified direct start and
#141's resume that takes the address back. The maintainer decides whether to keep them.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
