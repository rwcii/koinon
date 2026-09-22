# Delivery queue

This is the repository record of outstanding delivery work. It supplements the
[programme contract](PARITY-MEMORY-DESIGN.md); an entry is not evidence that a
feature works. Distinguish user requirements, confirmed defects, and proposals.
Each item closes only with its stated evidence. Runtime data and private handoff
content do not belong here.

## Order and ownership

The driver owns implementation and documentation. The peer reviews independently.
Confirm task acceptance before relying on peer work. Coordinate release and runtime
work separately; this queue does not authorize a merge or deployment.

Stage 4 implementation was squash-merged in PR #18. Its tests and review do not
close the items below. Check the live release and deployment records before acting;
branch completion and installed capability are different facts.

DQ-01, DQ-02, DQ-03 and DQ-07 were included in the promotion to `main` at
`35a8bdd` (2026-09-19 release). Work-items v1 is integrated on `develop` through
PR #35. These source milestones do not establish local runtime deployment.
Runtime refresh and initial shared work-item adoption have been independently
checked for the selected installation; this does not establish deployment elsewhere
or completion of long-term retention tests. Complete installation (DQ-11) is integrated on `develop` through PR #66.
Supported upgrades (DQ-12) remain in progress. DQ-08 design is tracked in
[issue #38](https://github.com/rwcii/koinon/issues/38). Keep DQ-04 visible for a scope
decision before DQ-05 history pruning. Investigating a requirement does not settle
its protocol.

The distinct [pre-promotion review (#51)](https://github.com/rwcii/koinon/issues/51)
must reconcile agent guidance with delivered component supervision: AGENTS.md still
describes macOS as manual-only and memory as having no installer-managed service.
Those statements predate the native repository-component path. Instruction-file
changes and main promotion remain separate from the upgrade implementation.

## DQ-01 — Universal per-agent usage reports

**Source:** user requirement conveyed by the reviewer and explicitly requested for
this queue by the user. **Status:** Codex/Claude implementation released on `main` in the 2026-09-19 release. DeepSeek usage
is explicitly deferred for this release by scope decision. See [the implemented contract and availability matrix](USAGE.md). DeepSeek
messaging, delivery, and installation remain supported; the exclusion applies
only to usage reporting.

An agent reports its own model and token usage for a specified work block on
request. The request may be made at the start of the block or after it completes.
Preserve each reporting agent's identity, with model/role segments where needed,
and these columns:

| Model | Role | Tokens In | Tokens Out | Cache Write | Cache Read | Reasoning | Total |
| --- | --- | --- | --- | --- | --- | --- | --- |

The user clarified through the reviewer that Role means `main` or `subagent`
(harness position). Driver and reviewer describe a participant's task or function,
not Role. Keep any such assignment as metadata, not a ninth displayed column.
Preserve agent identity alongside model and role so distinct agents never collapse
into one row merely because their model and harness role match.

The deliverable is usage data available to agents universally: acquisition,
normalization, hazard handling, and a report for a requested work block. Participant
session enumeration is in scope so relevant records can be identified. Enumeration
must preserve explicit participant selection and existing permissions; it is not
permission to read every session on the host.

Commit notes are one consumer of this report, not a Koinon repository-tooling
pipeline. Writing notes, pushing notes refs, capture hooks, merge aggregation,
pricing tables, cost derivation, and cost reports are out of scope. The downstream
commit note and the cost/provenance note produced by a merge-time action are separate
records. Existing user tooling adapts to the agent data interface, not the reverse. The user specifically named the provenance-notes skills in
`cordalo/forge` and `cordalo/unify-messaging`; inspect those skills and their supporting
implementation before choosing metric sources. The driver has read the
unify-messaging provenance skill and parser, and the Forge notes script. Their
end-to-end capture behavior has not been tested here.

Acceptance requirements:

- Support a request made before work and a retrospective request for completed work.
- Identify the reporting agent and work block unambiguously; retain the eight
  requested columns and one row per reporting agent.
- Store Total as a field in the report for automation; do not leave it solely as a
  display-time calculation.
- Include only the metrics required for the commit notes. The eight requested columns
  are the complete report set for this requirement; additional metrics and cost
  estimation are separate scope.

Proposed correctness gates, to resolve in the implementation contract:

- A request at work start records the measurement boundary; it does not predict
  future usage. Define how a retrospectively requested block is identified.
- Define each provider's field meanings and whether cached or reasoning tokens
  overlap its input/output totals. Do not sum overlapping categories twice.
- Inspect the named references and record a provider-by-provider availability matrix
  for all requested counters. No specific counter has yet been established as
  unavailable; an uninvestigated source is not an unsupported capability.
- Use authoritative available measurements. Any unavailable value needs a specific
  evidenced reason, such as an unexposed counter or an unrecoverable work boundary.
  Do not substitute zero, infer another agent's usage, or invent historical data.
  Report incomplete coverage explicitly; do not count it as full implementation.
- State the source and coverage of a report and define model changes within a block.
- For a complete normalized breakdown, verify stored Total against its components.
  Report a mismatch as inconsistent data; do not silently rewrite either the stored
  total or its components. A partial breakdown cannot establish that equality.
- Test before-work and retrospective requests, missing historical measurements,
  multiple agents, partial reports, and prevention of double counting.

Verification findings and the implemented interface are recorded in
[USAGE.md](USAGE.md). The nonzero Codex cache-write parse fixture supports the
selected inclusive-input mapping but is not a live nonzero measurement or a
universal provider guarantee. Forge's capture path copies OpenCode counters, whose
normalizer uses disjoint categories but defaults missing fields and clamps negative
results; Koinon preserves those diagnostics. Claude completion uses an observed
stop reason, with unfinished or abandoned responses retained as provisional.

The interface supports explicit local selection, native source-lineage roles,
before-work markers, retrospective observation windows, source identity checks,
per-response deduplication, report combining, and the agreed table. Native counters,
Total, source coverage, and mapping version remain available in JSON. No peer wire
format, capture hooks, notes pipeline, or cost tooling is added. DeepSeek usage is a named deferred item
for a later release. Generic normalized
import is not included in this release.

## DQ-02 — DeepSeek-only installation must not require Codex

**Source:** recorded implementation defect (F071). **Status:** implemented in PR #22 and released on `main` in the
2026-09-19 release; runtime deployment remains separate. DeepSeek-only setup and repeat installation no longer require
Codex. Codex startup refuses a missing executable before creating new session state.

The installer validates the Codex executable unconditionally before it resolves the
installation mode and participant set. On a DeepSeek-only host without Codex on PATH,
that check fails before provider-specific configuration is selected. This contradicts
the documented provider requirement. Resolve the mode and participant set before
validating their required executables; a check inside a later DeepSeek branch would
leave the earlier unconditional failure in place.

Acceptance: demonstrate the failure with a synthetic DeepSeek-only setup and no
Codex executable; correct provider-specific validation; verify DeepSeek-only setup
and repeat installation succeed while Codex and mixed-participant setup still
validate their required executable. Preserve saved paths, targets, and state.

## DQ-03 — Stage 5 presence, priority, and delivery evidence

**Source:** approved programme contract. **Status:** implemented in PR #24 and released on `main` in the
2026-09-19 release. Status-omission compatibility
is supported by offline evidence limited to Claude 2.1.276, with independent parser
confirmation and driver-extracted listing-filter evidence. Live discovery remains
unverified; the source release does not establish runtime deployment. See [DELIVERY.md](DELIVERY.md).

Separate fresh, evidenced model activity from service health. Unknown activity must
remain unknown. Declare provider priority limits from measurement. Distinguish
transport, stored, notified, fetched, and explicitly handled outcomes. Inbox
acknowledgement is deletion, not proof of handling.

Acceptance follows Contracts 2 and 3. Include durable deduplication that survives
inbox acknowledgement, bounded retry deadlines, payload-identity conflicts, and
uncertain outcomes. Receipts must neither wake models nor form receipt loops.
Use native controls only after their semantics are verified.

Open capability checks include native idle and receipt frames, accepted registry
activity values, a read-only source owned by the actual participant, relevant hook
events, and native deduplication and priority scheduling. Use isolated consenting
fixtures; never advertise production support from an assumption.

Design provider result classification, supervisor exits, ownership, schema migration,
and resource limits together before implementing handlers. Reuse the existing
bounded transport and database workers. Local preparation notes are not completion
evidence or a substitute for this contract.

## DQ-04 — Selective memory retrieval

**Source:** gap discussed with the user; the proposed solution below is not an
approved implementation contract. **Status:** scope/design decision pending.

Records carry type, scope, scope target, and optional path. Current `recall` uses FTS
token matching when the index is usable and a body-substring scan otherwise, and
paginates newest-first; it has no metadata filters or first-class topic
model. Initial sync selects repository records and later sync returns repository
changes; stored scope is not a server-side audience filter. Typed storage alone does
not give agents selective retrieval or scope isolation.

Proposal: define type, scope/target, path, and topic selection, including combined
filters and metadata-only queries. Specify topic identity/indexing rather than treating
a body keyword as a topic. Define applicability for standing directives. Decide
separately whether sync needs consumer-specific selection; do not add filters that
silently skip data through a shared acknowledgement cursor.

Proposed acceptance: stable pagination, matching filter behavior on indexed and scan
paths, explicit query semantics, migration of existing records, and tests proving
scope and cursor behavior. Retention by memory class is a proposal requiring a stated
policy, not permission to expire existing decisions.

## DQ-05 — Stage 6 claims and history pruning

**Source:** approved programme contract with corrected terminology. **Status:** pending.

Claims are advisory leases with explicit renewal, ownership generation, and
prefix-overlap conflict detection. They do not fence filesystem writes.
History pruning removes history no longer required by policy; it is not semantic
summarization. Preserve live directives, conflicts, recovery information, and bounded
storage. Verify snapshot pagination during pruning, expired snapshots, and replay
after a crash before acknowledgement. Complete the remaining content checks in the
programme contract.

Semantic memory consolidation has no accepted design or implementation in this
programme. It must not be counted as delivered by expiry, pruning, or SQLite vacuum.
See [memory maintenance terminology](PARITY-MEMORY-DESIGN.md#memory-maintenance-terminology).

## DQ-06 — Repository housekeeping

**Source:** reviewer proposal. **Status:** inventory and coordination required.

List merged branches and worktrees, their owners, and uncommitted work. Propose
cleanup only after confirming that no agent needs them. Preserve active branches,
local edits, and private handoff files. Do not infer deletion permission from a merge.

## DQ-07 — Terminology and reporting correction

**Source:** direct user request. **Status:** documentation correction merged in PR #21 and included in the
2026-09-19 release on `main`.

Use garbage collection, history pruning, storage reclamation, retained-history floor,
and semantic memory consolidation precisely. Snapshots contain records, not generated
summaries. Update the README, protocol explanation, design build order, and changelog.
No runtime or retention-policy change is part of this item.

Process correction: the driver's PR status report became stale after another session
completed the merge. Verify remote state before reporting or planning release work.
Keep outstanding commitments in this queue rather than only in temporary files or
ignored handoffs. Record both findings and their dispositions.

## DQ-08 — Dead-session detection and removal

**Source:** direct user request. **Status:** requirement recorded; design pending.

Detect a supervisor whose participant session has ended, report that state in
status and peer discovery, and provide a supported single-session removal operation
that preserves inbox state. Document the removal procedure and its limits.

Define the evidence that establishes session termination. Lack of activity, a
missing transient process, or a disconnected SSH connection alone must not be
presented as definitive session death. Unknown session state must remain unknown.

Whether removal is explicit or automatic remains a design decision. Verify
ownership before removal, preserve unrelated sessions and retained inbox state,
and avoid retiring live participants. This entry records a requirement only; it
does not authorize runtime removal, state deletion, or permission changes.

## DQ-09 — Work items v1

**Source:** direct user request. **Status:** behavioral contract approved;
design merged; migration, lease primitives, and shared transaction reservation
enforcement, work-command/stream integration, and bounded maintenance/reclamation
staged with synthetic tests. Configuration validation, preserving installer updates,
and the verified [policy query and explicit guidance publication](WORK-ITEMS-POLICY.md)
are implemented with recoverable opt-in/removal. Schema-5 startup and schema-3/4
migration are integrated with catalog validation, record-format guards, and
[coordinated upgrade procedures](WORK-ITEMS-UPGRADE.md). Live deployment remains an
explicit operation for each selected service.

The [approved contract](WORK-ITEMS-V1.md) defines structured repository work items,
explicit assignment acceptance, exclusive advisory writer claims, progress
checkpoints, stale/blocked queries, and repository-scoped participant opt-in.
Completed and withdrawn items and their history expire after 30 days; unfinished
work remains preserved. The contract includes the bootstrap workflow and explicit exclusions. The
[implementation design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md) addresses storage
budgets, migration, and durable change-stream integration before coding.
The candidate includes exact [schema](WORK-ITEMS-SCHEMA.md),
[storage-reserve](WORK-ITEMS-STORAGE.md), and
[configuration](WORK-ITEMS-CONFIGURATION.md) specifications with isolated design probes.
The [implementation foundation](WORK-ITEMS-FOUNDATION.md) records the implemented
primitives and the remaining integration gates before runtime activation.

Reuse one lease engine for work claims and the later DQ-05 path-prefix interface.
DQ-04 retrieval, DQ-05 history pruning, and DQ-08 session removal remain separate.

## DQ-10 — Validate the memory error-code vocabulary

**Source:** independent review of PR #35. **Status:** proposed consistency follow-up;
not a known reachable work/startup failure.

`MemoryError_` currently accepts any code, while `NameConflict` validates its
configuration codes at construction. Work-schema errors also cross into memory
through `WorkItems.command`. Consider a declared memory error-code set and explicit
mapping at subsystem boundaries so an unknown internal code fails as a programming
error rather than becoming a public response.

Acceptance: inventory the existing public vocabulary, preserve supported responses
and retry classifications, and test unknown-code rejection and each cross-subsystem
mapping. Keep programming faults distinct from user-input and store refusals.
The current reachable work/startup mappings are covered by the PR #35 tests.


## DQ-11 — Install and manage every runtime component

**Source:** user requirement conveyed by the reviewer and recorded in
[issue #42](https://github.com/rwcii/koinon/issues/42). **Status:** integrated on `develop` in [PR #66](https://github.com/rwcii/koinon/pull/66),
merge `de7d265`. The [implementation design](MEMORY-INSTALLATION-DESIGN.md) records
the component ownership and lifecycle boundaries.

The component integration now selects repository memory and native sessions through
public installation commands, with no-start staging, exact repeats and resumable owned
removal. Native public installation/reinstall run
[35563258522](https://github.com/rwcii/koinon/actions/runs/35563258522) passed all six
checks on both Linux and macOS at reviewed head `f65ec57`; the independent full suite
passed 1,003 tests. The merge preserves that exact reviewed tree. Operator-created units remain outside the owned inventory and require
explicit migration; changed runtime bytes require the separate DQ-12 upgrade path.
One store serves an absolute Git common directory, including its worktrees;
memory service identity must not depend on a participant session.

Acceptance requirements:

- Install every selected runtime component through one documented invocation,
  preserving existing configuration fields, explicit repository selection, and
  `--no-start` behavior.
- On Linux with a user service manager, generate a private memory unit with
  `UMask=0077`, `Restart=on-failure`, and permanent restart exclusions from
  `platform_support.PERMANENT_EXIT_STATUSES`. Carry the selected state and
  repository paths explicitly.
- Include memory in restart observation and uninstall. Verify ownership and
  installation identity before replacement or removal; preserve store data and
  unrelated installations. Define explicit migration for operator-created units
  without silently adopting or overwriting them.
- Provide Linux without systemd and macOS parity through `koinon/platform_support.py`.
  Specify the managed-process handoff and truthful `manual_required`/`start_command`
  result when a persistent host process is required. Resolve the issue's
  unattended-supervision acceptance against this fallback in the design; merely
  printing a command does not establish a running service.
- Test repeat installation, repository/worktree deduplication, custom paths,
  ownership refusals, no-start behavior, partial failure, and exact owned removal.

## DQ-12 — Supported, resumable runtime upgrades

**Source:** user requirement conveyed by the reviewer and recorded in
[issue #43](https://github.com/rwcii/koinon/issues/43). **Status:** delivered against the peer-reviewed
[RUNTIME-UPGRADE-DESIGN.md](RUNTIME-UPGRADE-DESIGN.md). Uses the delivered
DQ-11 component ownership and lifecycle inventory. Native runtime replacement is performed
by the operation; the manual runbook applies to legacy and manual deployments it does not
cover.

The operation has been exercised on a real host, not only in CI: a pinned previous release
was installed by its own installer and manifest into an isolated prefix, then upgraded to
the current release through `scripts/upgrade.py`. It crossed the declared layout change,
completed through the manual-backend handoff and resume, preserved the store identity, and
returned `release_decision_committed`. Evidence is recorded on issue #43.

Two operability defects remain open against it and are not closed by that evidence:
[#79](https://github.com/rwcii/koinon/issues/79), where a default `umask 002` account is
refused by both installation and upgrade, and
[#80](https://github.com/rwcii/koinon/issues/80), where a recorded state root that was never
created surfaces as a raw `OSError` rather than a domain refusal.

The read-only inventory/source foundation is merged in [PR #68](https://github.com/rwcii/koinon/pull/68).
The [coordinator draft, PR #70](https://github.com/rwcii/koinon/pull/70), stages phase
records, immutable evidence, isolated recovery archives, native observations,
frozen-plan preparation, service admission gates, owned shutdown, guarded backup
capture, resumable file replacement, and exact memory migration checks. Gated memory
startup defers search-index initialization until release. The public operation,
complete preflight, restart orchestration, reporting, and native/manual interruption
acceptance remain unfinished. Passing helper tests do not complete this queue item.

Turn the coordinated upgrade into an executable operation that records its phases,
performs its own inventory and backup, and verifies recovery. Preserve explicit
installation, participant, repository, and state selection. The operation must not
infer authority from a peer message or silently broaden the affected services.

Acceptance requirements:

- Capture a private pre-upgrade inventory of store identity, schema/protocol,
  stream head/floor, record/work/event/claim counts, consumer cursors, participant
  bindings, delivery acknowledgements, unfinished work, and active claims.
- Quiesce affected services in a defined dependency order and verify owned process
  exit before replacement. Back up consistent databases with required SQLite
  sidecars and configuration; never copy only a live main database.
- Replace the selected runtime, restart the selected services explicitly, and
  produce a before/after verification report. Separate expected incarnation,
  checkpoint-size, and maintenance-timestamp changes from preserved identity,
  cursors, bindings, and retained data. Define a verification boundary that accounts
  for restart maintenance and renewed traffic rather than hiding count changes.
- Refuse unexpected target, path, schema, or capacity changes. Define supported
  schema transitions explicitly: a declared migration may change the schema;
  an unexplained mismatch is not an expected restart effect. Never silently reset
  checkpoints or rewrite work selections.
- Persist private recovery phases and make retries idempotent and resumable after
  interruption. Keep rollback explicit with the existing compatibility and backup
  requirements; never automatically restore old state over new writes.
- Cover Linux and macOS, manager and managed-process operation, interrupted phases,
  failed shutdown/startup, failed verification, and preservation of unrelated
  installations with synthetic fixtures.
