# Sprint 2026-09-24 — stable alias — Decision

## Problem

This sprint solves a Codex problem. A Codex participant registers on the bridge with
`session.py ensure`. The peer name is derived from the thread:
`<family>-<repository label>-<two hex digits>`, made unique against the names already taken
(`session.py`, `details()` and `save_registration()`). A new thread therefore gets a new name.

The live test of 2026-09-24 (#141, comment of 15:33) established the Codex CLI behaviour:

- `/clear` keeps the CLI process (same pid and kernel start time) and changes
  `CODEX_THREAD_ID`. The new thread is off the bridge until `ensure` runs, and the old
  registration stays bound to the old thread.
- `/resume` in the same process switches back to the old thread.
- Inside the Codex sandbox the agent cannot see its host process, and the tmux socket refuses
  access. Only a command that runs outside the sandbox, as `ensure` does, can observe either.
- Codex has no supported interface that reports which thread its process has active now.

After each reset, every peer must learn the new name. A peer that sends to the old name reaches
the retired session. The predecessor keeps running until an operator stops it. The tmux session
name does not follow the peer name, so a person or a `peer-tmux` operator cannot match a
terminal to a bridge peer. On 2026-09-24 this cost three manual stops (codex-koinon-40, -ef,
-9e) and repeated peer listing refreshes in one day.

#144 made the cycled session retire its predecessor and rename its tmux session through the
`pickup` skill and the `reconnect` guide recipes. The alias, the verified rebind
and the name conflict rules are still missing from the runtime.

Only Codex has this problem. A Claude `/clear` keeps the Claude process and its peer name, and
Claude Code, not Koinon, owns that name.

## Acceptance criteria

1. **Stable alias.** Each Codex participant has a stable alias, derived from the family and
   the repository label (for example `codex-koinon`), in addition to its per-thread
   peer name. A Claude session and a Koinon sender can each send to the alias by name, with no
   new step on the sender's side.
2. **One holder.** The alias resolves to exactly one live registration. When no live
   registration holds it, a send to it fails with a clear error. It never resolves to two
   sessions, also under concurrent `ensure` runs of two threads of the same family and
   repository.
3. **Verified rebind at pickup.** The reset link from the old thread to the new thread is the
   pickup evidence of decision A (#141, item 3): the handoff records the old session ID, and
   the successor runs in the same tmux server and pane with another session ID. A `/resume` to
   another thread in that pane counts the same way. On that evidence the successor stops the
   predecessor, without asking. The alias moves to the successor when the predecessor holds it
   or when it is free. A live holder in another terminal keeps it; the predecessor still stops,
   and the result reports both (user decision, 2026-09-24). Direct user authorization that
   names the predecessor also allows the stop and the move.
4. **Host process is evidence only.** `ensure` records the host agent process (pid and kernel
   start time) of each registration and reports it. The same host process alone never moves
   the alias or stops a session. A session in another terminal, a session without tmux values
   in its handoff, a peer message, a retained process or a retained peer name only reports the
   predecessor and the stop command.
5. **Reversible, no inheritance.** The successor starts with its own checkpoint, inbox and work
   claims. The stop keeps the predecessor's inbox, checkpoint and claims in its own state
   directory; nothing is migrated. When the user resumes the predecessor's thread, `ensure`
   registers it again, and its pickup can take the alias back under criterion 3.
6. **tmux name follows the bridge name.** When the agent runs in tmux, `ensure` and the rebind
   name the agent's own terminal after its alias:
   - When the agent's pane is the only agent pane in its tmux session, rename the session,
     targeting it by session ID, and read the name back.
   - When the session holds more than one agent pane, do not rename the session; set the title
     of the agent's own pane and report it.
   - When another session already has the target name, report it and rename nothing.
   - Never rename another session or another pane. Outside tmux, do nothing.
7. **Reported.** `session.py ensure` and `status`, `bridge.py peers` and the `guide` output
   report the alias, whether this registration holds it, the recorded host process, and the
   tmux action taken or the reason none was taken.
8. **Documented.** `docs/INSTALL.md`, `PROTOCOL.md`, the installed `reconnect` guide topic, the
   `pickup` and `peer-tmux` skills, and `CHANGELOG.md` describe the alias, the rebind rule and
   the retirement rule, in the chunk that changes the behaviour.
9. Linux and macOS, Python 3.11 to 3.13.

## Constraints

- Claude senders resolve names through Claude Code's own session registry (the `name` field of
  `$CLAUDE_CONFIG_DIR/sessions/<pid>.json`), which Koinon does not control. The alias must
  therefore be addressable through that registry and through `bridge.py send` alike. Koinon
  never writes to a registry record that it does not own.
- A peer name is an address, not an identity. The alias carries no checkpoint, inbox, work
  claim or native identity. Work claims stay with the key that made them.
- Same-user security boundary. Ownership checks stay strict. The tmux and host observations
  run outside the agent sandbox through the normal approval mechanism, never by relaxing a
  check or a sandbox setting.
- A peer request never moves the alias, stops a session or renames a terminal.
- Peer addresses and registry socket paths stay unresolved (root `AGENTS.md`).
- Platform differences stay in `koinon/platform_support.py`. Standard library only. tmux is
  optional; its absence is a normal result.
- Tests use synthetic peers, temporary registry, state and tmux servers, and never touch the
  user's sessions, terminals or services.
- The live CLI behaviour cited above is the evidence base. A change of Codex behaviour
  that the build meets needs a new live test authorized by the user, not skill prose.
- Non-goals: an alias for Claude or DeepSeek sessions; detection of a host's active thread
  (no supported signal exists); role routing of work items (#140); the adoption package
  (#145); a release to `main`.

## Issues

- Delivers #141.
- Leaves #140 (role routing) and #145 (adoption package) to later sprints: each is separate
  from the bridge address.
