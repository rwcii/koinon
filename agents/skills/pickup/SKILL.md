---
name: pickup
description: "Resume work from this agent's newest handoff in its own directory under the Git-ignored _handoff/, verify its claims against the live repository, and continue with the next action. Use at the start of a session, or when the maintainer asks to pick up, resume or continue. Reads another agent's handoff when the maintainer names that agent."
---

# Pickup

Koinon's procedure source is [WDR 005](https://github.com/rwcii/koinon/blob/develop/docs/engineering/wdr/005-private-handoffs-and-continuity.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

This skill reads what the `handoff` skill writes.

## 1. Choose the handoff

Use your own agent ID (see step 1 of the `handoff` skill). Use another agent's ID or a file path
when the maintainer names it.

When you have no handoff of your own, say so, list the newest timestamped file name in each
other agent directory for information, and ask the maintainer what to resume.

## 2. Read the progress note, then find the newest handoff

Resolve Handoff path from the project facts or the declared default, matching the handoff
writer. The examples below show the default convention.

All handoffs are in the `_handoff/` directory of the main checkout, because a linked worktree
is removed when its work merges. A relative `_handoff/` path in a worktree names the worktree's
directory; these commands find the main checkout from any worktree. A session outside a Git
checkout keeps its handoffs in the directory where it started (step 2 of the `handoff` skill),
so run them from that directory:

```sh
if common=$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null); then
  root=$(dirname "$common")
else
  root=$PWD
fi
dir="$root/_handoff/<agent-id>"
note="$dir/progress.md"
if [ -f "$note" ] && [ "$(wc -c < "$note")" -le 4096 ]; then
  cat "$note"
else
  echo "no usable progress note"
fi
file=$(ls "$dir"/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9]*.md 2>/dev/null | sort | tail -1)
if [ -z "$file" ]; then
  echo "no handoff for <agent-id>"
  ls "$root/_handoff/"
else
  echo "newest handoff: $file"
fi
```

Read the progress note first when it exists (step 6 of the `handoff` skill). Then read the
newest handoff, or in a managed cycle the handoff that the cycle names (step 4), for what the
note does not cover, such as identity and claims, and always its Maintainer direction and My
proposals not yet raised sections: the note does not replace them. When the note is absent,
unreadable or larger than 4 KiB, read the whole handoff and say so in the report.

Handoff file names start with a UTC timestamp, so the last name in sort order is the newest.
The handoffs are the timestamped files: `progress.md` is the note, and a directory with only the
note has no handoff. Note the branch that the note or the handoff names; that is usually the
branch to resume. No handoff is a normal result; handle it as step 1 says.

## 3. Verify against the live state

The note and the handoff were true when they were written. Check each volatile claim once
against the live state:

```sh
git fetch --prune origin
git status --short
git log --oneline -10 <branch>
gh pr view <number> --json state,mergeable,mergeStateStatus,title
gh issue view <number> --json state,title
```

Also list the open pull requests and issues. A handoff knows the work that existed when it
was written:

```sh
gh pr list --state open
gh issue list --state open
```

When the handoff names a sprint, read its plan under `docs/sprints/` and list the open issues on
its milestone, if it has one: `gh issue list --milestone "<sprint>" --state open`.

Claims that a decision waits for the maintainer are checked against the decision board at the
end of step 4, when the daemon knows this session.

## 4. Reconnect to the daemon

Read the installed `koinon guide --agent <family>` and follow it for the maintainer's task. In a
not-yet-upgraded installation, use its own guidance. A reset changes native session identity
even when the process/pane remains. Compare the trusted current identity with the handoff, and
use the configured MCP tools to register/discover this current session. MCP identifies its
caller on every call, so tool arguments carry no session ID. DeepSeek uses the installed guide's
explicit command path under normal approvals.

When the installed guide has a Managed cycle reporting section, read all of it. After a
clear, query your own cycle with the guide's own-cycle status tool or command. For a cycle in
`reset_verified` or `waiting_pickup`, restore the handoff file that the cycle names. A progress note that is newer
than that file, or disagrees with it, is reconciled against the live state, difference by
difference. Then report your pickup with the guide's pickup tool or command as that section
says; the report says whether the note was used. When the report is refused because of the
cycle's stage, read the cycle again and report when it is at one of these stages. A runtime
without these tools or commands keeps the steps above.

When identity or daemon access is unavailable, report it with the error. Pickup follows the
installed runtime's succession result.

Read the current session's discovery record and registration result under the installed
guidance. Report its peer name, participant address, role, holder flag and last succession
result; compare the address and role with the saved handoff. A runtime with verified
succession can replace the holder on the current native session's own tool call: the same
host PID and start value after the tool-call guard (30 seconds by default), or the same verified tmux
socket and pane once the former host ended. The runtime retires and fences the predecessor
atomically.

A `holder_active` refusal includes a daemon wait; the MCP server retries at the first native
tool call after it. Renewal and observation timers are not activity, so they start no
succession. Other refusals have no scheduled retry: report `no_host`, `host_running`,
`other_pane`, `subagent`, `fenced` or `other_participant`, and tell the maintainer when the
dashboard's holder choice is needed. A `no_host` against a predecessor without a host record
(unlaunched, or registered before the upgrade) resolves when that predecessor expires: this
session takes the address at its next registration or renewal. The holder choice makes it
immediate. A fenced old thread keeps its participant fenced, even after the guard. Older
installed runtimes keep their own guidance.

Verify the paired recipient before reporting the current peer name. Read the saved checkpoint
and current work item before writing. When the runtime confirms this session holds the same
participant, its default participant consumer continues the participant's existing cursor,
claim generation and lease; reconcile those facts and keep the claim. Native-session claims
and cursors from before participant ownership keep their old owners, and other holders' claims
stay theirs until they expire or are released. Report identity changes, succession or refusal,
and pending leases; private values stay in the ignored handoff.

Then check the decision board, with the installed guide's decision tools or its command path
(WDR 005, Decision 5):

1. Read `decision_list` with the filter `waiting`, and `decision_pending`, each page by page to
   its end. `waiting` lists the active decision items and tasks; `decision_pending` lists the
   verdicts delivered to you that you have not acknowledged, not undecided items.
2. Read each decision item that the note or the handoff names with `decision_get`, also one
   that is not in the waiting list.
3. Compare each claim that a decision waits for the maintainer with that read:
   - an active item that the claim names still waits: report its reference, revision and what
     waits on it;
   - an item in another state (approved and in progress, returned with feedback, archived,
     withdrawn, or reopened at a newer revision) is a difference: report its state, revision and
     verdict, not a wait;
   - a claim that names no item, or an item that does not exist, is a difference too: report
     it as drift, not as a wait. A point that the handoff says was raised in a conversation
     keeps that source.
4. A verdict from `decision_pending` that you did not handle yet: handle it from its current
   item as the guide's Decisions section says, then acknowledge it. A superseded verdict is not
   handled again.

The report lists the waiting items of the work that you resume; the cards of other
repositories are not part of it. A read that is refused, an unavailable daemon or tool, an older runtime without
these tools and a page you could not finish leave the claim unverified: report it with the
error, not as none and not as a wait, and continue the other steps.

## 5. Orient, then continue

Report briefly:

- Where things stand now, with each difference from the note and the handoff named.
- The goal and success criteria, if they are still valid.
- The next action, corrected for the differences.
- Open work that the handoff does not name.
- Each subagent that the handoff lists as running: its result, or that it has not arrived
  (WDR 005, Decision 4).
- Blockers.
- Decisions waiting for the maintainer, only from the board read of step 4, with each reference
  and revision; each claim that the read did not confirm, as drift or as unverified.
- The maintainer's directions that the handoff quotes, with their dates, and your own proposals
  not yet raised, as yours.

In the saved handoff, Maintainer direction holds the maintainer's words and My proposals not
yet raised holds the previous session's own ideas: report each proposal as yours, not as a
question or a rule from the maintainer. The maintainer's current instructions are a direct
source as always.

Then start the next action. When the live state changed the next action materially or left it
ambiguous, ask the maintainer, with a recommendation. When the maintainer asked for orientation,
the report is the result.

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
  "filename": ".koinon/skills/pickup.md",
  "fields": [
    {
      "heading": "Handoff path",
      "required": false,
      "default": "_handoff/<agent>/ in main checkout"
    },
    {
      "heading": "Extra resume checks",
      "required": false,
      "default": "none"
    }
  ]
}
```
