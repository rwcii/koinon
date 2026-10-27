---
pdr: "006"
title: "Context Monitoring and Managed Handoffs"
status: draft
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

Koinon currently observes context use for some families (the Claude status line, Codex
rollouts, the agy status line, #512, a launched OpenCode session's own server, #513, a DeepSeek session's harness, #514, and the Grok status line, #515) and shows it in `peer_status` and the dashboard. It does nothing when a session
nears its limit. Issue #196 asks for a managed cycle: the daemon watches context against a
threshold, asks the session over Koinon to write its handoff, and after the ready reply enters
the family's reset and pickup commands in the agent's own tmux pane.

The cycle is built (#196), from a plan that the maintainer approved at Gate A and Gate B. The maintainer's recorded words cover
Enable carry-over across a verified reset and terminal commands as a required part of the
cycle. The rest is requested in #196 or proposed in its plan.

---

## Table of Contents

1. Problem Context
2. Decision 1: Family defaults with a per-session Enable
3. Decision 2: Enable carries over a verified managed reset
4. Decision 3: Ask, wait for ready, then enter commands in the agent's pane
5. Decision 4: Verify the reset, report every step, and pick up through Koinon's own skill
6. Decision 5: A native check certifies the CLI version current then
7. Statements this record does not adopt
8. Conclusion
9. Document History

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
Claude defaults to the handoff cycle and Codex to its native compaction. The daemon starts a
cycle from a fresh, verified observation of the session: a context source at most two minutes
old that the daemon confirmed in the last 90 seconds. For unknown or stale context it shows the
reason, such as `context_unknown`, `source_stale` or `confirmation_stale`.

In Codex's `native_compaction` mode the same trigger makes the daemon enter Codex's own
`/compact` in the enabled session's pane while Codex is idle, once per crossing. A fresh reading
below the threshold after the submission confirms it; the Sessions page shows each compaction
and its result. Koinon edits no Codex configuration. A compaction that ends before the daemon
types anything, or because the context epoch changed, keeps the session's Enable, and the next
reading at the threshold can start another. A compaction that the daemon sent but cannot
confirm within 10 minutes, or whose submission it cannot prove, turns the Enable off, so the
daemon never types `/compact` twice for one crossing; the session then gets no compaction
until it is enabled again.

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
Proposed in the unpublished #196 plan draft (9206a1d); not decided. The daemon's trigger has
both; its code cites the #196 definition of done, approved at Gate B. Before #487 this record
gave the freshness rule as "only fresh, verified observations from the session start a cycle"
from the plan; #487 states it as what the trigger checks (#487 provenance under Decision 3).
Current observations: Shipped in PR #193 (merge commit a19bc2b); no maintainer decision found.
Koinon-triggered native compaction: Maintainer, 2026-10-26, session transcript. The question:
"does the native compaction mode for context handoff actually perform the compaction?" The
answer was no: the mode did nothing at the threshold. Then: "I want an issue that FIXES the
gap." The intent, as recorded in #509: when a Codex session in `native_compaction` reaches its
threshold, Koinon makes Codex compact its context, so the mode does what its name and the
Settings page say. When a compaction turns the Enable off: Maintainer, 2026-10-27, decision
board item COMPACT-ENABLE-509 (revision 1), verdict 61, "approved". The question: "Native
compaction: when does a compaction turn the Enable off? (#509)". The card listed the four rules
above, in that order, as rules 1 to 4 of PR #517, and recommended: "Approve the rules as they
are in PR #517. Rules 3 and 4 stop a second `/compact` for one crossing, also after a restart.
Rules 1 and 2 let a busy or changed session try again without your action." The choices were
to approve the four rules unchanged, or to decline and name the wanted rule for a new PR after
#517. The verdict decides these rules only. The confirmation rule (a fresh reading recorded after the submission's
receipt, before the deadline) and that a reading of another epoch ends a compaction
(`epoch_changed`) are implementation details of #509, agreed with the Codex peer; no
maintainer decision.

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
and the maintainer's Enable; its system origin comes from stored metadata that only the daemon
sets.
The agent
pauses its work at an appropriate stopping point, writes its handoff, replies that it is
paused and ready, and stays idle; Koinon takes the ready reply from the session that it asked. After the reply, Koinon enters the family's commands in the
agent's own pane, proven as in the #156 rename (the pane process is an ancestor of the
agent's host with no other agent between them): `/clear` then `/koinon-pickup`, or `/clear`
then `$koinon-pickup` for Codex (Decision 4), with no appended task text (PDR 002, Decision 4);
for Codex the clear step
also enters the local `/status`, whose card names the new thread for the reset proof (#196
chunk 04). It reads the pane
after each step and handles prompts or alerts shown during the cycle (PDR 002, Decision 2). It
reports an outcome it cannot reconcile rather than counting it as a completed step. Koinon
enters only the family's fixed commands.
The ready reply is the signal that the session is ready for its handoff (#417): from the
reply until the cycle ends, Koinon's own wakes to the session wait, and the commands go in
once the session is at its prompt with an empty input line. A background task that the
session leaves running does not hold the cycle back; a turn in progress, a draft and a native
dialog do. When the session does not become idle after its ready reply,
`overwatch` asks it again to pause and hand off, under a new revision, within the cycle's
lifetime and event bounds (#431). A Claude session whose background subagent wrote its
transcript after the ready reply is asked again at once, without a clear: that work is not in
the handoff (#456). Koinon reads only the file times of the session's subagent transcripts,
compared with the ready report itself; a dialog resolved after it does not move that time. A
transcript directory that cannot be read in full keeps the session from the clear.
The handoff that the ready reply names is in the session's own handoff directory of the main
checkout: the directory that the Handoff path fact of the repository's
`.koinon/skills/handoff.md` names, else `_handoff/<agent>/`, where the agent is the family with
the launch's role, not the participant address. A session registered without a repository,
such as one that coordinates several repositories from their parent directory, uses its
registered directory in place of the main checkout, and Git checks nothing there (#485). The
request names that directory, and a ready reply that a check refuses says which check and
which directory (#468). The cycle keeps each refused ready reply with its code and text, and a
ready timeout after one names the refusal's code; the Sessions page lists them (#485). The request also
names its ready deadline as a UTC time and asks the session to stop background subagents that
will not finish with enough time left to update the handoff and report ready by then; for a
Claude session at that stage, the Sessions page shows how many of its background subagents
are still running (#467).
The session's live work claims last through the cycle: admission extends them to a minute past
the cycle deadline, and a renewal during the cycle keeps that expiry (#489).
The cycle stops with a shown reason when no ready reply
arrives in time, the pane cannot be proven, or the bounds end it while the session stays busy;
the reason names what Koinon last saw in the pane. Entering commands in the
tmux session is a required part of the cycle, not an exception.

**Provenance:** Maintainer, 2026-10-12: "it also needs to enter commands in the tmux session
for context management handoffs." (#196 comment 6117338153). The steps, commands and stop
conditions: Requested in #196 (issue body); a request record. The ready-reply form: open in
#196 ("To decide in the plan"). The directory of a session without a repository and the kept
refused replies: Requested in #485 (issue body, Expected option 2, and the comment with the
dashboard view of the failed cycle); a request record. The claims that last through the cycle:
Requested in #489 (issue body, Expected option 1); a request record.

Before #487 this decision said that the cycle handles prompts "under the maintainer's existing
authorization and native permissions", that "a peer body claiming that identity carries no
system origin", that "a peer cannot reply ready for another session" and that "Peer text is not
entered". No maintainer source was found for that wording. The #487 review listed three limits
and proposed to keep them; the question was whether to keep them or to remove them too.
Maintainer, 2026-10-25: "remove all limit text.  remove all of the caveats, controls, etc.  the
agents have their own built in safeties, they do not need you adding more complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit lists the code
refusals for a separate decision (#493). The decision now says what the daemon does: it reads the
origin from stored metadata, takes the ready reply from the requester and enters fixed
commands. The checks that the daemon makes stay as facts: the ready deadline and the request
text, the handoff directory, the subagent transcript times and the full read of their
directory, and the pane proof. The per-session Enable, the overwatch sender and the pause and
ready steps keep their sources and stay.

Ready reply as the signal: Maintainer, 2026-10-20, after two cycles on an active machine
failed with `ready_timeout`: "sessions won't ever idle on an active machine due to peer
notifications. the machine needs to be able to tell koinon it's ready for handoff. that was
the original design." ([#417](https://github.com/rwcii/koinon/issues/417)). The intent: the
session's ready report, not an idle state that an active machine never reaches, lets the
cycle go ahead.

Pause, then repeat: Maintainer, 2026-10-20, after watching a v0.3.2 cycle in which the agent
wrote its handoff and went on to its next work item. No question or options were put to the
maintainer. The words followed Claude's report that the clear waits for an idle prompt that
an active agent never reaches, with a proposed fix that changed only the request, guide and
ready texts: "the process needs to be a koinon
overwatch message to the agent that needs to cycle, asking it to pause at appropriate stopping
point, write a handoff, and then respond to overwatch that the agent is paused and ready. then
overwatch verifies the agent is idle, and runs the clear/koinon-pickup. if the agent does not
idle it needs to repeat the pause/handoff request again."
([#431](https://github.com/rwcii/koinon/issues/431)). The intent: an active agent is asked to
pause, not left to reach idle by chance, and the cycle asks again when it keeps working.

Subagent work after the ready reply: Maintainer, 2026-10-22, after a Claude session with
running subagents reported ready: "but when the subagent finishes it will have an invalid
handoff.", then "you can reproduce the conditions locally and figure out how to manage this"
([#456](https://github.com/rwcii/koinon/issues/456)). The intent: a clear never restores a
handoff that lacks work done after it. Asking again on a newer subagent transcript is the fix
that #456 proposes; no maintainer decision on it found.

Handoff directory and refusal detail: found on 2026-10-23, when a session's three ready replies
were refused while its handoff was in a directory named after its participant address; the
maintainer: "diagnose, this may be another handoff failure scneario", then "let's work
through all open items to prepare for the next release"
([#468](https://github.com/rwcii/koinon/issues/468)). The intent: a session can see why its
ready reply is refused and correct it. Reading the Handoff path fact, and naming the check
and the directory, are the design of #468; no maintainer decision on them found.

Ready deadline in the request: the maintainer, 2026-10-23, after a session held its ready reply
for its subagents and the Sessions page showed only `waiting_ready`: "claude-unify is mid
handoff, and looks to be waiting for overwatch to take acton. nothign is happening.", then
"yes, watch it and file the issue" ([#467](https://github.com/rwcii/koinon/issues/467)). The
intent: a session can plan its stop against the real deadline, and the maintainer can see why
a cycle waits. The deadline text, the stop rule and the subagent count are the design of #467;
no maintainer decision on them found.

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

## 5. Decision 4: Verify the reset, report every step, and pick up through Koinon's own skill

### Decision

Each step, wait, failure and outcome appears in the dashboard and the audit log. The plan
proposes that native reset, the session's registration and the receiving model's pickup be
verified separately; transport acceptance or a low context value is not enough. A restart
does not repeat terminal commands or count an uncertain reset as done. A session without an
installed handoff skill can use the embedded catalog's skill for this cycle. Families without
the native proofs tracked in #275 and #199 stay shown as unavailable.

The cycle picks up through its own Koinon-named skill, installed in the home of the person who
runs the daemon (Claude Code's personal skills, `~/.agents/skills` for Codex), never in a
project repository. A project's own `pickup` skill stays unchanged and no longer affects the
cycle. One user-level copy per agent family replaces per-project copies and the per-session
loader directory that Claude Code launches added (`--add-dir`).

As proposed in #406: the skill is named `koinon-pickup`, and its body calls `library_read` for
the catalog item `skill.pickup` and follows the returned procedure, so the procedure itself has
one copy, in the binary's catalog. `koinon install`, and the install
that an upgrade runs, writes it as `skills/koinon-pickup/SKILL.md` under the Claude Code
configuration root and `~/.agents/skills/koinon-pickup/SKILL.md` for Codex. The daemon also
writes both files at each start when one is missing or differs, so `brew upgrade`, which
restarts the daemon, installs it without another command; a failed write is logged and leaves
the install step shown. After the clear the
cycle enters `/koinon-pickup` or `$koinon-pickup`. Before each input the cycle checks only that
file, byte for byte; a missing or different file makes the cycle unavailable with a reason that
names the install command, and an entry that cannot be proven stops the cycle before reset.
Personal and project `pickup` skills are not read. The per-session `--add-dir` loader and the
`/add-dir` step before the clear are removed. Claude Code's discovery of the personal skill has
no separate native record (PDR 008, Decision 3); the pickup step's menu check, which submits
only when the command menu lists the skill's description first, proves it in each cycle.

**Provenance:** Reporting: Requested in #196 (issue body, step 4); a request record. Reset
verification is open in #196 ("To decide in the plan"). Separate verification and restart
handling: Proposed in the unpublished #196 plan draft (9206a1d); not decided. The embedded
handoff fallback: Proposed in the same draft, and named in the planning scope comment (#196
comment 6115978138), which is not a maintainer quote.

The Koinon-named pickup skill: Maintainer, 2026-10-19, answering #406's proposed fix (give the
cycle's pickup entry a name that no project uses, and type that name instead of the bare
`pickup`): "your recommendation seems fair. that koinon specific skill should be installed for
the user, instead of in the repo."
([record](https://github.com/rwcii/koinon/issues/406#issuecomment-6276165765)). Intent: the
cycle uses its own Koinon-named pickup skill, installed in the home of the person who runs the
daemon, never in a project repository, and a project's own `pickup` skill no longer affects the
cycle. The reason: "that way only a single copy needs to be managed"
([record](https://github.com/rwcii/koinon/issues/406#issuecomment-6276169525)). Intent: one
user-level copy per agent family replaces per-project copies and the per-session loader. The
name `koinon-pickup`, the paths, the install step, the typed commands, the byte check and its
reasons, and the loader's removal: Proposed in the #406 design comment
([record](https://github.com/rwcii/koinon/issues/406#issuecomment-6276914180)), for review in
the pull request; not a maintainer decision. The daemon's write at each start follows the
maintainer's words on #397 (2026-10-17), recorded in that issue's body: "'brew upgrade koinon'
is a full command. running '$(brew --prefix)/bin/koinon install' after is an undocumented
requirement". Intent recorded there: `brew upgrade koinon` alone is the complete upgrade, and a
second step is a defect. Writing at the daemon's start is how this change meets it; the
mechanism is proposed, not a maintainer decision. Before #487 the skill's body also required
its catalog digest to equal the cycle's and said to stop and report otherwise; #487
(provenance under Decision 3) removed that from the skill text. The cycle's byte check of the
skill file, its menu check and its stop before reset stay as what the daemon checks.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Trust the command send | Count the cycle done once commands are entered | Cannot tell a failed clear from a good one |
| Install missing skills first | Adopt the catalog's skills before the first cycle | Adoption is a separate step (PDR 008); `koinon-pickup` is Koinon's own entry, not a catalog item |
| The bare `pickup` entry | Enter `/pickup` or `$pickup`, which a personal or project skill, or Koinon's loader, answers | A project's own skill blocked the cycle, and a personal copy shadows every project's own (#406) |
| A per-session loader | Add a loader directory to each Claude Code session (`--add-dir`, or `/add-dir` before the clear) | Replaced by one user-level copy per family, "only a single copy" to manage (#406) |

### Rationale

1. A visible, audited outcome lets the maintainer see where a cycle stopped and why.
2. A name that no project uses keeps the cycle's entry and a project's own `pickup` apart.

### User Impact

Every cycle has a visible result. A family that cannot be proven shows why it is unavailable.
A session in a project with its own `pickup` skill gets the cycle, and that skill is left as
it is; `koinon install` keeps the one Koinon skill per family that the cycle needs.

---

## 6. Decision 5: A native check certifies the CLI version current then

### Decision

A family's cycle adapter is available on a platform once its native checks pass there. The
checks record the CLI version they ran with; the adapter runs with any version of that CLI,
and a newer version is not refused. This holds for the Claude Code, the Codex, the agy, the
OpenCode and the DeepSeek adapters; the agy checks ran with Antigravity CLI 1.4.7 on Linux (#512),
the OpenCode checks with opencode 1.18.35 on Linux (#513), the DeepSeek checks with DeepSeek
Harness 0.2.0-rc.2 on Linux (#514).
Koinon shows no certified or not-certified status for a session's version; the release notes
and the native evidence record name the versions that the checks ran with. The adapter's
per-step proofs (pane, fence, read-back) still stop a cycle whose screen does not match.

**Provenance:** Maintainer, 2026-10-16, #196. The question: the macOS checks of the Codex
adapter passed across two Codex CLI versions (0.162.1: X0, X1, X3; 0.163.0: X0, X2), so no
single version had every row, and the adapter refused every version outside an exact list.
The answer: "codex is a moving target.  we will never be able to clear all versions, nor do
we want to be in that business." (comment 6225646772). Asked whether the Claude Code adapter's
version check goes too: "we certify then the current version, if it moves, it moves."
(comment 6225649452). Then: "the cycle can check version, but what version passed is but a
point in time." (comment 6225660579), and, replacing a proposal to show a not-certified
status: "we dont even need to show certification status." (comment 6225667112).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Exact version list | Refuse every CLI version without its own full check run | The CLIs move faster than the checks can follow (the maintainer's reason above) |
| Show certification status | Run any version, but mark versions outside the checked list | Replaced by the maintainer's last answer above |

### Rationale

1. A check run is evidence for the version of its day; the per-step proofs, not a version list,
   stop a cycle on a screen that changed.

### User Impact

A CLI upgrade does not turn the managed cycle off. A changed native screen still stops a
cycle at the step that no longer matches.

---

## 7. Statements this record does not adopt

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
| 2026-10-16 | Decision 5: a native check certifies the CLI version current then; no version gate and no certification status (#196) |
| 2026-10-19 | Decisions 3 and 4: the cycle picks up through Koinon's own `koinon-pickup` skill, installed for the person, never in a repository; the loader and its bootstrap are removed (#406) |
| 2026-10-22 | Decision 3: a Claude subagent that worked after the ready reply makes the cycle ask again (#456) |
| 2026-10-23 | Decision 3: the handoff directory follows the Handoff path fact; the request names it, and a refused ready reply names the check (#468) |
| 2026-10-23 | Decision 3: the request names its ready deadline; the Sessions page counts a Claude session's running subagents (#467) |
| 2026-10-25 | Decisions 1, 3 and 4: removed the unsourced authorization and peer-text wording; the freshness rule and the checks are stated as what the daemon does; the `koinon-pickup` body loads `skill.pickup` and follows it (#487) |
| 2026-10-26 | Decision 3: a session without a repository keeps its handoff in its registered directory; refused ready replies are kept and named by the ready timeout (#485) |
| 2026-10-26 | Decision 3: the session's work claims last through the cycle (#489) |
| 2026-10-26 | Decision 1: `native_compaction` enters Codex's own `/compact` at the threshold (#509) |
| 2026-10-27 | Decision 1: when a compaction keeps or turns off the session's Enable, approved on the decision board (COMPACT-ENABLE-509) |
| 2026-10-27 | Decision 5: the agy adapter is listed, from its Linux checks with Antigravity CLI 1.4.7 (#512) |
| 2026-10-27 | Executive summary: OpenCode context is read from a launched session's own server (#513) |
| 2026-10-27 | Decision 5: the OpenCode adapter is listed, from its Linux checks with opencode 1.18.35 (#513) |
| 2026-10-27 | Executive summary: DeepSeek context is read from the session's harness (#514) |
| 2026-10-27 | Decision 5: the DeepSeek adapter is listed, from its Linux checks with DeepSeek Harness 0.2.0-rc.2 (#514) |
| 2026-10-27 | Executive summary: Grok context is read from its status line (#515) |
