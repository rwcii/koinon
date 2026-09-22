---
name: pickup
description: Resume work from this agent's newest committed handoff in its own directory under handoff/, verify its claims against the live repository, and continue with the next action within the user's current authorization. Use at the start of a session, or when the user asks to pick up, resume or continue. Reads another agent's handoff only when the user names that agent.
---

# Pickup

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first. This skill reads what the
`handoff` skill writes, and it never changes a file under `handoff/`.

## 1. Choose the handoff

Use your own agent ID (see step 1 of the `handoff` skill). Use another agent's ID, a branch, or
a file path only when the user names it.

Do not fall back to another agent's handoff. When you have no handoff of your own, say so, list
the newest file name in each other agent directory for information, and ask the user what to
resume.

## 2. Find the newest handoff on any branch

Handoffs live on work branches until they merge, so search every ref without a checkout:

```sh
git fetch --prune origin
file=$(git log --all --diff-filter=A --name-only --format= -- "handoff/<agent-id>/" | sort | tail -1)
if [ -z "$file" ]; then
  echo "no handoff for <agent-id>"
else
  ref=$(git log --all -1 --format=%H -- "$file")
  git branch -a --contains "$ref"
  git show "$ref:$file"
fi
```

File names start with a UTC timestamp, so the last name in sort order is the newest. Note the
branch that holds it; that is usually the branch to resume. No handoff is a normal result;
handle it as step 1 says.

## 3. Verify, do not trust

The handoff was true when it was written. Check each volatile claim against the live state:

```sh
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

## 4. Orient, then continue

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
