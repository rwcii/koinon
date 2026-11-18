# Board link — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Issue #746. Scope
review in `review.md`. No chunk starts before the verdict on BOARD-LINK-746; if the verdict
chooses the other option, this plan is replaced before any code.

Chunk 01 captures every change durably at the origin before any of it is sent, so nothing is
lost between the sprint's chunks or releases (acceptance 9).

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Capture at the origin](chunks/01-capture.md) | A card that names `NAME@COMPUTER` for a paired computer shows the recipient resolved as `remote`; each change and verdict is queued for each remote recipient and shows `queued`; existing tagged cards are reconciled at upgrade; an unpair marks waiting entries `unpaired` | Authority and identity (origin side); Recipients and upgrade; Visibility at the origin (`decision_get`, `decision_history`); Schema; Documentation and records (records) | None |
| [02 Delivery over the link](chunks/02-delivery.md) | The board stream with its probe delivers copies, changes and verdicts to each recipient on the other computer with their wakes; `decision_list`, `decision_get`, `decision_pending` and `decision_ack` show and handle them there; acknowledgements reach the origin; removal and unpair reach the copies | Authority and identity; Transport; Delivery to the agent; Acknowledgement; Lifecycle and retention (removal and unpair) | 01 |
| [03 Retention, dashboard and evidence](chunks/03-retention.md) | The retention sweep of copies; the dashboard view of remote recipients; the guide and USAGE; the live check on two paired computers | Lifecycle and retention (retention); Visibility at the origin (dashboard); Documentation and records; Checks and evidence (live) | 02 |

#746 closes when chunk 03 merges with its evidence and the change is promoted.
