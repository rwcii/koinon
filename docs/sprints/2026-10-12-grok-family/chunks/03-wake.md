# Chunk 03 — tmux wake and live check

Read `../decision.md`, `../definition-of-done.md`, `../spike.md` and `02-adapter.md` first.

## Outcome

An idle Grok session gets a content-free wake through its own tmux pane, and the sprint's live
check passes in the development container.

## Specification

1. **Channel.** Grok's default mode has no native interface that reaches a running session
   from outside (spike fact 6), so the `grok` wake provider types into the session's tmux
   pane. The session's MCP server reports its `TMUX` socket and `TMUX_PANE` (spike fact 1);
   they become the session's wake target at registration, as the socket path stays
   unresolved.
2. **Target proof.** Before each wake, the provider proves the pane as the #156 rename does:
   the pane's process is an ancestor of the session's recorded host, with no other agent
   between them, and the host PID and start value still match. Anything else is
   `grok_target_unavailable`, with no input sent.
3. **Idle only.** The provider reads the pane first and types only when it shows Grok's empty
   input prompt (`❯` with nothing after it) and no running turn. Otherwise the wake waits and
   is retried by the existing wake schedule; the message stays in the inbox.
4. **Input.** It sends Koinon's fixed notice text and Enter, as two `send-keys` calls with
   `-l` for the text, and nothing else. Peer text, sender names and message IDs never reach
   the pane. It reads the pane once more and records `delivered` only when the notice shows
   as a submitted prompt.
5. **Leader mode** stays refused (chunk 02). The leader's ACP `session/load` plus
   `session/prompt` path (spike fact 6) is recorded on a follow-up issue, not used.
6. **Docs.** PROTOCOL (wake providers), USAGE, the guide and CHANGELOG.

## Tests

Private tmux servers (`tmux -S`) with a synthetic Grok-like prompt: a wake reaches only the
proven pane; a foreign pane, a reused host PID or an intervening agent sends nothing; a busy or
non-empty prompt sends nothing and retries; the typed text is exactly the fixed notice.

## Live check (development container only)

Two Grok sessions started with `koinon grok` in two panes register and exchange a message; the
idle receiver is woken through its pane and reads the message; a `/clear` in one moves its
participant to the new session. The record names the Grok Build version. It runs with a
Koinon daemon inside the container, never against the maintainer's host daemon.

## Done

The tests pass on Linux and macOS CI, `check` passes, the live check is recorded on #254, and
the follow-up issue exists.
