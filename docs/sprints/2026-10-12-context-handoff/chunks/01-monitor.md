# Chunk 01 — context policy and observations

Build from [the decision](../decision.md), [DoD D1, D2, D6, D13](../definition-of-done.md)
and [the shared contract](../sprint.md). The maintainer assigns the driver; the other family
observes a frozen complete increment. This chunk executes no terminal command.

## Changes

1. Extend durable dashboard settings with the six family mode/threshold pairs and their
   checked defaults. Keep existing timing settings and native compaction behavior intact;
   the Codex default is the plan proposal, not a new quote. One atomic change writes its
   audit and applies the decoded value after commit.
2. Add the bounded private session Enable record and compared-revision dashboard actions.
   Enable capability is initially unavailable until chunk 03 supplies a proven adapter.
   Family edits enable no present/future session. Disable invalidates the session's pending
   cycle revision; no holder/claim changes occur.
3. Extend trusted Claude/Codex observations with private native epoch attribution. Reuse
   checked same-host metadata from #304 and native rollout selection. Do not derive a new
   epoch from retained MCP caller identity or a low usage value. Other families keep their
   current unavailable source reason.
4. Add a pure trigger selector over active session, Enable, family policy, known adapter,
   context/epoch and two clocks. It returns eligible or a fixed reason and writes nothing.
   Track an epoch/crossing latch; no timer or unreadable sample silently refreshes source
   time. Compaction/new epoch clears the old latch.
5. Show family configuration, per-session Enable, source/confirmation ages and unavailable
   reason in the existing dashboard. Public projections omit native epoch/host/socket IDs.
   Existing page refresh preserves focus, disclosures and pending forms.

## Interfaces and bounds

Use the existing dashboard mutation boundary with `context-family-settings` and
`context-enable`, matching login, literal Host, Origin and CSRF. The form names a displayed
peer plus current session revision; resolve atomically. Family names/modes come from fixed
enums. Settings and Enable rows share the existing SQLite store, not a new sidecar.
Choose the migration version from then-current develop and update every downgrade fixture.
Session purge removes its inactive Enable metadata only when no active cycle references it.

Observation additions use native internal routes only. Strict model arguments never take
an epoch, PID or target pane. Unit-test source validation and register/report attribution
for every family, including retained Claude MCP identity after its native epoch changes.

## Required verification

Run D1/D2 with injected clocks and synthetic hosts. Include a one-minute confirmation of a
two-hour-old usage record, unknown limit, percentage equality and usage greater than limit.
Prove repeated policy edits do not enable sessions and race selection against Disable.
On restart, durable settings survive and observations/eligibility remain unknown until
new native evidence arrives. Existing observation and dashboard suites must pass.

Add PROTOCOL/USAGE/Unreleased for the independently usable policy/monitoring behavior.
Use all publication gates from the DoD, including exact-head independent peer/refusal
review and squash auto-merge. The chunk makes no native-cycle availability claim.
