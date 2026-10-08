# Sprint 2026-10-08 — participants — Definition of done

The sprint is done when every acceptance criterion of `decision.md` holds on `develop`, each
chunk merged with its part of this file, and the final live check passed.

## Tests

All tests use synthetic sessions, temporary state directories, ephemeral ports and private tmux
servers addressed with `tmux -S <socket>`. Process trees are synthetic (`Parents` and `Command`
test doubles) unless a test starts its own child processes. No test reads or changes the
maintainer's sessions, Codex or Claude configuration, or services.

### Store and daemon (`internal/core`)

- **Participants (criteria 1, 2).** Registration and renewal for: one session without a role
  (short alias); two sessions of one family in one repository with different roles (two
  addresses); the same in two linked worktrees of one repository (shared participants); a
  session with a role in a repository without a participant row (created); a Codex sub-agent
  session (peer name only, never a holder). Two sessions with no role: the first holds, the second
  does not; after the holder expires, exactly-one-qualifier takes it, two qualifiers leave it free
  with the conflict reported, and the result is the same whichever session renews first
  (renewal order reversed). Concurrent registrations of two qualifiers leave at most one holder.
  The maintainer's dashboard choice sets the holder and is audited.
- **Daemon admission (criterion 6).** `Store.Register` through HTTP, MCP and `koinon register`
  for each launcher family: no launch ID, an unknown launch ID, and a launch of another family or
  directory are refused with `not_launched`, and nothing is registered. `koinon register --as
  deepseek:ID` still registers. Upgrade: a session that a develop `f88a42c` store holds without a
  launch record is refused at each renewal (repeated renewals through `/v1/sessions/renew`, as an
  old `koinon mcp` would make them), expires at the expiry it had at the upgrade, and keeps its
  inbox, acknowledgements, cursor and claims.
- **Launch binding (criterion 6).** A foreground launch: a caller whose host process is not the
  launch's host is refused. A background launch, in the order of F5: job A started with launch
  L1, then job B started directly in the same directory, which inherits L1 from the service: B is
  refused with `not_launched` and A registers. A job that registers before the launcher records
  its job ID is refused with `launch_pending`, registers nothing, and registers at its next tool
  call after the record; a launch whose `claude --bg` failed is retired and admits nothing.
- **Upgrade (constraint).** A store written by develop `f88a42c` with aliases, holders, inbox
  messages, acknowledgements, memory cursors and claims opens after the schema migration with
  every alias held by the same session, every message, acknowledgement, cursor and claim
  unchanged, and the alias rows turned into participants without a role. A migration failure
  leaves the old store untouched (existing migration rule).
- **Participant state (criterion 4).** A message sent to an address is in the participant's
  inbox; the holder reads it and acknowledges it; the acknowledgement records the native session.
  A message sent to a peer name stays with that session. After succession, the successor reads
  the unread participant messages from the old acknowledgement point, syncs memory from the old
  cursor, and renews, updates, releases and finishes the old claims with their existing
  generation; no claim moves to `open` and no lease is restarted by the change of holder.
- **Fencing (criterion 5).** After a change of holder, each call of the former holder that acts
  for the participant is refused with `stale_holder` and changes nothing: send as the
  participant, inbox read and ack of the participant inbox, memory sync and ack with the
  participant cursor, claim renew, work update, release and finish, also with a custom consumer
  key, through MCP, HTTP and the command line, and as a keyed retry of a request first made before
  the change. A fenced session that registers again gets only its peer name. The change of
  holder and the retirement of the former holder commit in one transaction; an injected failure
  after the first write leaves both unchanged. The fence persists: after a daemon restart, after
  repeated registration of the fenced session, and after the successor expires, the fenced
  session is not a qualifier and does not become the holder; a delayed call of the fenced session
  inside the 30-second guard is refused; a same-host registration of the fenced session after the
  guard is refused with `fenced` and changes nothing; only the maintainer's choice makes it the
  holder again and removes the fence.
- **Checkout roles (#83) with participants.** Before and after a succession: checkout status
  shows the current holder's exact peer for a `participant:<address>` writer; another agent's
  request reaches that holder and never the fenced former holder; the holder's own request is
  refused as a self-request; a `work_release` handoff followed by the requester's `work_start`
  creates a new generation. A custom consumer that is not a participant key stays
  `writer_unaddressable`.
- **Succession (criterion 3).** Accept: same host process with a new native session ID; same
  tmux server and pane with the former host process ended. Refuse and report: same host process
  with a sub-agent thread; same host process while the former holder made a tool call in the last
  30 seconds; a renewal-timer call (no succession without a tool call); same pane with the former
  host process still running; another pane; and after a `holder_active` refusal, the successor's
  first tool call after 30 seconds takes the participant;
  same family and repository only; no pane recorded (a launch outside tmux); a peer message that
  asks for it. A former holder that calls after a refused succession keeps the participant.

### MCP server and terminal naming (`internal/mcp`)

- **Launched sessions only (criterion 6).** For each launcher family, a `koinon mcp` with a known launch
  ID registers with its launch record; with no launch ID, or an unknown one, every tool call
  returns `not_launched` with the launcher command, and nothing is registered or listed. A
  launched Codex session (`--no-daemon`, launch variables through `env_vars`) and a
  `koinon claude --bg` job (launch variables through `--settings`) register with their launch
  records. `thread_source` `subagent` registers without a participant.
- **Naming (criteria 7, 8).** On a private tmux server: one agent pane renames its session to the
  address; two agent panes set only the own pane title; a nested agent and a foreign pane rename
  nothing; a taken name reports `name_taken`, overwrites nothing, and is renamed at a later
  renewal after the other session is gone; outside tmux nothing happens. A synthetic Claude
  background job (no `TMUX`) with a `claude attach <short ID>` client process in a private tmux
  pane gets that pane named; a client whose short ID does not match the job's session ID, and two
  clients for one job, rename nothing and report why.
- **Reporting (criterion 9).** `peers` (MCP and `koinon peers`) and the dashboard session view
  show the peer name, address, holder flag, role, last succession result and last naming result.

### Launcher (`internal/launcher`)

- `koinon codex` builds a Codex command line with `--no-daemon`, the `mcp_servers.koinon.env_vars`
  override for every variable `koinon mcp` reads, and the role when given. `koinon <family>
  --role <role>` validates the role (lower-case letters, digits and hyphens, 1 to 24 characters,
  not a peer-name suffix pattern) and records it in the launch record. Argument construction is
  tested without starting a CLI.

## Edge cases that must have tests

- A role that would make an address equal to an existing peer name or alias.
- A repository label of 32 characters plus a role (address length bound).
- A participant whose holder is retired by the dashboard action, then a new qualifier.
- A daemon restart between the change of holder and the former holder's next call.
- Storage at the ordinary ceiling: a change of holder still commits (control write), a message to
  an address is refused with `capacity`.
- Clock skew: lease and expiry decisions use the daemon clock only.

## Integration points exercised for real

- Private tmux servers on Linux and macOS CI (tmux is installed on both runners), for every
  naming test.
- The schema migration from a committed develop `f88a42c` fixture store.
- No service manager is touched; the guarded native lifecycle workflow runs unchanged and must
  stay green.

## Final live check (maintainer-authorized, throwaway sessions)

Before the last chunk's pull request is approved:

1. Linux: `koinon codex` in a scratch repository on a private tmux server: the session registers
   with its launch record, holds the short alias and names its tmux session; `/clear` keeps the
   participant with the new thread as holder, the predecessor is retired, and a message sent to the
   address before the `/clear` is read by the successor. A Codex sub-agent of that session does not
   take the address.
2. Linux: a direct `codex` start and a direct `claude` start are islanded: each tool call
   returns `not_launched`, and neither session appears among peers or in the dashboard.
3. Linux: two Claude sessions in one scratch repository, one launched with `--role review`: two
   addresses, each pane named after its own address; ending the first and starting a new Claude in
   the same pane gives the new session the first address and continues its claim.
4. Linux: a `koinon claude --bg` job with `claude attach` in a private tmux pane: the job registers with its launch record and the pane is named.
5. macOS: items 1 and 3, run by the maintainer or on a macOS host the maintainer names.

Each check records the CLI versions. Scratch configuration is restored byte for byte.

## Gate for each chunk

- The `check` skill: `go vet ./...`, `go vet -tags native ./...`, `go test -race ./...`,
  `git diff --check`, the four `CGO_ENABLED=0` builds and the private-content scan.
- CI: the `Tests` workflow (Go on Linux and macOS) and `Native Go lifecycle evidence`, both green
  on the pull request's head before the peer sign-off.
- Documentation of the behaviour in the same chunk (`decision.md`, criterion 10).
