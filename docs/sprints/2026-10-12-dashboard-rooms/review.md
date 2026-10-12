# Dashboard rooms — review

Claude reviewed `76bfd85602b42f6d9f7f1aca4b0ba7b4f44dfd03` in one batch.
The historical data-only authority disposition below is superseded by PDR 005, Decision 3;
the reconciliation section records the current plan.

| Finding | Disposition |
| --- | --- |
| MEDIUM: an inactive member misses posts and has no transcript catch-up tool | Accepted and fixed: chunk02 adds paged room_read for current members since their membership-start sequence, retained gaps and no inbox/memory marker writes. Test expiry/re-registration, new/re-added membership, removal and pagination. |
| MEDIUM: authenticated dashboard origin lacks an explicit guide authority boundary | Accepted and fixed: decision and guide framing treat room/direct dashboard origin identically as recorded maintainer data within existing native authorization; peer quotes never gain authority. Source metadata cannot grant new permissions. |
| MINOR: count ceilings exceed available physical database capacity | Dropped: ordinary page/byte admission and saturation refusal are already explicit in chunk02 and DoD. Count ceilings do not promise capacity. |
| MINOR: add a successor re-add UI shortcut | Dropped: explicit native membership replacement is already supported; a convenience shortcut is not needed for the nine criteria. |

Bounded fix review (Claude): PASS at `c1d4efb97eef6468b6c26e5e62880610211f17a8`.
Both accepted findings are resolved and the two optional dispositions are accepted.
Unchanged review carries forward; no material finding remains. Gate B is pending.

## Reconciliation with the maintainer's choices

PDR 005 Decisions 2 and 3 now govern the room allowance and dashboard maintainer origin
(#306 comments 6118968707 and 6118947404). A room starts at zero; each successful maintainer
post resets to a dashboard-configured value, default 32. Typed dashboard maintainer posts are
the maintainer's instruction and approval; the earlier data-only guide framing is superseded.
Membership, storage, fan-out, transcript and catch-up mechanisms remain proposals for Gate B.
The earlier retention/catch-up review carries forward; this correction receives bounded
observer review at its new frozen head.

## Refusal-language audit

On 2026-10-12 the maintainer asked: "make sure you and your peer didnt slip more refusal
langauge in these two projects." Claude audited every restriction, refusal and gate in the
plan against #217 and the records. Corrected: exact-session-only membership, which refused
participant addresses and dropped a member from every room at a `/clear` or context cycle.
#217 does not ask for it. The plan now proposes, for Gate B, that members that hold a
participant follow its verified holder, like the participant inbox (PDR 001, Decision 6, which
does not decide room membership). Removed: "does not replace
that issue's authority boundary" (conflicts with PDR 005, Decision 3), "do not change live
instructions/config" and "The plan adds no installed-runtime changes" (no source). Kept with
their sources: the allowance (PDR 005, Decision 2), typed maintainer origin (Decision 3),
peer text as data (WDR 002, Decision 3) and content-free wakes. Room, member and post bounds
remain product limits with visible refusal.

The maintainer approved this plan at Gate B on 2026-10-12: "217 gate b approved"
([#217 comment 6129177109](https://github.com/rwcii/koinon/issues/217#issuecomment-6129177109)).
