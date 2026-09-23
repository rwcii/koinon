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
- Upgrade: the upgrade workflows install the release pinned as `PREVIOUS_RELEASE` in
  `scripts/test-native-upgrade.py` and upgrade it to the commit under test. When that pin is
  the release now on `main`, their green runs are the upgrade evidence. Otherwise, upgrade the
  current `main` yourself. Do not move the pin to do so: it is the last release before the
  `koinon` package layout, and the workflows prove that layout migration from it.
  1. Install `main` into a scratch prefix with its own state and unit paths. Choose the
     components and backends by what the release changes. When the installed runtime is
     unchanged, one component on the manual backend is enough. When the release changes
     runtime or upgrade code, also cover each affected component on its native backend for
     each affected platform (systemd on Linux, launchd on macOS). A scratch unit path does not
     isolate the native manager and is not read by it, so run a native case only on a
     disposable runner or test account that has its own live user manager. Install with the
     default unit paths there, so the manager finds the units, and remove only the test
     services afterwards. Never run a native case in the user's own account. The native
     upgrade script cannot start from `main`, so it is not a substitute for this step.
  2. Initialize its state with the documented native start or manual start. A `--no-start`
     install has no state, and the upgrade refuses it with `missing_state_root`; see
     "Missing state root" in `docs/INSTALL.md`.
  3. Upgrade it to the release commit with the commands under "Upgrades and removal", and read
     `--status`.
  The upgrade must complete and report what it preserved. A refusal is a failure to fix, not
  evidence.

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
