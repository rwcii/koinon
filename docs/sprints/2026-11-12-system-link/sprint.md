# System link — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Issue #670. Scope review in `review.md`.

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Pairing](chunks/01-pairing.md) | Two computers with Remote on pair through one invitation and show each other as connected, offline or pending; unpair; Remote disable closes links | Identity, name and keys; Pairing and Remote; Documentation and records (ADR 001, ADR 002) | None |
| [02 Directory and addressing](chunks/02-directory.md) | Agents see the other computer's receivers as `NAME@COMPUTER` in `peers` and the dashboard | Directory and addressing | 01 |
| [03 Messages](chunks/03-messages.md) | Agents send and reply across computers, with the sender queue, the delivery states and the live Linux and macOS round trip | Messages; Checks and evidence (live); the links PDR | 02 |

#670 closes when chunk 03 merges with its evidence and the change is promoted.
