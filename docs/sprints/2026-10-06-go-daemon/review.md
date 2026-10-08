# Sprint 2026-10-06 — Go daemon — Review

Review by the Codex agent family of `b3d4d82` (plan and spike), one batch, 2026-10-06.

| # | Finding | Disposition |
| --- | --- | --- |
| 1 | The definition of done took MCP identity only from the environment, which contradicts the spike (Codex sends the thread per call and keeps its MCP server across `/clear`) and chunk 05. | Fixed: the MCP test takes identity from each family's source in `spike.md`, serves two successive Codex threads on one server, refuses identity in tool arguments, and separates two OpenCode sessions. |
| 2 | The definition of done promised no duplicate wake after a crash, which no provider can guarantee (`docs/NOTIFIER.md` documents the window). | Fixed: inbox storage is separate from the notice; notices are at least once, with an `uncertain` state retried while the sequence is unacknowledged (definition of done, chunks 03 and 04). |
| 3 | The OpenCode fallback (one server as one participant) did not say which native session an inbox or wake belongs to. | Fixed by removing the fallback: chunk 05 must prove a per-call OpenCode identity first, or the OpenCode scope returns to the maintainer. |
