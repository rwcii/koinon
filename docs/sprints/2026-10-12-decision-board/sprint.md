# Decision board — chunks

Each chunk cites `decision.md` and `definition-of-done.md` and merges complete through ship.
Work item 33 covers the sprint. Claude drives, Codex observes. One open PR per target.

| Chunk | Complete outcome | Criteria | Dependencies |
| --- | --- | --- | --- |
| [01 Records and agent queries](chunks/01-records.md) | Agents create, edit, list and read decision items through Koinon; read-only dashboard list | 2, 6, 9 (lookup), 10, 11 | None |
| [02 Verdicts and delivery](chunks/02-verdicts.md) | Dashboard verdicts with snapshots, reopen on edit, durable per-recipient delivery, pending and acknowledgement, archive | 3, 4, 5, 8, 9 | 01 |
| [03 The board](chunks/03-board.md) | Designed board with Markdown, agent rows, command boxes and archive lookup | 1, 7, 12, rest of 5 and 6 | 02 |

Close #206 only after all chunks and their evidence pass.
