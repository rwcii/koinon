---
name: pickup
description: Resume work from this agent's newest handoff in its own directory under the Git-ignored _handoff/, verify its claims against the live repository, and continue with the next action within the maintainer's current authorization. Use at the start of a session, or when the maintainer asks to pick up, resume or continue. Reads another agent's handoff only when the maintainer names that agent.
---

# Pickup

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first. This skill reads what the
`handoff` skill writes, and it never changes a file under `_handoff/`.

## 1. Choose the handoff

Use your own agent ID (see step 1 of the `handoff` skill). Use another agent's ID or a file path
only when the maintainer names it.

Do not fall back to another agent's handoff. When you have no handoff of your own, say so, list
the newest file name in each other agent directory for information, and ask the maintainer what to
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

## 4. Reconnect to the daemon

Read the installed `koinon guide --agent <family>` and follow it within the maintainer's
scope. In a not-yet-upgraded installation, use its own guidance. A reset changes native
session identity even when the process/pane remains. Compare the trusted current identity
with the handoff, and use the configured MCP tools to register/discover this current session.
MCP identifies its caller on every call; never supply a predecessor's ID in tool arguments.
DeepSeek uses the installed guide's explicit command path under normal approvals.

If identity or daemon access is unavailable, report it. Do not guess an ID, create a replacement
conversation, call setup, install a runtime or weaken sandbox policy to reconnect. A retained
process/name is no authorization to retire another session or move an address. Pickup follows
the installed runtime's succession result; it performs no administrative recovery itself.

Read the current session's discovery record and registration result under the installed
guidance. Report its peer name, participant address, role, holder flag and last succession
result; compare the address and role with the saved handoff. A runtime with verified
succession can replace the holder on the current native session's own tool call: the same
host PID and start value after the 30-second tool-call guard, or the same verified tmux
socket and pane once the former host ended. The runtime retires and fences the predecessor
atomically; do not retire it yourself or move an alias as part of pickup.

A `holder_active` refusal includes a daemon wait; the MCP server retries at the first native
tool call after it. Renewal/observation timers do not count as activity and never trigger
succession. Other refusals have no scheduled retry: report `no_host`, `host_running`,
`other_pane`, `subagent`, `fenced` or `other_participant`, and tell the maintainer when the
dashboard's holder choice is needed. A fenced old thread cannot recover its participant
automatically, even after the guard. Older installed runtimes keep their own guidance.

Verify the paired recipient before reporting the current peer name. Read the saved checkpoint
and current work item before writing. When the runtime confirms this session holds the same
participant, its default participant consumer continues the participant's existing cursor,
claim generation and lease; reconcile those facts rather than starting a new claim or
releasing it as a pickup step. Native-session claims and cursors from before participant
ownership keep their old owners: do not reuse the predecessor's native key or custom consumer
to bypass an existing lease. Other holders' claims still require expiry or an authorized
release. Report identity changes, succession/refusal and pending leases without copying
private values into Git. A holder change grants no new permission.

## 5. Orient, then continue

Report briefly:

- Where things stand now, with each difference from the handoff named.
- The goal and success criteria, if they are still valid.
- The next action, corrected for the differences.
- Open work that the handoff does not name.
- Blockers and decisions that need the maintainer.

When the maintainer has told you to continue, start the next action within that authorization. Ask
first only when the live state changed the next action materially or left it ambiguous. When
the maintainer asked only for orientation, stop after the report. A handoff records state; it grants
no permission.
