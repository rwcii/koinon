# Coordinated work — review

The observer reviews a frozen plan before Gate B; record one material batch and dispositions.
Changed scope/criteria/tests receive bounded follow-up. No product implementation yet.

| Finding | Disposition |
| --- | --- |
| Claude review of 31b1ab4801aa5beea965f0681cbc779b4c5f79b5: MEDIUM — audience-row cap has no defined overflow behavior | Accepted and fixed in chunk03 and definition of done: omit the new notice without eviction; expose usage/limit/saturation; preserve complete paged queries; retry after reclaim. Add a cap-and-recovery test. Bounded follow-up pending. |
| MINOR — name each family's native reset behavior | Dropped: the plan already requires tests of actual same versus different native identities; the installed family guide supplies reset semantics. No change to that contract is needed. |
| MINOR — name a new refusal code at the verdict reservation count limits | Dropped: ordinary funding refusals already use the existing capacity contract. A new code does not change the criteria or verification. |

Bounded follow-up (Claude): PASS at `e192b958e83f0fd2409ca1542a47b55a94e72f20`.
The accepted cap finding is resolved and the two minor dispositions are accepted. No
material finding remains. The reviewed plan is ready for the maintainer's Gate B.

## Reconciliation after the maintainer's verdict choice

The maintainer selected exclusion of the reporting worker, with later sessions in that worker's
role eligible (PDR 004, Decision 3; #306 comment 6119167156). The plan removes the additional
logical-participant exclusion and updates its verification cases. Existing/default participant
state continues across succession (PDR 001, Decision 6);
the proposed explicitly native-bound role-claim path remains distinct (PDR 004, Decision 1).
Role, reported-state/funding and discovery mechanisms remain proposals for Gate B. This
reconciliation's frozen head receives a bounded observer review; the earlier funding and
discovery review remains applicable to unchanged content.

Claude's review of `aa4d1e9` found that allowing the reporting native session to switch consumer
labels and accept its own report exceeded the framed choice. Accepted: retain the reporting
native-session guard and exclude only that session, rather than later sessions of the role.
PDR 004 Decision 3's consumer-only paraphrase is clarified against the question and explanation
in the existing source. The corrected frozen head receives bounded verification of this finding.

## Refusal-language audit after Gate B

The maintainer approved this plan at Gate B on 2026-10-12 ("approved as recommended.",
[#140 comment 6127384411](https://github.com/rwcii/koinon/issues/140#issuecomment-6127384411))
and in the same message asked: "make sure you and your peer didnt slip more refusal langauge
in these two projects." Claude audited every restriction, refusal and gate in the plan against
#140 and the records. Removed: the chunk 01 line "No runtime setup." (no source; the issue's
consuming-project non-goal stays in the decision), and the limit of role routing to "families
with verified participant roles", which excluded DeepSeek's default participant. Gate text now
records the approval. Kept with their sources: #140's non-goals, no claim inheritance, the
verdict rule (PDR 004, Decision 3), content-free wakes (ADR 001, Decision 3) and reported data
(WDR 002, Decision 3). The storage bounds are approved product limits with visible refusal,
not yet built.
