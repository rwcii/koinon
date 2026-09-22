---
name: check
description: Run the checks that must pass before a Koinon branch is pushed - the test suite, whitespace, shell syntax for shell edits, the changelog rule and a scan for private content in the diff. Use before every push, and as the gate that the ship and sprint skills name.
---

# Check

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first.

A CI run takes about an hour, so a failure found here saves one. Run the checks from the root of
the branch's worktree.

## 1. Tests and whitespace

```sh
python3 -m unittest discover -s tests
git diff --check origin/develop...HEAD
git diff --check
```

A passing test run on the same commit counts; do not run the suite again when only prose changed
since then. Name the commit the tests ran on when you report.

## 2. Shell syntax, for shell edits only

When the branch changes `scripts/setup-repo.sh` or `.githooks/pre-commit`:

```sh
bash -n scripts/setup-repo.sh
sh -n .githooks/pre-commit
```

## 3. Changelog

A change that a Koinon user can see (runtime behaviour, commands, installation, messages, the
documented interfaces) needs an entry in `CHANGELOG.md` in the same branch. Contributor tooling,
tests and internal refactors do not.

## 4. Private content

This repository is public. Scan the added lines:

```sh
git diff -U0 origin/develop...HEAD | grep '^+' | grep -nE \
  '/home/[a-z]|/Users/[A-Za-z]|[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z.]{2,}' \
  | grep -vE 'users\.noreply\.github\.com|@example\.(com|org|net)'
```

Read each match. Also read the diff for thread or session IDs, keys, inbox or peer message
bodies, and memory content; a pattern cannot find all of them. Remove every one before the push.

## Report

Report each check as passed, failed (with the output) or not needed (with the reason).
