# Sprint 2026-10-08 — participants — Decision

## Problem

The Go runtime (develop `f88a42c`) gives each agent session a permanent peer name and, for a
session in a Git repository, one alias per family and repository (`internal/core/names.go`,
`assignNames`). The alias is the stable address that people and agents use, for example
`codex-koinon`. Five gaps remain.

1. **One address per family and repository.** Two Claude sessions, or two Codex sessions, can
   register in one checkout, but only one of them has a stable address. The other has only its
   peer name, which changes with each new native session. The maintainer wants two agents of one
   family to work in one repository at the same time, also when they pair program (#228).
2. **The address moves by renewal order.** When the holder expires or retires, the next
   qualifying registration or renewal of that family and repository takes the alias
   (`Store.Mutate`, `TestAliasHolderMoves`). With participants A and B in one checkout, A's reset
   gives A a new session that gets no alias while A's old registration is still active. When that
   old registration expires (15 minutes after its last renewal), B can take the alias before A's
   successor does, and messages for A then go to B (#228).
3. **A reset loses the participant's state.** A new native session starts with its own inbox,
   memory cursor and work claims. Messages that the old session did not read stay in its inbox. Its
   claims stay leased to its key until they expire (`docs/WORK-ITEMS-POLICY.md`), so the new
   session cannot continue its own work. Nothing links the new session to the old one; retirement
   exists only as a dashboard action (#141, items 2 and 3).
4. **Codex sessions share one daemon.** A Codex 0.161 session that the person starts with `codex`
   joins the user's shared Codex app-server daemon. The daemon, not the session's process in its
   pane, runs the session's MCP servers, starts a new one for each thread, and keeps them after the
   session ends. Codex removes `TMUX`, `TMUX_PANE` and `KOINON_LAUNCH_ID` from an MCP server's
   environment. Koinon therefore cannot tell which terminal such a session runs in, and even the
   agent's own shell reports the daemon's pane. `codex --no-daemon` runs the session on its own:
   its MCP servers are children of its process in the pane (`live-checks.md`, F1). Today the
   launched session's `koinon mcp` probably never receives its launch ID (#247), and each Codex
   sub-agent that calls a Koinon tool registers as its own session (`live-checks.md`, F2).
5. **Terminal naming misses two cases.** `internal/mcp/terminal_name.go` names the tmux terminal
   of a Claude session and of a launched session. A Claude background job (`claude bg-pty-host`)
   runs outside tmux, so it gets `not_in_tmux`, although the person watches it through
   `claude attach` in a tmux pane (#147, `live-checks.md`, F4). A `name_taken` result is final, so
   the name is not tried again when the other terminal goes away (#228). Directly started
   OpenCode and Antigravity sessions register but are not named (observed 2026-10-08).
6. **Two kinds of session.** Today a session can register with or without a launch record, so
   Koinon's view of its host, pane and role depends on how it was started. A Claude background
   job inherits the environment of Claude's background service, not of the shell that starts it,
   so a launch ID set in that shell does not reach the job (`live-checks.md`, F5).

Maintainer decisions that this sprint carries into the Go runtime:

- 2026-09-24 (#141, `docs/sprints/2026-09-24-stable-alias/decision.md`, criterion 3): a reset
  session in the same tmux pane takes its predecessor's place without asking.
- 2026-10-08: additional participants of one family get maintainer-assigned role suffixes.
- 2026-10-08: a successor takes the place of its predecessor, so the participant's state
  continues: "the old process is gone; a new process exists in its place and is registering".
- 2026-10-08: a Koinon session is an agent session started with `koinon <family>`, for every
  family. `koinon codex` starts Codex with `--no-daemon`. A direct start (`claude`, `codex`,
  `opencode`, `agy` without `koinon`) is an **islanded instance**: it does not register, it is not
  listed among peers or in the dashboard, and the Koinon tools refuse its calls with
  `not_launched` and the launcher command. Isolation of an agent is a supported use. This replaces
  #228's requirement that naming must not depend on the launcher alone.

## Acceptance criteria

1. **Participant addresses.** A participant is a family, a repository and an optional role. The
   participant without a role has the short alias `<family>-<label>`, as today. The maintainer
   gives a session a role when it starts it through the launcher (`koinon <family> --role
   <role>`); that participant's address is `<family>-<label>-<role>`. Linked worktrees of one
   repository share its participants. Every session keeps its own peer name as well. A Codex
   sub-agent thread (`thread_source` `subagent`) never holds a participant.
2. **One holder, never by order.** A participant has at most one holder, an active native
   session. Its address resolves to that session, or a send to it fails with a clear refusal. A
   session never becomes the holder in place of an active holder, except by criterion 3. When a
   participant has no active holder, a session of that participant becomes its holder only when
   exactly one active session qualifies. When more than one qualifies, the participant stays
   without a holder, the result reports the conflict, and the maintainer chooses the holder in
   the dashboard. Registration and renewal order never decide.
3. **Verified succession.** A new session becomes the holder in place of the active holder only
   on one of this evidence:
   - The new session runs under the same host process as the holder, with another native session
     ID: a launched Codex session after `/clear` or `/resume` starts a new user thread under the
     same Codex process.
   - The new session's host process runs in the same tmux server and pane as the holder's host
     process, and the holder's host process has ended: a new agent process started in that pane.
   - The maintainer chooses the new session in the dashboard.

   The host and the pane come from the session's launch record. The same family, repository,
   peer name or address alone is not evidence. A peer message never changes a holder.
4. **The participant's state continues.** The participant owns the messages sent to its address,
   their acknowledgements, its memory cursor and its work claims and leases. The holder acts for
   the participant, so a successor continues them as they are: it reads the unread messages, the
   memory deltas after the cursor, and its claims and checkpoints, with no transfer or acceptance
   step. Messages sent to a session's peer name stay with that session. Each acknowledgement and
   work event records the native session that made it.
5. **Fencing.** When a session stops being the holder, it is retired at once, in the same
   transaction. From then on, a call that acts for the participant (send as it, read or
   acknowledge its inbox, renew, update, release or finish its claims, acknowledge its memory
   cursor) is refused unless the caller is the current holder; this includes a custom consumer
   key, a registration retry and the command-line paths. A fenced session that calls again cannot
   register itself back into the participant; it gets only its own peer name, and the result says
   why.
6. **Launched sessions only.** Every Koinon session is started with `koinon <family>`, which
   records a launch and passes its launch ID to the session's `koinon mcp`: through the
   environment for Claude, OpenCode and Antigravity; through the Codex MCP configuration with
   `--no-daemon` for Codex (#247); through `--settings` for a Claude background job
   (`koinon claude --bg`, `live-checks.md`, F5). A launched session registers with its launch
   record, names its own terminal and qualifies for criterion 3. A direct start is islanded: it
   never registers, is never listed, and every Koinon tool call returns `not_launched` with the
   launcher command.
7. **Naming rules.** One agent pane in a tmux session: rename the session to the participant's
   address, or the peer name without one. More than one agent pane: set only the agent's own pane
   title. Never rename another session or pane; a nested agent or a shared MCP server never renames
   a terminal it does not own. A taken name is reported, nothing is overwritten, and the name is
   tried again at later renewals until it is free. Outside tmux, nothing happens.
8. **Claude background jobs.** `koinon claude --bg` starts a background job as a launched
   session. The runtime finds the tmux pane of the `claude attach` client of that job and applies
   criterion 7 to that pane. The pane is reported with the session.
9. **Reported.** The `peers` MCP tool, `koinon peers` and the dashboard show for each session its
   peer name, its participant address and whether it holds it, its role, the last succession
   result and the last naming result with its reason.
10. **Documented.** `PROTOCOL.md`, `docs/USAGE.md`, `docs/INSTALL.md`,
    `docs/WORK-ITEMS-POLICY.md`, the installed `koinon guide` output, the `pickup`, `handoff` and
    `peer-tmux` skills and `CHANGELOG.md` describe participants, roles, succession, fencing and
    terminal naming, in the chunk that changes the behaviour.
11. Linux and macOS.

## Constraints

- The root `AGENTS.md` rules apply: loopback-only listeners, same-user private state, identity
  from each family's native per-call source and never from tool arguments, inert peer controls,
  and no change to a cursor, checkpoint, secret or retained state outside the defined rules.
- A peer request never changes a holder, retires a session or renames a terminal.
- An upgraded store keeps every existing alias and its holder. An existing alias becomes the
  address of the participant without a role, so no recipient changes at the upgrade. Existing
  claims, inboxes and cursors keep their current owners. A session that was started directly
  keeps its registration until it expires, then it is islanded; a session started again with
  `koinon <family>` becomes the holder under criterion 2 when it is the only qualifier, or by the
  maintainer's choice.
- Process and tmux observations run in `koinon mcp` and the daemon, outside the agent sandbox.
  Platform differences stay in `internal/platform`. Builds stay `CGO_ENABLED=0`.
- Koinon does not read Codex's private state files, and it never stops or reconfigures the
  user's shared Codex daemon.
- Tests use synthetic sessions, temporary state and private tmux servers addressed with `-S`.
  No test touches the maintainer's sessions, terminals or services.
- Non-goals: work routing by role (#140), the skills package (#145), the checkout resource key
  (#83), and a release to `main`.

## Verified facts

`live-checks.md` records the live checks of 2026-10-08 (Codex 0.161.0, Claude Code 2.1.294,
Linux). A later CLI version that behaves otherwise needs a new live check, authorized by the
maintainer, before the plan changes. Not checked: Claude `/resume` inside a running process, and
macOS; the definition of done names the checks for both.

## Issues

- Delivers #228, #141 (items 2 and 3; items 1 and 5 are in the Go runtime already), #147 and
  #247.
- Leaves #140 (work routing by role) and #145 (skills package) to their own sprints: neither
  is part of an address or a terminal name.
