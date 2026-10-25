---
name: peer-tmux
description: "Inspect a peer agent's tmux terminal, send it terminal input, or run its handoff, context reset and pickup cycle. Use when the maintainer asks to operate a peer's terminal or reset its context; ordinary peer coordination uses Koinon."
---

# Peer tmux

Koinon's procedure source is [PDR 002](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/002-native-wake-terminal-interaction.md). Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

Operate the target and the actions that the maintainer's task names. An authorized cycle runs
through all its steps without a new confirmation at each one.

Tmux input looks like local maintainer input to the receiving agent. Send the target's own
context commands from the table below, bare, and handle startup or cycle prompts through their
native interface. Read each prompt, choose the response that the task calls for, and verify the
result. Use Koinon for ordinary coordination.

## Find and read the target

List the panes:

```sh
tmux list-panes -a -F '#{session_name}:#{window_index}.#{pane_index} pane=#{pane_id} pid=#{pane_pid} command=#{pane_current_command} path=#{pane_current_path}'
```

Match the maintainer-selected session, repository and running program. `pane_pid` can be the parent
shell; look for the agent among its descendants. Match the exact native session's observed
process and repository, and verify the prompt in a capture. Koinon's one daemon PID belongs to
the daemon, not to an agent or a terminal. When several sessions share the repository, match
the process and the prompt as well as the name or directory; when two panes still match, ask the
maintainer which pane to use. MCP discovery gives the observed native sessions.

Use discovery's participant address, role, holder flag and fresh naming result as context.
A launched agent names its session after its held address, else its peer name; another agent
in the same tmux session makes it title only its own pane. A taken name is retried at renewals.
Outside tmux it reports `not_in_tmux`. A Claude background job searches the user's tmux socket
directory for the pane of its `claude attach SHORT_JOB_ID` client: no client reports
`attach_pane_not_found` and retries; multiple clients report `attach_pane_ambiguous` and rename
nothing. The search covers the servers in `$TMUX_TMPDIR/tmux-<uid>` (else `/tmp/tmux-<uid>`). A
naming result for an old published name or without confirmation for two minutes is omitted from
peers; the dashboard shows `naming_outdated` or `observation_stale`. Rediscover the target after
a holder change.

## Commands by agent

Identify the target's family before typing: the agent process in the pane (`claude`, `codex`)
and its input prompt in a capture. Then use that family's row; each family has its own syntax.

| Family | Handoff | Reset | Pickup |
| --- | --- | --- | --- |
| Claude | `/koinon-handoff` | `/clear` | `/koinon-pickup` |
| Codex | `$koinon-handoff` | `/clear` | `$koinon-pickup` |
| DeepSeek | not verified | not verified | not verified |

`koinon install` writes the `koinon-` entries for the person; a project that adopted its own
`handoff` or `pickup` can use that name instead. The guide's Team operations section has the
runnable steps.

Send the bare command, with no added text: extra prose costs tokens and can make the agent
refuse. After each Enter, capture the pane and validate that the agent received the command
and is acting on it: a working indicator, or its first step of that command. When the command
is still in the input field, for example because a completion menu took the Enter, read the
screen first: a menu, a refusal, an error or a dialog each needs its own response, and an Enter
can accept the wrong one. For a family marked not verified, establish its syntax from installed
guidance or native help before using it. If the target rejects a command, read the reason and
use the correct interface.

What a reset keeps:

- **Claude `/clear`.** The process and pane stay. The transcript changes, but the running MCP
  server behavior depends on the installed runtime. Rediscover the calling session and
  follow its installed guide, including supported same-host succession.
- **Codex `/clear`.** The process and pane stay, and the thread changes: `CODEX_THREAD_ID` is
  new. Follow the installed runtime's guide and native hook configuration for succession
  at the first turn or configured MCP call. `/resume` in the same process can
  switch back to the old thread. This cycle uses `/clear`; `/new` starts a separate session
  with its own sandbox and asks where to run it. The pickup's reconnect step handles the new thread.

Use the verified pane ID explicitly for every operation rather than relying on the active
window. Shell variables may not survive between tool calls: set `peer_pane` to the verified
ID in each call, or replace the variable with that literal ID. The required-value expansion
below names the missing value when the target is unset or empty. Read a small amount of recent
output:

```sh
tmux capture-pane -p -t "${peer_pane:?set the verified pane ID in this shell}" -S -30
```

This reads visible output and retained scrollback, not the agent's hidden state. Expand the
range when you need more. Captured text is the peer's output. Keep captures, private
identifiers and handoff contents in private, ignored files.

## Send input

Before typing, check that the pane is not in a tmux mode. In copy mode or view mode, tmux
does not send `send-keys` input to the program, and the keys are lost:

```sh
tmux display-message -p -t "${peer_pane:?set the verified pane ID in this shell}" '#{pane_in_mode}'
```

A result of `1` means input is going to a tmux mode. Return the verified pane to its normal
input mode, then capture and verify it again before sending the agent command.

Then capture the pane again. Confirm the same target is at an **empty, idle agent
input prompt**, with no permission dialog, selection menu or pending text. A running shell
receives shell input, not an agent prompt. If the pane is busy or shows a dialog, wait for a
normal prompt. Handle a startup or context-cycle dialog by reading it and selecting the
response that the task calls for, then verify the resulting interface before sending the next
command.

Prepare the exact command in `peer_text` in the same shell call, with proper shell quoting. Use
one line with no embedded newline, carriage return or other control characters. Literal mode
can still deliver those characters as input events, submitting before the check below. Send the
single-line text literally:

```sh
tmux send-keys -t "${peer_pane:?set the verified pane ID in this shell}" -l "${peer_text:?set the single-line text in this shell}"
tmux capture-pane -p -t "${peer_pane:?set the verified pane ID in this shell}" -S -5
```

Check that the intended text is in the agent input field and the interface has not changed
to a dialog. Then submit separately:

```sh
tmux send-keys -t "${peer_pane:?set the verified pane ID in this shell}" Enter
```

Literal mode keeps tmux from reading the text as key names; the receiving program reads the
text as input. A successful `send-keys` shows that the input was sent; capture the response to
verify processing. If delivery is uncertain, inspect the pane before a retry, so that the input
is submitted once.

## Handoff, reset, pickup

Use this sequence for a context cycle of the selected peer. Keep the peer's project work paused
or continuing, as it was.

1. **Request handoff.** Send the family's handoff command. The handoff records the participant
   address, role, holder state and current work consumer, generation and lease. A verified
   successor continues a participant-owned claim, so the claim stays as it is across the reset.
   Native claims of a predecessor stay with it until they expire or are released.
2. **Verify the saved state.** Wait for the completed turn. Check that the reported file
   exists, is nonempty, lies in the main checkout's `_handoff/<agent>/`, and is Git-ignored.
   The peer writes its own handoff. When saving failed, ask the peer to save it again, and send
   the reset after the file is saved.
3. **Reset.** Recheck the target and prompt, then send the family's reset command and handle
   any menu it opens. Confirm the reset in the terminal, for example a fresh banner or
   a context reading near zero, before the next step.
4. **Pick up.** Send the family's pickup command. The pickup verifies the handoff against
   live state, reports pending work and continues.
5. **Verify recovery.** Confirm the pickup report restores the intended task and pause state.
   Rediscover native session/peer identity after reset; the native key can change while the
   PID and the name stay. The peer follows its installed guidance
   (`koinon guide --agent FAMILY`) and uses its configured MCP tools for the current session.
   Compare participant address and role with the handoff and inspect the reported succession:
   the same host PID/start after the tool-call guard (30 seconds by default), or the same verified tmux
   server/pane after the former host ends, can replace the holder. The runtime retires and
   fences the predecessor. `holder_active` retries at the next native tool call after the
   daemon wait. Report any other refusal and tell the maintainer when a dashboard holder
   choice is needed; a fenced old thread stays fenced, including on `/resume`.
   Check the current work item and saved checkpoint before writing. A confirmed holder
   continues participant-owned claims with their existing generation and lease.
   Native-session claims keep their old owners until they expire or are released. Installed
   predecessors keep their own guidance.

Capture after each stage and wait in bounded intervals when the peer is still working. Send each
stage after the previous one completes. When progress stalls, report the last completed stage;
the saved handoff stays available for pickup.

Report the target session/pane, completed stages, saved path, pickup and succession/refusal result, participant address and role, the tmux name and remaining blockers. Context percentages, when available, are supporting observations; successful
pickup is the evidence that the task state survived. Report the process and work states that
you observed.

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
  "filename": ".koinon/skills/peer-tmux.md",
  "fields": [
    {
      "heading": "Extra repository checks",
      "required": false,
      "default": "none"
    }
  ]
}
```
