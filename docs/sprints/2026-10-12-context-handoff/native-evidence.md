# Managed context handoff — native evidence

This file records the native runs of the Claude Code cycle adapter (chunk 03) and the Codex
cycle adapter (chunk 04) against the [definition of done](definition-of-done.md). Each run used a dedicated temporary repository, a
private tmux server (`tmux -S`), a clean environment and an ephemeral daemon with its own state
directory and port. No ongoing work session was cleared. Captures, transcripts and native IDs
stay private; this file holds the observations needed to judge each result.

## Linux, Claude Code 2.1.296

Linux (kernel 7.0), Opus 5.5, auto permission mode. Sessions started with `koinon claude`;
the Claude family threshold was 1%, so each completed cycle admitted the next one at once.

### Complete cycles (chunk 03, before merge)

| Case | Pickup entry | Stages |
| --- | --- | --- |
| Loader, five cycles in sequence | `loader` | requested, ready, clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed) |
| Interactive bootstrap (launcher without the loader) | `interactive` | requested, ready, bootstrap_intent, bootstrap_verified, clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed) |
| Project skill byte-equal to the catalog item | `project` | as the loader case |
| Daemon restart while waiting_ready | `loader` | as the loader case; the ready report after the restart was accepted |

The receiving model's transcript shows, in order: the bare `/pickup`, `library_read
skill.pickup` (loader) or the project skill's base directory, `context_cycle_status`, the read
of the handoff file that the cycle named, the guide's Managed cycle reporting section and
`context_cycle_pickup` with outcome `resumed` (D8, D9). The successor was the requester itself:
the MCP caller stayed the same under the new native epoch, and Enable carried over (D10). The
first run found the Escape-then-Enter defect that chunk 03 fixed before merge.

### Native dialog inside a cycle (D7), 2026-10-13

The test repository's `.claude/settings.json` had the ask rule `Bash(touch:*)`. A test turn
told the model to run `touch` right after its `context_cycle_ready` call, so Claude Code showed
its permission dialog ("Do you want to proceed?", options 1 Yes and 2 No) while the cycle was
`ready`.

| Case | Stages |
| --- | --- |
| Answered on the dashboard | requested, ready, waiting_prompt (`native_dialog`), prompt_intent (`maintainer_option`), ready (`prompt_answered`), clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed) |
| Answered in the pane (the native session's own answer) | requested, ready, waiting_prompt (`native_dialog`), ready (`prompt_resolved`), clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed) |

The Sessions page showed the dialog text with one `context-cycle-answer` form for each option,
bound to the cycle ID, revision and dialog digest. Option 1 was applied once and the command
ran. No input was entered while the dialog was shown.

### Daemon crash around live inputs (D11), 2026-10-13

A helper polled the state database and sent SIGKILL to the daemon when the newest cycle
recorded the given stage, after an optional delay; the daemon was then started again on the
same state.

| Case | Kill | Pane at the kill | Result after the restart |
| --- | --- | --- | --- |
| E1 | at clear_intent | input line empty | uncertain (`reset_unproven`); native epoch unchanged; nothing typed; Enable ended |
| E2 | 1.5 s after clear_intent | `/clear` submitted; the daemon had recorded clear_received and reset_verified | waiting_pickup, then complete (resumed); one `/clear` and one `/pickup` in the pane history |
| E3 | at pickup_intent | input line empty | uncertain (`pickup_unreceipted`); no `/pickup` typed; Enable ended |
| E4 | 0.25 s after clear_intent | `/clear` typed, not submitted | uncertain (`reset_unproven`); native epoch unchanged; the text stayed unsubmitted and no Enter was sent; Enable ended |

The Sessions page showed each stage with its time and the fixed reason. A recorded intent was
never entered again after the restart.

### Second interactive bootstrap, 2026-10-13

A Linux run of the macOS steps found a defect. In a session launched without the loader, the
first cycle's bootstrap added the loader directory. The session keeps it across `/clear`, so
the next cycle's `/add-dir` got the reply "… is already added as a working directory." and no
dialog, and the cycle ended `uncertain` (`dialog_not_shown`) with Enable off. With the fix the
same session's next cycle ran requested, ready, bootstrap_intent, bootstrap_verified,
clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed).

## macOS

Not run yet. The adapter reports `platform_unverified` on macOS. The steps are in
[macos-live-check.md](macos-live-check.md); its results go here, and the evidenced platform and
version are added in the same change.

## Codex CLI 0.162.0, Linux, 2026-10-14

Sessions started with `koinon codex` in new standalone repositories, each with a project
`pickup` skill byte-equal to the catalog item and trusted in memory
(`-c 'projects={...}'`). The Codex family mode was `handoff_cycle` at 1%. Stages are in
order; `/clear` erases the pane history, so the pane shows only what followed it.

| Case | Stages and result |
| --- | --- |
| Cycles in sequence (two) | requested, ready, clear_intent, clear_received, reset_verified, pickup_intent, waiting_pickup, complete (resumed). The new thread took the address with succession evidence `cycle_reset`, and the Enable moved to it; the next cycle ran for it. |
| Daemon restart while waiting_ready | as above; the ready report after the restart was accepted. |
| Command approval before the clear, answered on the dashboard | requested, ready, waiting_prompt (`native_dialog`), prompt_intent (`maintainer_option`), ready (`prompt_answered`), clear_intent … complete (resumed). |
| The same, answered in the pane | requested, ready, waiting_prompt (`native_dialog`), ready (`prompt_resolved`), clear_intent … waiting_pickup. |
| `context_cycle_pickup` approval in the pickup wait, answered on the dashboard | … waiting_pickup, waiting_prompt (`native_dialog`), prompt_intent (`maintainer_option`), complete (resumed): the approved call was the report. |
| The same, answered in the pane | … waiting_pickup, waiting_prompt (`native_dialog`), complete (resumed). |

The receiving thread's own transcript shows, in order: `$pickup` as its first input, the
project skill, `context_cycle_status`, the read of the handoff file that the cycle named, the
guide's Managed cycle reporting section and `context_cycle_pickup` with outcome `resumed` (D8,
D9).

The dialog cases used `-a on-request`, `approvals_reviewer="user"`, Koinon tools approved and
`context_cycle_pickup` set to `prompt`. They found two defects that are fixed (chunk 04 C14,
C15): a requester that did not hold its address (a stale holder in another pane) was cleared
and its cycle failed `waiting_pickup_timeout`, because the new thread could not take the
address; and the approved report named the waiting_pickup revision that the dialog wait had
moved, so each report raised a new approval.

Daemon crashes (D11), with the helper of the Claude runs:

| Case | Kill | Pane at the kill | Result after the restart |
| --- | --- | --- | --- |
| E1 | at clear_intent | composer empty | uncertain (`reset_unproven`); nothing typed; Enable ended |
| E2 | 1.5 s after clear_intent | `/clear` and `/status` done; clear_received and reset_verified recorded | pickup_intent, waiting_pickup, complete (resumed); one `$pickup` |
| E3 | at pickup_intent | composer empty | uncertain (`pickup_unreceipted`); no `$pickup` typed; Enable ended |
| E4 | 0.25 s after clear_intent | `/clear` typed, not submitted | uncertain (`reset_unproven`); the text stayed unsubmitted and no Enter was sent; Enable ended |

With the default automatic approval review ("Approve for me"), one run's reviewer denied
`context_cycle_ready`, because the user's Codex instructions say that a peer request never
authorizes a context reset; it read the overwatch request as one. The same review allowed the
report in the other runs. The cycle waited in waiting_ready and entered nothing; Koinon does
not work around a native denial.

## macOS, Codex

Not run yet. The adapter reports `platform_unverified` on macOS; the steps are in
[macos-live-check.md](macos-live-check.md).

## Other families

Antigravity, OpenCode, Grok and DeepSeek have no admitted context source (see the native
proof matrix in the definition of done).
