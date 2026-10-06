# Chunk 10 — launcher

Read `../decision.md`, `../definition-of-done.md` and `codex_launch.py` first.

## Outcome

`koinon codex [arguments]`, `koinon agy [arguments]` and `koinon opencode [arguments]` start the configured CLI with the
behaviour of `codex_launch.py`: inside tmux it runs in the current pane; outside tmux it starts
a new tmux session named after the directory and attaches; without tmux it runs in the
terminal; `--tmux-session NAME` starts a detached named session for an agent and prints it. A
start folder that holds another repository is refused. OpenCode starts with a loopback `--port`, a
generated `OPENCODE_SERVER_PASSWORD` that the daemon stores as the wake target's credential,
and `--hostname 127.0.0.1`. The launched session does not inherit
`CLAUDE_*` variables from the tmux server or the caller (`../spike.md`, fact 1). The launched session's identity reaches
the daemon as the wake target.

## Tests

The launcher cases of the definition of done, on a private tmux server.

## Done

Gate passes.
