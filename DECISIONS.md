# Decisions

The maintainer's verdicts on the Koinon decision board, appended by Koinon's `decision_record`
tool. Each entry holds the card text that the maintainer saw when giving the verdict, and the
maintainer's note or feedback, with spelling and typing errors corrected. Entries are only
appended, never changed. Cite an entry as `DECISIONS.md#REFERENCE`; a link's target
is the reference in lower case.

The entries up to and including DECISION-RECORDS-536 were recorded on 2026-10-28, after their
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
