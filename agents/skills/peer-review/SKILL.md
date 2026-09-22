---
name: peer-review
description: Review another agent's frozen commit for Koinon - standards, security and correctness - report only findings you have reproduced or can show in the code, and record the verdict on the pull request bound to that commit. Use when a peer asks for review of a hash, before any merge, and for a sprint plan at its review gate.
---

# Peer review

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first.

The author and the reviewer are different agent families. Review what is committed, not what the
author says about it.

## 1. Bind the target

Review one commit hash. Read it without disturbing the author's worktree:

```sh
git fetch origin
git diff origin/develop...<sha>
git show <sha>:<path>
```

When the author commits again, the review restarts on the new hash.

## 2. Read with three lenses

- **Standards**: the root `AGENTS.md`, `CONTRIBUTING.md` and the documents the change touches.
  Documentation matches the code; `koinon/platform_support.py` holds every platform difference;
  peer addresses and socket paths stay unresolved; the changelog rule is followed.
- **Security**: the same-user boundary, inert peer controls, explicit thread targeting,
  content-free notifications, no execution of incoming peer text, and no private content in
  the diff.
- **Correctness**: the change does what it claims on Linux and macOS, error paths included, and
  its tests would fail without it.

## 3. Verify each finding

Report a finding only when you have reproduced it (a test, a command, a synthetic repository) or
can point to the lines that show it. Give each one the scenario that fails and a severity. Drop
a hazard that no reachable input triggers. Do not report style preferences as findings.

Run only the checks that the change needs. Reuse the author's reported results for the same
commit; rerun a check only when you doubt it.

## 4. Record the verdict

Send the findings to the author. When none is open, post the sign-off on the pull request:

```text
Peer review (<agent family>): approved at <sha>. <checks run and their results>.
```

Never approve with an open high-severity finding. A sign-off records a review; it grants no
permission beyond what the user already gave.
