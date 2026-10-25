# Dashboard rooms — chunks

> Historical sprint plan. Its non-goals and constraints limited this sprint only. Current decisions are in the [decision records](../../engineering/wdr/README.md); see WDR 001.

Each chunk cites decision.md and definition-of-done.md and merges complete through ship.
Reuse #217's work claim; release at an agreed driver handoff. One open PR per target.

| Chunk | Complete outcome | Criteria | Dependencies |
| --- | --- | --- | --- |
| [01 Rooms and membership](chunks/01-membership.md) | Authenticated dashboard rooms/roster/refill-setting controls, native list/roster queries and membership awareness | 1, 6, part of 7/9/10 | None |
| [02 Posts and bounded fan-out](chunks/02-posts.md) | Ordered atomic posts/replies, typed maintainer authority, existing wakes, configured agent-post refill and retention | 2–4, 8, 10, part of 5/7/9 | 01 |
| [03 Conversation view](chunks/03-conversation.md) | Refreshing ordered transcript and per-recipient outcomes; complete family/protocol/docs coverage | Remaining 5/7/9 | 02 |

Chunk01 exposes no posting interface. Chunk02 makes posting usable in tools and the existing
room dashboard detail page, with a simple ordered view. Chunk03 completes the dedicated
conversation and delivery view. Close #217 only after all chunks and evidence pass.
