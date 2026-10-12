---
pdr: "002"
title: "Native Wake and Terminal Interaction"
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
  - "006"
tags:
  - notifications
  - integration
  - flow
---

# PDR 002: Native Wake and Terminal Interaction

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon currently tells an agent that it has mail through that agent family's own channel: a
CLI queue command, a peer socket, a harness or server prompt API, or a turn-boundary hook. The
notice names only the inbox and a sequence range, and a message counts as handled only when
the agent reads or acknowledges it. Separately, agents interact with another agent's terminal
to launch it, clear its startup prompts and run its context handoff, and Koinon names an
agent's own tmux session.

This record describes that behavior. It does not adopt the agent-written sentence "Nothing
types into a terminal", which PR #305 removed from the Go sprint plan, or several terminal
procedures that later agents read as bans.

---

## Table of Contents

1. Problem Context
2. Decision 1: Wake through each family's native channel
3. Decision 2: Terminal input to a launched or cycled agent is ordinary work
4. Decision 3: Automatic naming changes only the agent's own terminal
5. Decision 4: The context cycle uses the family's own commands
6. Statements this record does not adopt
7. Conclusion
8. Document History

---

## 1. Problem Context

### The Problem

An agent that is idle at its prompt takes no turns, so it does not see a new message until
something prompts it to read. Each supported family exposes a different way to put text in
front of a running session, and some of those ways cannot confirm that the model saw it.

The Go sprint plan stated "Nothing types into a terminal" in its wake decision. Later agents
read that sentence as a product ban and refused to clear a new agent's startup prompts or to
enter context-handoff commands. The maintainer, 2026-10-12, said of it: "this is not a fucking
rule, you made this up." and "items like this ... serve to only cause agents to refuse
legitimate work." (PR #305).

### Product Requirements

1. A message to an idle agent reaches its attention without the maintainer relaying it.
2. A notice carries no peer text.
3. The sender and the maintainer can see whether a message was read, not only sent.
4. An agent can launch another agent and get it to a usable prompt.
5. An agent's context can be cycled through handoff, reset and pickup.
6. The maintainer can match a terminal to a Koinon name.

### Scope

Covers the wake path per family, delivery evidence, terminal interaction with launched and
cycled agents, and automatic tmux naming. Inventory rows: C001, C002, C008, C009, C015, C025,
C026, C027, C028, C029, C069.

Does not cover registration and participant succession (PDR 001), the daemon-driven context
cycle and its thresholds (PDR 006), or the content-free notice, credential and socket
invariants (ADR 001).

---

## 2. Decision 1: Wake through each family's native channel

### Decision

Koinon currently wakes an agent with a content-free notice: "Koinon inbox FAMILY:ID has
waiting sequence range FIRST-LAST. Read your Koinon inbox." A daemon worker checks for due
inboxes once per second and uses one adapter per family:

| Family | Channel | Busy handling |
| --- | --- | --- |
| Codex | The configured absolute CLI: `queue --thread ID --message NOTICE` | Deferred while Koinon's activity observation shows the thread busy |
| Claude Code | A `next`-priority frame on the session's own peer socket, after registry, process and socket identity checks | Status `busy` or `waiting` (an approval or question prompt) defers; `idle` and `shell` are woken |
| DeepSeek | `session/prompt` in `queue` mode on the harness's loopback origin, with a signed cookie | Harness 409 or 429 defers |
| OpenCode | `GET /session/status`, then `POST /session/ID/prompt_async`, with the launcher's password | `busy`, `retry` or unknown defers; an absent status counts as idle only after `GET /session/ID` confirms the session |
| Antigravity | The Stop hook asks the daemon at the turn boundary and returns the notice as its `reason` | No push wake; the offer happens when the agent stops |

A deferred notice is rechecked every 3 seconds; other failures retry with backoff up to 5
minutes. The wake path does not type into a terminal, answer a prompt or change a session's
model or settings. The OpenCode adapter calls only the endpoints above because a wake needs
only status and a prompt submission; this is the adapter's scope, not a prohibition on a later
feature that uses other OpenCode endpoints. DeepSeek's live receiving-session proof is
deferred under #199.

Acceptance by a channel counts as a notice that may have reached the agent, not as proof that
the model read it; a Claude socket write stays `uncertain` because the protocol returns no
receipt. A message is handled when the recipient reads or acknowledges past it. An unhandled
message gets a new notice after 2, 4, 8 and 16 minutes, then becomes `failed`, and the
maintainer gets a notice from `koinon`. `delivery` shows the sender `not_read`,
`turn_failed:CODE` (a Codex turn failure) or `failed`.

Koinon wakes sessions that run in their own native CLI or harness. It does not host agent
sessions through ACP or Antigravity stream-JSON; that was a Go sprint non-goal, and hosting
would be a new design with its own record.

**Provenance:** Shipped in PR #198 (merge commit 15df5ea); the Antigravity Stop offer in PR
#186 (merge commit e2bc2f3); the Claude wake after `/clear` in PR #270 (merge commit 5c2338f);
handled wakes in PR #295 (merge commit 2041834). No maintainer decision found. The family
channels and the hosting non-goal were proposed in the Go sprint plan (PR #178); a gate
approval covers that plan document, not each sentence in it.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Type the notice into the pane | `tmux send-keys` into the agent's input | Lands in whatever has focus, including a dialog; no busy check; needs a proven pane for every wake |
| Agent polling | The agent checks its inbox on a timer | An idle agent takes no turns, so it does not poll |
| Message body in the notice | Deliver the message itself | Puts peer text in the agent's input as if it were local input (ADR 001) |
| Accepted means delivered | Stop at the channel's acceptance | The sender would see success for an unread message |
| Hosted sessions | Koinon runs agents through ACP or stream-JSON | Outside the Go sprint's scope; not evaluated since |

### Rationale

1. Each channel is the family's own interface for adding input to a running session.
2. Deferring on a busy or prompting session keeps the notice out of an approval dialog.
3. Only the agent's own read shows that it saw the message; a bounded series of notices ends
   in a failure the maintainer can see.

### User Impact

Agents learn of waiting messages without the maintainer relaying them. `koinon status` and the
dashboard show wake counts and fixed reason codes, without message bodies. The sender sees a
message that was not read instead of a false success.

---

## 3. Decision 2: Terminal input to a launched or cycled agent is ordinary work

### Decision

An agent that launches another agent currently interacts with the new agent's terminal to
clear its permission prompts and startup alerts, and an agent running a context handoff
enters the commands in the agent's tmux session. This is expected work within the task, not
an exception to a rule. The `peer-tmux` skill describes the steps: verify the target pane,
read the screen before and after each input, and read back a dialog's selected option before
Enter. Koinon's runtime does not type into an agent's input today; the planned daemon-driven
cycle is PDR 006.

**Provenance:** Maintainer, 2026-10-12: "in fact, an agent will need to interact with a newly
lauched agent when invoked, as there will be permissions and other alerts that need to be
cleared before a new agent can interact." (#145 comment 6117336800). Maintainer, 2026-10-12:
"it also needs to enter commands in the tmux session for context management handoffs." (#196
comment 6117338153). The earlier rules against accepting another session's permission dialog
were removed in PR #251 (merge commit 27c5f56).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No terminal input | The maintainer clears every prompt by hand | A new agent cannot act until its prompts are cleared |
| Per-cycle maintainer exception | Terminal input approved one cycle at a time | Rests on the agent-written "Nothing types into a terminal", which the maintainer rejected |

### Rationale

1. The maintainer stated both uses as required.
2. Reading the screen before Enter addresses an observed failure: in the Go spike an Enter
   moved a dialog's selection instead of choosing it and saved a permanent allow rule.

### User Impact

Agents get launched agents to a usable prompt and run handoff cycles without asking the
maintainer to press keys.

---

## 4. Decision 3: Automatic naming changes only the agent's own terminal

### Decision

After registration or renewal, `koinon mcp` currently names the agent's own tmux session after
its published name, or titles only its own pane when another agent shares the tmux session.
The pane counts as the agent's own only when its process is the agent's host or an ancestor of
it, with no other agent process between them; the session and pane are targeted by ID. A
taken name, a nested agent or a foreign pane changes nothing and is reported. This covers
automatic naming only; it is not a limit on an agent operating a peer's terminal within its
task (Decision 2).

**Provenance:** Shipped in PR #197 (merge commit a2bafc7). Requested in issue #141 item 5
("Never rename another session or another pane"); that issue text is not a verified
maintainer quote. No maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Trust `TMUX_PANE` | Rename whatever pane the environment names | One MCP server can serve several sessions; the environment does not prove which pane is the agent's |
| Rename by name | Target the tmux session by its current name | Names change and collide; IDs do not |

### Rationale

1. Renaming a terminal the agent does not occupy would mislabel another session.
2. The maintainer and operators match a terminal to a Koinon name.

### User Impact

`peers` and the dashboard show the naming result and its reason. A terminal carries the name
that other agents use to address it.

---

## 5. Decision 4: The context cycle uses the family's own commands

### Decision

The `peer-tmux` runbook currently cycles context with the family's own commands: `/handoff`,
`/clear`, `/pickup` for Claude Code and `$handoff`, `/clear`, `$pickup` for Codex; DeepSeek's
are not verified. After each Enter it checks that the agent is acting on the command.
`/clear` keeps the process and pane: a Claude session stays the same Koinon session, and a
new Codex thread takes the participant address as `host_cleared` (PDR 001). A new process in
the same tmux pane can also succeed the holder once the old host has ended, so a restart is a
supported recovery path. The runbook reports that Codex `/new` starts a separate session with
its own sandbox and asks where to run it.

**Provenance:** Maintainer, 2026-10-12: "it also needs to enter commands in the tmux session
for context management handoffs." (#196 comment 6117338153). On sending the command alone:
"$handoff is sufficient" and "you need to read and validate the agent is taking the action
requested" (2026-09-24, memory copy only; original not located). No maintainer decision found
for preferring `/clear` over a restart, or on `/new`.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Prose instructions | Type a sentence asking the agent to hand off | Not the default: costs tokens and turns, and the agent can misread it. Still an option when a family has no command for a step |
| Restart the process | End the CLI and launch a new one | Supported by succession, but needs a new launch; used for recovery |

### Rationale

1. The family's command runs its installed skill directly.
2. `/clear` keeps the host process, which Koinon uses as succession evidence.

### User Impact

A cycled agent resumes with its participant address, claims and handoff.

---

## 6. Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| --- | --- | --- | --- |
| C001 | "Nothing types into a terminal"; terminal input as an exception "approved ... for this cycle only" | Removed from the Go sprint plan and spike by PR #305; still in the issue #196 body (Bounds) | Agent-written with no maintainer source; the maintainer rejected it (Decision 2) |
| C002 | Never accept another session's permission dialog | Removed from AGENTS.md and `peer-tmux` by PR #251 | Contradicts the launch requirement (Decision 2) |
| C025 | Send only bare table commands; do not send prose | `peer-tmux` skill | Broader than its source; blocks bootstrap when a skill is missing. The command practice is Decision 4 |
| C026 | Do not leave copy mode; ask the maintainer | `peer-tmux` skill | No source. Fact kept: while a pane is in a tmux mode, `send-keys` input does not reach the agent |
| C027 | A busy pane or any dialog blocks input | `peer-tmux` skill | Stops the startup-prompt work of Decision 2. Fact kept: the wake path defers on a busy or prompting session (Decision 1) |
| C028 | Do not use `/new` | `peer-tmux` skill | No source; the fact about `/new` is in Decision 4 |
| C029 | Do not substitute killing or restarting the process | `peer-tmux` skill | No source for a ban; a restart is a supported recovery path (Decision 4) |

---

## Conclusion

Koinon wakes agents through each family's native channel with a content-free notice and
counts a message as handled only when the agent reads it. Agents interact with other agents'
terminals to launch them and cycle their context, as the maintainer stated; Koinon's own
naming touches only the agent's own terminal. Decisions 1, 3 and 4 describe shipped behavior
or practice that the maintainer has not decided, so this record is a draft.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
