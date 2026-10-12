# Coordinated work — chunks

Reuse #140's one work item across the chunks. Close the issue only when all three criteria
sets, integration evidence and reviews pass. Keep one open PR per target branch.

| Chunk | Complete outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Role routing and native ownership](chunks/01-role.md) | Typed role proposals, native-bound leases, current assignment queries; legacy continuation preserved | 1–3, part of 6/9 | None |
| [02 Reports and funded verdicts](chunks/02-verdict.md) | Report/accept/reject with immutable history, exact reporting-consumer exclusion and retained control credits | 4–5, 8–9 | 01 |
| [03 Production discovery and wake](chunks/03-discovery.md) | Creator/assignee catch-up and content-free coalesced native notices with restart reconciliation | Remaining 6–7/9 | 02 |

Chunk01 remains useful with existing v1 finish behavior; chunk02 adds an optional coordinated
reporting path without reopening finished items. Until chunk03, guides describe polling current
work and memory catch-up and do not claim automatic work-change model notices.
