# Staged work maintenance

The schema-5 maintenance implementation is exercised on synthetic stores. Public
startup remains schema 4: its initial maintenance check reports disabled and exits.
No activation flag, migration at startup, participant opt-in, or work capability is
introduced here.

After the service prints readiness, a separate task immediately submits one ordinary
database-worker job. It waits 30 seconds using the event loop's monotonic timer after
each completion or skipped submission. It never overlaps jobs or uses the reserved
status/stop queue. Queue saturation skips that interval; storage failures retain a
maintenance fault and retry on the next interval. These are retry bounds, not a
wall-clock delivery guarantee.

Each job selects at most 32 due targets and calls the same target reconciliation used
by validated mutations. Reconciliation emits at most one event per target; expiry
wins over overdue progress. Each transition has its own transaction and publishes a
content-free change hint after commit. Partial batch progress survives a later
failure and durable markers prevent duplicate events on retry or clock reversal.
The current capacity of 16 claims is below the batch bound.

The job then reclaims at most one inactive claim bundle (and its at-most-nine
resources) using shared control headroom while preserving remaining work debt, and
at most one expired finished work item. Start-boundary bundle cleanup still requires
ordinary admission.
Finished reclamation atomically removes the current record, up to 64 scope revisions,
and up to 2048 paired stream/history rows. It advances the stream floor to at least
the largest removed sequence without moving the head or identity counters. Frozen
snapshot payloads and retained idempotency responses remain intact. Work events have
no FTS posting to remove. This control transaction preserves every remaining funded
obligation; a capacity or storage failure rolls back the whole item deletion. The
expired item remains hidden from queries but physically intact for a bounded retry.
An inactive claim must be reclaimed before its associated finished item. Unfinished
work is never selected for retention cleanup.

Status includes `work_maintenance`: `enabled`, `last_successful_sweep`, `observed_at`,
`pending_due`, `expired_items`, `inactive_bundles`, `fault`, `fault_at`, and
`skipped_submissions`. Counts are read-only observations, available even when writes
are blocked. If the worker cannot answer status promptly, the service returns the
last cached observation with its original `observed_at`; it is not a fresh count.
A null successful-sweep timestamp means no successful sweep has been recorded in this
process. Diagnostics reset at service restart; durable event markers do not. Corruption
refusals stop the batch and remain visible as faults; maintenance never skips corrupt
claim accounting to delete other data or silently repairs it.

Shutdown stops scheduling, cancels and awaits the loop, and then drains accepted
worker jobs before closing the store. Cancelling the loop never abandons a transaction
already accepted by the worker. Maintenance does not execute participant content,
change owners, bind peers, or send model messages.

Activation, mixed-version binding compatibility, participant opt-in and the remaining
upgrade/capacity documentation are later integration work.
