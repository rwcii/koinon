# Chunk 10 — launcher

Read `../decision.md`, `../definition-of-done.md` and `codex_launch.py` first.

## Outcome

`koinon codex [arguments]` and `koinon agy [arguments]` start the configured CLI with the
behaviour of `codex_launch.py`: inside tmux it runs in the current pane; outside tmux it starts
a new tmux session named after the directory and attaches; without tmux it runs in the
terminal; `--tmux-session NAME` starts a detached named session for an agent and prints it. A
start folder that holds another repository is refused. The launched session's identity reaches
the daemon as the wake target.

## Tests

The launcher cases of the definition of done, on a private tmux server.

## Done

Gate passes.
