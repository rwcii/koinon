# Chunk 02 — correlated handoff contract

Build after chunk 01 merges, from [the decision](../decision.md),
[DoD D3–D5, D10–D12](../definition-of-done.md) and [the shared contract](../sprint.md).
Use the delivered #145 catalog/read interfaces; state inspection/adoption is not needed to
install anything. This chunk implements the durable contract and injected controller,
with native input unavailable until chunk 03 proves and supplies its adapter.

## Changes

1. Add bounded cycle/event schema and page debt. Implement the shared compared-revision
   state transitions, fixed deadlines, credit release and bounded retention. Write the
   structural page derivation alongside the storage documentation before claiming that
   admitted final transitions are funded. Test actual maximum-size rows and fragmentation.
2. Implement atomic admission: current eligible session/policy, one cycle per native
   session and host/start, credit, fixed typed handoff inbox request, event and audit.
   Repeated ticks cannot duplicate the request. Ordinary capacity refusal stores nothing.
   Existing wakes receive only the inbox/range notice after commit.
3. Add own-cycle status, ready and pickup native interfaces as specified in the shared
   contract. Ready/pickup obtain caller and epoch through the native client. Bearer routes
   recheck active registration, pinned/requested owner and transition revision. Another
   native peer, a model-supplied unknown field or ordinary inbox reply cannot advance it.
4. Verify the ready artifact through the checked main checkout and rooted metadata reads.
   Keep its body out of the daemon. Record basename, inode/size/time metadata privately.
   The agent, not the controller, writes its own canonical handoff. Preserve participant
   claims and require their current owner/generation under existing work rules.
5. Implement cancellation, timeout, restart classification and query fallback with an
   injected no-input adapter. Add read-only dashboard cycle stage/deadline/reason history;
   wait polling does not create repeated records. Each accepted mutation/audit is atomic.
6. Extend guidance to explain the typed request, readiness/pickup reporting and own-cycle
   queries without a second handoff/pickup prose implementation. Canonical procedures
   remain the catalog source. Do not present this intermediate chunk as a working reset.

## Required verification

Run D3–D5 with temporary common directories and linked worktrees, synthetic families,
private native roots and ephemeral daemons. Exercise duplicate/lost ready replies,
conflicting reports, old epochs, expired/removed/replaced holders, another role, copied Git
pointer, path traversal, symlink swaps, wrong family, empty/stale/nonignored artifacts and
large basenames. Reads change no ordinary inbox, memory or work marker.

Crash before/after admission and every state commit; recover exactly one request and its
fixed cycle ID. Test deadline equality and monotonic transition revision. Fill ordinary
storage after maximum concurrent admission; every cancel/timeout/failure/end control and
existing funded work verdict must still complete. Verify no audit/status/wake includes a
private native ID or body. Audit quota/retention must not consume promised final capacity.

Update PROTOCOL/USAGE/Unreleased and tool-purpose/family-guard contributor coverage. Use
the full DoD publication gates, exact-head review and squash auto-merge. The next chunk
depends on this merged contract, not an unpublished author checkout.
