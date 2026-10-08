# Chunk 04 — Claude

Criteria 7, 8 and 10 of [decision.md](../decision.md).

## Change

1. **Claude view.** Topics: the peer name (the session's own agent listing), `bridge.py peers`
   for model, context and work, the session key change after `/clear` (claims held under the old
   key stay with it), and held incoming cross-session messages (tell the user; never change the
   inbound setting). `--brief` prints the overview in at most 40 lines.
2. **Managed `CLAUDE.md` block** in `${CLAUDE_CONFIG_DIR:-~/.claude}/CLAUDE.md`, with the
   criterion 1 contract, written through `participant_instructions` with its own markers and the
   same locking, backup and preservation.
3. **Managed `SessionStart` hook** in the Claude user settings file that
   `koinon/claude_statusline.py` edits: one command hook with a `timeout` of 10 seconds. Claude
   runs a hook command through the shell, so the command is a POSIX `sh` script that works
   when the prefix is gone:
   `if [ -f '<prefix>/session.py' ]; then '<interpreter>' '<prefix>/session.py' guide --agent claude --brief; else echo 'Koinon guidance unavailable: <prefix>/session.py is missing'; fi; exit 0`
   (paths quoted with `shlex.quote`). Set-up, decline, removal, saved state and conflict
   detection reuse the status-line integration's settings code; the hook entry is found by its
   exact command, and other hooks are untouched. The Claude block uses the reconciliation of
   chunk 02.
4. **Install, upgrade, uninstall.** Installation for a Claude user sets up both by default;
   `--no-claude-guidance` declines, `--claude-guidance` and `--remove-claude-guidance` act on an
   existing installation, like the status-line options in `scripts/install.py`. The upgrade
   operation includes both in its preflight and completion report and keeps a saved decline.
   Uninstall removes both while they are still Koinon's.
5. **Acknowledgement.** `guide-ack` for Claude writes the file named in
   [sprint.md](../sprint.md). `peers` shows `guide_stale` for Claude peers from it.
6. **Documents.** `docs/INSTALL.md` ("Claude status line" becomes "Claude integration"),
   `README.md`, `CHANGELOG.md`.

## Tests

The "Claude integration" tests and the Claude edge cases of the definition of done.
