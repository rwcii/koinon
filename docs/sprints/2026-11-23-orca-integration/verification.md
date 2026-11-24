# Orca integration (#795) — verification

Live checks gate the sprint's completion, not a chunk's merge (`sprint.md`). Each row gives the
check, the computer, the result and its evidence. An open row stays a listed item of #795.

| Chunk | Check | Computer | Result | Evidence |
| --- | --- | --- | --- | --- |
| 02 | One Claude and one Codex member started with `orca terminal create --worktree "path:$PAIR" --command "koinon FAMILY --directory '$PAIR' --role ROLE"` register with their addresses, show their panes in Orca, receive a Koinon message and a wake, and `coordinate` reads their panes | A computer with Orca | Open | The development computer has no Orca; the synthetic launcher tests pass (`internal/launcher/orca_test.go`) |
| 04 | The first `preflight` and `board-verdict` on chunk 04's own pull request; after the merge, the `develop` ruleset requires both, read back with `gh api repos/rwcii/koinon/rulesets` | This computer | Open | Chunk 04 |
| 04 | A released v0.13.3 binary reports the new fact headings "Merge approval" (ship) and "Promotion merge" (release) as undeclared until v0.13.4 is installed; after the v0.13.4 install, `koinon library state` reports neither | This computer | Open | The release |
| 04 | The first release with the gates (v0.13.4): the `release` skill stops before the merge into `main`, the maintainer merges, the tag and the publish steps follow | This computer | Open | The release |
| 05 | A verdict from the Fleet page reaches the card's node, on one Linux and one macOS node | Two linked nodes | Open | Chunk 05 |
