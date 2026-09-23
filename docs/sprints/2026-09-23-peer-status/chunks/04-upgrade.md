# Sprint 2026-09-23 · Chunk 04 — Upgrade

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 03's
> `koinon/claude_statusline.py`. Not restated here.

## Scope

The runtime upgrade sets up the wrapper by default, keeps a saved decline, and reports the
settings edit. This amends `docs/RUNTIME-UPGRADE-DESIGN.md`, which today excludes any
"guidance opt-in" from upgrade; the user approved this exception at gate A.

## Approach

- Preflight (`koinon/upgrade_command.py` `prepare`): observe the Claude settings file and the
  `claude_statusline` selection; record the planned action (`set_up`, `unchanged`,
  `declined`) and the file digest in the `prepared-checks` document. A malformed settings file
  is reported and the action becomes `skipped`; it does not block the runtime upgrade.
- After the runtime is released (`koinon/upgrade_complete.py`, before the `complete`
  document): apply the planned action with chunk 03's set-up, which saves `original` once. A
  digest that differs from the preflight observation is a `settings_conflict` and nothing is
  written.
- Record the result in the `complete` document next to `preservation`: action, outcome and the
  saved original's digest. The journal marks the step done, so a resumed operation does not
  repeat it.
- `docs/RUNTIME-UPGRADE-DESIGN.md`: state the exception, its reason and its limits.

## Build decisions (recorded during the build)

- The set-up runs after `finish()`, not before the `complete` document. While the operation
  runs, `install.json` must equal its frozen copy and `finish()` writes that copy back, so the
  saved original entry could not be recorded earlier. The outcome is returned with the result
  and kept as the `claude-statusline` document instead of inside `complete`.
- A completed operation that is run again reports the retained outcome and calls `finish()`
  only while the upgrade marker is still present, because the status-line record changes
  `install.json` after release.
- The native upgrade fixture and `tests/test_upgrade_command.py` give the upgrade a private
  Claude configuration that holds a user status line, and check the wrapped result.

## Documents

`docs/RUNTIME-UPGRADE-DESIGN.md`, `docs/INSTALL.md` "Upgrades and removal".

## Done-criteria (this chunk's slice)

- The upgrade parts of criterion 3 hold.
- The upgrade tests of the definition of done pass, and the native upgrade workflow passes for
  its memory, session and combined cases with a temporary `CLAUDE_CONFIG_DIR`.
