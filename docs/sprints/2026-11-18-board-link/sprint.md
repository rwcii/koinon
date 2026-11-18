# Board link — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Issue #746. Scope
review in `review.md`. No chunk starts before the verdict on BOARD-LINK-746; if the verdict
changes the design, this plan changes before any code.

Chunk 01 captures every event durably at the origin before anything is sent, so no notice is
lost between the sprint's chunks or releases (acceptance 9).

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Capture at the origin](chunks/01-capture.md) | A card that names `NAME@COMPUTER` for a paired computer shows the recipient resolved as `remote`; each event queues one notice for each remote recipient and shows `queued`; existing tagged cards are reconciled at upgrade; an unpair marks waiting notices `unpaired` | One place for the card (origin); Recipients and upgrade; Lifecycle at the origin (`decision_get`, `decision_history`, unpair); Schema; Documentation and records (records) | None |
| [02 Notices over the link](chunks/02-notices.md) | The notice stream with its probe puts each notice in the agent's inbox on the other computer with its wake; the origin shows received, refused and unsupported | Notices; Lifecycle at the origin (removal) | 01 |
| [03 Remote read and acknowledgement](chunks/03-remote-read.md) | The agent reads the live card, its list and its waiting verdicts from the origin, and acknowledges there; the dashboard view; the guide and USAGE; the live check | Remote read and acknowledgement; One place for the card (agent side); Lifecycle at the origin (dashboard); Documentation and records; Checks and evidence (live) | 02 |

#746 closes when chunk 03 merges with its evidence and the change is promoted.
