---
wdr: "004"
title: "Publication and Verification"
status: draft
created: "2026-10-12"
updated: "2026-11-25"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "001"
  - "002"
  - "003"
tags:
  - workflow
  - agentic
  - ci
  - enforcement
---

# WDR 004: Publication and Verification

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

This record describes how a Koinon change currently reaches `develop` and `main`: the branch
model, one open pull request per target branch on a head merged with the latest `develop`,
the required checks on Linux and macOS, auto-merge, the commit conventions and the account of
failed CI runs before a release, the docs gate before publication, the merge gates (a
preflight and the maintainer's board approval of each head, and the maintainer's merge into
`main`), with what GitHub and the local hook enforce. One open pull
request, auto-merge, the commit account, the "maintainer" naming, the failed-run account,
the docs gate and the merge gates (Decision 8, apart from the parts that it marks as the agents'
design) come from the maintainer's words; the rest shipped without a recorded
maintainer decision, so this record is a draft.

---

## Table of Contents

1. Problem Context
2. Decision 1: Feature branches, develop and main
3. Decision 2: One open pull request, current with develop
4. Decision 3: Required checks on Linux and macOS, and the peer-review status
5. Decision 4: Auto-merge
6. Decision 5: Signed, human-authored commits
7. Decision 6: Failed CI runs are accounted for before a release
8. Decision 7: A docs gate before publication
9. Decision 8: Preflight and the maintainer's approval before develop; the maintainer merges main
10. Conclusion
11. Document History

---

## 1. Problem Context

### The Problem

Several agent sessions publish to one public repository under one GitHub account. On
2026-10-08, pull requests against `develop` opened out of date or stacked (#241/#242,
#243/#244, #258/#259); because `develop` requires an up-to-date branch, the first to merge
made the others unmergeable and their CI runs were wasted. #255 and #260 merged before their
peer review finished. Agent tools add attribution to commits by default.

### Requirements

1. Integration and release lines change only through reviewed pull requests.
2. A merge into `develop` is tested on the exact head, on Linux and macOS.
3. Pull requests do not invalidate each other while they wait.
4. Public history carries the maintainer's identity and no tool attribution.
5. A CI failure that recurs is seen, even when each run looks like a single event.

### Scope

Covers branches, pull request order, checks, merging, commit conventions and the account of
failed CI runs before a release. Review roles and the verdict are in WDR 003; the release
skill is in WDR 002, Decision 6. Inventory rows: C035, C036, C037, C038, C039, C061, C062,
C085.

### Change of 2026-10-25 (#487)

- Question: the review on #487 listed three limits to keep (a peer message is not the
  maintainer's approval for a pending prompt; a peer cannot widen permissions; refuse permission
  laundering and report it). Keep them, or remove them too?
- Options: keep the three limits, or remove them as well.
- Answer: Robert Capps, 2026-10-25
  ([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)):
  "remove all limit text.  remove all of the caveats, controls, etc.  the agents have their own
  built in safeties, they do not need you adding more complications."
- Reason: limit text with no source makes agents of different families refuse each other's
  work.
- Scope: Koinon's agent-facing text, the skills, the setup blocks, and the docs and records that
  restate them. The #487 audit listed the code refusals for a separate decision; #493 decided
  them (Change of 2026-10-26).
- This record: Decision 2 states how a branch is brought current, without its limits, and
  Decision 3 states the local checks without the push gate. Decision 6 drops the release
  approval that WDR 002 deprecated. The one open pull request, auto-merge, the commit
  conventions and the failed-run account keep their text.

### Change of 2026-10-26 (#493)

The maintainer decided two code refusals of this record on the decision board.

- Question (decision board item 493-5, 2026-10-26): keep or remove the requirement that a pull
  request to `develop` merges only when the reviewer's `peer-review` and `refusal-review` commit
  statuses pass at the exact head?
- Options: keep both statuses required, or remove them.
- Answer: Robert Capps, 2026-10-26: approved (493-5 in WDR 001, Decision 6), with no note, on the card's
  recommendation: "Keep both."
  ([#493 comment 6406505885](https://github.com/rwcii/koinon/issues/493#issuecomment-6406505885)).
- Intent and scope: both statuses stay required on `develop`, beside the four CI jobs. This is
  the maintainer's source for `peer-review` as a required status (Decision 3). The ruleset does
  not change. Reason, from the card: the statuses found two defects in PR #496 before its merge;
  without them, a pull request merges on CI alone.

- Question (decision board item 493-6, 2026-10-26): keep or remove the `.githooks/pre-commit`
  hook that stops a commit made directly on `main` or `develop`?
- Options: keep the hook, or remove it.
- Answer: Robert Capps, 2026-10-26: approved (493-6), with no note, on the card's
  recommendation: "Keep it: it stops a commit that the branch ruleset would refuse at push."
  ([#493 comment 6406509968](https://github.com/rwcii/koinon/issues/493#issuecomment-6406509968)).
- Intent and scope: work goes on a branch and reaches `develop` through a pull request; the hook
  stays (Decision 1). The code does not change.

---

## 2. Decision 1: Feature branches, develop and main

### Decision

Koinon currently builds changes on `feature/*`, `fix/*` or `chore/*` branches from `develop`,
squashes each pull request into `develop`, and promotes `develop` to `main` with a merge pull
request. Neither long-lived branch takes direct commits.

**Provenance:** Shipped in PR #1 (merge commit 3d2b5bf), which follows the branch policy of
rwcii/engineering-standards; no maintainer decision found. The `pre-commit` block on `main` and
`develop`: Robert Capps, 2026-10-26, kept it (493-6, [#493 comment 6406509968](https://github.com/rwcii/koinon/issues/493#issuecomment-6406509968); Problem
Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Trunk only | Pull requests merge straight to `main` | No integration line between merged work and a release |
| Rebase merges | Keep each branch's commits on `develop` | One squash commit per pull request keeps one change per commit |

### Rationale

1. `main` holds released code; `develop` collects reviewed work until a release is shown to install and upgrade.

### Enforcement

**Gate crossed:** merge-time and agent-time. The active `develop branch policy` and `main
branch policy` rulesets have no bypass actors. Each requires a pull request with resolved
review threads, blocks force pushes (`non_fast_forward`) and deletion, and allows one merge
method: squash on `develop`, merge commit on `main`. Repository settings allow squash and
merge commits, disallow rebase merges and delete merged head branches. `scripts/setup-repo.sh`
writes these settings and sets `core.hooksPath` to `.githooks`, whose `pre-commit` refuses a
commit on a local `main` or `develop`.

---

## 3. Decision 2: One open pull request, current with develop

### Decision

The repository currently keeps one open pull request per target branch, drafts included,
whatever agent family or tool opens it; the next opens after it merges or closes. Sessions
coordinate the publication turn through Koinon, and when a race leaves two open, the later one
closes until the first merges or closes. The author fetches and merges the latest
`origin/develop` into the branch before it pushes or freezes a head (the `ship` facts), so the
reviewed commits stay reachable. When `develop` moves after a verdict, the author merges it and
the reviewer binds the verdict to the new head (WDR 003).

**Before #487:** "Before a new pull request against `develop`, the author tells the other
active sessions through Koinon", "A head is pushed, opened or frozen for review only when it
contains the latest `origin/develop`", and a pushed branch "is not rewritten"; "a branch that
was never pushed may rebase."

**Provenance:** One open pull request: Robert Capps, 2026-10-08, session transcript: "I dont
approve any new prs to develop until the existing PRs are mergeable and merge. stop stacking PRs
based off develop, causing each stacked pr to be unmergeable as soon as the prior merges." and
"I don't understand why opening an additional pr when one or more are already open against the
target branch... ie stacking where the first pr to merge causes all the others to fail merge."
An earlier 2026-10-08 statement, "you keep stacking up prs ... this is wasteful.", is a memory
copy only; original not located. Current with `develop`, merge not rewrite: shipped in PR #260
(merge commit bd9c727) with the one-open rule; the up-to-date ruleset on `develop` shipped in
PR #1 (merge commit 3d2b5bf), and `main` stopped requiring it in PR #212 (merge commit
8e71104); no maintainer decision found. Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Parallel pull requests | Each change opens when ready | Each merge makes the others out of date; they rerun CI and fail to merge |
| Merge queue | GitHub serializes merges | Requires a repository owned by an organization |
| Rebase pushed branches | Force-push a rebased branch | Drops the reviewed commits that a sign-off names |
| GitHub update button | Let GitHub merge `develop` at merge time | The new head has no review and no `peer-review` status |

### Rationale

1. The maintainer named stacked pull requests as the failure and asked for this check before a
   pull request opens.
2. CI and review on an out-of-date head test code that will not be merged.

### Enforcement

**Gate crossed:** merge-time for the up-to-date rule. The `develop` ruleset is strict
(`strict_required_status_checks_policy`): a pull request merges when its branch is up to date
with `develop`. `main` is not strict, because its earlier merge commits never reach `develop`.
The `check` skill fetches the base and merges it when the branch is behind. The one open pull
request (the `ship` facts, `CONTRIBUTING.md`) and the merge of `develop` into a published branch
are practice.

---

## 4. Decision 3: Required checks on Linux and macOS, and the peer-review status

### Decision

Koinon currently supports Linux and macOS, with operating-system differences in
`internal/platform`. The `develop` ruleset requires these checks to pass on the head commit: on
both systems, `go vet ./...`, `go test -race -timeout 30m ./...` and the four `CGO_ENABLED=0` builds
(linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) and the native service lifecycle job under
systemd and launchd; on Linux, the `decision-citations` check of the pull request text (WDR 001,
Decision 7); and the reviewer's `peer-review` and `refusal-review` commit statuses. The verdict is a pull
request comment bound to the head commit, because every agent acts under one GitHub account
and an author cannot approve its own pull request (WDR 003). The `check` skill runs the suite
locally with `git diff --check`, shell syntax checks for shell edits, the changelog rule and a
private-content scan.

**Before #487:** the `check` skill ran these checks "Before a push".

**Provenance:** macOS support and the macOS CI runner shipped in PR #8 (merge commit 4456efe);
Robert Capps, 2026-09-18: "MacOS Support is not optional" (memory copy only; original not
located). The Go and native lifecycle checks shipped in PR #207 (merge commit 10ea1e4) and the
`peer-review` status in PR #262 (merge commit ce75054). The `peer-review` status as a required
check: Robert Capps, 2026-10-26, kept it (493-5 in WDR 001, Decision 6,
[#493 comment 6406505885](https://github.com/rwcii/koinon/issues/493#issuecomment-6406505885); Problem Context). Changed by #487 (Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Local checks only | Trust the author's reported results | No evidence on the exact head, and no macOS run on a Linux host |
| Linux in CI, macOS by hand | Test macOS only before a release | Platform defects reach `develop` unseen |
| Approving review count | Require one GitHub approval | Impossible under one account; the ruleset sets the count to 0 |
| Skip CI for agent files | Run nothing for non-runtime changes | Contributor tests check the shared skills and layout, so the Go suite runs on every change |

### Rationale

1. Required checks bind the evidence to the commit that merges.
2. The product runs under systemd on Linux and launchd on macOS; each needs its own evidence.
3. The `peer-review` status lets GitHub see a missing sign-off, which it could not before #262.

### Enforcement

**Gate crossed:** merge-time. `tests.yml` runs the `go` job on `ubuntu-latest` and
`macos-latest` for every pull request. `native-go-lifecycle.yml` runs `go-lifecycle` with
`KOINON_NATIVE_JOB=1` on disposable runners; for a pull request that changes only
`docs/sprints/`, `.claude/`, `.agents/` or `.codex/`, the jobs still report but skip the
service steps. The `develop` ruleset requires `go (ubuntu-latest)`, `go (macos-latest)`,
`go-lifecycle (ubuntu-latest, systemd)`, `go-lifecycle (macos-latest, launchd)`,
`peer-review` and `refusal-review` (WDR 001, Decision 6), `decision-citations` (#536), and,
after the maintainer applies `scripts/setup-repo.sh`, `preflight` and `board-verdict`
(Decision 8); `main` requires the first four. `release.yml` builds the four binaries and
checksums for a version tag.

---

## 5. Decision 4: Auto-merge

### Decision

Koinon pull requests into `develop` currently set squash auto-merge. GitHub
merges the pull request once the ruleset conditions pass, including the `peer-review` status
on the head, so auto-merge does not bypass review or checks.

**Provenance:** Robert Capps, 2026-10-11, session transcript: "all prs need to set automerge,
and you and your peer should continue work on subsequent issues until we have closed all open
issues, or you need my guidance." The words were given for the work through the open issues.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Manual merge | An agent runs `gh pr merge` after the checks | A green pull request waits for a session to return |

### Rationale

1. The maintainer asked for it; the rulesets keep the same gate either way.

### Trade-offs Accepted

GitHub creates and signs the squash commit of an auto-merge and takes its author address from
the account's settings, not from an `--author-email` that a manual merge passes. The `ship`
facts use squash auto-merge for every pull request into `develop`.

---

## 6. Decision 5: Signed, human-authored commits

### Decision

Koinon commits are currently signed (`git commit -S`), carry a DCO `Signed-off-by` line
(`-s`), and are authored under the maintainer's GitHub identity with the GitHub no-reply
address. Commits and pull requests carry no co-author trailer, tool signature or model name.
Git-related prose and internal documents refer to Robert as "the maintainer", not as "the
user". A decision record names the person who decided by name, Robert Capps (WDR 001, Decision 7;
Robert Capps, 2026-11-05: decisions "should clearly indicate who made the decision (name of the
author, not 'maintainer' or 'owner')").

**Provenance:** Robert Capps, 2026-09-22, session transcript: "you will also commit only under my
github username, never claude." and "I DO NOT TOLERATE ANTHROPIC SPAM". Robert Capps,
2026-10-07, session transcript: "please stop using the term "user" when referring to me.  I am
the "maintainer"" and "this goes for all git related prose, and internal docs" (shipped in PR
#201, merge commit 5121d87). Signing and the no-reply address: Robert Capps, 2026-09-18: "this is
a public repo, so we commit with the rwcii noreply ... commits get signed" (memory copy only;
original not located). The DCO sign-off shipped in PR #1 (merge commit 3d2b5bf); no separate
maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Tool attribution | Keep the agents' default co-author trailers | The maintainer rejected it |
| Separate agent accounts | Commit as each agent family | The maintainer asked for commits under the maintainer's username |
| "The user" | Agents' default term for the maintainer | The maintainer rejected it |

### Rationale

1. Signature, DCO certification and authorship are separate facts; each is kept.
2. The account, attribution and naming choices are the maintainer's.

### Enforcement

Practice only: the pull request template checklist and the `ship` skill. No ruleset requires
signed commits and no CI job checks the DCO line.

---

## 7. Decision 6: Failed CI runs are accounted for before a release

### Decision

The promotion pull request lists every failed CI run since the released baseline, so that a failure that recurs is seen and given a cause. The
scope is each failed run and each failed attempt of the repository's workflows from the
baseline release tag's commit to the candidate's exact-head CI, on all branches: also deleted
pull request branches, and a failure that a rerun later turned green. Each entry has the run
and attempt, the commit, the job and platform, the cause with its evidence (else "unknown"),
and the pull request that fixed it or the open issue. An open issue records a fault that is
not fixed; it does not replace the exact-candidate checks (Decision 3). The `release` skill
accounts for failed runs in the scope that the project's Release checks define; Koinon's
release facts name the source.

**Provenance:** Robert Capps, 2026-10-24, session transcript, after the `main` pushes for the
v0.4.1 preparation and for v0.4.2 each had a failed macOS run: "why do we have CI tests
constantly failing on nearly every CI run?", then "see main". Of the 120 runs
since 2026-10-21, 6 had failed; the two that were not yet fixed were test timing faults (#475).
Robert Capps, 2026-10-25, session transcript, to the question "Should I add the step in gap 1 to
the `release` skill? I recommend yes.", where gap 1 was that no step measures accepted work, from
a review of an article on agent harness setup: "yes, also ask your claude peer to review the
article and weigh in on any required changes, come to me with a mutually agreed upon proposal".
The jointly reviewed proposal (scope, entries, location, the issue limit, the portable skill
sentence and the facts), to the question "Do you approve this proposal?": "yes". #487 removed
the words "Before the maintainer's release approval" from the first sentence, with the release
approval (WDR 002, Decision 6).

**Rerun of jobs that no runner acquired (#630):** a GitHub-hosted macOS job can wait for a
runner until GitHub cancels it, with the annotation "The job was not acquired by Runner of type
hosted even after multiple attempts". The run then fails though no step ran. The v0.10.0
account listed three such runs on 2026-11-05, after others on 2026-10-21 and 2026-11-03. The
workflow `rerun-unacquired.yml` runs `scripts/rerun-unacquired.sh` after each failed Tests,
Native Go lifecycle evidence or Release binaries run, from the default branch's own checkout.
Release binaries was added by #729: in the v0.12.0 release run, attempts 1 and 2 of the macOS
Homebrew check got no runner, so nothing was published until a manual rerun. A rerun of a tag
run repeats only its failed jobs and the jobs that wait for them, so publication runs once. It
reruns the failed jobs of the attempt, at most twice (attempts 1 and 2), only when every failed job has runner
ID 0, no step that ran and that exact annotation, on every page, and the run is still that
attempt, completed with failure. It does not rerun a run that has another failure, so that
failure stays visible; a failed API call means no rerun. Each failed attempt stays in the account above.
GitHub starts `workflow_run` workflows from the default branch, so the rerun works after the
workflow is on `main`; the Release binaries entry works from the first promotion after #729. `TestRerunUnacquired` in `internal/contributor` covers the decision.

- Decided by: Robert Capps, 2026-11-06, in a Claude Code terminal session, while the v0.10.0
  release was prepared: "fix the repeated CI issues in 0.10".
- Context: the failed-run account since v0.9.2 had five cancellations by a newer push, which the
  workflows' concurrency does by design, and three runs whose macOS job no runner acquired. The
  macOS test failures since 2026-10-20 were fixed, and none recurred after 2026-10-28.
- Reason and scope: the unacquired job is a GitHub capacity fault, not a test result. The rerun
  covers only that fault, in the Tests and Native Go lifecycle evidence workflows, and since
  #729 also in Release binaries, the same fault in the release run. A test failure, a timeout
  and a cancellation by a newer push are not rerun.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Each failure on its own | Fix or rerun a failed run when it is seen | The macOS timing failures recurred for days; each run looked like one event |
| Rerun every failed run | Rerun any failed Tests run automatically | It also reruns test failures, so a recurring fault is read as noise (#630) |
| Latest attempt only | List the runs whose final result is failure | A failure that a rerun turned green is not seen |
| More measures | Also record usage with retries, review time and corrections per task | Not requested; an optional later test on comparable accepted tasks |

### Rationale

1. The release is where the maintainer reads the evidence for the whole period.
2. A cause with evidence, a fix or an issue keeps a recurring failure from being read as noise.

### Enforcement

Practice only: the `release` skill and the promotion pull request evidence. No CI job checks the
list. The rerun of unacquired jobs is the `rerun-unacquired.yml` workflow.

---

## 8. Decision 7: A docs gate before publication

### Decision

Before a change is published, its author compares the project docs and the agent files with
the code that will ship. The `preflight` and `ship` skills run this docs gate before the head is
frozen and before the other checks: the author lists the user-visible changes against the
base, reads each listed file at the head (first the parts that describe a changed item, then
the rest), corrects each statement that the code contradicts, describes each new user-visible
feature, makes the versions, release names and skill lists agree with the code, and commits the
corrections in the same change. The `release` skill runs the same gate on the candidate, with
the released baseline as its base, and merges its corrections before the promotion. The
optional repository fact "Docs and agent files" names the files; its default is the README, the
user docs, the agent instruction files and the project's skills. Koinon's
`.koinon/skills/ship.md` and `.koinon/skills/release.md` name Koinon's own files.

- Decided by: Robert Capps, 2026-11-23, in a Claude Code terminal session: "can you validate
  that the main readme and other in-repo docs are consistent with the current repo. the main
  readme is out of sync with current features. a few more fixes, the preflight and ship should
  include a gate to validate that the project docs and agents files are all up to date with the
  release version of the code". The first change put the gate in `preflight` and `ship` only;
  to the question whether the `release` skill should run the docs gate too, Robert Capps,
  2026-11-23: "yes, add the docs gate to release too".
- Context: the README did not describe rooms, the decision board, context cycles, the skills
  catalog or signed answers, and cited a closed issue and a deprecated decision (#791). No
  step of `check`, `preflight`, `ship` or `release` compared the docs with the code.
- Reason and scope: a docs fix in the same change keeps the docs and the code in step, so the
  docs on develop match the code that the next release publishes; the release gate checks the
  whole candidate against the version that it publishes. The gate is a step of the author; it does not replace the independent reviews. A
  historical document that says so is out of scope.
- Review when: a deterministic check can compare a doc with the code, for example the skill
  lists with the catalog.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Release only | Check the docs once, before each release | A change that ships without its docs leaves them wrong on develop until the release |
| Delta only | Read only the files that describe a changed item | A statement that an earlier change made wrong stays wrong |
| A deterministic check only | A contributor test compares the docs with the code | It covers lists and names, not the description of a feature |
| No gate | Fix the docs when a reader finds a gap | The README went out of sync over several releases (#791) |

### Rationale

1. The author of a change knows which docs it affects.
2. A read of every listed file at each change finds what an earlier change missed.
3. The release gate finds what the changes since the released baseline missed together.

### Enforcement

Practice only: the `preflight`, `ship` and `release` skills and their review evidence. No CI
job checks the docs.

---

## 9. Decision 8: Preflight and the maintainer's approval before develop; the maintainer merges main

### Decision

For a project whose facts opt in, as Koinon's do (the opt-in facts, the revision check, the
card's Where target and content, and the worker and publish details are the agents' design,
below):

- Before a pull request into `develop` opens, the author runs the `preflight` skill at the
  frozen head with the lenses security, code, standards, refusal and wording, each in a fresh
  claude worker with no forge credential (`scripts/preflight-run.sh`). The lens policy comes
  from the pull request's base; the change that adopts preflight runs with its own head's
  policy, because its base has none. After the pull request opens,
  `scripts/preflight-publish.sh` posts one result comment and the `preflight` commit status on
  that head.
- The `preflight` status is `success` only when all lenses approve, and `failure` on a
  REJECT or a failed lens. It is a required status: a pull request merges only with a passing
  preflight on its head.
- After `peer-review` and `refusal-review` pass, the author puts a card on the decision board
  whose Where field is exactly the target `OWNER/REPO#PR@HEAD`, with the full head, and whose
  context gives the preflight result and each open preflight finding with what the author did
  about it. After the maintainer approves it, `scripts/board-verdict.sh` reads the card, checks
  that the approval is for the card's current revision and that its approved Where field is the
  target of this repository, pull request and head, checks the pull request's head, and posts
  the `board-verdict` commit status. A new head needs a new card or revision. The required
  status holds the merge, so the author can set auto-merge before it posts.
- The `release` skill stops when the promotion pull request and its evidence are ready and
  reports to the maintainer. The maintainer merges into `main`; the agent then pushes the tag
  and checks the publish steps. This partly restores the maintainer's gate on each release that
  WDR 002, Decision 6 recorded before #487, and changes that Decision for Koinon.
- The skills take the gates from the optional facts "Merge approval" (`ship`, default "none")
  and "Promotion merge" (`release`, default "the promotion policy"). A project without them
  keeps its steps. Koinon's `.koinon/skills/ship.md` and `release.md` opt in, and Koinon adopts
  `preflight` (PDR 008, Decision 5).
- `scripts/setup-repo.sh` declares `preflight` and `board-verdict` as required statuses of the
  `develop` ruleset. The maintainer applies the live ruleset after this change merges.

- **Decided by:** Robert Capps. **Date:** 2026-11-23, terminal.
- **Context:** the Orca integration spec (#795), section 8, restores approval gates: a board
  verdict on the exact reviewed head before a merge to `develop`, and a human merge to `main`.
  The agents work under the maintainer's GitHub account, so GitHub cannot tell an agent from
  the maintainer. The options were skill rules only; a required status from a GitHub App that
  only the fleet board holds; and a separate GitHub identity for the agents.
- **Decision:** asked "Do you want the preflight required on `develop`?", with the lenses
  security, code and standards proposed, the maintainer added the refusal lens: "sorry, I meant
  security, code, standards, refusal for preflight". The source gives no further reason; these
  four lenses were Koinon's first lens policy. Asked whether the agents get their own GitHub
  identity (the third option): "they do not. that was a DISASTER when we tried to implement
  it on unify." The threat model: "An agent bypassing a gate and merging is not a threat
  model we're trying to solve for at this point." Then "I approve your non blocking controls
  proposed": the required `preflight` and `board-verdict` statuses, and a `release` skill that
  stops before the merge to `main`.
- **Reason and scope:** the gates are process rules that GitHub shows as statuses; they do not
  stop an agent that goes around them. The ruleset change is a repository setting, applied with
  the maintainer's direct go.
- **The agents' design, reviewed in the plan and the pull request (#795), not the maintainer's
  words:**
  - Opt-in facts: the gates live in the optional facts "Merge approval" and "Promotion merge",
    so a project without them keeps its steps (the plan review's refusal finding).
  - The revision check: `board-verdict.sh` takes only an approval of the card's current
    revision, so a new head needs a new card or revision.
  - The card's Where target: the approved Where field must be exactly `OWNER/REPO#PR@HEAD`, so
    an approval names one repository, pull request and head; a Where field with more text is
    refused (the Codex peer review of #795).
  - The card's content: its context gives the preflight result and each open preflight finding
    with what the author did about it (the author's rule).
  - The workers: the `preflight` skill asks for a fresh worker with no forge credential. A
    report is posted in public, and the reviewed change can hold instructions for its reviewer,
    so the workers also write nothing and use no network. The worker is `claude -p` for an
    author of any family: it has only the Read, Grep and Glob tools, loads no settings,
    auto-memory or instruction files, and reads only its worktree: in the default permission
    mode, with no settings that allow more, Claude Code asks before a read outside the working
    directory, and a `-p` run has nobody to answer, so the read is denied (probed 2026-11-23:
    `/etc/hostname`, the home directory and the parent folder were denied). A test checks the
    worker's options. The author reads each report before publishing it. A `codex exec` worker
    is not used, because Codex's read-only sandbox can read files outside the worktree.
  - The publish checks: Claude Code gives a worker the account's email address, so the publish
    script refuses a report with a secret, any path under `/home`, `/var/home` or `/Users`
    (placeholder paths included) or under the running user's home, or an email-shaped string,
    synthetic ones included; the author then edits the report. It posts each report as a fenced
    block, and refuses a result run against another base than the pull request's, because the
    lens policy comes from that base.
  - The first-adoption exception: the change that adopts preflight has a base with no lens
    policy, so its run takes the head's policy and says so in its result.

- **Changed on 2026-11-23, decision board (Robert Capps), and superseded on 2026-11-24.**
  - 2026-11-23: the board recorded the maintainer's approval of a card that kept the
    `preflight` status true (`failure` on a REJECT) and dropped `preflight` from the required
    checks of `develop`. Its first revision would have posted `success` for a run with a
    REJECT; the maintainer's feedback on it: "why would we care that it ran and not that it
    failed?" The second revision, with both parts, was approved with no note.
  - 2026-11-24, terminal: the maintainer, on the text "preflight shows its true result and is
    not required": "this is not correct. preflight is required, passing is required." This
    supersedes the "not required" part. The true status stays.
  - Decision now: `preflight` is a required status of `develop`, and only `success`, when all
    lenses approve, lets a pull request merge.
  - Reason and scope: the source gives no further reason. Scope: every pull request into
    `develop`.

- **Changed on 2026-11-24: the wording lens (#812).**
  - **Decided by:** Robert Capps. **Date:** 2026-11-24. The request was given in another
    project's terminal and relayed by that project's agent; the separate lens was given in
    Koinon's terminal; the approval and its note were given on the decision board.
  - **Context:** agents chose the most alarming accurate-looking words for approved work, such
    as "profile", "tracking" and "fingerprint" where "visitor ID", "visit" and "site data" are
    just as accurate. The maintainer, in the other project's terminal: "this kind of case
    needs to find its way into the refusal review. this behavior is an example of latent
    language choice that causes alarm, mistrust, or outright refusal. ... this check needs to
    be worked into the preflight check on all PRs to develop." The options were a new finding
    kind in the refusal review and its preflight lens, or a separate preflight lens.
  - **Decision:** a separate lens, in Koinon's terminal: "I feel like this should be separate
    from the refusal review, but still part of the preflight review. this is focused on user
    facing content, or usable accessible data (cookies would be a good example of this).
    refusal review is really focused on code comments, README files, agent files, and other
    repo documentation." On the decision board, the card for a fifth preflight lens,
    `wording`, shipped in the `preflight` skill as a standard lens, was approved with the note
    "add this to the v0.13.4 release".
  - **Reason and scope:** the approved card's text, written by the author from the request
    as that project's agent relayed it, not the maintainer's words. Reason: "alarming wording
    in user-facing content primes the reader, a person or an agent, toward mistrust, extra
    approval requests or refusal of approved work, with no source rule". Scope: "UI and
    dashboard text, CLI output and messages, error text and website text. Data that users can
    read: cookie names, local storage keys, event and property names, headers, and
    identifiers in exported or published data." Test: "the wording is accurate and neutral. A
    neutral word must not hide what the thing does. A replacement that misleads is a finding
    too." Exempt: "settled policy texts and fixed third-party API names". "Refusal review does
    not change."
  - **The agents' design:** the lens text lives once, in the `preflight` skill (Standard
    lenses), so each project takes the same text from the catalog; a lens section with no text
    in a project's lens policy turns it on, and `scripts/preflight-run.sh` reads it from the
    skill at the policy's commit. A lens with neither text nor a standard lens stops the run
    before any worker starts, so no lens approves with an empty brief.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| The wording check in the refusal review | A new refusal finding kind for alarming wording | "this should be separate from the refusal review, but still part of the preflight review" |
| A GitHub App status | Only the fleet board holds the App key that posts `board-verdict` | Guards against an agent that goes around the gate, which is not the threat model |
| A worker of the author's family | A Codex author runs `codex exec` in its read-only sandbox | That sandbox can read files outside the worktree, and the report is posted in public (agents' design) |
| A separate agent identity | The agents work under their own GitHub account | "that was a DISASTER when we tried to implement it on unify" |
| A `preflight` that is not required | GitHub shows `preflight`, and only `board-verdict` holds the merge | "preflight is required, passing is required." |
| A `preflight` that reports only that it ran | `success` when every lens ran, also with a REJECT | "why would we care that it ran and not that it failed?" |
| Skill rules only | The skills say what to do, with no status | GitHub shows nothing, and a merge without the approval is not visible on the pull request |

### Rationale

1. The maintainer approves each change into `develop` on the board, with its exact head.
2. The statuses show on the pull request which gates passed, for that head.
3. Each lens of the preflight sees the change in a fresh context, before the independent
   reviews.

### Enforcement

The `develop` ruleset requires `preflight` and `board-verdict` after the maintainer applies
`scripts/setup-repo.sh`. The scripts refuse a
status for another head. Contributor tests (`internal/contributor/merge_gates_test.go`) run the
scripts with fake `gh`, `koinon` and agent workers, and check that no skill tells an agent to
merge into `main`.

---

## Conclusion

Koinon currently publishes through one squash pull request at a time into `develop`, on a head
merged with the latest `develop`, with the Linux and macOS checks, the `decision-citations`
check and commit-bound `peer-review` and `refusal-review` statuses required, and auto-merge set; a release lists the failed CI runs since the
last one, and each change and release passes the docs gate. GitHub rulesets enforce the branch,
check and up-to-date rules; one open pull request, the merge of `develop` into a published
branch, signing and DCO are practice. The pull request order, auto-merge, commit identity,
naming, the failed-run account, the docs gate and the merge gates are the maintainer's choices,
apart from the parts that Decision 8 marks as the agents' design; the rest waits for the
maintainer's decision. Each pull request into `develop` has a passing preflight and the
maintainer's board approval of its head, and the maintainer merges each release into
`main`.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | Decision 6: failed CI runs are accounted for before a release |
| 2026-10-25 | #487: Decision 2 states the branch update without its limits; Decision 3 drops the push gate; Decision 6 drops the release approval |
| 2026-10-26 | #493: Decision 3 cites the maintainer's verdict that keeps the required `peer-review` and `refusal-review` statuses (493-5 in WDR 001, Decision 6); Decision 1 cites the verdict that keeps the `pre-commit` block (493-6) |
| 2026-11-06 | Decision 6: the rerun of CI jobs that no runner acquired (#630) |
| 2026-11-23 | Decision 7: a docs gate in `preflight` and `ship` (#792) |
| 2026-11-23 | Decision 7: the docs gate in `release` too |
| 2026-11-23 | Decision 8: preflight, the board approval of each head, and the maintainer's merge into `main` (#795) |
| 2026-11-24 | Decision 8: a passing preflight is required; the card's Where target and content are the agents' design; the release bullet cites WDR 002, Decision 6, which it partly restores (#795) |
| 2026-11-24 | Decision 8: the wording lens (#812) |
| 2026-11-25 | Decision 3 and the Conclusion: the required `decision-citations` check and `refusal-review` status, and the lifecycle exemption without `agents/`, as `scripts/setup-repo.sh` and the workflows have them |
