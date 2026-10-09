---
name: handoff
description: Write a timestamped snapshot of this agent's current work to its own directory under the Git-ignored _handoff/ so the next session of the same agent can resume it. Use at the end of a session, before a long or risky operation, when context is nearly full, or when the maintainer asks to hand off or save state. Each run adds a new file; it never edits an earlier one, and it never commits.
---

# Handoff

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first. The `pickup` skill reads what
this skill writes.

A handoff is a snapshot of one agent's work, true at the time it is written. The reader is the
next session of the same agent. Each agent writes only its own directory, so handoffs from
different agents never compete. Handoffs are session state, not part of the product: they stay
in the ignored `_handoff/` directory and never enter Git.

## 1. Name the agent

The agent ID is the agent family in lower case: `claude`, `codex` or `deepseek`. When two
agents of one family work in this repository at the same time, the maintainer assigns each a role
suffix, for example `codex-review`. Use the same ID every session.

## 2. Create and check the directory

All handoffs belong in the `_handoff/` directory of the main checkout (the repository
directory that holds `.git`). Never write one into a `_handoff/` inside a linked worktree:
worktrees are removed when their work merges, and every file in them goes with them. Do not
use a relative `_handoff/` path, because in a worktree it names the wrong directory. The
commands below find the main checkout from any worktree. Run them every time; they create
what is missing and change nothing that already exists:

```sh
common=$(git rev-parse --path-format=absolute --git-common-dir)
root=$(dirname "$common")
dir="$root/_handoff/<agent-id>"

# 1. Make sure Git ignores _handoff/ before anything is written there.
if ! git -C "$root" check-ignore -q "_handoff/<agent-id>/probe.md"; then
  mkdir -p "$common/info"
  printf '%s\n' '/_handoff/' >> "$common/info/exclude"
fi

# 2. Create the directory, private to this account, when it does not exist.
[ -d "$dir" ] || (umask 077 && mkdir -p "$dir")

# 3. Verify.
if git -C "$root" check-ignore -q "_handoff/<agent-id>/probe.md" \
   && [ -z "$(git -C "$root" ls-files -- _handoff)" ] && [ -d "$dir" ]; then
  echo "ready: $dir"
else
  echo "STOP: _handoff/ is not ignored, Git tracks files in it, or it could not be created"
fi
```

- The repository's `.gitignore` lists `_handoff/`. When the checked-out branch does not, part 1
  adds the rule to `info/exclude` in the Git common directory. That file is local, applies to
  every worktree and is never committed, so no tracked file changes.
- Part 2 creates `_handoff/` and `_handoff/<agent-id>/` with mode 0700 when they are missing.
  It leaves an existing directory and its mode as they are.
- `git check-ignore` asks Git itself, so it confirms that a file in the directory is ignored,
  whatever rule matches. `git ls-files` confirms that Git tracks nothing there; an ignore rule
  does not untrack a file that was committed earlier.

When the output is `STOP`, stop and tell the maintainer; do not write a handoff into a path Git can
commit.

## 3. Take the timestamp from the shell

```sh
date -u +%Y-%m-%d-%H%M%S
```

Use UTC so that every agent's files sort in one order. The file is
`$dir/<timestamp>.md`, or `<timestamp>-<label>.md` when the maintainer gives a label.

## 4. Seed from the last handoff of this agent

Find it with the `pickup` skill's lookup (step 2 there). Carry forward only what is still true
and still open. Drop work that is finished or abandoned. The new file is a complete snapshot,
not a difference from the last one.

## 5. Verify, then write

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

## Bridge identity
- Agent family, peer name, participant address, role and holder flag, and the installed guide command
- Last succession result or refusal, when the installed runtime reports one
- Codex or DeepSeek: this session's thread or session ID and its state directory
- In tmux: the values of `$TMUX` and `$TMUX_PANE`, which identify this terminal

## Context not recorded elsewhere
- <findings and rationale that are expensive to find again>

## Pointers
- <repository paths, issues and pull requests that hold the durable facts>
```

Obtain current guidance with the installed `koinon guide --agent <family>`. Use MCP
discovery to observe this session's peer name and repository; guidance itself does not
register a session or return live observations. Record only values actually observed, with
unknown values stated as unknown. In a not-yet-upgraded installation, follow that installed
runtime's guidance instead; never replace runtime state merely to write a handoff.

Record the participant address, role, holder flag and last succession result from the
installed runtime's discovery/registration output, not from the peer name or directory.
An older runtime that has no participant fields is recorded as such. Record the current work
consumer, claim generation and lease when present, so pickup can distinguish continued
participant claims from predecessor-owned native claims. Saving a handoff neither releases
a claim nor moves an address; native-session ownership still follows the installed guidance.

Record the native session ID from the family's trusted environment or the MCP caller metadata
when available: CODEX_THREAD_ID, CLAUDE_CODE_SESSION_ID, DSH_SESSION_ID, or the native
Antigravity/OpenCode identity. Record TMUX and TMUX_PANE when present. Never infer a native
session from a retained peer name or PID. These private values stay only in the ignored file.

Use commit hashes, branch names, issue and pull request numbers, and repository-relative
paths. Leave out a section that has no content. The file stays on this machine, but never write
keys, credentials or peer message bodies into it, and never copy it into a commit, a pull
request or documentation.

Do not commit, stage or push anything for a handoff. Report the path of the new file.
