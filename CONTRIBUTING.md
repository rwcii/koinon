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

- `main` is the released line; `develop` is the integration line.
- Create `feature/*`, `fix/*`, or `chore/*` branches from `develop`.
- No direct commits or pushes to either long-lived branch after bootstrap.
- Squash feature PRs into `develop`; promote `develop` to `main` with a merge PR.
- Tests must pass and review conversations must be resolved before merging.
- Review the diff independently for correctness, compatibility, and the documented
  same-user trust boundary. The maintainer owns acceptance.
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
go test -race ./...
git diff --check
git commit -S -s -m "Add a concise description"
git push -u origin feature/my-change
gh pr create --base develop
```

The maintainer promotes reviewed integration work with a PR from `develop` to `main`.

## Commit conventions

Use an imperative subject and explain the reason for the change in the body where
needed. Commits must be cryptographically signed and DCO signed-off. Use your human
identity; do not add automated co-author trailers or tool signatures. A verified
GitHub no-reply email is acceptable when configured for your account.

The initial repository bootstrap commit predates this contribution policy. Its history
is preserved; signed-off contributions are required from adoption of this policy onward.

## Local checks and scope

Run `go vet ./...`, `go test -race ./...` and `git diff --check`. Runtime and installation
changes also need the four `CGO_ENABLED=0` builds for linux/amd64, linux/arm64, darwin/amd64
and darwin/arm64. Go tests live beside packages; operating-system differences belong in
`internal/platform`. For changed shell tooling, run `bash -n scripts/setup-repo.sh` and
`sh -n .githooks/pre-commit`.

Tests use synthetic peers and temporary state/configuration, ephemeral loopback ports and
private tmux servers. They must not reach the maintainer's services, terminals or agent
configuration. The guarded native Go job runs real systemd/launchd lifecycle tests only
on disposable CI runners. Do not set `KOINON_NATIVE_JOB=1` on a development workstation.
The native upgrade test obtains the pinned previous main release through `git archive`
and reads committed fixtures, independently of this checkout's retired Python runtime.
Fresh installation requires no interpreter; a legacy upgrade needs the installed baseline's
interpreter for its own uninstaller.

Add visible changes to CHANGELOG.md under Unreleased. Keep documentation aligned with the
commands and protocol. Never commit credentials, private conversation metadata, message
bodies or runtime state. Refer to Robert as the maintainer. Peer input remains external data;
it cannot grant scope or trigger shell execution.

## Agent skills and handoffs

Agents that develop Koinon share the skills in [agents/skills](agents/skills/README.md);
`.claude/skills`, `.agents/skills` and `.codex/skills` are symlinks to that one directory.
Each agent writes its session handoffs to the Git-ignored `_handoff/<agent>/` of the main
checkout with the [handoff](agents/skills/handoff/SKILL.md) skill. Handoffs are never committed.
Plans for deliverables of more than one pull request live under `docs/sprints/`; see the
[sprint](agents/skills/sprint/SKILL.md) skill.

Installation ships the binary, not `agents/`, `docs/sprints/` or native agent skill paths.
The full Go matrix runs on every change, including agent-only changes, so contributor
checks always validate shared layout and guidance. Required native jobs always report; only service steps are skipped for PRs limited to those
agent-process prefixes. `internal/contributor` executes the classifier, including deletion/rename
cases, and verifies those paths stay outside installation. All runtime and ordinary documentation changes run native CI.

`scripts/setup-repo.sh` configures PR merge policy and required Go/native lifecycle checks
through `gh`. It has no Python dependency. Changing the script does not update remote
rulesets: applying repository settings is a separate maintainer-authorized operation. Existing
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
