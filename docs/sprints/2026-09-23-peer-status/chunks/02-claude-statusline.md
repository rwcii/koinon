# Sprint 2026-09-23 · Chunk 02 — Claude status line

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API. Not
> restated here.

## Scope

The `statusline.py` root entrypoint that Claude Code runs as its status-line command, and the
Claude context in `peers`. Installing it into the settings is chunk 03.

## Approach

- `statusline.py [--command STRING]`, installed with the other entrypoints (add it to
  `scripts/install.py` `FILES` and `koinon/upgrade_layout.py`).
  1. Read standard input once, as bytes, completely, with no size limit; these bytes are what
     the user's command receives.
  2. In a `try` that catches every exception, and only when the input is at most 1 MiB:
     parse the JSON; take `session_id`, `model.id`, `context_window.context_window_size`,
     `context_window.total_input_tokens`, `context_window.used_percentage`, and whether
     `context_window.current_usage` is null (a boolean `usage_available`); call
     `participant_status.write('claude', session_id, ...)`. Nothing else from the input is
     read or stored. Larger input is forwarded and not parsed.
  3. Run the user command, if any, with the same bytes on its standard input, the same
     environment and working directory; copy its standard output and standard error, and exit
     with its exit status. With no user command, print nothing and exit 0.
  The user command runs in every case, including when step 2 fails.
- The user command is the original `statusLine` command string, unchanged, passed as one
  quoted `--command` argument. The wrapper runs it with `/bin/sh -c <string>`, which is how
  Claude Code runs a status-line command: observed on 2026-09-23 on Linux with Claude Code
  2.1.280, the parent of the user's script was `/bin/sh -c 'bash $HOME/...'`. Expansions,
  pipes and quoting therefore keep their meaning. On macOS, observed on 2026-09-23, the direct
  child of Claude Code was `bash /Users/<user>/.claude/statusline-command.sh`, with `$HOME`
  already expanded: a shell interpreted the command and replaced itself with `bash`, which
  the macOS `/bin/sh` does for a single simple command and Linux `dash` does not. Both are
  consistent with `/bin/sh -c`. Chunk 03 builds the entry.
- `peers`: a Claude record with `usage_available` false or a zero or missing limit gives
  `unknown` with `no_token_usage`; `fill` uses `total_input_tokens / context_window_size`,
  the formula that the Claude documentation gives for `used_percentage`.
- Keep start-up light: import only what steps 1 to 3 need, so the median added time stays
  within the measurement bound of the definition of done.

## Done-criteria (this chunk's slice)

- Criterion 2 holds for a synthetic record, and the wrapper parts of criterion 3.
- The status-line wrapper and allowlist tests of the definition of done pass.
- The pull request records the time measurement of the definition of done.
