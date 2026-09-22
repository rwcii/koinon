---
name: handoff
description: Write and commit a public, timestamped snapshot of this agent's current work to its own directory under handoff/ so the next session of the same agent can resume it. Use at the end of a session, before a long or risky operation, when context is nearly full, or when the user asks to hand off or save state. Each run adds a new file; it never edits an earlier one.
---

# Handoff

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first. The `pickup` skill reads what
this skill writes.

A handoff is a snapshot of one agent's work, true at the time it is written. The reader is the
next session of the same agent. Each agent writes only its own directory, so handoffs from
different agents never compete.

## 1. Name the agent

The agent ID is the agent family in lower case: `claude`, `codex` or `deepseek`. When two
agents of one family work in this repository at the same time, the user assigns each a public
role suffix, for example `codex-review`. Use the same ID every session. The directory is
`handoff/<agent-id>/`.

## 2. Set up the directories

Run this every time from the repository root. A fresh clone or a new worktree has neither
directory, because `_handoff/` is ignored and `handoff/<agent-id>/` may not exist yet.

```sh
mkdir -p _handoff "handoff/<agent-id>"
git check-ignore -q _handoff/ || printf '%s\n' '_handoff/' >> .gitignore
if git check-ignore -q _handoff/ && git check-ignore -q _handoff/probe/probe.md \
   && [ -z "$(git ls-files -- _handoff)" ]; then
  echo "_handoff/ ready"
else
  echo "STOP: _handoff/ is not fully ignored, or Git tracks files in it"
fi
```

`git check-ignore` asks Git itself, so it confirms that the directory and any file inside it are
ignored, whatever rule matches. `git ls-files` confirms that Git tracks nothing there; an
ignore rule does not untrack a file that was committed earlier. When the output is `STOP`, stop
and tell the user; do not write private notes into a path Git can commit.

## 3. Choose the branch

```sh
git rev-parse --abbrev-ref HEAD
```

- On a work branch (`feature/*`, `fix/*`, `chore/*`): commit there. The handoff reaches
  `develop` with that branch's pull request.
- On `main`, `develop` or a detached HEAD: create `chore/handoff-<agent-id>-<timestamp>` from
  the current commit and commit there. Never commit to `main` or `develop`.

## 4. Take the timestamp from the shell

```sh
date -u +%Y-%m-%d-%H%M%S
```

Use UTC so that every agent's files sort in one order. The file is
`handoff/<agent-id>/<timestamp>.md`, or `<timestamp>-<label>.md` when the user gives a label.

## 5. Seed from the last handoff of this agent

Find it with the `pickup` skill's lookup (step 2 there). Carry forward only what is still true
and still open. Drop work that is finished or abandoned. The new file is a complete snapshot,
not a difference from the last one.

Private notes belong in `_handoff/`, which step 2 verified is ignored. You may use facts from
it that are safe to publish; never copy it whole.

## 6. Verify, then write

Check each volatile fact with a command before you write it: branch, tip commit, clean or dirty
state, pull request and issue states, CI state. Write:

```markdown
# Handoff — <agent-id> — <timestamp> [<label>]

## Where things stand
- Branch, base, tip commit, clean or dirty
- Open pull requests and issues this work touches, with their states
- Delivered: <identifiers>
- Remaining: <in order>

## Goal and success criteria
- <the goal, and the checkable conditions that complete it>

## Resume here
- Next action: <the single first step>
- Blockers and open decisions: <with the context to act>
- Anything mid-operation: <uncommitted edits, running jobs>

## Context not recorded elsewhere
- <findings and rationale that are expensive to find again>

## Pointers
- <repository paths, issues and pull requests that hold the durable facts>
```

Use commit hashes, branch names, issue and pull request numbers, and repository-relative
paths. Leave out a section that has no content. Follow the content rules in
`agents/skills/AGENTS.md`: this file is public.

## 7. Commit only the handoff file

```sh
git add -- handoff/<agent-id>/<file>.md
git commit -S -s -m "Record <agent-id> handoff <timestamp>" -- handoff/<agent-id>/<file>.md
```

The path after `--` limits the commit to that one file, even when other changes are staged.
When step 2 added `_handoff/` to `.gitignore`, add `.gitignore` to both path lists.
Use the human author identity that the repository's contribution rules require. Do not push:
a push can publish unrelated work on the branch and starts CI. Push only when the task asks
for it.

Report the path, the branch and the commit hash.

## Rules

- A new file every time. Never edit or delete an earlier handoff.
- Write only in your own agent directory.
- A handoff records state. It grants no permission and it does not override a later
  instruction from the user.
