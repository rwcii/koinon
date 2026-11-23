# Chunk 02 — Orca launch

Cites `../decision.md` (The observer's scratch copy; Decided in the spec and kept: Orca;
acceptance 2) and `../definition-of-done.md` (Orca launch).

## Outcome

`koinon <family> --directory DIR --role ROLE` works as an Orca terminal command, the address
and tmux rules for pairs in worktrees are tested and documented, and `pair` says where the
observer reproduces findings.

## Work

1. Read `internal/launcher/launcher.go` (`defaultSessions`, the `TMUX` choice in the start
   path, `cleanEnvironment`) and `core.AddressBase`; add tests with a fake tmux for the cases
   in the definition of done. Fix only what a test shows wrong; record each finding.
2. docs/USAGE.md, section "Run Koinon members from Orca": the command form with the two
   example `orca terminal create` lines, always `--role` with the pair, the label from the Git
   common directory, attached and in-pane behaviour by `TMUX`, the observer's scratch copy,
   one coordinator for each computer, and that Orca's idle detection and titles may see the
   launcher, not the agent.
3. `agents/skills/pair/SKILL.md`: the observer's scratch copy is a detached worktree of the
   frozen checkpoint outside the driver's worktree, made and removed by the observer.
4. The live check on a computer with Orca, into `../verification.md`. It gates the sprint's
   completion, not this chunk's merge (`../sprint.md`).
5. Records: PDR 001 (participant addresses) records the role rule for pairs; WDR 003 (pairing)
   records the scratch copy; CHANGELOG.

## Tests

The launcher cases of the definition of done, with `-race`, on Linux and macOS CI.
