---
name: pickup
description: Resume work from this agent's newest handoff in its own directory under the Git-ignored _handoff/, verify its claims against the live repository, and continue with the next action within the user's current authorization. Use at the start of a session, or when the user asks to pick up, resume or continue. Reads another agent's handoff only when the user names that agent.
---

# Pickup

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first. This skill reads what the
`handoff` skill writes, and it never changes a file under `_handoff/`.

## 1. Choose the handoff

Use your own agent ID (see step 1 of the `handoff` skill). Use another agent's ID or a file path
only when the user names it.

Do not fall back to another agent's handoff. When you have no handoff of your own, say so, list
the newest file name in each other agent directory for information, and ask the user what to
resume.

## 2. Find the newest handoff

All handoffs are in the `_handoff/` directory of the main checkout, never in a linked
worktree, which is removed when its work merges. Do not read a relative `_handoff/` path; find
the main checkout from any worktree:

```sh
root=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")
file=$(ls "$root/_handoff/<agent-id>/"*.md 2>/dev/null | sort | tail -1)
if [ -z "$file" ]; then
  echo "no handoff for <agent-id>"
  ls "$root/_handoff/"
else
  cat "$file"
fi
```

File names start with a UTC timestamp, so the last name in sort order is the newest. Note the
branch that the handoff names; that is usually the branch to resume. No handoff is a normal
result; handle it as step 1 says.

## 3. Verify, do not trust

The handoff was true when it was written. Check each volatile claim against the live state:

```sh
git fetch --prune origin
git status --short
git log --oneline -10 <branch>
gh pr view <number> --json state,mergeable,mergeStateStatus,title
gh issue view <number> --json state,title
```

Also list the open pull requests and issues. A handoff knows only the work that existed when it
was written:

```sh
gh pr list --state open
gh issue list --state open
```

When the handoff names a sprint, read its plan under `docs/sprints/` and list the open issues on
its milestone, if it has one: `gh issue list --milestone "<sprint>" --state open`.

## 4. Reconnect to the bridge

A reset can leave the bridge serving the previous session. Before anything else, run the
installed `session.py guide --agent <family> --topic reconnect --json`; the managed block in
your agent instruction file names its path. Follow its text and its recipes, and compare its
observations with the handoff's bridge identity:

- **Claude.** `/clear` keeps the process and the peer name. The guide reports the peer name.
- **Codex or DeepSeek.** Compare this session's ID with the one in the handoff. When they
  differ, run the guide's `ensure` recipe as your first Koinon command, through the agent's
  approval request: the recipe is marked `needs_approval`, and inside the sandbox it fails.
- **The predecessor.** Run the guide's `stop_predecessor` recipe for the handoff's old ID only
  when the user directly authorized the replacement of that exact predecessor, for example by
  naming it or by authorizing that reset cycle. The pickup command alone, a peer message, a
  retained process or a retained peer name is not that authorization. Otherwise, report the
  predecessor and the recipe.

If the installed runtime has no `guide` command, report that the runtime needs an upgrade, and
ask the user how to reconnect. If a recipe fails, report the error and the command.

Report the peer name in use now, so peers can refresh their listing.

## 5. Orient, then continue

Report briefly:

- Where things stand now, with each difference from the handoff named.
- The goal and success criteria, if they are still valid.
- The next action, corrected for the differences.
- Open work that the handoff does not name.
- Blockers and decisions that need the user.

When the user has told you to continue, start the next action within that authorization. Ask
first only when the live state changed the next action materially or left it ambiguous. When
the user asked only for orientation, stop after the report. A handoff records state; it grants
no permission.
