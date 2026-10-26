---
name: handoff
description: "Write a timestamped snapshot of this agent's current work to its own directory under the Git-ignored _handoff/ so the next session of the same agent can resume it. Use at the end of a session, before a long or risky operation, when context is nearly full, or when the maintainer asks to hand off or save state. Each run adds a new file beside the earlier ones."
---

# Handoff

Koinon's procedure source is [WDR 005](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/005-private-handoffs-and-continuity.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

The `pickup` skill reads what this skill writes.

A handoff is a snapshot of one agent's work, true at the time it is written. The reader is the
next session of the same agent. Each agent writes its own directory, so handoffs from different
agents stay apart. Handoffs are session state: they stay in the ignored `_handoff/` directory of
the main checkout.

Beside its snapshots, each agent keeps one short progress note, `progress.md`, in the same
directory (step 6). The note is updated in place; each snapshot is a new file.

## 1. Name the agent

The agent ID is the native agent family in lower case, such as `claude`, `codex`,
`deepseek`, `agy` or `opencode`. When two
agents of one family work in this repository at the same time, the maintainer assigns each a role
suffix, for example `codex-review`. Use the same ID every session.

## 2. Create and check the directory

Resolve Handoff path from the project facts or the declared default. The examples below use
that default; apply an evidenced override consistently for writing and pickup.

All handoffs belong in the `_handoff/` directory of the main checkout (the repository
directory that holds `.git`), because a linked worktree and every file in it are removed when
its work merges. A relative `_handoff/` path in a worktree names the worktree's directory. A
session that runs outside a Git checkout, such as one in a workspace directory that holds
several repositories, keeps its handoffs in the directory where it started: Koinon registered
that directory for the session, a managed cycle reads the handoff there, and Git checks
nothing there. The commands below find the right directory from any worktree, or from the
directory where the session started. Run them every time; they create what is missing and
change nothing that already exists:

```sh
if common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null); then
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
    echo "not ready: _handoff/ is not ignored, Git tracks files in it, or it could not be created"
  fi
else
  # Outside a Git checkout: the directory where the session started.
  dir="$PWD/_handoff/<agent-id>"
  [ -d "$dir" ] || (umask 077 && mkdir -p "$dir")
  if [ -d "$dir" ]; then echo "ready: $dir"; else echo "not ready: $dir could not be created"; fi
fi
```

- The repository's `.gitignore` lists `_handoff/`. When the checked-out branch does not, part 1
  adds the rule to `info/exclude` in the Git common directory. That file is local and applies to
  every worktree, so no tracked file changes.
- Part 2 creates `_handoff/` and `_handoff/<agent-id>/` with mode 0700 when they are missing.
  It leaves an existing directory and its mode as they are.
- `git check-ignore` asks Git itself, so it confirms that a file in the directory is ignored,
  whatever rule matches. `git ls-files` confirms that Git tracks nothing there; an ignore rule
  does not untrack a file that was committed earlier.

When the output is `not ready`, tell the maintainer which check failed, with the output.

## 3. Take the timestamp from the shell

```sh
date -u +%Y-%m-%d-%H%M%S
```

Use UTC so that every agent's files sort in one order. The file is
`$dir/<timestamp>.md`, or `<timestamp>-<label>.md` when the maintainer gives a label.

## 4. Seed from the progress note and the last handoff of this agent

Start from `$dir/progress.md` when it exists (step 6), then take what the note does not cover
from the last snapshot. Find both with the `pickup` skill's lookup (step 2 there), which
selects the timestamped files. Carry forward what is still true and still open, and leave out
work that is finished or abandoned. The new file is a complete snapshot, not a difference from
the last one.

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

Obtain current guidance with the installed `koinon guide --agent <family>`; its Team operations
section has the runnable steps. Use MCP discovery to observe this session's peer name and
repository; the guide itself describes the runtime and returns no live observations. Record the
values you observed, and write unknown values as unknown. In a not-yet-upgraded installation,
follow that installed runtime's guidance.

Record the participant address, role, holder flag and last succession result from the
installed runtime's discovery/registration output, not from the peer name or directory.
An older runtime that has no participant fields is recorded as such. Record the current work
consumer, claim generation and lease when present, so pickup can distinguish continued
participant claims from predecessor-owned native claims. The claim and the address stay with
the participant while you write the handoff; native-session ownership follows the installed
guidance.

Record the native session ID from the family's trusted environment or the MCP caller metadata
when available: CODEX_THREAD_ID, CLAUDE_CODE_SESSION_ID, DSH_SESSION_ID, or the native
Antigravity/OpenCode identity. Record TMUX and TMUX_PANE when present. These private values go
in the ignored file.

Use commit hashes, branch names, issue and pull request numbers, and repository-relative
paths. Leave out a section that has no content. The file stays on this machine: name a key or
credential by where it is kept, and summarize peer messages in your own words.

Then update the progress note in the same step (step 6), so that it agrees with the new
snapshot. Report the path of the new file.

## 6. Keep the progress note

The note is `$dir/progress.md`, in the resolved Handoff path, at most 4 KiB, with these fields:

```markdown
# Progress — <agent-id>
- Task: <the current task and its issue>
- PR and head: <number and commit, or none>
- Completed: <stages and checks that are done>
- Decisions: <confirmed choices and their sources>
- Open issues: <missing evidence or blockers>
- Next action: <one concrete step>
```

Update this short note after a completed stage, recording the current result and next action.
Write verified values, with the same privacy rules as a snapshot, and replace the whole note
each time. A managed cycle reports the timestamped snapshot.

## Repository facts

The project's facts for this skill are local data in the file named below, in the current
worktree: Version 1, Skill, Sources and the headings declared here. An optional heading that
the file leaves out takes its default, and a skill with no required heading works without the
file. When a required fact is missing, derive it from the project's evidence and record it in
the file. The parser reads the file as plain text, up to 64 KiB.

<!-- koinon:repository-facts -->
```json
{
  "version": 1,
  "filename": ".koinon/skills/handoff.md",
  "fields": [
    {
      "heading": "Handoff path",
      "required": false,
      "default": "_handoff/<agent>/ in main checkout"
    },
    {
      "heading": "Extra verification",
      "required": false,
      "default": "none"
    }
  ]
}
```
