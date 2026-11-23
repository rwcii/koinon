# Orca integration (#795) — verification

Live checks gate the sprint's completion, not a chunk's merge (`sprint.md`). Each row gives the
check, the computer, the result and its evidence. An open row stays a listed item of #795.

| Chunk | Check | Computer | Result | Evidence |
| --- | --- | --- | --- | --- |
| 02 | One Claude and one Codex member started with `orca terminal create --worktree "path:$PAIR" --command "koinon FAMILY --directory '$PAIR' --role ROLE"` register with their addresses, show their panes in Orca, receive a Koinon message and a wake, and `coordinate` reads their panes | A computer with Orca | Open | The development computer has no Orca; the synthetic launcher tests pass (`internal/launcher/orca_test.go`) |
| 05 | A verdict from the Fleet page reaches the card's node, on one Linux and one macOS node | Two linked nodes | Open | Chunk 05 |
