# Sprint 2026-10-12 — Grok agent family — Definition of done

Read `decision.md` first. The numbers below refer to its acceptance criteria.

## Where Grok runs

The product supports Grok Build installed locally on the user's machine, the same way as the
other families. Koinon contains no container support for Grok: no image, no container launcher
and no container-specific behavior.

For this sprint's own development, Grok Build does not run on the maintainer's machine outside
a container. The maintainer decided this on 2026-10-12 and accepted the Docker-root risk (#254
comments 6116272046 and the scope clarification that follows it). The container is a test bed
that stands in for a user's machine: Grok Build and a Koinon daemon run side by side inside it,
as they would on that machine. The container recipe stays outside the repository. Every live
Grok session in this sprint runs in a container that:

- runs as the maintainer's UID, with all capabilities dropped and `no-new-privileges`;
- mounts no part of the home directory, no repository, no host Koinon state or secret and no
  Claude socket; a repository it needs is a fresh synthetic one inside the container;
- has no route to host loopback services (`--network host` is never used);
- gets its xAI credential either from a read-only file mount or from `grok login
  --device-auth` stored in a container volume; the credential never enters Git, CI logs,
  the image or Koinon state;
- runs its own Koinon daemon with temporary state, built CGO-free from the commit under test
  and mounted read-only.

CI never runs Grok Build and never contacts the xAI provider.

## Tests (synthetic, in the Go suite)

- **Identity (2).** A synthetic Grok MCP client stands in for Grok Build with the native
  metadata that chunk 01 records. Two concurrent sessions on one host, including two that
  share one MCP server process, each get their own key, or the call is refused with
  `identity_unavailable`. A call with no proven session, or one whose metadata names a
  session the server was not started for, is refused. Caller fields in tool arguments are
  refused as for every family.
- **Admission (1).** A Grok session that was not started through the Koinon launcher is
  refused with `not_launched` and the launcher command. A launched session registers with
  its launch ID and host evidence.
- **Reset and resume (3).** The tests model the reset and resume behavior that chunk 01
  records: a reset that gives a new native session registers the new session; a resumed
  session gets the identity chunk 01 shows it presents.
- **Messaging (4).** Peer discovery, send, inbox, acknowledgement, delivery, memory and
  work claims run end to end from a synthetic Grok session against a test daemon.
- **Wake (5).** The `grok` provider types the fixed notice only into the session's proven,
  idle pane (chunk 03); tests on private tmux servers cover a foreign pane, a reused host, an
  intervening agent and a busy prompt, none of which receive input.
- **No regression (7).** The full suite passes for every existing family.

## Integration points exercised for real

- Chunk 01 records each fact from real Grok Build sessions in the container above.
- The final chunk's live check runs in the same container: two concurrent Grok sessions
  register through the launcher, exchange a message, the idle receiver is woken through its
  pane, and a `/clear` in one moves its participant to the new session. The record names the Grok Build version.

## Edge cases that must have tests

- One Grok leader process serving two sessions; a leader restart.
- `GROK_SESSION_ID` missing, or present but not matching the calling session.
- A forged `X-Grok-Agent-ID` header or caller field from model-controlled input.
- `--session-id` reused, or naming an existing session.
- A subagent's Koinon call, which the `grok-pre-tool-use` hook refuses (chunk 02, item 8).
- A Grok version other than the recorded ones: setup reports it and does not assume support.

## Gate

The `check` skill, the Go workflow on Linux and macOS, and the observer's sign-off bound to
each chunk's head. The live check above runs before #254 closes.
