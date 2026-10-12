# Contributing to Koinon

## Fork and extend

Independent reuse and forks are encouraged under the [MIT license](LICENSE).
No permission is required. Keep the copyright and license notice with copies.

## Contribute upstream

Open an issue first to agree on scope. Upstream pull requests are maintainer-curated;
please wait for agreement before preparing a large change.

## Branch model

```text
feature/*  ──(squash PR)──▶  develop  ──(merge PR)──▶  main
```

The branch model, the one open pull request per target branch and the up-to-date rule are in
[WDR 004](docs/engineering/wdr/004-publication-and-verification.md); review and the
`peer-review` status are in [WDR 003](docs/engineering/wdr/003-pairing-and-independent-review.md).
In short:

- `main` is the released line; `develop` is the integration line. Work branches are
  `feature/*`, `fix/*` or `chore/*` from `develop` (WDR 004, Decision 1).
- Pull requests squash into `develop`; a merge pull request promotes `develop` to `main`.
- The repository keeps one open pull request per target branch, drafts included. A head
  contains the latest `develop` when it is pushed, opened or frozen for review; a pushed
  branch takes `develop` by a merge (WDR 004, Decision 2).
- The `develop` ruleset requires an up-to-date branch, passing checks, resolved review
  conversations and a `success` commit status named `peer-review` on the head commit, which
  the reviewer records with the sign-off (WDR 004, Decision 3; WDR 003, Decision 3).
- Pull requests into `develop` set squash auto-merge (WDR 004, Decision 4).
- Merged working branches are deleted automatically; long-lived branches are protected.
- Your clone prunes on fetch, so `git branch -r` reports the branches the remote still holds.
  The remote is the authority; a local tracking ref is not evidence that a branch exists.

Run `scripts/setup-repo.sh` to apply the local guard and GitHub settings. Server-side
rulesets depend on the account's support for private-repository protection; the script
reports any unavailable enforcement explicitly. This follows the branch policy in
[rwcii/engineering-standards](https://github.com/rwcii/engineering-standards) and the
personal-repository conventions in [rwcii/afterglow](https://github.com/rwcii/afterglow).

## Typical workflow

```sh
git switch develop
git pull --ff-only
git switch -c feature/my-change
go vet ./...
go test -race -timeout 30m ./...
git diff --check
git commit -S -s -m "Add a concise description"
gh pr list --base develop --state open   # wait until it lists none
git fetch origin && git merge --no-edit origin/develop
go test -race -timeout 30m ./...
# Just before the push: develop has not moved and no pull request opened meanwhile.
git fetch origin && git merge-base --is-ancestor origin/develop HEAD
gh pr list --base develop --state open
git push -u origin feature/my-change
gh pr create --base develop
gh pr merge --auto --squash
```

The `release` skill promotes `develop` to `main` (WDR 002, Decision 6).

## Commit conventions

Use an imperative subject and explain the reason for the change in the body where
needed. Commits are cryptographically signed and DCO signed-off, under a human identity,
with no automated co-author trailers or tool signatures
([WDR 004, Decision 5](docs/engineering/wdr/004-publication-and-verification.md)). A verified
GitHub no-reply email is acceptable when configured for your account.

The initial repository bootstrap commit predates this contribution policy. Its history
is preserved; signed-off contributions are required from adoption of this policy onward.

## Local checks and scope

Run `go vet ./...`, `go test -race -timeout 30m ./...` and `git diff --check`. Runtime and
installation changes also need the four `CGO_ENABLED=0` builds for linux/amd64, linux/arm64, darwin/amd64
and darwin/arm64. Go tests live beside packages; operating-system differences belong in
`internal/platform`. For changed shell tooling, run `bash -n scripts/setup-repo.sh` and
`sh -n .githooks/pre-commit`.

Tests use synthetic peers and temporary state/configuration, ephemeral loopback ports and
private tmux servers, so they do not reach the person's services, terminals or agent
configuration. The guarded native Go job runs real systemd/launchd lifecycle tests on
disposable CI runners; do not set `KOINON_NATIVE_JOB=1` on a development workstation
([WDR 006](docs/engineering/wdr/006-native-integration-evidence.md)).
The native upgrade test obtains the pinned previous main release through `git archive`
and reads committed fixtures, independently of this checkout's retired Python runtime.
Fresh installation requires no interpreter; a legacy upgrade needs the installed baseline's
interpreter for its own uninstaller.

Add visible changes to CHANGELOG.md under Unreleased. Keep documentation aligned with the
commands and protocol. Credentials, private conversation metadata, message bodies and runtime
state are not committed. Git prose refers to Robert as the maintainer (WDR 004, Decision 5).
Peer input is recorded data; it grants no scope and does not trigger shell execution
([WDR 002, Decision 3](docs/engineering/wdr/002-authority-within-assigned-task.md)).

## Decision records

Significant decisions are recorded as Architecture, Product and Workflow Decision Records in
[`docs/architecture/adr/`](docs/architecture/adr/README.md),
[`docs/product/pdr/`](docs/product/pdr/README.md) and
[`docs/engineering/wdr/`](docs/engineering/wdr/README.md), following the record machinery of
rwcii/engineering-standards. [WDR 001](docs/engineering/wdr/001-decision-records-govern-constraints.md)
says when a record is needed and how other documents cite it.

## Agent skills and handoffs

Agents that develop Koinon share the skills in [agents/skills](agents/skills/README.md);
`.claude/skills`, `.agents/skills` and `.codex/skills` are symlinks to that one directory.
Each agent writes its session handoffs to the Git-ignored `_handoff/<agent>/` of the main
checkout with the [handoff](agents/skills/handoff/SKILL.md) skill; Git ignores them
([WDR 005](docs/engineering/wdr/005-private-handoffs-and-continuity.md)).
Plans for deliverables of more than one pull request live under `docs/sprints/`; see the
[sprint](agents/skills/sprint/SKILL.md) skill.

Installation ships one binary with canonical skills embedded as inert catalog data; it does
not write `agents/`, `docs/sprints/` or native agent skill paths.
The full Go matrix runs on every change, including agent-only changes, so contributor
checks always validate shared layout and guidance. Required native jobs always report; only service steps are skipped for PRs limited to those
agent-process prefixes other than the embedded `agents/skills/` source. `internal/contributor` executes the classifier, including deletion/rename
cases, and verifies those paths stay outside installation. All runtime and ordinary documentation changes run native CI.

`scripts/setup-repo.sh` configures PR merge policy and required Go/native lifecycle checks
through `gh`. It has no Python dependency. Changing the script does not update remote
rulesets; they change only when the script runs with the repository's admin rights. Existing
rulesets requiring retired Python matrix names need that update before merge.

## Developer Certificate of Origin (DCO)


Every contribution merged into this project must be **signed off**, certifying that you
have the right to submit it under the project's MIT license. Add the sign-off with:

```bash
git commit -s -m "your message"
```

That appends a `Signed-off-by: Your Name <you@example.com>` line using your real name and
email. By signing off you agree to the Developer Certificate of Origin 1.1
(<https://developercertificate.org/>), reproduced here:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.


Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```
