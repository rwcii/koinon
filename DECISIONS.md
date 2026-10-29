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
