# Sprint 2026-10-08 — participants — Decision

## Problem

The Go runtime (develop `f88a42c`) gives each agent session a permanent peer name and, for a
session in a Git repository, one alias per family and repository (`internal/core/names.go`,
`assignNames`). The alias is the stable address that people and agents use, for example
`codex-koinon`. Four gaps remain.

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
3. **No successor link.** A reset starts a new native session. Codex `/clear` keeps the Codex
   process and its Koinon MCP server and changes the thread ID that each tool call carries
   (`docs/sprints/2026-10-06-go-daemon/spike.md`, fact 1; #141, live test of 2026-09-24). The
   new thread registers as a new session. The old registration stops renewing, keeps its alias
   until it expires, and stays listed. Retirement exists only as a dashboard action
   (`internal/core/dashboard_actions.go`). Nothing links the new session to its predecessor (#141,
   items 2 and 3).
4. **Terminal naming misses two cases.** `internal/mcp/terminal_name.go` names the tmux terminal
   of a Claude session and of a session that the Koinon launcher started. A Codex session that
   the person started directly connects and gets its name, but its terminal keeps the old name
   (#228). A Claude background job (`claude bg-pty-host`) runs outside tmux, so it gets
   `not_in_tmux`, although the person watches it through a `claude` client in a tmux pane (#147).
   A `name_taken` result is final, so the name is not tried again when the other terminal goes
   away (#228).

The maintainer decided on 2026-09-24 (#141, `docs/sprints/2026-09-24-stable-alias/decision.md`,
criterion 3) that a reset session in the same tmux pane retires its predecessor without asking,
and takes the address when the predecessor holds it or when it is free. On 2026-10-08 the
maintainer chose maintainer-assigned role suffixes for additional participants. This sprint
carries both decisions into the Go runtime.

## Acceptance criteria

1. **Participant addresses.** A participant is a family, a repository and an optional role. The
   participant without a role has the short alias `<family>-<label>`, as today. The maintainer
   gives a session a role when it starts it through the launcher (`koinon <family> --role
   <role>`); that participant's address is `<family>-<label>-<role>`. Linked worktrees of one
   repository share its participants. Every session keeps its own peer name as well.
2. **One holder, never by order.** An address resolves to exactly one active session, or a send
   to it fails with a clear refusal. A session never takes an address from an active holder,
   except by criterion 3. When an address is free (its holder expired or retired), it goes to a
   session of that participant only when exactly one active session qualifies. When more than one
   qualifies, the address stays free, the result reports the conflict, and the maintainer binds
   one session in the dashboard. Registration and renewal order never decide.
3. **Verified succession.** A new session takes its participant's address from the active holder
   at once, and the holder is retired, only on one of this evidence:
   - the same Koinon MCP server process served the holder and now serves the new session, and
     the holder made no call after the new session's first call (a Codex `/clear` or `/resume`
     in one process);
   - the new session's host process runs in the same tmux server and pane as the holder's host
     process, and the holder's host process has ended (a new agent process started in that pane);
   - the maintainer binds the session in the dashboard.

   The same family, repository, host process or peer name alone is not evidence. Two sessions
   that are active in one MCP server at the same time are not a succession: the address stays
   with its holder, and the result reports both. A peer message never moves an address or
   retires a session.
4. **Reversible, no inheritance.** Each native session keeps its own inbox, acknowledgements,
   memory cursor and work claims. Succession transfers none of them and releases no claim. A
   retired predecessor that calls again registers again, as today, and can take the address back
   only under criterion 3. A change of the pair's driver does not change any address or claim.
5. **Direct Codex terminal naming.** A Codex session that the person started without the
   launcher names its own tmux terminal, as a Claude session does, when the daemon verifies that
   its MCP server's parent is the Codex process in that pane. When that cannot be verified, the
   result says why and nothing is renamed.
6. **Naming rules.** One agent pane in a tmux session: rename the session to the published
   address. More than one agent pane: set only the agent's own pane title. Never rename another
   session or pane; a nested agent or a shared MCP server never renames a terminal it does not
   own. A taken name is reported, nothing is overwritten, and the name is tried again at later
   renewals until it is free. Outside tmux, nothing happens.
7. **Claude background jobs.** For a Claude background job, the runtime finds the tmux pane of
   the `claude` client attached to that job and applies criterion 6 to that pane. The peers
   listing and the dashboard show one session for the job and its attached client, not two
   unrelated peers.
8. **Reported.** The `peers` MCP tool, `koinon peers` and the dashboard show for each session
   its peer name, its participant address (held or not), its role, the last succession result
   and the last naming result with its reason.
9. **Documented.** `PROTOCOL.md`, `docs/USAGE.md`, `docs/INSTALL.md`, the installed `koinon
   guide` output, the `pickup` and `peer-tmux` skills and `CHANGELOG.md` describe participants,
   roles, succession and terminal naming, in the chunk that changes the behaviour.
10. Linux and macOS.

## Constraints

- The root `AGENTS.md` rules apply: loopback-only listeners, same-user private state, identity
  from each family's native per-call source and never from tool arguments, inert peer controls,
  and no change to a cursor, checkpoint, secret or retained state outside the defined rules.
- A peer request never moves an address, retires a session or renames a terminal.
- An upgraded store keeps every existing alias and its holder. An existing alias becomes the
  address of the participant without a role, so no recipient changes at the upgrade.
- Process and tmux observations run in `koinon mcp` and the daemon, outside the agent sandbox.
  Platform differences stay in `internal/platform`. Builds stay `CGO_ENABLED=0`.
- Tests use synthetic sessions, temporary state and private tmux servers addressed with `-S`.
  No test touches the maintainer's sessions, terminals or services.
- Non-goals: work routing by role (#140), the skills package (#145), detection of a host's active
  thread beyond the evidence in criterion 3, and a release to `main`.

## Unverified facts

Each fact needs a live check before the chunk that depends on it is built. A live check runs only
with the maintainer's authorization, in scratch sessions.

- **F1.** In an interactive Codex session that the person started directly, the Koinon MCP
  server's parent is the Codex process that runs in the tmux pane, and that server serves only
  the threads of that process. The spike checked `codex exec` only.
- **F2.** Codex sends tool calls from more than one thread through one MCP server at the same
  time (for example from a sub-agent), or it does not.
- **F3.** Claude `/clear` keeps the Claude process, its MCP server and the server's
  `CLAUDE_CODE_SESSION_ID`, so Koinon sees no new session (observed once, on 2026-10-08, in a
  Claude session of this repository). Claude `/resume` inside a running process does the same, or
  it starts new MCP servers.
- **F4.** For a Claude background job: which process runs the job's MCP servers, how the
  attached `claude` client in a tmux pane can be linked to that job from the process table and
  the job's sockets, and whether the attached client starts its own Koinon MCP server.

## Issues

- Delivers #228, #141 (items 2 and 3; items 1 and 5 are in the Go runtime already) and #147.
- Leaves #140 (work routing by role) and #145 (skills package) to their own sprints: neither
  is part of an address or a terminal name.
