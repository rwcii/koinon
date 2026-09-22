---
name: release
description: Promote develop to main with a merge pull request once the release is shown to install and upgrade - dated changelog, CI evidence on the exact commit, an upgrade from the current main in a scratch prefix, and the user's approval before the merge. Use when the user asks to release, promote or ship to main.
---

# Release

Read `agents/skills/AGENTS.md`, the root `AGENTS.md`, `CONTRIBUTING.md` and
`docs/INSTALL.md` first.

Green CI alone is not a release. A release is ready when a user can install it and upgrade to
it with the documented commands.

## 1. Prepare on a work branch

On a `chore/*` branch from `develop`, give the `CHANGELOG.md` section its date and title. Ship it
with the `ship` skill.

## 2. Collect the evidence for the release commit

The evidence counts only for the exact `develop` commit that will merge.

- CI: every workflow under `.github/workflows/` is green on that commit, macOS included.
  Reuse these runs; rerun a workflow only when it did not run on that commit.
- Upgrade: the CI evidence starts from fixtures. Also upgrade a real install of the current
  `main` to the release commit, in a scratch prefix with its own state and unit paths and
  `--no-start`. Use the commands in `docs/INSTALL.md` under "Upgrades and removal", and report
  what the upgrade preserved and what it refused.

## 3. Open the merge pull request

```sh
gh pr create --base main --head develop --title "<release title>"
gh pr view <number> --json mergeStateStatus
```

`main` accepts only merge commits. When the pull request is `BEHIND`, stop and tell the user:
bringing `main` into `develop` needs a merge commit, which the `develop` ruleset does not allow.

## 4. Stop for the user

Report the release commit, the CI runs, the upgrade result and any known limits. Merge only
after the user approves this release. An approval for earlier work does not cover it.

```sh
gh pr merge <number> --merge --match-head-commit <sha> \
  --author-email <github-no-reply-address>
gh pr view <number> --json state,mergeCommit
```
