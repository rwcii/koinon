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

A reset can leave the bridge serving the previous session. Compare the handoff's bridge identity
with the live one before anything else:

- **Claude.** `/clear` keeps the process and the peer name. Confirm the name with the installed
  `bridge.py peers`. The native session key changes, so claims under the old key stay with it.
- **Codex or DeepSeek.** Run the installed `session.py status` from this session's own shell
  (DeepSeek adds `--agent deepseek`). For a new session it reports `stopped` with a new peer
  name; run `session.py ensure` the same way to start it. Then stop the
  predecessor with `session.py stop --thread <old ID from the handoff>`, because bridge notices
  and peer messages otherwise keep going to a conversation that no longer runs. If `ensure`
  fails, report the error and the command; do not stop the predecessor.

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
