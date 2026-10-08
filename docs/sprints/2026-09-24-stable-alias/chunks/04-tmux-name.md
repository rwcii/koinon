# Chunk 04 — tmux name follows the alias

Read `decision.md` and `definition-of-done.md` first. Criterion 6, and the tmux part of 8.
Depends on chunk 03.

## Outcome

After `ensure` and after `rebind`, the Codex agent's own tmux session carries the name that the
registration publishes (the alias when it holds it, else its per-thread name). In a shared
tmux session only the own pane title changes.

## Specification

- **Input.** The `terminal.json` just written by chunk 01's observation in the same command.
  Not `observed`: no-op, report its reason.
- **Agent panes.** List the panes of the recorded `session_id` (`list-panes -s -t <session_id>
  -F '#{pane_id}\t#{pane_pid}'`). A pane is an agent pane when one of its descendant processes
  is a Codex process (`platform_support.codex_process`) or a live Claude registry pid
  (`bridge.peers()` records that are not `kind: daemon`). Put the descendant walk in
  `platform_support` (`/proc` on Linux, `ps -A -o pid=,ppid=` on macOS).
- **One agent pane.** When the session name differs from the target name and no other session
  has that name (`has-session -t =<name>`), run `rename-session -t <session_id> <name>`, then read
  the name back; a different read-back reports `rename_unconfirmed`. Another session with the
  name: report `name_taken`, rename nothing.
- **More than one agent pane.** Leave the session name. Run `select-pane -t <pane_id> -T <name>`
  on the own pane only and report `pane_titled`.
- **Never** target another session or pane than the recorded ones. All calls use argument
  arrays, the recorded socket and a timeout.
- **Reporting.** `ensure` and `rebind` add `tmux` with `renamed`, `pane_titled`, `unchanged`,
  `name_taken`, `rename_unconfirmed` or the chunk 01 reason.
- **Skills.** Remove the rename recipe from `agents/skills/pickup/SKILL.md` for Codex; `ensure`
  does it. Claude sessions keep the skill's rename, since Claude runs no `ensure`.

## Tests

The tmux-naming tests of `definition-of-done.md` on a private tmux server, on Linux and macOS.

## Live check 3

After merge and an authorized runtime upgrade: live check 3 of `definition-of-done.md`.

## Documentation

`docs/INSTALL.md` (tmux naming), `agents/skills/peer-tmux/SKILL.md` (a Codex terminal carries
its alias), `CHANGELOG.md`.
