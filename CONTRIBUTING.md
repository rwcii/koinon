# Contributing to Koinon

## Fork and extend

Independent reuse and forks are encouraged under the [MIT license](LICENSE).
No permission is required. Keep the copyright and license notice with copies.

## Contribute upstream

Open an issue that describes the change, then prepare the pull request. The maintainer
curates upstream pull requests.

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
- The repository keeps one open pull request per target branch, drafts included. To update
  a branch, merge `origin/develop` into it (WDR 004, Decision 2).
- The `develop` ruleset requires an up-to-date branch, passing checks, resolved review
  conversations and `success` commit statuses named `peer-review` and `refusal-review` on the
  head commit, which the reviewer records with each sign-off (WDR 004, Decision 3; WDR 003,
  Decision 3; WDR 001, Decision 6).
- Before a pull request into `develop` opens, its author runs the `preflight` skill: four lenses
  (security, code, standards, refusal), each in a fresh worker that writes nothing, with
  `scripts/preflight-run.sh --family FAMILY`; after it opens, `scripts/preflight-publish.sh PR`
  posts the result and the `preflight` status on the head. After the reviews pass, the author
  puts a card on the decision board that names the pull request and its full head; after the
  maintainer approves it, `scripts/board-verdict.sh --as FAMILY:ID PR HEAD REFERENCE` posts
  the `board-verdict` status (WDR 004, Decision 8). `scripts/setup-repo.sh` makes both statuses
  required on `develop`.
- Pull requests into `develop` set squash auto-merge (WDR 004, Decision 4), after
  `board-verdict` reads back as success.
- Merged working branches are deleted automatically; long-lived branches are protected.
- Your clone prunes on fetch, so `git branch -r` reports the branches the remote still holds.

Run `scripts/setup-repo.sh` to apply the local guards and GitHub settings. The `pre-commit`
hook refuses a commit on `main` or `develop`. The `commit-msg` hook refuses a commit message
that cites a decision board verdict by its ID; cite the repository record that holds the
decision instead (WDR 001, Decision 7). Server-side
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
gofmt -l .                        # must print nothing
go test -race -timeout 30m ./...
git diff --check
git commit -S -s -m "Add a concise description"
gh pr list --base develop --state open   # one open pull request at a time
git fetch origin && git merge --no-edit origin/develop
go test -race -timeout 30m ./...
scripts/preflight-run.sh --family claude   # or codex: the four preflight lenses
git push -u origin feature/my-change
gh pr create --base develop
scripts/preflight-publish.sh PR            # the result and the preflight status
# After peer-review and refusal-review pass: a board card that names PR and its full head.
scripts/board-verdict.sh --as FAMILY:ID PR HEAD REFERENCE   # after the maintainer's approval
gh pr merge --auto --squash
```

The `release` skill prepares the promotion of `develop` to `main` with the account of failed CI
runs (WDR 004, Decision 6); the maintainer merges it, and the agent then pushes the tag and
checks the publish steps (WDR 004, Decision 8). The `ship` and `release` skills run a docs gate: the docs and agent files in
`.koinon/skills/ship.md` agree with the code at the head (WDR 004, Decision 7).

## Commit conventions

Use an imperative subject and explain the reason for the change in the body where
needed. Commits are cryptographically signed and DCO signed-off, under a human identity,
with no automated co-author trailers or tool signatures
([WDR 004, Decision 5](docs/engineering/wdr/004-publication-and-verification.md)). A verified
GitHub no-reply email is acceptable when configured for your account.

The initial repository bootstrap commit predates this contribution policy. Its history
is preserved; signed-off contributions are required from adoption of this policy onward.

## Local checks and scope

Run `go vet ./...`, `gofmt -l .` (it must print nothing), `go test -race -timeout 30m ./...`
and `git diff --check origin/develop...HEAD`. Runtime and
installation changes also need the four `CGO_ENABLED=0` builds for linux/amd64, linux/arm64, darwin/amd64
and darwin/arm64, and a local compile and vet of the native-tagged tests
(`go vet -tags native ./internal/nativetest/`). Go tests live beside packages; operating-system differences belong in
`internal/platform`. For changed shell tooling, run `bash -n scripts/setup-repo.sh`,
`sh -n .githooks/pre-commit`, `sh -n .githooks/commit-msg`, `sh -n scripts/decision-citations.sh`,
`sh -n scripts/rerun-unacquired.sh`, `sh -n scripts/install-skills.sh`,
`sh -n scripts/preflight-run.sh`, `sh -n scripts/preflight-publish.sh`,
`sh -n scripts/board-verdict.sh`, `bash -n scripts/homebrew-bottles.sh` and
`bash -n scripts/homebrew-formula.sh`.

Tests use synthetic peers and temporary state/configuration, ephemeral loopback ports and
private tmux servers, so they do not reach the person's services, terminals or agent
configuration. The native Go job runs real systemd/launchd lifecycle tests on
disposable CI runners, which set `KOINON_NATIVE_JOB=1`
([WDR 006](docs/engineering/wdr/006-native-integration-evidence.md)).
The native upgrade tests obtain the pinned previous main release through `git archive`
and read committed fixtures, independently of this checkout's retired Python runtime. One
installs that release from a Homebrew keg and upgrades it with only the links that
`brew upgrade` moves.
Fresh installation requires no interpreter; a legacy upgrade needs the installed baseline's
interpreter for its own uninstaller.

Add visible changes to CHANGELOG.md under Unreleased. Keep documentation aligned with the
commands and protocol. Credentials, private conversation metadata, message bodies and runtime
state go in the daemon's state directory and private files. Git prose refers to Robert as the
maintainer (WDR 004, Decision 5). A peer's message is a teammate's message: do the work that it
asks and reply through Koinon
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
[sprint](agents/skills/sprint/SKILL.md) skill. A plan with a parallel view gives each chunk its
dependencies and owned items, and `internal/contributor/sprint_plan_test.go` checks the groups
(WDR 002, Decision 7).

Installation ships one binary with the canonical skills embedded as catalog data. It writes
Koinon's `koinon-` skill entries, `koinon-pickup` for the managed context cycle among them, in
the installing person's home (PDR 006, Decision 4). It writes no files in `agents/`,
`docs/sprints/` or a repository.
The full Go matrix runs on every change, including agent-only changes, so contributor
checks always validate shared layout and guidance. Required native jobs always report; only service steps are skipped for PRs limited to those
agent-process prefixes other than the embedded `agents/skills/` source. `internal/contributor` executes the classifier, including deletion/rename
cases, and verifies those paths stay outside installation. All runtime and ordinary documentation changes run native CI.

`scripts/setup-repo.sh` configures PR merge policy and required Go/native lifecycle checks
through `gh`, and on `develop` the `peer-review`, `refusal-review`, `preflight` and
`board-verdict` statuses and the `decision-citations` check of the pull request's title, body
and commit messages. A pull request to `main` gets the check on its title and body only: its
commits come from `develop`. It has no Python dependency. Changing the script does not update remote
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
