# Increment 4: Attention and diagnostics

See [decision](../decision.md), [definition of done](../definition-of-done.md)
and the original `docs/REMOTE-CONTROL-V2-SPEC.md`.

Make Attention the remote default while keeping local landing behavior. One source
snapshot gives waiting decisions/oldest age, direct-to-maintainer unread messages,
canonical room posts, source links and recent receipts. Browser-specific explicit
read cursors never acknowledge an agent inbox or resolve a decision. Cached agent
activity is an observation; unknown/stale values must remain labeled.

Show unresolved browser operation IDs separately and link to inspect their targets.
Consolidate observed session count, storage health, reconciliation time and stable
rejection reasons. Distinguish host configuration, server-observed phone request
and unverified cellular access. Do not log credentials, message bodies or QR data.
Retain existing typography/mobile style and route validation. No authenticated
page cache, background-write queue, push or terminal streaming is added.

Checks: UI-1, LIFE-1, same-snapshot counts/links, cursor monotonicity and isolation,
no inbox acknowledgement, unavailable observations and unsupported receipt records.
Chrome layout fixtures are synthetic; actual Safari/Chrome at 360/390px, keyboard,
zoom and elapsed retention remain separately measured acceptance.
