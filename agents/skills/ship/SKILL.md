---
name: ship
description: Take one change from a work branch to a squash merge on develop - branch, signed commits, the check skill, a pull request, peer sign-off bound to the head commit, CI, the merge, and closing the issues it delivers. Use when a change is ready to go into develop, or when a pull request is red and must be made ready to merge.
---

# Ship

Read `agents/skills/AGENTS.md`, the root `AGENTS.md` and `CONTRIBUTING.md` first.

One pull request holds one change. Do not combine independent work into one branch to save a CI
run.

## 1. Branch and commit

For an existing pull request, work on its branch in its existing worktree. For new work:

```sh
git fetch origin
git switch -c <feature|fix|chore>/<slug> origin/develop
```

Work in a worktree that no other agent writes to. Commit with `git commit -S -s`, using the
human author identity and the GitHub no-reply address. Add no automated attribution: no
co-author trailer, no tool signature, no model name, in commits or in the pull request.

### Hold the issue's work item

When the change delivers an issue, hold one memory work item for that issue from the first
commit to the merge, so peers see in `bridge.py peers` who builds it. Reuse the same item
through build, review fixes and merge; never create a second item for the same issue. A
reviewer that only reads does not claim work. The commands are in
`docs/WORK-ITEMS-COMMANDS.md`.

Use the installed `memory.py` (the default prefix is `~/.local/share/koinon`) with your native
session identity as the consumer key: `CLAUDE_CODE_SESSION_ID` for claude, `CODEX_THREAD_ID`
for codex, `DSH_SESSION_ID` for deepseek. When you use another stable key, declare it once
with the installed `session.py work-key --key <key>`. Never write the key into a commit, a
pull request or a handoff.

```sh
KEY=${CLAUDE_CODE_SESSION_ID:-${CODEX_THREAD_ID:-$DSH_SESSION_ID}}   # or your declared key
mem() { python3 ~/.local/share/koinon/memory.py \
  --repo-path "$(git rev-parse --show-toplevel)" --consumer "$KEY" "$@"; }
soon() { echo $(( $(date +%s) + $1 )); }
```

1. Find the item: `mem work list` and look for the title `#<issue>: ...`. When there is none,
   create it:
   `mem work create --title "#<issue>: <issue title>" --criteria "<acceptance>" --non-goals "<non-goals>" --reference <issue URL> --key issue-<issue>-create --deadline $(soon 300)`.
2. Start it before the first write, with the `revision` from `mem work get <id>`:
   `mem work start <id> --if-revision <revision> --checkpoint "<state>" --next-artifact "<next>" --progress-deadline $(soon 14400) --lease-seconds 3600 --key issue-<issue>-start-<n> --deadline $(soon 300)`.
   A `claim_conflict` means another session holds it: report that and do not build.
3. At each stage (push, review, CI), record progress and renew the lease, with the `revision`
   and `current_claim.generation` from `mem work get <id>`:
   `mem work update <id> --if-revision <revision> --claim-generation <generation> --progress "<done>" --checkpoint "<state>" --next-artifact "<next>" --progress-deadline $(soon 14400) --renew-for 3600`.
   When the lease has expired, read the item again and start it again.
4. Close it in step 5.

When a work command refuses because the repository has no memory service or its store is too
old, say so in the pull request and continue. Do not create another store to get around it.

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

Then close the issue's work item, with the `revision` and `current_claim.generation` from
`mem work get <id>`:

- When the merge completes the issue:
  `mem work finish <id> --if-revision <revision> --claim-generation <generation> --outcome completed --reference <pull request URL>`.
- When more work on the issue remains, for example a later chunk of a sprint, release the
  claim with a checkpoint that names the next step, so that the next builder starts the same
  item:
  `mem work release <id> --if-revision <revision> --claim-generation <generation> --checkpoint "<merged; next step>"`.
