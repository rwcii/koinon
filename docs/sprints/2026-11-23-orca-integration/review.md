# Orca integration (#795) — plan review

The other agent family reviews the plan hash with `peer-review`. Each finding and its
disposition go here; a changed scope or test gets a bounded review.

| Round | Head | Reviewer | Result | Findings and dispositions |
| --- | --- | --- | --- | --- |
| 1 | cbb64a1 | Codex | Changes requested (peer, refusal) | Peer: ownership and overlap rules, Fleet reads with the key locked, unreachable node outcome and board audit, chunk 04 rollout and acceptance: all accepted and fixed. Refusal: the gates are opt-in project facts (accepted, fixed); the admin password rule cites the maintainer secret's rule and the key removal has no confirmation (fixed); the live checks gate the sprint's completion, not a chunk's merge (fixed in `sprint.md`). The read authentication goes to the maintainer as a decision card. |
| 2 | 9d505cd | Codex | Blocked by the read decision | The read key, the outcome check across the 5-minute and 7-day limits, no verdict queue and the admin password rule went to the maintainer; approved at revision 4 and recorded in `decision.md` (Fleet reads, lost answers and the admin password), the definition of done and chunk 05. |
