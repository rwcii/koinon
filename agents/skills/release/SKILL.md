---
name: release
description: Promote develop to main with a merge pull request once the release is shown to install and upgrade - dated changelog, CI evidence on the exact commit, an upgrade from the current main in a scratch prefix, the maintainer's approval before the merge, then the version tag that publishes the binaries and the Homebrew formula. Use when the maintainer asks to release, promote or ship to main.
---

# Release

Read `agents/skills/AGENTS.md`, the root `AGENTS.md`, `CONTRIBUTING.md` and
`docs/INSTALL.md` first.

Green CI alone is not a release. A release is ready when the maintainer can install it and upgrade to
it with the documented commands.

## 1. Prepare on a work branch

On a `chore/*` branch from `develop`, give the `CHANGELOG.md` section its date and title. Ship it
with the `ship` skill.

## 2. Collect the evidence for the release commit

The evidence counts only for the exact `develop` commit that will merge.

- CI: every workflow under `.github/workflows/` is green on that commit, macOS included.
  Reuse these runs; rerun a workflow only when it did not run on that commit.
- Upgrade: `internal/nativetest/native_test.go` pins the previous main release and obtains
  it with `git archive`. The native Go lifecycle workflow installs that baseline with its own
  code, seeds committed fixtures, upgrades through `koinon upgrade --from-python`, verifies
  preservation and tests interruption/resume on both platforms. When its pin equals current
  main, its successful exact-commit runs are the legacy upgrade evidence.
- If main has moved beyond that supported legacy baseline, do not pretend the pinned test
  proves the newer upgrade. Identify the actual supported path and test it in isolated state
  on a disposable runner/account before release. A normal workstation is not a native test
  account. Do not change a baseline pin merely to hide missing upgrade coverage.
- Fresh installation needs no Python interpreter. Verify the four binaries/checksums and the
  no-interpreter installation test. State-preserving uninstall and native restart must pass.
- Record the sprint's maintainer-authorized live checks. Name the deferred DeepSeek proof
  under #199 as an outstanding limit; synthetic coverage is not live receipt.

The upgrade must complete and report preserved counts/cursors. A refusal is a failure to fix,
not upgrade evidence. Do not operate on the maintainer's installed runtime without direct
scope for that exact change.

## 3. Open the merge pull request

The root `AGENTS.md` allows one open pull request per target branch: open the merge pull
request only when `gh pr list --base main --state open` lists none.

```sh
gh pr create --base main --head develop --title "<release title>"
gh pr view <number> --json mergeStateStatus
```

`main` accepts only merge commits. Its earlier promotion merge commits never reach `develop`, so
GitHub may report the pull request as `BEHIND`. The `main` ruleset does not require an up-to-date
branch, so that state does not block the merge; the required checks on the `develop` head do.
When `main` has content that `develop` lacks (`git diff origin/develop...origin/main` is not
empty), stop and tell the maintainer: that content must reach `develop` through a reviewed pull
request first.

## 4. Stop for the maintainer

Report the release commit, the CI runs, the upgrade result and any known limits. Merge only
after the maintainer approves this release. An approval for earlier work does not cover it.

```sh
gh pr merge <number> --merge --match-head-commit <sha> \
  --author-email <github-no-reply-address>
gh pr view <number> --json state,mergeCommit
```

## 5. Tag the release and check the tap

Tag the main merge commit with the version the maintainer approved, in the form
`vMAJOR.MINOR.PATCH`, and push only the tag. The tag message becomes the GitHub release
description, so write it as release notes in Markdown: one sentence on what the release is, a
**Highlights** list of the user-visible changes with the commands to use them, the **Known
limits**, and links to the changelog and the installation guide at that tag. State facts only.

```sh
git tag -s --cleanup=verbatim -F <notes file> <tag> <merge commit>
git push origin <tag>
```

The tag starts `release.yml`. Its jobs attach the four binaries and `SHA256SUMS` to the GitHub
release, then write `Formula/koinon.rb` in `rwcii/homebrew-koinon` with
`scripts/homebrew-formula.sh` and push it with the `HOMEBREW_TAP_DEPLOY_KEY` deploy key. Check
both results:

```sh
gh run list --workflow release.yml --limit 1
gh release view <tag> --json assets --jq '.assets[].name'
gh run list -R rwcii/homebrew-koinon --limit 1
```

The tap's own workflow installs, tests and audits the formula on Linux and macOS. The release is
published only when that run passes. A failure there is a release defect: fix the generator in
this repository and release a new patch version; never edit the formula in the tap by hand.
