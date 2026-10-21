---
adr: "001"
title: "Native Identity and Local Runtime Trust"
status: draft
created: "2026-10-12"
updated: "2026-10-19"
parent_adr: ""
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related: []
tags:
  - architecture
  - security
  - auth
  - api
  - integration
  - persistence
---

# ADR 001: Native Identity and Local Runtime Trust

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon is one daemon per user that carries messages, memory and work items between that user's
agent sessions. This record describes how the Go runtime currently decides who a caller is and
whom it trusts. Identity comes from each agent's own per-call metadata. The daemon serves only
loopback callers that hold a private same-user secret, and keeps all state in one store. Wake
notices carry no peer text, and provider socket paths stay literal. The dashboard has its own
browser login and request checks. The upgrade from the Python runtime runs that runtime's own
uninstaller.

Every decision here is shipped behavior with no maintainer decision found, so the record is a
draft: it states what Koinon currently does and why.

---

## Table of Contents

1. Problem Context
2. Decision 1: Identity from native per-call metadata
3. Decision 2: Same-user loopback daemon with one private store
4. Decision 3: Content-free wakes and literal provider socket paths
5. Decision 4: Dashboard login and request checks
6. Decision 5: Upgrade through the previous release's uninstaller
7. Statements this record does not adopt
8. Conclusion
9. Document History

---

## 1. Problem Context

### The Problem

The Python runtime spoke over Unix sockets, which sandboxed Codex could not reach. The Go runtime
replaced it with one daemon per user, reached through `koinon mcp` and the `koinon` commands. It
has to know which session calls when a model can write any argument, who may reach the daemon,
what a peer's message may cause, how a browser reaches it, and how a Python installation moves
over. The answers were spread across AGENTS.md, sprint plans, README.md and PROTOCOL.md, partly
as blanket statements with no reason.

### Requirements

1. A tool call is attributed to the session that made it, not to a session the model names.
2. Only the user's own processes on the same host reach the daemon.
3. A peer's message reaches the recipient as data; it does not run or configure anything.
4. The dashboard can act as the maintainer without letting another web origin act through it.
5. An upgrade, a refusal or a failed write keeps stored data.

### Scope

Covers caller identity, the daemon's trust boundary and store, wake content, dashboard checks
and the Python upgrade. Registration rules are PDR 001, wake delivery and terminal input PDR 002,
installation and recovery PDR 003.

Inventory rows: C006, C007, C044, C047, C053, C058, C063, C064, C065, C066, C067, C068.

---

## 2. Decision 1: Identity from native per-call metadata

### Decision

Koinon currently takes the calling session from the agent's own per-call source, not from tool
arguments: Codex `_meta.threadId` (client named `codex…`), Antigravity
`_meta["antigravity.google/conversation_id"]`, Claude `CLAUDE_CODE_SESSION_ID` (client
`claude-code` whose process is the server's parent), the OpenCode plugin's `koinon_session`
argument (client `opencode` only) and Grok `GROK_SESSION_ID` (client `grok-shell-koinon` whose
parent is a Grok terminal process, not Grok's leader; #254). A call whose arguments hold `caller`, `family`, `id`, `as`,
`session` or `session_id` is refused with `identity_unavailable`. DeepSeek has no MCP identity
source yet and uses the `koinon` commands with `--as`.

On that identity, a caller reads and acknowledges only its own inbox and the participant inbox it
holds. An acknowledgement only moves forward, and a memory snapshot is acknowledged only after
every page was issued.

Koinon also reads native status metadata, read-only, where the agent publishes it: the session's
own Codex rollout file under `CODEX_HOME/sessions` (model, context window, token counts, task
start and end, only when its first record names the session; no conversation text is kept), Claude
Code's registry record for its parent process, OpenCode's status endpoint and the Antigravity
Stop hook. `koinon codex` starts Codex with `--no-daemon` and passes its launch variables through
`-c` overrides for that launch, so the launched CLI hosts its own MCP servers in its pane. Koinon
does not use, stop or change the user's shared Codex app-server or its configuration file.

**Provenance:** Shipped in PR #186 (merge commit e2bc2f3); inbox acknowledgement in PR #184
(11fdabf); memory acknowledgement in PR #188 (3cbe830); rollout reading in PR #193 (a19bc2b);
`--no-daemon` in PR #263 (fec77b1). No maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Caller in arguments | The model passes `caller` on each call | A model can name any session and read or acknowledge its inbox |
| Environment of the MCP server | Read identity from the server's variables | One server can serve several sessions; Codex removes variables from MCP servers |
| Shared Codex app-server | Let launched Codex join the user's daemon | Its MCP servers lose `TMUX` and the launch ID and report the daemon's pane |
| Edit `config.toml` | Write `--no-daemon` or the variable list into the Codex configuration | Changes every Codex session of the user, not only launched ones |

### Rationale

1. Each family already sends a session ID that the model cannot set.
2. Inboxes, memory cursors and work claims are keyed by that identity; a wrong one would act on
   another session's state.
3. Model, context and activity are useful only when they come from the agent itself.
4. Trade-off: the command path takes `--as`, so any process of the user that holds the secret
   can act as any session there (Decision 2).

---

## 3. Decision 2: Same-user loopback daemon with one private store

### Decision

Koinon currently runs one daemon per user. It listens only on literal loopback addresses
(`127.0.0.1`, `[::1]`) and refuses any other listen address. Every API request needs a bearer
256-bit secret from the state directory, and a request with an `Origin` header or a non-loopback
`Host` is refused. The state directory is mode 0700 and owned by the user; state files are opened
without following links, mode 0600 and owner-checked. macOS system mode runs the same daemon as
the user through the plist's `UserName`.

All state is one SQLite database in that directory, with one memory and work store per Git
common directory, so worktrees share their repository's store. The daemon holds an exclusive
lock on `daemon.lock`; a second daemon on the same directory exits. A capacity or validation
refusal changes nothing. A failed log proof blocks writes with `storage_blocked`, reads continue,
and `koinon recover` clears it. Upgrade and uninstall keep the state. Runtime data stays there;
the repository holds code and synthetic fixtures, and `koinon mcp` does not log a secret, session
ID or message body.

**Provenance:** Shipped in PR #180 (d7a7901) and PR #188 (3cbe830); lock exit in PR #300
(5abb46b); system mode in PR #301 (496ec52). No maintainer decision found. The 2026-10-06 Go
sprint plan says loopback plus a user-only secret replaces Unix socket permissions; that is
agent-written plan text.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Unix sockets | The Python runtime's transport | Sandboxed Codex refused `connect()` on them |
| Other interfaces or `localhost` | Listen beyond loopback, or bind a name | Exposes state beyond the host; a name can resolve elsewhere |
| Store per worktree | Key memory by checkout path | Worktrees of one repository would not share memory |
| Fresh store or second daemon | Start new state where the current one refuses or is unreachable | Its sessions and history are separate, so peers do not see them |

### Rationale

1. Loopback TCP works from the agents' default sandboxes where Unix sockets did not.
2. The private secret limits callers to processes that can read the user's state.
3. One store gives one record of what was sent, delivered and handled; a refusal names its
   recovery path instead of losing data.
4. Trade-off: an agent runs as the user and can read the secret. The audit log records
   administrative actions; the daemon does not tell an agent from the user.

---

## 4. Decision 3: Content-free wakes and literal provider socket paths

### Decision

Koinon currently wakes a recipient with a fixed notice that names only the inbox, JSON-quoted,
and its sequence range, and asks it to read its Koinon inbox. No peer body or provider output
enters a notice, a shell command or a runtime configuration. Codex gets the notice as an argument
of `codex queue`, without a shell. Daemon notices to the maintainer hold fixed text, IDs, names
and reason codes. Work item references are stored strings that Koinon does not fetch or run.

For the Claude wake, Koinon checks the socket's owner, mode and kernel UID/PID, and resolves
directories only to compare them with the allowlist. It hashes the literal, unresolved socket
path to find Claude's key file. The tmux socket directory is kept as tmux names it.

**Provenance:** Shipped in PR #198 (15df5ea); tmux socket directory in PR #271 (5751938). No
maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Body in the notice | Push the text into the recipient's prompt | Peer text would reach the model outside the inbox and its acknowledgement |
| Shell-built wake commands | Run the notice through a shell | A quoting fault would let content run |
| Canonical socket paths | Resolve links before hashing (macOS `/tmp` is a link) | Claude names its key from the literal path; a resolved path finds no key |

### Rationale

1. The inbox stays the one place where a message is read and acknowledged.
2. A notice with only IDs and numbers carries no instructions.
3. The provider's own naming decides the key lookup, so Koinon matches it.

---

## 5. Decision 4: Dashboard login and request checks

### Decision

Koinon currently serves the dashboard on the same listener with its own login. `koinon
dashboard` gets a one-time 256-bit link token that works once within 60 seconds. Its use sets an
`HttpOnly`, `SameSite=Strict` cookie scoped to `/dashboard/`; the daemon keeps only hashes in
memory, so a restart ends every login. A login ends after 24 hours without a request or 7 days
after it was made, and the cookie's `Max-Age` is those 7 days (#435). A request is refused
unless `Host` is a literal loopback IP with a port (`127.0.0.1:PORT` or `[::1]:PORT`, not
`localhost`) and any `Origin` is `http://` plus that `Host`. The port need not be the listener's, so an SSH tunnel from another
local port reaches the dashboard. A request other than `GET` or `HEAD` also needs a CSRF token
derived from the session. Responses carry a strict content security policy and deny framing.
Each administrative action writes an audit record.

**Provenance:** Shipped in PR #193 (a19bc2b); actions and audit log in PR #202 (ed07677). No
maintainer decision found. Until #407, `Host` had to be the address of the listener that
accepted the request, port included. #407 records the maintainer's report on v0.3.1: "on the
dashboard host, I can't even remap the port in the ssh forwarding level.
http://127.0.0.1:47670/dashboard/login?token=... results in a "foreign host" error". The tunnel
forwarded local port 47670 to the remote daemon's 47671. Accepting any port is the fix that #407
proposes; no maintainer decision on it found. Until #435 a login ended after 30 minutes without a
request or 12 hours after it was made, with a browser-session cookie. #435 records the
maintainer's report of 2026-10-21, made unprompted, not in answer to a question: "also
address the dashboard timeout, you seem to have added a shorter timeout, as I frequently have
to create a new link". The intent: a login lasts long enough that a new link is rarely needed.
The limits had not changed since #193. The 24-hour and
7-day limits are the fix that #435 proposes; no maintainer decision on the values found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| API secret in the browser | Reuse the bearer secret | Page scripts could read and replay it |
| No login on loopback | Trust every local browser request | Any web page can send requests to a loopback port |
| Accept `localhost` | Match a host name | Allows DNS rebinding |
| Match the listener's port | `Host` must be the accepting listener's address, port included | Refuses an SSH tunnel from another local port (#407), and adds nothing against DNS rebinding |

### Rationale

1. Other sites' pages can reach loopback ports, so the dashboard checks host, origin and token,
   while the API refuses browsers outright.
2. Message bodies are shown there, so a login gates the view.
3. A DNS-rebinding page reaches the daemon under its own host name, never under a literal IP, so
   a literal loopback IP with any port keeps that protection. A browser takes `Host` and
   `Origin` from the URL it opens, so through a tunnel both name the tunnel's port.

### Trade-offs Accepted

Browsers do not separate cookies by port. Two daemons opened under the same IP, such as a local
daemon and a tunnel to another host's daemon, share the `koinon_dashboard` cookie, so a login to
one replaces the browser's login to the other. Opening one under `127.0.0.1` and the other under
`[::1]` keeps both.

---

## 6. Decision 5: Upgrade through the previous release's uninstaller

### Decision

`koinon upgrade --from-python` currently accepts only a Python installation that honors the
upgrade marker and has its own installed `scripts/uninstall.py`. It stops the Python services,
imports their state into a staging database and swaps it in only when fully verified. It then
runs that installed `uninstall.py` from the installation prefix, with `PYTHON*` variables
removed, and installs and starts the Go service. A failure before the swap restores the Python
runtime; a failure after it keeps the import and resumes. The Python state tree stays as a backup.

**Provenance:** Shipped in PR #205 (f65ad6e). No maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Go removes Python artifacts | Reimplement the uninstall in Go | Guesses at services and files the Go repository no longer describes |
| Run code from a checkout | Use Python modules from a source tree | The checkout no longer carries the Python runtime |

### Rationale

1. The installed release knows its own services and guidance sections.
2. Importing before removal means a failed import loses nothing.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C006 | "Koinon does not read Codex's private state files" | 2026-10-08 participants sprint `decision.md` | Contradicts shipped behavior: `koinon mcp` reads the session's own rollout metadata (Decision 1); no source found for a blanket read ban |

---

## Conclusion

Koinon currently trusts the native agent for identity, the user account for access and a browser
login for the dashboard. Peer content stays in the inbox, and provider paths are used as the
provider names them. One store holds all state, and upgrades and refusals keep it. The main cost
is that any process of the user can act as any session, because the boundary is the user, not the
agent.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Added Grok Build's identity source (#254 chunk 02) |
| 2026-10-19 | Decision 4: dashboard `Host` is a literal loopback IP with any port (#407) |
