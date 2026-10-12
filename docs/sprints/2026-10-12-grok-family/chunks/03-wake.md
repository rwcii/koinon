# Chunk 03 — wake limitation and live check

Read `../decision.md`, `../definition-of-done.md`, `../spike.md` and `02-adapter.md` first.

## Outcome

Koinon reports that a Grok session cannot be woken while idle, and the sprint's live check
passes in the development container.

## Specification

1. **No wake in default mode.** Grok's default mode has no interface that reaches a running
   session from outside (spike fact 6). A message to a Grok session is delivered to its inbox
   and read at the session's next Koinon tool call. The wake provider for `grok` reports
   `wake_unsupported`; `peers`, `peer_status`, the dashboard and the guide state it. No other
   family's wake path and no terminal input is used for a Grok session.
2. **Leader-mode wake is a follow-up.** The leader's ACP `session/load` plus `session/prompt`
   reaches an exact session (spike fact 6), but leader mode is refused by chunk 02 because its
   process evidence does not identify sessions. A follow-up issue on the milestone records the
   evidence and the open question: a per-session host proof in leader mode.
3. **Docs.** PROTOCOL (wake providers), USAGE, the guide and CHANGELOG.

## Tests

The `grok` provider returns `wake_unsupported` and never calls another provider; a message to a
Grok session stays unacknowledged until that session reads it.

## Live check (development container only)

Two Grok sessions started with `koinon grok` in two panes register, exchange a message and
acknowledge it; a `/clear` in one moves its participant to the new session; the record names
the Grok Build version. It runs with a Koinon daemon inside the container, never against the
maintainer's host daemon.

## Done

The tests pass on Linux and macOS CI, `check` passes, the live check is recorded on #254, and
the follow-up issue exists.
