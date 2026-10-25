---
wdr: "004"
title: "Publication and Verification"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
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
model, one open pull request per target branch on a head that contains the latest `develop`,
the required checks on Linux and macOS, auto-merge, the commit conventions and the account of
failed CI runs before a release, with what GitHub and the local hook enforce. One open pull
request, auto-merge, the commit account, the "maintainer" naming and the failed-run account
come from the maintainer's words; the rest shipped without a recorded
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
8. Conclusion
9. Document History

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
failed CI runs before a release. Review roles and
the sign-off are in WDR 003; the release approval is in WDR 002. Inventory rows: C035, C036,
C037, C038, C039, C061, C062, C085.

---

## 2. Decision 1: Feature branches, develop and main

### Decision

Koinon currently builds changes on `feature/*`, `fix/*` or `chore/*` branches from `develop`,
squashes each pull request into `develop`, and promotes `develop` to `main` with a merge pull
request. Neither long-lived branch takes direct commits.

**Provenance:** Shipped in PR #1 (merge commit 3d2b5bf), which follows the branch policy of
rwcii/engineering-standards; no maintainer decision found.

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
whatever agent family or tool opens it; the next opens after it merges or closes. Before a new
pull request against `develop`, the author tells the other active sessions through Koinon, and
when a race leaves two open, the later one closes until the first merges or closes. A head is
pushed, opened or frozen for review only when it contains the latest `origin/develop`. A pushed
branch takes `develop` by a merge and is not rewritten, so its reviewed commits stay reachable;
a branch that was never pushed may rebase. When `develop` moves after a sign-off, the author
merges it and the reviewer binds the sign-off to the new head (WDR 003).

**Provenance:** One open pull request: Maintainer, 2026-10-08, session transcript: "I dont
approve any new prs to develoo until the existing PRs are mergable and merge. stop stacking PRs
based off develop, causing each stacked pr to be unmergable as soon as the prior merges." and
"I don't understand why opening an additional pr when one or more are already open against the
target branch... ie stacking where the first pr to merge causes all the others to fail merge."
An earlier 2026-10-08 statement, "you keep stacking up prs ... this is wasteful.", is a memory
copy only; original not located. Current with `develop`, merge not rewrite: shipped in PR #260
(merge commit bd9c727) with the one-open rule; the up-to-date ruleset on `develop` shipped in
PR #1 (merge commit 3d2b5bf), and `main` stopped requiring it in PR #212 (merge commit
8e71104); no maintainer decision found.

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

**Gate crossed:** merge-time for the up-to-date rule. The `develop` ruleset requires branches
to be up to date (`strict_required_status_checks_policy`); `main` does not, because its earlier
merge commits never reach `develop`. The `check` skill runs `git merge-base --is-ancestor
origin/develop HEAD`. The one-open limit (`ship` step 2, `CONTRIBUTING.md`) and not rewriting
a pushed feature branch are practice only.

---

## 4. Decision 3: Required checks on Linux and macOS, and the peer-review status

### Decision

Koinon currently supports Linux and macOS, with operating-system differences in
`internal/platform`, and merges into `develop` only when the head commit has passed on both:
`go vet ./...`, `go test -race -timeout 30m ./...` and the four `CGO_ENABLED=0` builds
(linux/amd64, linux/arm64, darwin/amd64, darwin/arm64), the native service lifecycle job under
systemd and launchd, and the reviewer's `peer-review` commit status. The sign-off is a pull
request comment bound to the head commit, because every agent acts under one GitHub account
and an author cannot approve its own pull request (WDR 003). Before a push the `check` skill
runs the suite locally with `git diff --check`, shell syntax checks for shell edits, the
changelog rule and a private-content scan.

**Provenance:** macOS support and the macOS CI runner shipped in PR #8 (merge commit 4456efe);
Maintainer, 2026-09-18: "MacOS Suport is not optional" (memory copy only; original not
located). The Go and native lifecycle checks shipped in PR #207 (merge commit 10ea1e4) and the
`peer-review` status in PR #262 (merge commit ce75054); no maintainer decision found.

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
`KOINON_NATIVE_JOB=1` on disposable runners; for a pull request that changes only `agents/`,
`docs/sprints/`, `.claude/`, `.agents/` or `.codex/`, the jobs still report but skip the
service steps. The `develop` ruleset requires `go (ubuntu-latest)`, `go (macos-latest)`,
`go-lifecycle (ubuntu-latest, systemd)`, `go-lifecycle (macos-latest, launchd)`,
`peer-review` and `refusal-review` (WDR 001, Decision 6); `main` requires the first four. `release.yml` builds the four binaries and
checksums for a version tag.

---

## 5. Decision 4: Auto-merge

### Decision

Koinon pull requests into `develop` currently set squash auto-merge. GitHub
merges the pull request once the ruleset conditions pass, including the `peer-review` status
on the head, so auto-merge does not bypass review or checks.

**Provenance:** Maintainer, 2026-10-11, session transcript: "all prs need to set automerge,
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
the account's settings, not from the `--author-email` that `ship` step 5 passes to a manual
merge, which is the only merge `ship` on `develop` describes.

---

## 6. Decision 5: Signed, human-authored commits

### Decision

Koinon commits are currently signed (`git commit -S`), carry a DCO `Signed-off-by` line
(`-s`), and are authored under the maintainer's GitHub identity with the GitHub no-reply
address. Commits and pull requests carry no co-author trailer, tool signature or model name.
Git-related prose and internal documents refer to Robert as "the maintainer", not by name or
as "the user".

**Provenance:** Maintainer, 2026-09-22, session transcript: "you will also commit only under my
github username, never claude." and "I DO NOT TOLLERATE ANTHROPIC SPAM". Maintainer,
2026-10-07, session transcript: "please stop using the term "user" when referring to me.  I am
the "maintainer"" and "this goes for all git related prose, and internal docs" (shipped in PR
#201, merge commit 5121d87). Signing and the no-reply address: Maintainer, 2026-09-18: "this is
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

Before the maintainer's release approval, the promotion pull request lists every failed CI run
since the released baseline, so that a failure that recurs is seen and given a cause. The
scope is each failed run and each failed attempt of the repository's workflows from the
baseline release tag's commit to the candidate's exact-head CI, on all branches: also deleted
pull request branches, and a failure that a rerun later turned green. Each entry has the run
and attempt, the commit, the job and platform, the cause with its evidence (else "unknown"),
and the pull request that fixed it or the open issue. An open issue records a fault that is
not fixed; it does not replace the exact-candidate checks (Decision 3). The `release` skill
accounts for failed runs in the scope that the project's Release checks define; Koinon's
release facts name the source.

**Provenance:** Maintainer, 2026-10-24, session transcript, after the `main` pushes for the
v0.4.1 preparation and for v0.4.2 each had a failed macOS run: "why do we have CI tests
constantly failing on nearly every CI run?" (typos corrected), then "see main". Of the 120 runs
since 2026-10-21, 6 had failed; the two that were not yet fixed were test timing faults (#475).
Maintainer, 2026-10-25, session transcript, to the question "Should I add the step in gap 1 to
the `release` skill? I recommend yes.", where gap 1 was that no step measures accepted work, from
a review of an article on agent harness setup: "yes, also ask your claude peer to review the
article and weigh in on any required changes, come to me with a mutually agreed upon proposal".
The jointly reviewed proposal (scope, entries, location, the issue limit, the portable skill
sentence and the facts), to the question "Do you approve this proposal?": "yes".

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Each failure on its own | Fix or rerun a failed run when it is seen | The macOS timing failures recurred for days; each run looked like one event |
| Latest attempt only | List the runs whose final result is failure | A failure that a rerun turned green is not seen |
| More measures | Also record usage with retries, review time and corrections per task | Not requested; an optional later test on comparable accepted tasks |

### Rationale

1. The release is where the maintainer reads the evidence for the whole period.
2. A cause with evidence, a fix or an issue keeps a recurring failure from being read as noise.

### Enforcement

Practice only: the `release` skill and the promotion pull request evidence. No CI job checks the
list.

---

## Conclusion

Koinon currently publishes through one squash pull request at a time into `develop`, on a head
that contains the latest `develop`, gated by the Linux and macOS checks and a commit-bound
`peer-review` status, with auto-merge set; a release lists the failed CI runs since the last
one. GitHub rulesets enforce the branch, check and
up-to-date rules; one open pull request, no rewriting of pushed branches, signing and DCO are
practice. The pull request order, auto-merge, commit identity, naming and the failed-run account are
the maintainer's choices; the rest waits for the maintainer's decision.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | Decision 6: failed CI runs are accounted for before a release |
