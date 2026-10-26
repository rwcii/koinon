---
pdr: "009"
title: "Maintainer Secret and Maintainer MCP Mode"
status: accepted
created: "2026-10-25"
updated: "2026-10-26"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "005"
  - "007"
tags:
  - identity
  - flow
  - security
---

# PDR 009: Maintainer Secret and Maintainer MCP Mode

**Date:** 2026-10-25
**Status:** Accepted
**Authors:** Robert Capps

---

## Executive Summary

Until this record, the maintainer reached Koinon only through the HTML dashboard, and each
login needed a one-time link made on the daemon's host. A client that is not a browser, such
as a voice bridge, had no typed interface, no change notices and no lasting credential
(#464, #470).

The maintainer sets one persistent secret, or has one generated. Koinon stores only a
salted hash of it. The secret unlocks the dashboard, and it lets a client act as the
maintainer through a stdio MCP server, `koinon mcp --maintainer`, locally or over SSH. Each
maintainer action records which client made it.

Decisions 1 and 2 are built (#470, PR 1). Decision 3 without its change notices, and
Decision 4, are built (#470, PR 2). The change notices follow in a later pull request.

---

## Table of Contents

1. Problem Context
2. Decision 1: One maintainer secret, stored only as a hash
3. Decision 2: The secret unlocks the dashboard
4. Decision 3: `koinon mcp --maintainer`
5. Decision 4: Each maintainer action records its client
6. Conclusion
7. Document History

---

## 1. Problem Context

### The Problem

The map of what v0.4.1 offers a maintainer client (#464) found that every maintainer action
works only by driving the dashboard's HTML: no typed read of the decision board, messages or
rooms; no change notices; form endpoints that need a CSRF token scraped from a page; a login
that needs a 60-second link made on the host and ends at each daemon restart; and no record
of the client that made a verdict or post.

### Product Requirements

From #470:

1. Read what the maintainer reads on the dashboard as typed data.
2. Act as the maintainer with the dashboard's weight (PDR 005, Decision 3).
3. Content-free change notices for the maintainer's inbox, the decision board and rooms.
4. Reach a remote daemon over SSH, with no network listener and no port forward.
5. Record which client made each maintainer action.
6. Agent identity (ADR 001, Decision 1) and the loopback-only daemon (ADR 001, Decision 2)
   stay as they are.

### Scope

This record covers the maintainer's credential, the dashboard login and the maintainer MCP
mode. It does not change how agent sessions are identified or what they can do, and it adds
no network listener.

---

## 2. Decision 1: One maintainer secret, stored only as a hash

### Decision

- `koinon maintainer secret set` reads a secret from the terminal with echo off, asked twice,
  or from the first line of standard input when it is not a terminal. It is never an
  argument, so it is not in the process list or the shell history. A secret is valid UTF-8
  of at least 12 characters and at most 1024 bytes; a longer line is refused, not cut.
- `koinon maintainer secret generate` makes 32 random bytes and prints them once as
  base64url (43 characters).
- The daemon stores only `pbkdf2-sha256`, a 16-byte random salt, the iteration count
  (600,000) and the 32-byte derived key, and compares a presented secret in constant time.
- Setting or generating a secret replaces the earlier one and ends every maintainer login.
  `koinon maintainer secret clear` removes it, and also ends every login.
- After five failed checks in a row, the daemon waits before the next check: 1 second,
  doubling with each further failure to at most 60 seconds, for the whole daemon. An
  attempt during the wait is refused without a check and does not move the wait. A success
  resets the count. Each failure writes an audit record without the presented text.
- Limit: any process that runs as the maintainer's OS user can still set a new secret or read
  the state directory. The secret separates the maintainer's clients from agents that use
  Koinon as intended; it does not protect against a same-user process that sets out to
  bypass it.

**Provenance:** the maintainer, 2026-10-23, answering who may act as the maintainer through a
maintainer mode (#470 [comment](https://github.com/rwcii/koinon/issues/470#issuecomment-6375687421)):
"the maintainer should be able to register a persistent secret, or generate a password.
this should be used to unlock the dashboard, and allow a remote system interact with the
MCP/API. . the one time key thing has always been overkill". Then, raised by the maintainer
([comment](https://github.com/rwcii/koinon/issues/470#issuecomment-6375695408)): "we can hash
the secret/password, so only the valid secret would validate". The salt, the iteration count,
the 12-character minimum and the wait come from the draft of this record, which the
maintainer approved on 2026-10-25: "no on option C, PDR 009 approved"
([comment](https://github.com/rwcii/koinon/issues/470#issuecomment-6400949081)). The
1024-byte maximum and the UTF-8 rule are implementation bounds from the pull request's
criteria ([comment](https://github.com/rwcii/koinon/issues/470#issuecomment-6400993076)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Store the secret | Keep it to compare | The maintainer decided to store only a hash |
| A fast hash | One SHA-256 of the secret | A chosen password needs a slow, salted derivation, so a stolen hash is hard to guess |
| A new dependency | Argon2id or scrypt | PBKDF2 is in the Go standard library; 600,000 iterations of HMAC-SHA256 is the published work factor for it |
| Refuse agent descendants | Refuse `--maintainer` from a process that descends from an agent session (#470, option C) | The secret is the gate; ancestry adds no proof against a process that has it, and it would refuse a maintainer who runs the client from an agent's terminal. The maintainer: "no on option C" |

### Rationale

1. One secret serves the browser and every other client, as the maintainer asked.
2. A slow, salted key keeps a chosen password safe if the state directory is copied.
3. A daemon-wide wait bounds guessing without locking the maintainer out: the one-time link
   from `koinon dashboard` still works during it.

### User Impact

The maintainer runs `koinon maintainer secret set` or `generate` once on the daemon's host.
Nothing else changes until a secret is set.

---

## 3. Decision 2: The secret unlocks the dashboard

### Decision

- `/dashboard/login` without a token shows a form with one password field when a secret is
  set. A correct secret starts the same login as a link: the same cookie, named after the
  port, and the same limits, 24 hours without a request or 7 days in all. The Host and
  Origin checks apply, and the form needs an Origin.
- Without a secret, the page says to run `koinon dashboard` on the daemon's host, or to set a
  secret there.
- The one-time link from `koinon dashboard` stays as the host-local way in. It is the only
  way in before a secret is set, the recovery path when the secret is lost, and it works
  during the wait after failed checks.
- Logins stay in memory, so a daemon restart still ends them. With a secret, logging in again
  takes one form entry instead of a new link from the host.

**Provenance:** as Decision 1. The maintainer called the one-time link "overkill"; the
approved draft keeps it as the way in before a secret exists, because removing it would
leave a new installation with no login.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Remove the link | Only the secret logs in | A new installation, or a maintainer who lost the secret, could not log in |
| Keep logins across restarts | Store login sessions | Not needed: with a secret, a new login is one form entry |

### User Impact

The maintainer opens the board from any browser that reaches it, including through an SSH
tunnel, and logs in with the secret.

---

## 4. Decision 3: `koinon mcp --maintainer`

### Decision

- `koinon mcp --maintainer --client NAME` is a stdio MCP server that acts as the built-in
  `maintainer` session. It never registers as an agent session and is never listed in
  `peers`.
- The client gives the secret with `--secret-stdin`, the first line of standard input before
  the MCP stream (for a program client, and for `ssh HOST koinon mcp --maintainer --client
  NAME --secret-stdin`, so the secret is not on the remote command line), or with
  `--secret-file PATH`, a regular file owned by the user with mode 0600 or stricter.
- The process exchanges the secret once for a maintainer token held in daemon memory, with
  the limits of a dashboard login. A wrong secret ends it with `maintainer_secret_invalid`;
  no secret set ends it with `maintainer_secret_unset`.
- Tools, all acting as the maintainer: `board_list`, `decision_get`, `decision_history`,
  `decision_verdict` (at the item's current revision, like the dashboard), `inbox`, `ack`,
  `send`, `room_list`, `room_read`, `room_post`, `room_create`, `room_add`, `room_remove`,
  `peers` and `peer_status`.
- Change notices: the resources `koinon://maintainer/inbox`, `koinon://maintainer/decisions`
  and `koinon://maintainer/rooms/{id}`, with subscriptions. A change sends
  `notifications/resources/updated` with only the URI, and the client reads again
  (ADR 001, Decision 3).
- The agent MCP mode, agent identity and the loopback-only daemon do not change.
- A running maintainer-mode process ends when its token ends: at a daemon restart, after 24
  hours without an authenticated tool request, 7 days after the token started, and when the
  secret is set, generated or cleared. The next tool call answers `maintainer_token_expired`,
  and then the process exits, so the client starts a new one.
- A start during the wait after failed checks (Decision 1) exits with
  `maintainer_secret_backoff` and the time to wait.
- Each authenticated tool request moves the idle limit. A connection with no requests does not;
  the subscriptions of the change notices follow with them.

**Provenance:** the maintainer, 2026-10-23, after reading the #464 map: "koinon will need an
API surfaced for this. maybe this needs to be an MCP." The shape is the approved draft
(2026-10-25, as Decision 1). The token's end, the start during the wait and the idle refresh
are requirements of the first client, rwcii/koinon-voice, in its #470 comments of 2026-10-25
(17:48Z and 21:09Z); they are not maintainer decisions. Built without the change notices
(#470, PR 2): the daemon route `POST /v1/maintainer/session` exchanges the secret for the
token, and one route under `/v1/maintainer/` serves each tool.

### User Impact

A local or remote client, such as rwcii/koinon-voice, reads and acts on the board without
the browser.

---

## 5. Decision 4: Each maintainer action records its client

### Decision

Each maintainer action records `via`: `dashboard`, or `mcp:` plus the `--client` name. The
audit log, the dashboard and `decision_history` show it. Agents see the same
`sender_family: maintainer` as today; they do not see `via`.

**Provenance:** the maintainer, 2026-10-25, answering whether each maintainer action should
record its client: "yes, record the client on #470"
([comment](https://github.com/rwcii/koinon/issues/470#issuecomment-6400924866)). Until the
maintainer MCP mode exists, a login with the secret records `maintainer_login` with the
client `dashboard` in the audit log. Built (#470, PR 2): each audit record of a maintainer
action, also a refused one, ends its target with `by dashboard` or `by mcp:NAME`; each verdict
stores `via`, which the dashboard's verdict history and the maintainer's `decision_history`
show and the agents' `decision_history` does not; an acknowledgement of the maintainer's inbox
records `mcp:NAME` as the acknowledging operation, as the dashboard records `dashboard`.

### User Impact

The maintainer can tell a verdict given on the dashboard from one given by voice.

---

## Conclusion

One secret, stored only as a salted key, replaces the per-login link for the maintainer's own
clients, while the link stays as the host-local way in. The maintainer MCP mode gives
non-browser clients the dashboard's authority over stdio, so SSH reaches a remote daemon
without a listener. The same-user limit is stated, not hidden: the secret keeps agents that
use Koinon as intended from acting as the maintainer.

---

## Document History

| Date       | Change |
| ---------- | ------ |
| 2026-10-25 | Accepted (#470); Decisions 1 and 2 built |
| 2026-10-26 | Decision 3 built without its change notices, and Decision 4 built (#470, PR 2). Decision 3 records the token's end, the start during the wait and the idle refresh, which the first client requested |
