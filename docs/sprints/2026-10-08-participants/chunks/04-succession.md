# Chunk 04 — Succession

Criteria 3 and 10 of `decision.md`. Depends on chunk 03 (and chunk 01 for launched Codex).
Facts: `live-checks.md`, F1 and F3. Delivers #141.

## Current state

- A Claude session records `claude_pid` in its wake target; a launched session records the
  launch record with `host_pid`. No session records its host's start time or its tmux pane.
- Chunk 02 never moves an active holder; chunk 03 retires a former holder at every holder
  change.

## Change

1. **Host record.** `koinon mcp` sends with each registration the host it found: the host
   process ID (the Claude process, or the launched CLI from the launch record) and that
   process's start time, and, when `TMUX` and `TMUX_PANE` are set and the pane holds the host
   (the existing ancestor check in `terminal_name.go`), the tmux socket path and pane ID. A
   session under the shared Codex daemon sends no host. The daemon stores the host record with
   the session. `internal/platform` gains `ProcessStart(pid)`: Linux reads field 22 of
   `/proc/<pid>/stat`; macOS reads `kern.proc.pid.<pid>` through `sysctl` without cgo.
2. **Evidence.** When a registering session S qualifies for a participant whose active holder H
   is another session, the store checks, in the registration transaction:
   - **same host:** S and H have the same host process ID and start time, and S is not a
     sub-agent. Case: a launched Codex `/clear` or `/resume`, a Claude process that starts a new
     session.
   - **same pane, host ended:** S and H have the same tmux socket and pane, and H's host
     process with H's start time no longer exists (checked by the daemon with
     `ProcessStart`). Case: a new agent process in the pane where the old one ended.

   With either, S becomes the holder and H is retired (chunk 03, item 5). Without both, H keeps
   the participant and the registration result reports `succession_refused` with the reason:
   `no_host`, `host_running`, `other_pane`, `subagent`, `holder_active` or `other_participant`.

   Succession is evaluated only for a registration that S's own tool call caused. The server's
   renewal timer never registers a session (`tools.go`, `renew`: a failed renewal waits for the
   next tool call), so a retired session's server cannot take the participant back by itself.
   **Guard:** the daemon records each session's last tool call time. Same-host evidence is refused
   with `holder_active` when H made a tool call in the 30 seconds before S's registration, so two
   user threads that are active in one host at once never swap the participant. Not verified: whether
   one Codex process runs two user threads at once; the guard covers it either way.
   After a `holder_active` refusal, `koinon mcp` registers S again at S's first tool call after the
   30 seconds have passed, so a successor that called Koinon right after a `/clear` takes the
   participant at its next call instead of waiting for H to expire. Every other refusal is final
   for that registration.
3. **Records.** Each holder change and each refusal is a participant event with the evidence
   (kind, host process ID, pane). The session view shows the last one (chunk 02, item 6).
4. **Skills.** `pickup` no longer retires a predecessor or moves an alias: it reads the
   registration result, reports the succession or the refusal, and tells the maintainer when the
   dashboard choice is needed. `handoff` records the participant address and role. `peer-tmux`
   describes the reset cycle under succession. `agents/skills/AGENTS.md` rules apply.

## Done

- The tests of `definition-of-done.md`, "Succession", with synthetic host records and a test
  double for `ProcessStart`, plus `ProcessStart` tests on Linux and macOS against the test
  process itself.
- `PROTOCOL.md` (host record, evidence, `succession_refused`), `docs/USAGE.md`,
  `docs/INSTALL.md`, the installed `koinon guide` text, the three skills, `CHANGELOG.md`.
