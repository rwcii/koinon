# Sprint 2026-10-06 — Go daemon — Decision

## Problem

Koinon is a Python standard-library runtime that starts processes for each agent session: a
bridge, a notifier and a session supervisor. Each repository with memory also has a memory
service. On 2026-10-06 one host ran 72 sessions, which made 216 session processes and one memory
service. Many of those sessions ended a day earlier, but their services still ran.

Agents in a sandbox cannot reach the runtime. Codex 0.160 with `workspace-write` refuses
`connect()` on a Unix socket with EPERM, and it refuses TCP unless network access is enabled for
all destinations. Since #166 every Codex and DeepSeek bridge command is marked `needs_approval`,
so each send, inbox read, acknowledgement and memory sync needs a maintainer approval.

Koinon is not the coordination point for all agent traffic. A Claude session sends to another
Claude session through Claude Code's own peer sockets, and Koinon never sees that message. Koinon
carries only messages that involve a Codex or DeepSeek session. No single record shows what the
agents sent, what arrived and what was handled.

The user has no oversight view. Session state, delivery state, memory heads and work claims are
visible only through separate commands of each agent.

Google replaced the Gemini CLI with the Antigravity CLI (`agy`). Koinon does not support it, and
it does not support OpenCode either. The maintainer added both families to this sprint on 2026-10-06.

The maintainer prefers Go to the Python constraint, and wants one binary that listens on local ports.

## Acceptance criteria

1. **One binary.** Koinon is one Go binary, `koinon`, built without cgo for Linux and macOS on
   amd64 and arm64. Installing it needs no Python and no other language runtime.
2. **One daemon per user.** Each user runs one Koinon daemon under the maintainer's service manager
   (systemd user unit on Linux, launchd agent on macOS). It serves every session and every
   repository of that user. A session is a record in the daemon, not a process.
3. **Loopback only.** The daemon listens only on loopback addresses and refuses a configuration
   that names any other address. A caller without the maintainer's secret, which only that user can
   read, is refused.
4. **All agent messages go through Koinon.** A Claude, Codex, DeepSeek, Antigravity or OpenCode
   session sends every message to another agent through Koinon. The daemon stores each message and
   records its delivery and acknowledgement state. Claude sessions use Koinon for coordination
   messages, also to other Claude sessions.
5. **Agent access without per-command approval.** Each agent family uses Koinon through MCP
   from inside its default sandbox, with no approval for each command beyond the agent's own
   tool approval. A family that cannot use MCP keeps a documented command path.
6. **Wake.** A content-free notice reaches an idle Claude, Codex, DeepSeek or OpenCode session through
   that family's channel after a message arrives for it. An Antigravity session gets its waiting
   messages at its next turn boundary, or sooner through a wake path that the spike proves.
   Nothing types into a terminal. Peer text never travels in a notice.
7. **Memory.** One memory store serves each Git common directory, with the snapshot, delta and
   acknowledgement behaviour of `PROTOCOL.md` and `docs/PARITY-MEMORY-DESIGN.md`.
8. **Work items.** Work records, advisory claims and events behave as the work item documents
   under `docs/` specify (schema 5).
9. **Dashboard.** A loopback web dashboard shows the sessions (name, family, repository, model,
   context, activity, terminal), the messages with their bodies and their delivery and
   acknowledgement state, the memory heads, the work claims, the service health and an audit
   log. From it the user can retire a session, release a claim, acknowledge or clear an inbox,
   send a message to an agent and start a Codex, Antigravity or OpenCode session through the launcher.
   Every action is recorded in the audit log. The dashboard is reachable only after a login
   through a link that the `koinon` command prints, and it refuses requests whose `Host` or
   `Origin` is not its own loopback address.
10. **Launcher.** `koinon` starts Codex, Antigravity and OpenCode sessions with the guarantees of
    `codex_launch.py` today, including the refusal of a start folder that holds another
    repository.
11. **Upgrade.** An upgrade from the current `main` release moves every inbox, memory store, work
    item and claim into the Go runtime without loss, on Linux and on macOS, then stops and
    removes the Python services. An uninstall preserves the state as today.
12. **Python removed.** The repository holds no Python runtime code. `README.md`,
    `PROTOCOL.md`, `docs/INSTALL.md`, the root `AGENTS.md` and the agent skills describe the Go
    runtime.

## Constraints

- **Non-goals:** access from other hosts or the LAN; sessions that Koinon hosts through ACP or
  `agy` stream-JSON; typing into terminals; the legacy bridge and notifier unit pair, the
  `codex_instructions.py` shim and the legacy notice formats; role assignment (#140); the
  adoption package (#145); a checkout resource key (#83).
- **Security boundary:** the same-user boundary stays. Loopback plus a secret readable only by
  the user replaces the file permissions of Unix sockets. Peer content is data, notices stay
  content-free, peer controls stay inert, thread targeting stays explicit, and a peer grants no
  permission. Message bodies appear only in the maintainer's own dashboard. An OpenCode server that
  Koinon starts or uses binds loopback only and requires a password, and Koinon calls only its
  prompt and status endpoints, never its permission or question endpoints.
- **Known limit:** an agent runs as the user and can do what the user can do. The audit log
  records administrative actions; the daemon does not try to tell an agent from the user.
- **Claude Code's peer protocol is Claude Code's.** The daemon follows the Unix socket protocol
  in `PROTOCOL.md`, including unresolved socket paths and the Linux and macOS differences, for
  the Claude wake path.
- **Platforms:** Linux and macOS are both required for every chunk, with real systemd and launchd
  checks where a chunk touches them. Platform differences live in one Go package.
- **Unverified facts that the first chunk must settle before any other chunk starts:** MCP use
  from inside the Codex, Claude, DeepSeek, Antigravity and OpenCode sandboxes; DeepSeek harness support for
  MCP; the Claude wake path from a Go sender; the Antigravity stop hook format and behaviour in
  `agy` 1.3.0, and its local language-server RPC as a possible wake path; the OpenCode session
  identity in MCP calls and its `prompt_async` API as the wake path.
- **Order:** the Python runtime stays installable and upgradable on `main` until the release that
  carries the upgrade of criterion 11. Every release must install and upgrade through its
  documented path.
- **Repository rules:** feature branches off `develop`, squash merges, signed DCO commits, the
  `check` skill before each push, and `CHANGELOG.md` for user-visible changes.

## Issues

- **Delivered:** #57 (CLI ergonomics), #82 (delivery outcome for the sender), #90 (stale
  notices), #162 (Claude guide for sending to Codex), #171 (peer discovery across PID
  namespaces).
- **Closed as obsolete when the Python runtime goes:** #154, #158, #174, #175.
- **Left to a later sprint:** #140, #145, #83 — not needed for this goal.
- **Outside this sprint:** #164 (Codex sandbox and `/tmp/.git`), #147 and #156 (tmux naming) —
  they do not depend on the runtime language.
