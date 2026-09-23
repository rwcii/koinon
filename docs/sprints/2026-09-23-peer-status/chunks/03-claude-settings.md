# Sprint 2026-09-23 · Chunk 03 — Claude settings

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 02's `statusline.py`.
> Not restated here.

## Scope

Setting up, declining and removing the wrapper in the user's Claude settings during
installation and uninstall. Upgrade is chunk 04.

## Approach

- The settings file is `<claude config dir>/settings.json`, where the directory is
  `CLAUDE_CONFIG_DIR` or `~/.claude`, the same rule as the registry reader in `bridge.py`.
  Installation sets up the wrapper when that directory exists. Only the `statusLine` entry
  changes.
- New module `koinon/claude_statusline.py`, modelled on `koinon/work_guidance.py` (pending and
  enabled states with digests in `install.json`) and `koinon/participant_instructions.py`
  (locks, atomic replacement, preserved mode):
  - Set-up: read the file; if `statusLine` is already the wrapper, change nothing. Otherwise
    save the current `statusLine` value (or `null`) as `original` in `install.json` once, and
    write a `statusLine` of `{"type": "command", "command": "<python> <prefix>/statusline.py
    --command <original command string, quoted as one shell word>"}`, keeping every other
    field of the original entry. A test proves that `bash $HOME/...`, a pipe and a quoted
    argument run with the same result as before. An original that
    is not a `command` entry is kept as `original` and the wrapper runs with no user command.
  - Before the replacement, read the file again and compare it with the first read; after the
    replacement, read it again and compare it with what was written. A difference is a
    reported `settings_conflict` that leaves the file as found.
  - Decline: `--no-claude-statusline` records `state: declined` and changes no file. A later
    installation keeps the decline until `--claude-statusline` is given.
  - Removal: `--remove-claude-statusline` and uninstall restore `original` only while the
    entry is still the wrapper that `install.json` records; otherwise keep the entry and
    report `statusline_changed` with the manual action.
  - Malformed JSON: refuse with `settings_invalid` and write nothing.
- `peers` repair hint: `statusline_missing` carries the exact installer command that sets the
  wrapper up again.

## Documents

`docs/INSTALL.md`: the default set-up, the three options, the saved original, conflicts, the
advice not to change Claude settings during installation or removal, and the repair command.
`README.md`: one line in the installation summary.

## Done-criteria (this chunk's slice)

- The settings parts of criterion 3 hold.
- The settings-edit tests of the definition of done pass, and the native installation workflow
  passes with a temporary `CLAUDE_CONFIG_DIR`.
