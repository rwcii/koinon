# Chunk 02 — reported result and funded verdict

Implements decision criteria 4–5 and 8–9. Depends on chunk01 and its native ownership model.

## State and operations

Add MCP/CLI/API `work_report`, `work_accept`, `work_reject`. Report requires live ownership,
generation, expected revision, evidence references, and a fixed idempotency key/deadline. It
atomically records a `reported` event tied to the current scope, releases the claim/resource
bundle, clears its progress deadline and keeps an immutable report sequence/summary. Keep
the full frozen report in the existing work-event payload; item fields reference that event
instead of duplicating its large text. A reported item cannot be started or edited into another
result; rejection is its rework transition. Unreported v1 finish/withdrawal remains available.

Accept/reject require current item revision, exact report sequence, evidence and fixed retry
fields. The reporting consumer and reporting native session cannot record the verdict
(PDR 004, Decision 3). Store a bounded native-actor digest from the trusted transport and
compare it as well as the consumer before verdict replay and in the committing transaction;
changing consumer labels does not make the reporting session a different worker. A different
consumer from a different native session is eligible, including after succession in the same
worker participant. Participant equality adds no verdict exclusion. Keep normal native-caller,
repository and participant-consumer checks (ADR 001, Decision 1). Participant metadata may
describe provenance/discovery audiences. The verdict remains same-user reported data
(WDR 002, Decision 3).

Accept records an accepted event and finished/completed outcome; 30-day retention starts at
acceptance. Reject requires corrected criteria/non-goals (optional title/references), always
creates a new scope revision, retains the old report and both scopes, and returns open with
no claim. Reported items do not expire. Stale report sequences/revisions refuse atomically;
retries use existing fingerprint/deadline rules. Expose report/verdict references in get/list,
memory snapshots/deltas and dashboard work views without adding a decision-board API.

## Funding and bounds

A committed report must fund its later verdict. Add an unindexed 0/1 verdict-credit flag
on the reported work item. Report is an ordinary, capacity-checked transaction: acquire its
future verdict reservation before releasing the old claim or acknowledging the report.
If it cannot fund the page/byte/slot/scope promise, refuse atomically and retain the old
claim and guaranteed release/finish. Existing end/overdue credits are released consistently
with the claim, but report does not gain permission to consume ordinary space earmarked for
other controls. At most 16 pending verdict reservations per store and 32 daemon-wide.
Verdict spends that credit in its transaction; retry/rollback never spends it twice.

Claim start keeps its existing end guarantees and does not require a future scope-history
slot merely to begin. Thus at the scope-history limit, a worker can still start and withdraw
instead of stranding a rejected open item; a subsequent report explicitly refuses if it
cannot promise both possible verdicts. Already reported items always retain their verdict.

Use existing tables and indexes only. Current controls touch ten allocating objects with
raw bound 710 pages. Reject adds a scope-history row and its primary index, at most
`(69+5)+69 = 143` pages. Pointer-map allowance gives `853 + ceil(853/819) + 1 = 856`;
reserve **1,024 pages** per verdict. Keep work images/events at 16 KiB, replay results at
2 KiB and new bounded metadata within the existing overflow-page assumptions. Validate
these row/header bounds in the implementation; a schema/index change requires revising the
derivation before claiming this reserve sufficient.

Reserve one stream entry, event, replay and future scope row for each verdict, plus **64 KiB**
logical bytes (image, event and scope at most 16 KiB each; replay 2 KiB; at most 14 KiB for
keys/provenance/row charges). Every ordinary work/memory write subtracts those reservations
from shared ceilings, including per-item/per-store scope limits. Credit spending uses flag
overwrites and cannot depend on an ordinary-space check. Maximum page debt is
`32*1344 + 32*1024 = 75776`, leaving the existing reserve and a positive ordinary band.
No new notification row is written in a funded verdict transaction; chunk03 reconciles afterward.

Extend schema guards, migration, debt/usage/status and saturation tests and update
WORK-ITEMS-GO-STORAGE.md with the complete derivation. Reserve observation/report metadata
before acknowledging a report promise; maximum-size rows and the legacy migration cases
must not make a funded verdict fail its view/byte bound. Preserve all old controls.

Update protocol/commands/guide, actual permission-purpose coverage and Unreleased. Verify
report/accept/reject, reporting-consumer/native-session refusal across consumer labels,
different-session verdicts after role succession, immutable history, retries,
resources released, retention and all capacity dimensions before publishing.
