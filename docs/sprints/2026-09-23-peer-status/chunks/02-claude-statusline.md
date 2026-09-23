# Sprint 2026-09-23 · Chunk 02 — Claude status line

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API. Not
> restated here.

## Scope

The `statusline.py` root entrypoint that Claude Code runs as its status-line command, and the
Claude context in `peers`. Installing it into the settings is chunk 03.

## Approach

- `statusline.py [--command STRING]`, installed with the other entrypoints (add it to
  `scripts/install.py` `FILES` and `koinon/upgrade_layout.py`).
  1. Read standard input once, as bytes, bounded to 1 MiB.
  2. In a `try` that catches every exception: parse the JSON; take `session_id`, `model.id`,
     `context_window.context_window_size`, `context_window.total_input_tokens` and
     `context_window.used_percentage`; call `participant_status.write('claude', session_id,
     ...)`. Nothing else from the input is read or stored.
  3. Run the user command, if any, with the same bytes on its standard input, the same
     environment and working directory; copy its standard output and standard error, and exit
     with its exit status. With no user command, print nothing and exit 0.
  The user command runs in every case, including when step 2 fails.
- The user command is the original `statusLine` command string, unchanged, passed as one
  quoted `--command` argument. The wrapper runs it the way Claude Code runs a status-line
  command, through the same shell, so that expansions such as `$HOME`, pipes and quoting keep
  their meaning. The build verifies which shell Claude Code uses on Linux and macOS and
  records it; chunk 03 builds the entry.
- `peers`: a Claude record with `current_usage` null or a zero or missing limit gives
  `unknown` with `no_token_usage`; `fill` uses `total_input_tokens / context_window_size`,
  the formula that the Claude documentation gives for `used_percentage`.
- Keep start-up light: import only what steps 1 to 3 need, so the median added time stays
  within the measurement bound of the definition of done.

## Done-criteria (this chunk's slice)

- Criterion 2 holds for a synthetic record, and the wrapper parts of criterion 3.
- The status-line wrapper and allowlist tests of the definition of done pass.
- The pull request records the time measurement of the definition of done.
