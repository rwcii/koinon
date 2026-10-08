# Chunk 05 — Terminal naming

Criteria 7, 8, 9 and 10 of `decision.md`. Depends on chunk 02 (and chunk 01 for Codex).
Facts: `live-checks.md`, F4. Delivers #147; closes #228.

## Current state

- `internal/mcp/terminal_name.go` names the session's tmux session after `published(session)`
  (the alias, else the peer name). `name_taken` is in `namingFinal`, so it is not tried again
  until the published name changes.
- Without `TMUX` and `TMUX_PANE` the result is `not_in_tmux`. A Claude background job's
  `koinon mcp` has neither; its Claude process is a child of `claude bg-pty-host`.

## Change

1. **Published name.** The participant address while the session holds it, else its peer name.
2. **Retry.** `name_taken` is no longer final: it is tried again at each renewal, and the name
   is set once the other session with that name is gone. All other rules stay as they are.
3. **Background job.** For a session whose launch record marks a background job
   (`koinon claude --bg`, chunk 01), the server has no `TMUX`, so it looks for the attached client: on each tmux server socket in
   the user's tmux socket directory (`$TMUX_TMPDIR/tmux-<uid>`, else the platform default, through
   `internal/platform`), it lists the panes and searches each pane's process tree for a
   `claude attach <short ID>` process whose short ID is the first eight characters of the job's
   session ID. Exactly one matching pane: apply the naming rules to that pane, with the attach
   client in place of the host for the ancestor check. No match: `attach_pane_not_found`, tried
   again at the next renewal. More than one: `attach_pane_ambiguous`, nothing renamed. Panes on a
   private tmux server outside the socket directory are not searched.
4. **Launched sessions only.** Naming runs for every launched session of every family (chunk
   01); the `KOINON_LAUNCH_ID` condition in `nameAfterRegistration` becomes the launch record
   that the session registered with. A direct start never registers, so it is never named.
5. **Reporting.** The naming result and its reason text are in the session view and in `peers`
   (chunk 02, item 6).

## Done

- The tests of `definition-of-done.md`, "Naming", on private tmux servers: retry after a taken
  name is released, background job with one, none and two attached clients, wrong short ID.
- The final live check of `definition-of-done.md`.
- `PROTOCOL.md`, `docs/USAGE.md`, `docs/INSTALL.md`, the `peer-tmux` skill, the installed
  `koinon guide` text, `CHANGELOG.md`.
