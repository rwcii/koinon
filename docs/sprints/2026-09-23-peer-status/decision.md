# Sprint 2026-09-23 — peer status — Decision

## Problem

A peer cannot see how full another agent's context is, which model it runs, or what it works
on. `bridge.py peers` and the notifier `status` report service reachability and, for Claude
peers only, busy or idle activity from the Claude session registry (`docs/DELIVERY.md`,
"Presence and priority"). Codex activity is `unknown` (#84). Nothing reports the model or
context use of any peer (#119).

Context use decides when an agent should write a handoff and let a new session pick it up.
Today only the agent itself can judge that, and it is the party least able to notice its own
degradation. A peer that can see the numbers can suggest the cycle at the right time. The same
peer also needs to know whether the other agent is mid-turn, so that a message does not cross
its work (#84), and what the other agent has claimed, so that two agents do not take the same
work.

The sources exist but are not read:

- **Claude.** The command named by the Claude Code `statusLine` setting receives, on each
  update, `model.id`, `context_window.context_window_size` ("200000 by default, or 1000000
  for models with extended context"), `context_window.total_input_tokens`,
  `context_window.used_percentage` and `session_id`
  (<https://code.claude.com/docs/en/statusline.md>). The transcript records the model name but
  not the context variant, and its format "is internal to Claude Code and changes between
  versions" (<https://code.claude.com/docs/en/sessions.md>).
- **Codex.** The session log records `turn_context` (model), `token_count` events with
  `last_token_usage` and `model_context_window`, and turn events `task_started`,
  `task_complete` and `turn_aborted`, each with a turn ID. Codex's local state database maps a
  thread ID to its log path. Both are internal and version-specific. The app-server schema
  defines token-usage and turn notifications, but a live, side-effect-free subscription is not
  verified.
- **Claimed work.** Memory work items record claims with a lease, a checkpoint and a progress
  deadline (`docs/WORK-ITEMS-POLICY.md`). A claim is held under a participant session key;
  nothing links that key to a peer in the listing, and agents do not start work items today.

## Acceptance criteria

1. `bridge.py peers` reports, for each Claude and Codex peer, `model`, `context` and `work`
   next to the existing `presence`. Each observed value has a source, the time it was read
   and the time the source recorded it. Each unobserved value is `unknown` with a typed
   reason. The same fields appear in the local notifier `status` for its own participant.
2. **Claude context.** With the Claude status-line integration in place, a Claude peer reports
   its model ID, context limit, tokens used and fill level, taken from the status-line data,
   with source `claude_statusline`. When the integration is missing or has been changed, its
   context is `unknown` with reason `statusline_missing` and the exact command that repairs
   it. Activity and claimed work do not depend on the integration.
3. **Status-line integration.** Installation and upgrade for a Claude user set it up by
   default; an explicit option declines it, and a matching option removes it.
   - An existing status-line command keeps working unchanged: it receives the same input and
     its output and exit status are returned unchanged. Koinon adds nothing to the display.
   - The existing command runs even when Koinon's own part fails.
   - The previous `statusLine` value is saved once, when the integration is first set up. A
     repeated installation, upgrade or retry never wraps Koinon's command again and never
     replaces the saved value. A saved decline survives upgrade until the user reverses it.
   - Removal and uninstall restore the saved value only while the entry still is Koinon's
     command. An entry that the user changed afterwards is kept, and the result reports it
     with the action that removes Koinon's part. Other Claude settings, and other fields of
     the `statusLine` entry, are unchanged.
   - A settings edit detects the concurrent changes it can observe. The file is compared with
     what was read immediately before it is replaced and checked again afterwards; a
     difference found there is a reported conflict that keeps the user's content. A Claude Code
     write between the last comparison and the replacement cannot be detected, so the
     documentation tells the user not to change Claude settings while Koinon installs,
     upgrades or removes the integration.
   - The upgrade operation includes the settings edit in its preflight, keeps the saved value
     as evidence, and reports the edit or its conflict in its completion and preservation
     report.
   - The added time per update stays within a bound that the definition of done states and
     measures.
   - A test uses a command that reads its whole input and prints a line, and proves identical
     input and output with and without the integration. Separate tests fail the user's command
     and Koinon's part, and prove that the input is read once and given to the user's command
     unchanged in both cases.
4. **Codex activity (#84).** A Codex peer reports `busy` while its latest `task_started` has
   no matching `task_complete` or `turn_aborted` and the selected participant's own process is
   verified live, and `idle` when its latest turn has ended and that process is verified live.
   Bridge or notifier liveness is not participant liveness. When the participant process
   cannot be associated and verified, or has ended, activity is `unknown`.
5. **Codex context.** A Codex peer reports its model, `model_context_window`, the tokens of the
   last request and the fill level from its session record, with a Codex source name. A missing,
   unreadable or unrecognized record is `unknown` with a typed reason.
6. **Age.** Each value carries two times: when its source recorded it, and when Koinon read it.
   A fresh read of a live participant keeps reporting a context value that its source recorded
   long ago; an idle session's value does not become `unknown` by age alone. A cached reading
   expires as the existing presence contract requires: after the 15-second freshness window or
   a disconnect (`docs/DELIVERY.md`, "Presence and priority"). A value becomes `unknown` when
   the participant process is no longer live.
7. **Claimed work.** A peer's active work claims appear under `work`, with the work ID, title
   and checkpoint. The listing reads them through an explicit association of the peer with a
   memory store (its repository) and with the participant session key that holds the claims.
   A successful query with no active claim reports no claimed work, which does not mean idle.
   A missing association or an unavailable memory service reports `unknown` with a reason. A
   claim whose lease has expired is not shown; the work item itself is not changed.
8. **Both families read.** Run from a Codex session's shell, `bridge.py peers` shows the same
   fields for Claude and Codex peers as it does from a Claude session.
9. **Content-free.** Only an allowlist is stored or reported: numbers, identifiers, states and
   times, plus the work item title and checkpoint of criterion 7, which agents record in the
   shared memory store for that purpose. No transcript text, message text, prompt or file
   content reaches a stored file or a peer. A test feeds text fields to every source reader
   and proves that they do not appear.
10. **Process.** The `ship` and `sprint` skills tell an agent to start one memory work item for
    each deliverable issue, linked to the issue and reused through build, review and merge by
    the agent that builds it. A read-only reviewer does not claim work
    (`docs/WORK-ITEMS-POLICY.md`).
11. Linux and macOS, Python 3.11 to 3.13; the documents that describe presence, installation
    and the peer listing describe the new fields.

## Constraints

- Same-user boundary: sources are read only when owned by the current user, and stored files
  are owner-only. Platform differences stay in `koinon/platform_support.py`.
- No new value is published into the Claude session registry. Data reaches peers through
  Koinon's own state and the existing listing (`docs/PARITY-MEMORY-DESIGN.md`, Contract 3).
- No Codex thread is resumed, replaced, loaded or subscribed to observe it
  (`docs/DELIVERY.md`). The Codex app-server may be a source only if a test shows that reading
  it has no side effects; otherwise the session log is the source.
- Internal formats are treated as version-specific: an unrecognized format or version is
  `unknown` with a reason, never a guessed value. The Claude transcript is not a source.
- The Claude settings change is limited to the `statusLine` entry of the user's own Claude
  configuration and is part of installation, which a request to install Koinon authorizes
  (root `AGENTS.md`). The user can decline it at installation.
- A reported value grants nothing. No automatic alerts, notices or forced handoffs; peers
  read the listing and decide what to suggest.
- Standard library only. Tests use synthetic peers and synthetic source files and never touch
  live sessions, the user's Claude settings or user services (root `AGENTS.md`).
- Non-goals: a table of published limits (both families report a measured limit); a DeepSeek
  source (DeepSeek peers report `unknown` with a reason); changes to Claude Code's own
  `ListAgents` display.

## Issues

- Delivers #84 and #119.
- DeepSeek activity and context stay `unknown`; a verified DeepSeek source gets its own issue
  when one is found.
