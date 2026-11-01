# Decisions

The maintainer's verdicts on the Koinon decision board, appended by Koinon's `decision_record`
tool. Each entry holds the card text that the maintainer saw when giving the verdict, and the
maintainer's note or feedback, with spelling and typing errors corrected. Entries are only
appended, never changed. Cite an entry as `DECISIONS.md#REFERENCE`; a link's target
is the reference in lower case.

The entries up to and including SUBAGENT-CLEAR-533 were recorded on 2026-10-28, after their
verdicts. Their card text was written for the private board, so each one is a public rewrite,
written once for this record: the verdict was given on the original card text, and the
maintainer's notes are quoted as given (#536).

<a id="493-1"></a>

## 493-1

<!-- koinon-decision 493-1 revision 1 approved 1792979307057 -->

**Approved**, revision 1, 2026-10-26 01:48:27 UTC. Public rewrite of the card.

- Title: Keep or remove the `identity_unavailable` refusal for identity arguments? (#493, item 1)
- Where: `internal/mcp/identity.go`; ADR 001, Decision 1

Context:

> A Koinon MCP tool call fails with `identity_unavailable` when its arguments contain
> `caller`, `family`, `id`, `as`, `session` or `session_id`. The server takes the caller's
> identity from the agent family's own per-call data, for example Codex's thread ID or the
> Claude host process, never from the model's arguments (ADR 001, Decision 1). If a call could
> name an identity, an agent could read another session's inbox or send as that session, also
> by mistake, and messages, claims and cycles would go to the wrong session. Without the
> refusal, tool calls would accept an identity argument in place of the native identity.

Recommendation:

> Keep the refusal. It sends each message, claim and cycle to the session that made the call,
> and it stops no work.

Note:

> keep it

<a id="493-2"></a>

## 493-2

<!-- koinon-decision 493-2 revision 1 approved 1792979679850 -->

**Approved**, revision 1, 2026-10-26 01:54:39 UTC. Public rewrite of the card.

- Title: Keep or remove the `grok_leader_mode` refusal? (#493, item 2)
- Where: `internal/mcp/identity.go` (`errGrokLeader`); #254

Context:

> A Koinon tool call from a Grok session that runs through Grok's leader process fails with
> `grok_leader_mode`; a Grok terminal session in Grok's default mode works. In leader mode the
> MCP server runs with the first terminal's directory and pane, so Koinon would register the
> session with the wrong repository, host and pane, and wakes and cycles would reach another
> terminal (#254). Without the refusal, leader-mode Grok sessions would register with the
> leader's directory and pane.

Recommendation:

> Keep the refusal until leader mode has its own identity source.

<a id="493-3"></a>

## 493-3

<!-- koinon-decision 493-3 revision 1 approved 1792979687118 -->

**Approved**, revision 1, 2026-10-26 01:54:47 UTC. Public rewrite of the card.

- Title: Keep or remove the DeepSeek launch ID and subagent refusal? (#493, item 3)
- Where: DeepSeek registration (PROTOCOL.md, session registration); PDR 001

Context:

> A DeepSeek session registers without a launch ID. A DeepSeek registration that names a
> launch ID, or sets `subagent: true`, is refused. DeepSeek runs through the `koinon` command
> line, not through a launched host that Koinon can verify, so Koinon has no launch or subagent
> evidence for it and accepts no claim of either (#199). Without the refusal, a DeepSeek
> registration could claim a launch ID or subagent status that Koinon cannot verify.

Recommendation:

> Keep the refusal until DeepSeek has a verified launch path (#199).

<a id="493-4"></a>

## 493-4

<!-- koinon-decision 493-4 revision 1 declined 1792979723149 -->

**Declined**, revision 1, 2026-10-26 01:55:23 UTC. Public rewrite of the card.

- Title: Keep or remove the room `exchange_limit`? (#493, item 4)
- Where: rooms; PDR 005 (room allowance)

Context:

> A room starts with no agent posts allowed. Each maintainer post sets the room's allowance to
> the number set in the dashboard (default 32), each agent post uses one, and at zero agent
> posts fail with `exchange_limit` until the maintainer posts again. The rule came from the
> maintainer's choice of 2026-10-12 on #306. Without it, agents can post in a room with no
> bound and without a maintainer post between rounds.

Recommendation:

> Keep the allowance. The verdict declined this recommendation: the allowance is removed.

<a id="493-5"></a>

## 493-5

<!-- koinon-decision 493-5 revision 1 approved 1792979739016 -->

**Approved**, revision 1, 2026-10-26 01:55:39 UTC. Public rewrite of the card.

- Title: Keep or remove the required `peer-review` and `refusal-review` statuses on `develop`? (#493, item 5)
- Where: `develop` branch ruleset; WDR 001, Decision 6; WDR 004, Decision 3

Context:

> A pull request to `develop` merges only when the reviewer's `peer-review` and
> `refusal-review` commit statuses pass at the exact head, together with the CI jobs. The
> refusal review comes from the maintainer's approval of Gate B for #140 on 2026-10-12, which
> asked for "a gate for identifying this same refusal language in future PRs to run alongside
> our code review, security review, and standards review gates" (WDR 001, Decision 6). The peer
> review shipped in PR #262 (WDR 004, Decision 3). Without them, a pull request merges on CI
> alone, without the reviewer's verdict at that head.

Recommendation:

> Keep both statuses. They found two defects in PR #496 before its merge.

<a id="493-6"></a>

## 493-6

<!-- koinon-decision 493-6 revision 1 approved 1792979749593 -->

**Approved**, revision 1, 2026-10-26 01:55:49 UTC. Public rewrite of the card.

- Title: Keep or remove the pre-commit block on `main` and `develop`? (#493, item 6)
- Where: `.githooks/pre-commit`; WDR 004

Context:

> The repository's pre-commit hook stops a commit made directly on `main` or `develop`, so
> work goes on a branch and reaches `develop` through a pull request (WDR 004). The branch
> ruleset would refuse the push of such a commit anyway, so the hook stops the mistake early.
> Without it, a local commit on `develop` or `main` succeeds and its push fails.

Recommendation:

> Keep the hook.

<a id="compact-enable-509"></a>

## COMPACT-ENABLE-509

<!-- koinon-decision COMPACT-ENABLE-509 revision 1 approved 1793059321167 -->

**Approved**, revision 1, 2026-10-27 00:02:01 UTC. Public rewrite of the card.

- Title: Native compaction: when does a compaction turn the session's Enable off? (#509)
- Where: PR #517, `internal/core/context_cycle.go` (`endCycleTx`)

Context:

> PR #517 (#509) makes the daemon enter Codex's `/compact` at the context threshold. The Codex
> reviewer passed it and agreed with these rules, which were agent design until this verdict
> (PDR 006):
>
> 1. A compaction that ends before the daemon enters anything keeps the session's Enable on:
>    for example, the pane is not idle for 15 minutes, the mode or threshold changes, the
>    context reading becomes old, or the pane check fails. The next reading at the threshold
>    can start a new compaction.
> 2. A compaction that ends because the context epoch changed keeps the Enable on.
> 3. A compaction that the daemon sent but cannot confirm in 10 minutes turns the Enable off.
> 4. A compaction whose submission the daemon cannot prove (an Enter without its receipt, or a
>    restart after the intent) turns the Enable off, so the daemon never enters `/compact`
>    twice for one crossing.
>
> After the Enable is off, the session gets no compaction until the maintainer enables it
> again.

Recommendation:

> Approve the rules as PR #517 has them. Rules 3 and 4 prevent a second `/compact` for one
> crossing, also after a restart; rules 1 and 2 let a busy or changed session try again
> without the maintainer.

<a id="pickup-wait-525"></a>

## PICKUP-WAIT-525

<!-- koinon-decision PICKUP-WAIT-525 revision 1 approved 1793110789062 -->

**Approved**, revision 1, 2026-10-27 14:19:49 UTC. Public rewrite of the card.

- Title: Make the pickup deadline longer than 2 minutes? (#525)
- Where: managed context cycle, `waiting_pickup` stage (all families)

Context:

> The managed cycle gave the new session 2 minutes to report its pickup (`waiting_pickup`),
> one of the #196 cycle limits. In the #514 tests with DeepSeek, which runs each pickup step as
> one command, pickups took 81 s, 106 s and 115 s, and one took more than 120 s: that cycle
> failed with `waiting_pickup_timeout` while the new session finished its pickup after the
> cycle ended.

Recommendation:

> Make the `waiting_pickup` limit 5 minutes for all families, and keep the cycle's 30-minute
> limit. A slow pickup then completes, and a pickup that stops still ends the cycle.

<a id="decision-records-536"></a>

## DECISION-RECORDS-536

<!-- koinon-decision DECISION-RECORDS-536 revision 2 approved 1793142710090 -->

**Approved**, revision 2, 2026-10-27 23:11:50 UTC. Public rewrite of the card.

- Title: Record each decision board verdict in `DECISIONS.md`, through MCP? (#536)
- Where: decision board, MCP decision tools, the guide and skills, every adopting repository

Context:

> The maintainer, 2026-10-27: "IF these kind of references will be added, they MUST be
> tracked in repo." Then, on the first proposal (an export command into `.koinon/decisions/`):
> "can this be part of the MCP, where the act of reading the card prompts the agent to record
> it or update the decision record? can we also standardize where the decisions live. I'm
> thinking it should probably be somewhere visible in the repo. not .koinon like I first
> believed. this should be a changelog like append file."
>
> A decision item and its verdicts are kept in the daemon's private store on one machine
> (ADR 001, Decision 2), so a verdict ID does not resolve for another contributor, a clone or
> CI. Koinon's records, commits and pull requests cited verdicts that way. The store keeps,
> for each verdict, the card text that the maintainer saw, the verdict, the note and the time.

Recommendation:

> 1. `DECISIONS.md` at the repository root, beside `CHANGELOG.md`, in every adopting
>    repository; entries are only appended.
> 2. One entry per verdict (approve, decline, and later feedback): the reference, revision,
>    verdict and UTC time, the card text that the maintainer saw, and the note or feedback.
> 3. Reading a verdict through MCP returns the entry and the path; a new `decision_record`
>    tool appends it to the agent's checkout, once per verdict, and the agent commits it with
>    the work.
> 4. Commits, pull requests and documents cite `DECISIONS.md#REFERENCE`, never a verdict ID,
>    and a contributor test checks tracked documents.
> 5. One pull request records the verdicts that Koinon's documents cite and changes those
>    citations.
> 6. A recorded entry is public, so agents write every card as public text.

<a id="work-limit-529"></a>

## WORK-LIMIT-529

<!-- koinon-decision WORK-LIMIT-529 revision 1 approved 1793116785197 -->

**Approved**, revision 1, 2026-10-27 15:59:45 UTC. Public rewrite of the card.

- Title: Let new work items replace old finished items when a store is full? (#529)
- Where: koinon: work items, per-store limit (all repositories)

Context:

> Each repository store can hold 128 work items. A finished item stays for 30 days after it is
> finished, and it counts against the limit for all of that time.
>
> The koinon store had 128 items: 116 finished, 6 open, 5 active and 1 blocked. Thus `work_create`
> was refused with `capacity`. The first finished item would reach 30 days on 2026-11-05. Until
> then, no agent could make a work item for this repository.
>
> Storage is not the cause. The store used 2974 of 128633 pages.
>
> Issue: https://github.com/rwcii/koinon/issues/529

Recommendation:

> When a new item would go past the limit, Koinon removes the oldest finished item first, also
> before its 30 days. Koinon never removes an item that is open, active, blocked or reported. The
> limit of 128 and the storage bounds do not change. A finished item then stays for up to 30 days,
> or until the store needs its space.
>
> If declined: the maintainer says which change to make, for example a higher limit or a shorter
> retention.

<a id="subagent-clear-533"></a>

## SUBAGENT-CLEAR-533

<!-- koinon-decision SUBAGENT-CLEAR-533 revision 1 approved 1793137483202 -->

**Approved**, revision 1, 2026-10-27 21:44:43 UTC. Public rewrite of the card.

- Title: Let the managed cycle clear a Claude session while its background subagents run? (#533)
- Where: koinon: managed context cycle, Claude sessions (all repositories)

Context:

> A Claude session that reports ready while background subagents run is never cleared. Claude Code
> shows "Waiting for N background agents to finish" and its registry status stays `busy`.
> Overwatch reads `busy` as "not idle" and asks for the handoff again. In the #533 cycle this
> occurred 6 times in 14 minutes, and the maintainer ran the cycle by hand.
>
> The request text also tells the session to stop subagents that will not finish before the ready
> deadline. The maintainer said: "context handover won't interrupt the review agents".
>
> Native check on 2026-10-27 (Claude Code 2.1.311, scratch session, no Koinon):
> - In the wait state the input line is empty and the footer has no "esc to interrupt".
> - `/clear` there runs at once. The subagent continues and finishes after the clear.
> - Its result arrives in the new conversation as "Message from @general-purpose". The new session
>   does not know the task unless the handoff names it.
>
> The #456 rule asks again when a subagent works after the ready report, because "when the
> subagent finishes it will have an invalid handoff" (the maintainer, #456). A subagent that still
> runs at the clear sends its result to the new session, so its work is not lost.
>
> Issue: https://github.com/rwcii/koinon/issues/533

Recommendation:

> 1. Overwatch clears a Claude session after its ready report when the pane shows only the
>    background-agent wait, the input line is empty, and the subagent records show running
>    subagents. The `busy` status then does not stop the clear.
> 2. A subagent that still runs at the clear does not cause a new request. A subagent that
>    finished after the ready report still causes a new request (#456), because its result went to
>    the old conversation.
> 3. The request and the guide say: a `/clear` does not stop background subagents. Do not stop
>    them and do not wait for them. In the handoff, list each running subagent with its task and
>    where its result goes. The new session gets each result as a message. The text "stop any that
>    will not finish" is removed.
>
> If declined: the session must stop its subagents or wait for them before the clear, as before.

<a id="board-verdicts-537"></a>

## BOARD-VERDICTS-537

<!-- koinon-decision BOARD-VERDICTS-537 revision 1 approved 1793143269923 -->

**Approved**, revision 1, 2026-10-27 23:21:09 UTC.

- Title: Decision board actions: feedback, acknowledge, and a task kind (#537)
- Where (the item's text when recorded; the verdict's snapshot has none): koinon: decision board, dashboard, maintainer client, MCP decision tools, guide, PDR 007

Context:

> You said (2026-10-27): "the decision board needs an option to approve, decline, or 'provide feedback'. provide feedback providing guidance where approve or decline would be misleading. the FYI also is not very useful as it stands. I get tasks placed here, where I have to 'dismiss' is the only option to close or acknowledge an issue. valid options should be acknowledge. tasks should be in the waiting category with, completed, decline, or 'provide feedback' as options." Issue: https://github.com/rwcii/koinon/issues/537
>
> Your answers in this session (each one is on #537 with its question):
> 1. Feedback returns the card to its author: "agreed, your recommendation is correct".
> 2. A task kind replaces the "ACTION FOR YOU" prefix: "I agree".
> 3. Acknowledge on an fyi tells the author, with no DECISIONS.md entry: "yes".
> 4. The other uses keep their current behavior, and the full design goes on one card: "agreed".
>
> Today: a verdict is approved, declined or dismissed. An fyi takes only dismiss, and a dismissal tells nobody. A task is an fyi whose title starts with "ACTION FOR YOU". The waiting list shows only decision cards.

Recommendation:

> 1. Decision cards: approve, decline, provide feedback.
> 2. Task cards (a new kind): completed, declined, provide feedback. Tasks are in the waiting list. Open fyi cards whose title starts with "ACTION FOR YOU" become tasks, and the prefix is retired.
> 3. Fyi cards: acknowledge.
> 4. Feedback needs text. It does not approve or decline. The card leaves the waiting list and goes back to its author, who changes it into a new revision (back to waiting) or withdraws it. A card can get feedback more than once.
> 5. Every action goes to the card's recipients. Approve, decline, completed, declined and feedback each add a DECISIONS.md entry (#536). Acknowledge adds none.
> 6. No change: notes on approve and decline, agent withdrawal, commands on cards. Old "dismissed" verdicts stay in the history.
> 7. The dashboard, the maintainer client, the MCP tools, the guide, PROTOCOL.md and PDR 007 change together. Tests cover each kind and action and the stale-revision and already-decided refusals.
>
> Order: build it after #536.
>
> If you decline: say what to change.

<a id="cli-rules-550"></a>

## CLI-RULES-550

<!-- koinon-decision CLI-RULES-550 revision 2 approved 1793208842866 -->

**Approved**, revision 2, 2026-10-28 17:34:02 UTC.

- Title: Recommended Claude permission rules cover the agent-facing Koinon CLI
- Where: rwcii/koinon #550: internal/setup/permissions.go (ClaudePermissions), docs/PERMISSIONS.md, guide

Context:

> Issue #550 (filed from the Mac install): each `koinon` CLI call by a Claude agent needs a manual permission prompt. You had to tell the agent to add rules by hand.
>
> Now `koinon install` and `koinon setup claude` on a terminal offer only four rules: `mcp__koinon__*`, `Bash(koinon guide --agent claude)`, `Bash(koinon context show)`, `Bash(koinon status)`. The person answers yes or no; without a terminal nothing changes. The rules use only the PATH name `koinon`, but the installed Claude config calls the absolute binary path, so agents also use that path.
>
> Agents run the CLI when the MCP tools do not work (as in #549) and when a skill says so (for example `koinon decision record`).

Recommendation:

> Approve. The offered rules become, for the PATH name `koinon` and for the installed binary's absolute path:
>
> - `mcp__koinon__*`
> - `koinon guide`, `status`, `context show`, `peers`, `peer-status`, `inbox`, `ack`, `send`, `cycle-status`, `cycle-ready`, `cycle-pickup`
> - `koinon decision` (create, edit, list, get, history, pending, ack, close, withdraw, restore, record)
> - `koinon work` and `koinon claim renew`
> - `koinon memory`
> - `koinon room` (list, members, post, read)
> - `koinon library`
>
> Each with and without its arguments. Left out: `install`, `uninstall`, `upgrade`, `serve`, `setup`, `maintainer`, `dashboard` (it gives a login), `context set/enable/disable/threshold`, `sessions` and `participant holder`.
>
> The offer stays the same: a yes on the terminal adds them; nothing is added without it. `koinon uninstall` leaves rules in place, as now.

<a id="speak-scope-553"></a>

## SPEAK-SCOPE-553

<!-- koinon-decision SPEAK-SCOPE-553 revision 1 approved 1793226390913 -->

**Approved**, revision 1, 2026-10-28 22:26:30 UTC.

- Title: Spoken alert: one switch for all repositories, or one switch for each repository?
- Where: rwcii/koinon #553: dashboard Settings page, app.js

Context:

> Issue #553. You asked: "can we make it distinct per project?" You decided that the speech runs in the browser ("browser").
>
> The alert speaks when a new decision or task starts to wait for you. It says the repository, for example "Koinon: a decision waits for you".
>
> Open question: do you turn the alert on or off with one switch for all repositories, or with one switch for each repository?
>
> Facts:
> - The browser keeps the switch, because the speech runs in that browser. A different browser or computer has its own switch.
> - A browser speaks only after a click on the page. The dashboard reloads the whole page after each action. After a reload, the alert speaks after your next click on the page. The page shows a note when the browser stopped the speech.

Recommendation:

> One switch for all repositories. Each alert says its repository, so the alert is distinct for each project. A switch for each repository adds a list to maintain. Add it later if you want to mute one repository.

Note:

> One switch for all repositories running on that Koinon instance. We may change this later, but let's keep it simple for now.

<a id="card-form-562"></a>

## CARD-FORM-562

<!-- koinon-decision CARD-FORM-562 revision 1 approved 1793251343863 -->

**Approved**, revision 1, 2026-10-29 05:22:23 UTC.

- Title: Put one decision card form in the Koinon guide, and point the card tools to it
- Where: Koinon guide (Decisions section), decision_create and decision_edit, PDR 007

Context:

> - The guide and `decision_create` give no card form today. They say only that card prose is ASD-STE100, and which Markdown shows (`internal/setup/guide.go`, Decisions section; checked 2026-10-29).
> - Card prose in ASD-STE100 is your decision of 2026-10-12, recorded in PDR 007 (#206 comment 6119203856).
> - Your instruction, 2026-10-29, relayed by claude-unify: "we need to add some guidance to agents using koinon, not leave it up to the agent to make it up." (#562; spelling corrected)
> - On 2026-10-29, a card of another repository had long sentences with many facts in each (claude-unify's report, #562).
> - Its later revisions kept the facts but used new headings and list fragments (claude-unify's report, #562).
> - You said of those revisions: "these cards are suddenly dropping long standing norms" and "why did you change the format?" (2026-10-29, in a session, relayed by claude-unify).
> - You said to me, 2026-10-29: "[a card of another repository] is a good example of an acceptable content and formatting." (#562 comment). The form below follows that card.

Recommendation:

> ## Recommendation: put the card form below in the guide's Decisions section, and point decision_create and decision_edit to it
>
> **What:** one pull request in Koinon:
> 1. add the text below to the guide's Decisions section, as "Card form";
> 2. point the `decision_create` description ("Use the card form in koinon guide, Decisions") and the `decision_edit` description ("Keep the card form in koinon guide, Decisions") to it, and docs/USAGE.md too;
> 3. record this verdict in DECISIONS.md, and cite it from PDR 007 and the changelog.
>
> **Why:** the form is new guide text, and this card asks you to decide it. Each agent makes up its own form today, so cards change form from agent to agent and from revision to revision. Every agent family reads the guide (`koinon guide --agent FAMILY`) and sees the tool descriptions when it writes a card. So one text reaches every agent.
>
> **Fast or complete:** complete for the base form. Limits: Koinon does not check the form of a card; the text is guidance, not a refusal. An adopting project can add its own rules. Cost: one pull request, about 20 minutes of CI.
>
> **Corrections from the peers:** claude-unify proposed a catalog skill too; I recommend no skill, because a second copy can drift. codex-koinon: the text does not make a peer's agreement a condition of a card, because not every agent works in a pair; a reviewed card lists the peer's corrections and open disagreements.
>
> **Open point:** the rule that a dropped or deferred goal goes in the heading has no earlier source. It is part of this proposal.
>
> **The text** (the guide, as committed):
>
> ```text
> Card form (#562): bring the maintainer only what needs the maintainer, one card for each
> decision, and wait for its verdict. Give one recommendation, not a menu of options; other
> options can follow at the end, each with the facts to judge it.
> - Title: the action, in one line.
> - Context: a short list of facts. Each item has one fact, its source (a file and line, a
>   record, a pull request or a report) and the date you checked it. When the card changes an
>   earlier direction of the maintainer, the first item names that direction and what changes.
>   Quote the maintainer only from the maintainer's own words.
> - Recommendation: the heading "## Recommendation:" and the action in one line, then in this
>   order: **What:** the concrete change, its source and what gets recorded, with numbered steps
>   when there are steps; **Why:** the defect or the goal, from the records (recommend the fix for
>   the defect, not for its symptom); **Fast or complete:** which one it is, each gap or limit
>   that stays, with its source, and the cost in time, money or CI; then, when they apply, the
>   corrections from a peer who reviewed it, each disagreement that is still open, and each open
>   point with how to handle it.
> A recommendation that drops or defers a goal that the maintainer set says so in its heading,
> and names it as a limit in Fast or complete. ASD-STE100 applies to the sentences: short active
> sentences, one idea each, and common words. The labels and their order are the form, so a
> correction of style changes the sentences and keeps the form.
> ```

<a id="idle-compact-564"></a>

## IDLE-COMPACT-564

<!-- koinon-decision IDLE-COMPACT-564 revision 1 approved 1793271279278 -->

**Approved**, revision 1, 2026-10-29 10:54:39 UTC.

- Title: Let an idle Codex session above its threshold compact without a new turn
- Where: native_compaction trigger (internal/core/context_policy.go), PDR 006

Context:

> - The trigger needs a context reading at most 2 minutes old, and its confirmation at most 90 seconds old. These clocks are from the #196 definition of done, which you approved at Gate B (#196 comment 6136155493; the definition of done at d78822d; `internal/core/context_policy.go:432`; checked 2026-10-29). This card changes how the 2-minute clock applies to an idle Codex session in `native_compaction`.
> - On 2026-10-29 at 05:35 UTC, `native_compaction` was on for each Codex session on this machine, at 60% (`koinon context show`).
> - One idle Codex session was at 68%: its rollout's last `token_count` was at 05:04 UTC, 322443 of 475000 tokens.
> - Its state was `source_stale`, and no compaction started (`koinon context show`, 05:35 UTC).
> - A Codex rollout gets no new `token_count` while the session is idle. So the reading of an idle session is always older than 2 minutes, though it is still its current context.
> - The session's own `koinon mcp` reads its rollout every 5 seconds, and sends an unchanged reading again each minute (`internal/mcp/observe.go:30-31`).
> - A read can stop before the end of the rollout: after one 4 MiB batch, or at a partial last record. It then reports the older reading as current and idle. codex-koinon reproduced both cases with synthetic rollouts (2026-10-29). So a fresh confirmation alone does not show that the reading is current.
> - Filed as #564 (2026-10-29).

Recommendation:

> ## Recommendation: in native_compaction, accept an older reading of an idle Codex session when a caught-up read shows that it is current
>
> **What:** one pull request in Koinon:
> 1. The reader marks a Codex reading "caught up" only when it read the rollout to its end, the last record is complete, and no read failed. It sends the rollout's identity, its size at that end and the last turn's ID with the reading.
> 2. In `native_compaction`, the trigger accepts a reading older than 2 minutes only when its newest confirmation is caught up, at most 90 seconds old, for the same rollout and epoch, and the last turn is complete. A changed, partial or unreadable rollout ends the caught-up state.
> 3. Before Koinon types `/compact`, it needs a new caught-up confirmation, received after its choice, with the same rollout size and turn.
> 4. All other checks stay: the threshold, one compaction for each crossing, an idle and empty prompt in the pane, and a fresh reading after `/compact` to confirm it. The `handoff_cycle` mode does not change.
> 5. Tests: an idle session with a caught-up reading above the threshold compacts. These do not: a busy session; an old confirmation; a reading below the threshold; a read that stopped at a 4 MiB batch with newer records after it; a partial last record; a read failure; another rollout or thread; a new epoch; a daemon or `koinon mcp` restart, until a new caught-up confirmation.
> 6. PDR 006 and docs/USAGE.md give the rule; DECISIONS.md records this verdict.
>
> **Why:** the 2-minute age makes sure that the reading is the current context. For an idle Codex session, a caught-up confirmation shows the same thing: the reader read the whole rollout, and the reading is its last record. So the change keeps the purpose of the rule and removes the gap. Without it, an idle session above the threshold starts its next turn with a full context, and compacts only after that turn.
>
> **Fast or complete:** complete for Codex `native_compaction`. Limit: an idle session compacts within about one minute after it becomes eligible, at the next confirmation. Cost: one pull request. The reading gets new fields, which can need a state schema step. About 20 minutes of CI.
>
> **Corrections from the peers:** codex-koinon showed that a fresh confirmation alone does not prove that the reading is current, because a read can stop before the end of the rollout. The rule now needs a caught-up read, and a new one before the input.
>
> **Open point:** I did not check whether an idle Claude session in `handoff_cycle` has the same gap. I check it during this work, and file it as its own issue if it does.

<a id="claude-idle-566"></a>

## CLAUDE-IDLE-566

<!-- koinon-decision CLAUDE-IDLE-566 revision 1 approved 1793280765119 -->

**Approved**, revision 1, 2026-10-29 13:32:45 UTC.

- Title: Make Koinon's Claude status line run every 30 seconds, so an idle Claude session gets its handoff cycle
- Where: koinon setup claude (internal/setup/statusline.go), PDR 006

Context:

> - The trigger needs a context reading at most 2 minutes old, and its confirmation at most 90 seconds old. These clocks are from the #196 definition of done, which you approved at Gate B (`internal/core/context_policy.go`; checked 2026-10-29). This card does not change them.
> - A Claude session's context comes only from its status line. Koinon's hook gives each reading the time of the hook run (`internal/setup/hooks.go:169`; checked 2026-10-29).
> - Claude Code runs the status line on a new event. So an idle Claude session's reading gets older than 2 minutes, and the trigger shows `source_stale` (PROTOCOL.md, "Session observations"; #566).
> - So an idle Claude session in `handoff_cycle` gets no cycle when its crossing comes while it is idle: after an Enable, a lower threshold or a daemon restart. It starts its next turn with a full context (#566, filed 2026-10-29).
> - Claude Code 2.1.312, the version on this machine, has the setting `statusLine.refreshInterval`: "Re-run the status line command every N seconds in addition to event-driven updates" (its settings schema, read from the installed binary, 2026-10-29). The value must be a number of at least 1; another value counts as unset, and then there is no refresh (the same schema; the Claude Code settings reference, "statusLine").
> - Claude Code added the setting in version 2.1.97: "Added `refreshInterval` status line setting to re-run the status line command every N seconds" (anthropics/claude-code CHANGELOG.md, section 2.1.97, at commit 0e1f478; checked 2026-10-29).
> - You said, 2026-10-29, about an older Claude Code without the setting: "in such a case, we can specify a minimum client version, and move on." (in a session).
> - PDR 006, Decision 5: an adapter runs with any version of its CLI, and Koinon shows no certification status for a version.
> - `koinon setup claude` already writes the `statusLine` entry in the Claude settings. It wraps your status-line command, keeps the entry's other fields, and restores the earlier entry on removal (`internal/setup/statusline.go`).
> - Setup and removal leave a Koinon entry that you edited as it is, and report `statusline_changed` (#530, #532; PROTOCOL.md, the Claude status-line paragraph). This card does not change that rule.

Recommendation:

> ## Recommendation: koinon setup claude sets refreshInterval to 30 seconds in Koinon's status-line entry
>
> **What:** one pull request in Koinon:
> 1. The Claude status-line entry that `koinon setup claude` writes gets `"refreshInterval": 30`. An earlier entry with a valid interval from 1 to 30 seconds keeps its own value. No interval, a longer one, or one that Claude Code does not accept (such as 0, a negative number, a value under 1 or a value that is not a number) becomes 30.
> 2. Removal restores the earlier entry as it was, with its own interval or none, under the existing rule: an entry that you edited stays as it is.
> 3. An existing installation gets the setting at its next `koinon setup claude`, unless you edited Koinon's entry. Then setup reports `statusline_changed`, and you add `"refreshInterval": 30` to your entry yourself. The changelog and docs/USAGE.md give both steps.
> 4. The docs give Claude Code 2.1.97 as the minimum version for the handoff cycle of an idle Claude session. Koinon does not check the version, as PDR 006, Decision 5 says.
> 5. Tests: a new entry; an earlier entry with an interval of 1, of 30, of 120, of 0, a negative one, one under 1 and one that is not a number; the upgrade of an existing Koinon entry; an edited Koinon entry, also one where only the interval changed (`statusline_changed`, kept at setup and at removal); and removal, which restores the earlier entry exactly.
> 6. PDR 006, PROTOCOL.md, docs/USAGE.md, docs/INSTALL.md and the changelog give the rule and the minimum version. DECISIONS.md records this verdict.
>
> **Why:** the defect is that the source of an idle Claude session reports nothing, not the 2-minute clock. With the refresh, Claude Code runs the status line every 30 seconds while the session is idle. Each run is a new reading from Claude Code itself, so the existing clocks admit it and no rule changes. 30 seconds gives a margin of three runs inside the 90-second confirmation age.
>
> **Fast or complete:** complete for Claude Code 2.1.97 and later. Limits:
> - An older Claude Code does not have the setting, so the gap stays there. The docs give the minimum version, as you said.
> - A Koinon entry that you edited keeps the gap until you add the interval yourself, because setup does not change an edited entry.
> - Your status-line command runs every 30 seconds in each Claude session, as well as on events.
> - I checked the setting in the binary, not in a live idle session. The live check is an open point.
> - Cost: one pull request, about 20 minutes of CI.
>
> **Corrections from the peers:** codex-koinon: keep only an interval that Claude Code accepts (1 to 30), because a kept 0 or invalid value turns the refresh off; and name the existing rule for an edited entry, with its upgrade step and limit, instead of saying that every installation gets the setting.
>
> **Open points:**
> - Live check: after the merge, you run `koinon setup claude` on this machine. I then confirm with `koinon context show` that an idle Claude session stays fresh, and report it.
> - agy and Grok sessions in `handoff_cycle` read their context from their status lines too, which also run on events (PROTOCOL.md). They can have the same gap. Grok has its own `refresh_interval` option. I check both and file them as their own issue.

<a id="pulled-idle-569"></a>

## PULLED-IDLE-569

<!-- koinon-decision PULLED-IDLE-569 revision 1 approved 1793290873441 -->

**Approved**, revision 1, 2026-10-29 16:21:13 UTC.

- Title: Let an idle Codex, OpenCode or DeepSeek session above its threshold get its handoff cycle without a new turn
- Where: context trigger (internal/core/context_policy.go), the Codex, OpenCode and DeepSeek context readers, PDR 006

Context:

> - The trigger needs a context reading at most 2 minutes old (`internal/core/context_policy.go`, `contextSourceMaxAge`; checked 2026-10-29).
> - The trigger needs the newest confirmation of that reading at most 90 seconds old (the same file, `contextConfirmationMaxAge`; checked 2026-10-29).
> - Both clocks are from the #196 definition of done, which you approved at Gate B. This card changes how the 2-minute clock applies in `handoff_cycle` to three families.
> - DECISIONS.md#IDLE-COMPACT-564, which you approved on 2026-10-29, accepts an older Codex reading in `native_compaction` only, when its newest confirmation comes from a caught-up read with the last turn ended.
> - Codex: the source time is the rollout's last `token_count`, and an idle session writes none (#564; checked 2026-10-29).
> - Codex: since #564, the session's `koinon mcp` sends the caught-up mark with each confirmation (`internal/mcp/observe.go`; checked 2026-10-29).
> - Codex: the report that clears the mark after a failed read is best effort. A lost report leaves the last mark until its confirmation is 90 seconds old (PROTOCOL.md, "Session observations"; codex-koinon reproduced it at 4ad7bb0, 2026-10-29).
> - OpenCode: every 15 seconds the daemon reads the session's newest messages from its own server, and the source time is the newest finished assistant message's completion time (`internal/core/opencode_context.go`; checked 2026-10-29).
> - OpenCode: the daemon reads the session's activity from `GET /session/status` on the same server (`internal/core/opencode_status.go`; checked 2026-10-29).
> - DeepSeek: every 15 seconds the daemon reads the session's harness, and the source time is the time of the usage event that gave the context pressure (`internal/core/deepseek_context.go`; checked 2026-10-29).
> - DeepSeek: the cycle adapter already has a quiet-point check: no running status, no queued input, no open question, the newest turn closed and no compaction running, in one stable read (`internal/core/cycle_deepseek.go`, `deepSeekQuiet`; checked 2026-10-29).
> - So the reading of an idle session in each of the three families gets older than 2 minutes, though it is still its current context. In `handoff_cycle` the trigger then shows `source_stale`, and the session starts its next turn with a full context (#569, filed 2026-10-29).
> - Each of the three families has a handoff cycle adapter (#196, #513, #514; checked 2026-10-29).
> - The family default is `native_compaction` for Codex and `observe` for OpenCode and DeepSeek (`internal/core/context_policy.go`, `defaultContextPolicy`; checked 2026-10-29). So the gap applies only where you chose `handoff_cycle`.

Recommendation:

> ## Recommendation: in handoff_cycle, accept an older reading of an idle Codex, OpenCode or DeepSeek session when the reader shows that the reading is current
>
> **What:** one pull request in Koinon:
> 1. A confirmation is "current" when its reader shows, at that read, that no newer context exists:
>    - Codex: the caught-up mark of #564, unchanged.
>    - OpenCode: in the same round, the session's newest message is the finished assistant message that gave the reading, and `GET /session/status` for that exact session shows it idle.
>    - DeepSeek: in the same round, the adapter's quiet-point check passes, and the reading's usage event is the newest usage, with no model or context change after it. Events after the closed turn that change none of these do not end the proof.
> 2. In `handoff_cycle`, the trigger accepts a reading older than 2 minutes only when its newest confirmation is current, at most 90 seconds old, of the same epoch, and at the threshold.
> 3. For OpenCode and DeepSeek the daemon reads the source itself, so a failed read or a read without the proof clears the current state at once. For Codex the clearing report stays best effort, as in #564: after a failed read, the last current confirmation can still admit a cycle until it is 90 seconds old.
> 4. All other checks of the handoff cycle stay: the Enable, the pins of host and pane, the request, the ready report, the quiet checks and fences before the clear, and the pickup. `native_compaction` stays as #564 decided, and Codex only.
> 5. Tests, for each family: an idle session with a current reading above the threshold gets its cycle. These do not: a busy session; a newer message, turn or usage after the reading; a source that changes between the reads of one round; a failed read; an old confirmation; a reading below the threshold; a new epoch; a daemon restart, until a new current confirmation. For DeepSeek also: a closed turn followed by events that change nothing still counts.
> 6. PDR 006, PROTOCOL.md and docs/USAGE.md give the rule. DECISIONS.md records this verdict.
>
> **Why:** the 2-minute age makes sure that the reading is the current context. For these sources, a read that shows the session idle with nothing after the reading shows the same thing. So the change keeps the purpose of the rule, as #564 did, and removes the gap. Without it, an idle session above the threshold starts its next turn with a full context.
>
> **Fast or complete:** complete for Codex, OpenCode and DeepSeek in `handoff_cycle`. Limits:
> - Codex: for up to 90 seconds after its rollout becomes unreadable, a cycle can still start from the last current confirmation. Then the session gets a request that it did not need yet. The clear still needs its own ready report, the quiet checks and the fences, so no context is lost.
> - The OpenCode proof comes from the recorded native evidence (OpenCode 1.18.35, Linux, #513). The DeepSeek proof uses the adapter's existing quiet-point check (DeepSeek Harness 0.2.0-rc.2, #514). I do not run the DeepSeek harness on this machine, so its live check runs in its development container, as for #514. Until that run, the DeepSeek idle trigger is unverified.
> - Cost: one pull request, about 30 minutes of CI.
>
> **Corrections from the peers:** codex-koinon: the Codex clearing report is best effort, so the card names the 90-second window and its effect instead of promising an immediate end; the DeepSeek proof uses the quiet-point check, because a closed turn can be followed by events that change nothing; and each context item has one fact, its source and its date.
>
> **Open point:** agy and Grok (#568) need another fix, because their source is a status line. I bring that as its own card.

<a id="grok-idle-568"></a>

## GROK-IDLE-568

<!-- koinon-decision GROK-IDLE-568 revision 1 approved 1793292193629 -->

**Approved**, revision 1, 2026-10-29 16:43:13 UTC.

- Title: Make Koinon's Grok status line run every 30 seconds, so an idle Grok session gets its handoff cycle
- Where: koinon setup grok (internal/setup/grok_status.go), PDR 006

Context:

> - The trigger needs a context reading at most 2 minutes old, and its confirmation at most 90 seconds old (`internal/core/context_policy.go`, `contextSourceMaxAge` and `contextConfirmationMaxAge`; checked 2026-10-29). These clocks are from the #196 definition of done, which you approved at Gate B. This card does not change them.
> - A Grok session's context comes only from its status line, and the source time is the time of the hook run (PROTOCOL.md, the Grok status-line paragraph; checked 2026-10-29).
> - Grok's documentation: "An idle session does not re-run the script. Set `refresh_interval` (seconds) on a `command` row to also run it on a timer." It gives no valid range and no first version with the key (docs.x.ai/build/features/status-line; checked 2026-10-29).
> - The same page says that Grok reads its configuration at startup, so a change to `config.toml` needs a restart of Grok. docs/USAGE.md already says to start Grok again after `koinon setup grok` (checked 2026-10-29).
> - So an idle Grok session's reading gets older than 2 minutes, and the trigger shows `source_stale`. An idle Grok session in `handoff_cycle` gets no cycle when its crossing comes while it is idle, and it starts its next turn with a full context (#568, filed 2026-10-29).
> - `koinon setup grok` owns only `type` and `command` of the `[ui.status_line]` table in Grok's `config.toml`, and keeps `refresh_interval` as it was. It saves the earlier values in `grok-statusline.json` and restores them on removal (PROTOCOL.md, the Grok status-line paragraph; `internal/setup/grok_status.go`; checked 2026-10-29).
> - Setup and removal leave a Koinon entry that you edited as it is, and report `statusline_changed` (PROTOCOL.md, the same paragraph). This card does not change that rule.
> - DECISIONS.md#CLAUDE-IDLE-566, which you approved on 2026-10-29, closed the same gap for Claude with Claude Code's `statusLine.refreshInterval`, 30 seconds, and kept a valid interval of 1 to 30 seconds.
> - Grok runs only in its development container on this machine. Its native evidence is Grok Build 1.0.50 on Linux, and that evidence leaves `refresh_interval` unverified (docs/sprints/2026-10-12-context-handoff/evidence.md, #515).
> - `grok-statusline.json` of an existing installation holds only `type` and `command`, so it has no record of an earlier `refresh_interval` (`internal/setup/grok_status.go`; codex-koinon reproduced it at efdd411, 2026-10-29).

Recommendation:

> ## Recommendation: koinon setup grok also owns refresh_interval and sets it to 30 seconds
>
> **What:** one pull request in Koinon:
> 1. `refresh_interval` becomes an owned key of the `[ui.status_line]` table, with `type` and `command`. Setup writes `refresh_interval = 30`. An earlier integer value from 1 to 30 stays. No value, a longer one, or one that is not an integer of at least 1 becomes 30. This 1-to-30 rule is Koinon's own choice, as in DECISIONS.md#CLAUDE-IDLE-566; it is not a range that Grok documents.
> 2. A new installation saves the earlier `refresh_interval`, as spelled, or its absence, with the other owned keys in `grok-statusline.json`. Removal restores it, or removes the key when there was none, under the existing rule: an entry that you edited stays as it is.
> 3. An existing installation: at its next `koinon setup grok`, setup checks only `type` and `command` against the old record, because the old record owned only these. When they are unchanged, setup saves the `refresh_interval` that is in the file at that time, as spelled, or its absence, as the earlier value for removal, and then applies rule 1. From then on, an edit of only the interval is also `statusline_changed`. When `type` or `command` changed, setup reports `statusline_changed` as now, and you add `refresh_interval = 30` yourself.
> 4. After setup, or after you add the interval yourself, start Grok again: Grok reads its configuration at startup. The changelog and docs/USAGE.md give the steps of rules 3 and 4.
> 5. Tests: a new table; an earlier interval of 1, of 30, of 120, of 0, a negative one, a float and a string; an old record with no interval in the file, with an interval, and with an interval that changed after the earlier setup; setup again after the upgrade, and removal after it; an edited Koinon entry before the upgrade (`type` or `command`) and after it (only the interval), each `statusline_changed`, kept at setup and at removal; and removal, which restores the earlier table exactly.
> 6. Live check before the merge, in the `grok-dev` container as for #515: the branch binary's setup, then a Grok process started after that setup, idle for more than 2 minutes, whose `koinon context show` reading stays inside the existing clocks; `grok inspect` shows no problem with the key. The check proves the value 30. It does not prove which other values Grok accepts.
> 7. PDR 006, PROTOCOL.md, docs/USAGE.md and the changelog give the rule. DECISIONS.md records this verdict.
>
> **Why:** the defect is that the source of an idle Grok session reports nothing, not the 2-minute clock. With the timer, Grok runs the status line every 30 seconds while the session is idle. Each run is a new reading from Grok itself, so the existing clocks admit it and no rule changes. 30 seconds gives a margin of three runs inside the 90-second confirmation age. It is the rule that you approved for Claude.
>
> **Fast or complete:** complete for Grok in `handoff_cycle`. Limits:
> - Until the live check of What 6, it is unverified that Grok Build 1.0.50 accepts `refresh_interval = 30` and runs the status line while idle. The documentation gives no first version with the key; a Grok without it keeps the gap.
> - An existing installation: when you changed the interval after the earlier setup, removal restores the interval that was in the file at the upgrade, not the one from before the earlier setup. The old record has no evidence of the earlier one.
> - A Koinon entry that you edited keeps the gap until you add the interval yourself.
> - A running Grok session keeps the gap until you start it again.
> - Your wrapped status-line command runs every 30 seconds in each Grok session, as well as on events.
> - Cost: one pull request, about 20 minutes of CI, and the container check.
>
> **Corrections from the peers:** codex-koinon: the live check is planned, not done, and the 1-to-30 rule is Koinon's choice, not a Grok range; an old record owns only `type` and `command`, so the upgrade saves the interval that is in the file at that time and the edit check starts there; and Grok needs a restart after a change of its configuration.
>
> **Open point:** agy has no setting that runs its status line while idle (Antigravity CLI 1.4.7; #568). Its fix needs another source and uses the rule of DECISIONS.md#PULLED-IDLE-569, which you approved on 2026-10-29, so I bring it as its own card.

<a id="agy-idle-568"></a>

## AGY-IDLE-568

<!-- koinon-decision AGY-IDLE-568 revision 1 approved 1793295007501 -->

**Approved**, revision 1, 2026-10-29 17:30:07 UTC.

- Title: Document that an idle agy session gets its handoff cycle at its next turn, and track the idle source in a new issue
- Where: PDR 006, PROTOCOL.md, docs/USAGE.md; a new issue for the agy idle source

Context:

> - #568 is done when an idle agy or Grok session in `handoff_cycle` at or above its threshold gets its cycle without a new turn, "or the docs say why it cannot" (#568, "Done when"; checked 2026-10-29).
> - DECISIONS.md#GROK-IDLE-568, which you approved on 2026-10-29, closes the Grok half with Grok's `refresh_interval`.
> - An agy session's context comes only from its status line (`agy_statusline`; PROTOCOL.md, "Session observations"; checked 2026-10-29). Antigravity CLI runs it when its state changes, not while it is idle (#512 evidence). I found no setting in Antigravity CLI 1.4.7 that runs it on a timer (a search of the installed binary, 2026-10-29). So an idle agy session's reading gets older than 2 minutes, and the trigger shows `source_stale`.
> - DECISIONS.md#PULLED-IDLE-569, which you approved on 2026-10-29, admits an older reading only when its reader shows that no newer context exists.
> - Koinon's status-line hook sends each report once, with a one-second timeout and no retry (`internal/setup/hooks.go`; codex-koinon, 2026-10-29). So a turn can happen without a new reading in the daemon, and the last `idle` report alone does not show that the session is still idle.
> - I checked two other native sources on this machine (2026-10-29, keys and counts only, no text):
>   - The conversation's SQLite files. codex-koinon showed with a WAL reproduction that file metadata cannot tell the checkpoint after a turn from a later turn whose reports were lost.
>   - The conversation's `transcript.jsonl`. In 27 files with 538 steps, the steps have `created_at` to the second only (201 adjacent steps share a second), no field for the model or the context window, and no record that a turn ended: a `DONE` model step is followed by tool steps in the same turn 219 times. 9 of 27 files end with a step without token counts. codex-koinon showed that a newer turn with equal counts in the same second gets bound to the old reading.
> - So Koinon has no verified idle source for Antigravity CLI 1.4.7. This does not show that no native source exists.
> - When the idle session starts its next turn, the status line reports again, and the trigger starts the cycle from that fresh reading (PROTOCOL.md, "Session observations"). The session then runs that turn with its full context.

Recommendation:

> ## Recommendation: partial fix of #568 for agy: the docs say that an idle agy session gets its cycle at its next turn, and a new issue keeps the idle goal open
>
> **What:** one pull request in Koinon, plus one new issue:
> 1. PDR 006, PROTOCOL.md and docs/USAGE.md say: in `handoff_cycle`, an idle agy session above its threshold gets its cycle at its next turn, not while it is idle. They give the reason: Koinon has no verified status-line timer for Antigravity CLI 1.4.7 (the inspected binary showed no timer setting, and idle timer behavior is not live-checked), Koinon's hook reports are not retried, and Koinon has no verified native record that shows the session idle with nothing after the reading. The changelog says the same.
> 2. Koinon adds no agy idle reader, and no trigger rule changes for agy.
> 3. I file a new issue, "An idle agy session above its threshold gets its handoff cycle without a new turn", with the facts of this card, the two sources that failed and why, and what would close it: a verified Antigravity status-line timer (the binary search found none; not live-checked), or a native record that ties a reading to a closed turn with its model and window. The docs link to it.
> 4. #568 closes when the Grok PR (DECISIONS.md#GROK-IDLE-568) and this PR are merged; the new issue holds the rest of the agy goal.
> 5. DECISIONS.md records this verdict.
>
> **Why:** a cycle from a reading that may be old can come from wrong counts. The two native sources that I checked do not prove that the reading is current, so a rule built on them could start a cycle from wrong counts, or keep one alive. The docs path is the alternative that #568 gives, and the new issue keeps the goal visible until agy gives a source.
>
> **Fast or complete:** partial. Limits:
> - An idle agy session above its threshold in `handoff_cycle` starts its next turn with its full context, and gets its cycle during that turn.
> - agy's default mode is `observe`, so this applies only where you chose `handoff_cycle` for agy (`internal/core/context_policy.go`, `defaultContextPolicy`).
> - Cost: one documentation pull request, about 20 minutes of CI, and one issue.
>
> **Corrections from the peers:** codex-koinon: file metadata and the transcript do not prove an idle current reading (two reproductions); say that Koinon has no verified idle source for 1.4.7, not that none can exist; say that no timer setting was found, not that none exists; and name this a partial fix with the rest of the goal in a new issue.
>
> **Open point:** none.

<a id="card-labels-582"></a>

## CARD-LABELS-582

<!-- koinon-decision CARD-LABELS-582 revision 1 approved 1793467001178 -->

**Approved**, revision 1, 2026-10-31 17:16:41 UTC.

- Title: Add labels for open disagreements, other options and a separate choice to the guide's card form
- Where: Koinon guide (Decisions, card form), PDR 007, docs/USAGE.md; issue #582

Context:

> - This card changes your card form of 2026-10-29 (DECISIONS.md#CARD-FORM-562). It adds labels after **Fast or complete:** and keeps the rest of the form.
> - The form today says: "other options can follow at the end, each with the facts to judge it", and, after Fast or complete, "the corrections from a peer who reviewed it, each disagreement that is still open, and each open point with how to handle it" (`internal/setup/guide.go`, Decisions; develop 575909d; checked 2026-10-31). These parts have no labels.
> - claude-review-bot asked for the labels through Koinon on 2026-10-31, at your request. It relayed your words about card review-bot-07-policy-first: "I really like the additions ... especially Open disagreements, Other options, and 'A separate choice, which you can add to any option'. it's nice context." (relayed; your verdict on that card has no note).
> - That card uses the labels, and you approved it at revision 5 on 2026-10-31 (decision_get review-bot-07-policy-first, read 2026-10-31).
> - Issue #582 holds the proposed text and the acceptance criteria.

Recommendation:

> ## Recommendation: add the five labelled parts below to the card form, after Fast or complete
>
> **What:** one pull request in Koinon:
> 1. In the guide's card form, after **Fast or complete:**, in this order:
>    - **Corrections from the peer:** when a peer reviewed it, each correction.
>    - **Open disagreements:** "none" when that is true; else each position, with whose it is.
>    - **Other options:** when there are real alternatives, a list. Each says what it keeps and what it costs.
>    - **A separate choice, which you can add to any option:** when a choice is independent of the options. It gives the cost, what the choice does not solve, whether the author recommends it, and the default when you do not take it.
>    - Each open point, with how to handle it.
> 2. The sentence "other options can follow at the end" points to the **Other options:** label.
> 3. PDR 007, docs/USAGE.md and the CHANGELOG match. A guide test checks the labels and their order for every family.
> 4. I record this verdict in DECISIONS.md and cite it from PDR 007 and the changelog.
>
> **Why:** the form names these parts but gives no labels, so each agent writes them in its own way. A label for a separate choice stops an independent choice from looking like a competing option. "none" makes it clear that the author checked for disagreements.
>
> **Fast or complete:** complete for issue #582. **Limits:** the new text reaches agents only after the next release. Cost: one small pull request with its reviews.
>
> **Corrections from the peer:** none yet. codex-koinon reviews the pull request.
>
> **Open disagreements:** none.
>
> **Other options:**
> - Keep the form as it is. It keeps the approved text unchanged. Each agent then keeps its own wording for these parts.

Note:

> add them as standard options, but they don't all need to be present in ALL cards if there is no relevant data.

<a id="cycle-no-new-work-487"></a>

## CYCLE-NO-NEW-WORK-487

<!-- koinon-decision CYCLE-NO-NEW-WORK-487 revision 1 approved 1793490775910 -->

**Approved**, revision 1, 2026-10-31 23:52:55 UTC.

- Title: Put "start no new work" back into the managed cycle request, the guide and the ready reply
- Where: internal/core/context_cycle.go (cycle request, cycleReadyNext), koinon guide (Managed cycle reporting), PDR 006

Context:

> - This card changes part of your direction of 2026-10-25 on #487: "remove all limit text. remove all of the caveats, controls, etc. the agents have their own built in safeties, they do not need you adding more complications."
> - In PR #490 for #487 (commit cd0db4e, released in v0.5.0), I removed "and start no new work" from the cycle request and the guide, and changed the ready reply from "start no other work" to "stay idle" (`git show cd0db4e`, checked 2026-10-31).
> - The request now says only: "Pause your work at the next safe stopping point." (`internal/core/context_cycle.go`, develop 0c3e0a9; checked 2026-10-31).
> - On 2026-10-31 at 05:06 UTC, claude-review-bot read a cycle request and wrote: "I'll post the summary and do the verified merge first, then write the handoff." The merge call waited until 05:23:54, the 15-minute ready deadline passed, and the cycle failed (`waiting_ready_timeout.read`). The failure turned its Enable off (agent transcript and daemon store, read 2026-10-31).
> - The failure at 21:47 UTC had another cause: the request arrived while a merge call was already waiting, so the agent did not read it in time. This change does not help that case.

Recommendation:

> ## Recommendation: put "start no new work" back as a step of the cycle
>
> **What:** one pull request in Koinon:
> 1. The cycle request says again: "Pause your work at the next safe stopping point and start no new work."
> 2. The guide's Managed cycle reporting section says the same.
> 3. The ready reply says: "end your turn now, start no other work and stay idle."
> 4. PDR 006 and the changelog cite this card. A test checks the three texts.
>
> **Why:** the sentence is a step of the cycle, not a safety limit. Without it, an agent can start a new task after the request, as claude-review-bot did at 05:06. The ready deadline then passes, the cycle fails, and you must enable the session again.
>
> **Fast or complete:** fast. **Limits:** it does not help when the request arrives during a command that already waits (the 21:47 case). Cost: one small pull request with its reviews.
>
> **Other options:**
> - Keep the text as it is now. It keeps your #487 direction exactly. An agent can still start new work after a request and let the cycle fail.

Note:

> do the complete/correct fix.

<a id="ready-timeout-enable-487"></a>

## READY-TIMEOUT-ENABLE-487

<!-- koinon-decision READY-TIMEOUT-ENABLE-487 revision 3 approved 1793491044950 -->

**Approved**, revision 3, 2026-10-31 23:57:24 UTC.

- Title: Keep the Enable when a cycle times out before Koinon enters any command in the pane
- Where: internal/core/context_cycle_controller.go (waiting_ready deadline), PDR 006 Decision 2; with CYCLE-NO-NEW-WORK-487 and #584

Context:

> - This card changes your PDR 006 Decision 2 of 2026-10-12 for one case. Today: "A failed or uncertain cycle, or a change of pane or participant, ends the Enable; the maintainer enables again."
> - Your note on CYCLE-NO-NEW-WORK-487 (2026-10-31): "do the complete/correct fix." That card named a case that the text does not fix (DECISIONS.md#CYCLE-NO-NEW-WORK-487).
> - **Correction:** that card said that the request arrived "while a merge call was already waiting". That is wrong. Koinon admitted the cycle and put its request in the inbox at 21:47:12 UTC. The agent was in a turn and did not see the request. It started `gh pr merge` at 21:47:15, the call returned at 22:58:42, and the agent read the request at 22:58:46 (agent transcript and daemon store, read 2026-10-31; codex-koinon found the error).
> - A cycle waits 15 minutes for the ready report and lives at most 30 minutes (`cycleReadyWait`, `cycleLifetime`, `internal/core/context_cycle.go`; checked 2026-10-31). So no request again inside the cycle reaches a session that waits for 71 minutes.
> - The cycle failed with `waiting_ready_timeout.unread`, and the failure turned the Enable off. Koinon had entered nothing in the pane, and the context was unchanged (daemon store, read 2026-10-31).
> - Koinon knows when a session reads its inbox, for every family. For a cycle that failed unread, the reason already records whether the request was read (`cycleRequestRead`, `internal/core/context_cycle_controller.go`; checked 2026-10-31).
> - A new Enable has no effect in the same epoch today (#584, with codex's agreed criteria).

Recommendation:

> ## Recommendation: a cycle that times out before any pane input keeps the Enable once per epoch, and the next cycle starts after the session reads its inbox again
>
> **What:** in the same pull request as CYCLE-NO-NEW-WORK-487:
> 1. When a cycle ends at its ready deadline (`waiting_ready_timeout`), and Koinon entered no command in the pane at any stage of that cycle, the Enable stays on. This happens once in each context epoch. A second such timeout in the same epoch ends the Enable, as Decision 2 says today. When you turn the Enable on again yourself, the session gets this one automatic retry again (#584).
> 2. Koinon starts the next cycle only after the session reads its own inbox after that timeout. This read shows that the agent can see a new request. Then the next fresh reading at or above the threshold starts the cycle. Koinon does not start a cycle while the session has not read its inbox.
> 3. Every other end of a cycle (another failure, a cancel, a disable, a change of pane or participant) keeps today's rule. Only one cycle is active for a session at a time.
> 4. PDR 006 Decision 2, PROTOCOL.md and the changelog say this. Tests: an unread and a read timeout keep the Enable; no new cycle before the inbox read; one new cycle after it; a second timeout in the epoch ends the Enable; your new Enable gives one more retry; a failure after pane input still ends it.
>
> **Why:** in this case Koinon reset nothing, so no Enable carry-over is in question. Turning the Enable off only stops the managed cycles until you notice it.
>
> **Fast or complete:** complete for both cases of CYCLE-NO-NEW-WORK-487. **Limits:** a session that waits for a long time gets its next cycle only after the wait ends and it reads its inbox. Cost: one pull request with its reviews.
>
> **Corrections from the peer:** codex-koinon found the wrong timing above, asked that the next cycle start only on evidence, not on an assumed turn end, and asked that the reset of the retry by your new Enable be on this card. This revision does all three.
>
> **Other options:**
> - Keep Decision 2 as it is. After #584, a new Enable works, but you must notice each failure and enable the session again.
