---
name: ship
description: Take one change from a work branch to a squash merge on develop - branch, signed commits, the check skill, a pull request, peer sign-off bound to the head commit, CI, the merge, and closing the issues it delivers. Use when a change is ready to go into develop, or when a pull request is red and must be made ready to merge.
---

# Ship

Read `agents/skills/AGENTS.md`, the root `AGENTS.md` and `CONTRIBUTING.md` first.

One pull request holds one change. Do not combine independent work into one branch to save a CI
run.

## 1. Branch and commit

```sh
git fetch origin
git switch -c <feature|fix|chore>/<slug> origin/develop
```

Work in a worktree that no other agent writes to. Commit with `git commit -S -s`, using the
human author identity and the GitHub no-reply address. Add no automated attribution: no
co-author trailer, no tool signature, no model name, in commits or in the pull request.

## 2. Check, then push once

Run the `check` skill. Push when it passes, and open the pull request against `develop`. Say in
the body what changed, why, and which checks ran on which commit. Name the issues it delivers;
`develop` is not the default branch, so `Closes #N` does not close them.

## 3. Review

Freeze the head commit and ask the other agent family to review it with the `peer-review` skill.
One account holds both agents, so the sign-off is a pull request comment that names the head
commit, not a formal approval. A new commit needs a new sign-off.

## 4. When CI or review fails

Collect every failing check and every open finding first. Fix them together on the branch, run
`check` again, and push once. Do not push one fix at a time: each push starts CI again. Once CI
is running, the branch is frozen for new work; put unrelated follow-ups on a new branch.

## 5. Merge

When CI is green on the signed-off head commit and the rulesets allow it:

```sh
gh pr merge <number> --squash --match-head-commit <sha> \
  --author-email <github-no-reply-address>
```

Then read back the result, because a failed `gh` call can print a message that looks like
success:

```sh
gh pr view <number> --json state,mergeCommit
```

Close each delivered issue with a comment that names the pull request, and read back its state.
Report the merge commit and the closed issues.
