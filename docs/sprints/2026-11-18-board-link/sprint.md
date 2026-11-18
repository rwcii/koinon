# Board link — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Issue #746. Scope
review in `review.md`. No chunk starts before the verdict on BOARD-LINK-746; if the verdict
chooses the other option, this plan is replaced before any code.

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Card copies](chunks/01-card-copies.md) | A card that names `NAME@COMPUTER` reaches that agent's computer as a read-only copy, with each revision; the agent sees it with `decision_list --filter mine` and `decision_get`; the origin shows each remote delivery's state | Authority and identity; Recipients; Transport; Delivery to the agent (copies); Visibility at the origin; Documentation and records (records) | None |
| [02 Verdicts and acknowledgement](chunks/02-verdicts.md) | Each verdict reaches the remote agent with its wake and in `decision_pending`; `decision_ack` there marks the origin's delivery acknowledged | Delivery to the agent (verdicts); Acknowledgement | 01 |
| [03 Lifecycle and evidence](chunks/03-lifecycle.md) | Recipient removal, unpair and retention of copies; the dashboard view; the guide and USAGE; the live check on two paired computers | Lifecycle; Visibility at the origin (dashboard); Documentation and records; Checks and evidence (live) | 02 |

#746 closes when chunk 03 merges with its evidence and the change is promoted.
