# Sprint 2026-10-08 — participants — Review

## Gate B review 1 (Codex), plan at `c59f3f2`

| # | Finding | Disposition |
| --- | --- | --- |
| P1 | A fenced former holder can become the holder again: chunk 03 made a retired session active again on registration, chunk 02 then counted it as a qualifier when the successor expired, and chunk 04 accepted its same-host registration. This contradicts `decision.md`, criterion 5. | **Fixed in part.** Chunk 03, item 5: every holder change records a persistent fence (survives restart and repeated registration). Chunk 02: a fenced session never counts as a qualifier. Chunk 04: a fenced session is checked only for same-host succession, which removes the fence. `definition-of-done.md`: persistent fence tests (restart, repeated registration, successor expired, delayed call inside the guard). **Rejected in part:** the finding asked that only the maintainer's choice lift a fence. The maintainer decided on 2026-09-24 (#141, `docs/sprints/2026-09-24-stable-alias/decision.md`, criterion 3, and criterion 5 there) that a `/resume` back to the former thread takes the address again. Same-host succession after the 30-second guard is that case, so it stays. |
| P2 | Participant-owned claims break the merged #83 flow: `CheckoutStatus` resolves the writer's peer only from `FAMILY:ID`, so a `participant:<address>` writer is `writer_unaddressable`, and the self-request check compares only `FAMILY:ID`. | **Fixed.** Chunk 03, item 7: a participant consumer resolves to its current holder's peer, re-read inside the send transaction; the holder's own request is a self-request; other custom consumers stay unaddressable. `definition-of-done.md`: checkout tests before and after a succession. |

## Gate B review 2 (Codex), plan at `c32d901`

P2 and the persistent fence of P1 carry forward. The direct-start naming findings are closed as
scope removed by the launched-only decision.

| # | Finding | Disposition |
| --- | --- | --- |
| P1 | (open part) Same-host succession lifts a fence after the 30-second guard, but a retained old-thread call after a quiet period looks the same as an intentional `/resume`. | **Fixed by maintainer decision (gate B).** Only verified resume evidence may lift a fence, and none is verified today, so only the maintainer's dashboard choice lifts it. `decision.md` (decisions, criteria 3 and 5), chunk 03 item 5, chunk 04 item 2 (`fenced` refusal), `definition-of-done.md` fence tests. |
| L1 | The launched-only rule is enforced only in new MCP servers; HTTP `Register` takes no launch ID and an old MCP server renews a direct registration indefinitely. | **Fixed.** Chunk 01 item 4: `Store.Register` refuses a launcher-family registration without a valid launch on every path; `Store.Mutate` refuses, without extending, the renewal of a session with no launch record, so an upgraded direct session expires at its upgrade-time expiry with its state kept. `decision.md` criterion 6 and the upgrade constraint; `definition-of-done.md`, "Daemon admission". |
| L2 | A later background job inherits an earlier job's launch ID from the Claude service (F5) and passes the family and directory check. | **Fixed.** Chunk 01 item 5: a foreground launch admits only its host; a background launch admits only the recorded job, and a registration before the record gets `launch_pending`. Item 2: `claude --bg` runs without launch variables, and a failed start retires the launch. `definition-of-done.md`, "Launch binding" (the F5 two-job case and the ordering). |
| L3 | "Every family" includes DeepSeek, which has no launcher and keeps its command registration under #199. | **Fixed by maintainer decision (gate B).** The launcher families are Claude, Codex, OpenCode and Antigravity; `koinon register --as deepseek:ID` stays the one admission without a launch record until #199; no DeepSeek launcher is added or recommended. `decision.md`, chunk 01 items 4 and Done, `definition-of-done.md`. |
