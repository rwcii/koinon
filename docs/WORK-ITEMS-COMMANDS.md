# Work commands

The Go daemon implements the [approved work contract](WORK-ITEMS-V1.md). Work shares
the repository memory store in `state.sqlite3`. See [PROTOCOL.md](../PROTOCOL.md#work-items)
for the routes and [INSTALL.md](INSTALL.md) for upgrades from Python-era state.

## Interface

The CLI uses `koinon work OPERATION --as FAMILY:ID --consumer KEY` and
`koinon claim renew WORK_ID --as FAMILY:ID`. MCP exposes the same operations as
`work_create`, `work_start`, `work_update` and the other `work_*` tools, plus `claim_renew`.
`--consumer` is optional. A holder defaults to `participant:<address>`; a non-holder uses
`FAMILY:ID`. Only the current holder may use a participant key, including when named
explicitly. A successor continues the participant's live generation and deadlines; reconcile
the checkpoint before acting. Old native-session claims retain their owners. Work IDs are positional except on create
and list. Wire names are `work-create`, `work-get`, `work-list`, `work-propose`,
`work-edit`, `work-start`, `work-update`, `work-release`, `work-finish`, `claim-renew`,
`work-report`, `work-accept` and `work-reject`. Wire fields use underscores; CLI options use hyphens. Unknown work
request fields are refused. References are inert reported strings.

| Operation | Fields beyond work ID and consumer |
| --- | --- |
| create | Required title, criteria, non_goals, key, deadline; optional proposed_assignee or proposed_role, references, author. |
| get | Optional revision selects a retained scope revision instead of the current view. |
| list | Optional lifecycle, owner, proposed_assignee, assigned_to_me, after, stale, blocked, limit (1–100). |
| propose | Required if_revision and exactly one of proposed_assignee or proposed_role; JSON null clears it, or CLI `--clear-assignee`. |
| edit | Required if_revision and at least one of title, criteria, non_goals; a live claim requires its owner's claim_generation. |
| start | Required if_revision, checkpoint, next_artifact, progress_deadline, key, deadline; optional lease_seconds and resources. |
| update | Required if_revision, claim_generation, progress, checkpoint, next_artifact, progress_deadline; optional active/blocked lifecycle, blocker, references, renew_for. Blocked requires blocker text. |
| release | Required if_revision, claim_generation and final checkpoint; optional paired handoff_to and checkout_resource notify an exact same-repository peer to pick up the released checkout role. |
| finish | Required if_revision, claim_generation, outcome; completed requires references, withdrawn requires reason. |
| renew | Required claim_generation and if_claim_revision; optional lease_seconds. Work revision is unchanged. |
| report | Required if_revision, claim_generation, summary (up to 2,048 bytes), references, key, deadline. Releases the claim; the item stays reported. |
| accept | Required if_revision, report_seq, references, key, deadline. Finishes the item as completed. |
| reject | Required if_revision, report_seq, references, criteria, non_goals, key, deadline; optional title. Writes a new scope revision and returns the item open. |

Schema 16 adds the approved role path ([PDR 004, Decision 1](product/pdr/004-work-results-role-routing.md)):
create and propose accept `proposed_role: {"family":"codex","role":"worker"}` instead of
`proposed_assignee`. Both fields are mutually exclusive. Supply exactly one on propose;
JSON null clears the proposal. The CLI accepts `--proposed-role '{"family":"codex","role":"worker"}'`
and `--proposed-role null`. Empty role names the family's default participant, including
DeepSeek. Family and role use the supported family and launcher role grammar.

Current views and summaries include `proposed_role` and, when set, `role_resolution` with
`state` (`held`, `unheld`, `conflict`), the known participant address, and its held peer name.
A native session's role start requires that participant's current holder and the usual resource checks.
It derives `native-work:` plus the SHA-256 hex digest of the JSON tuple `[family, native ID]`.
An explicit consumer must equal that key. The claim view labels this `ownership: native`;
legacy claims are labeled `legacy`. The binding lasts for the complete generation, including
after a proposal changes or clears. Every owner write and ownership retry checks the native
actor; a different session waits for release/expiry and starts a fresh generation. An active
original native owner can release its generation even after the participant changes holder.
Existing/default participant claims, cursors and inboxes continue as above.
Authenticated maintainer release/finish and explicitly marked purge keep their existing
administrative controls.
Their ownership retries retain the native claim's binding for agent callers as well.

Schema 17 adds the reported result and its verdict ([PDR 004, Decisions 2 and 3](product/pdr/004-work-results-role-routing.md)).
The claim owner reports with a summary and evidence references. The report releases the
claim and its resources, and the item stays `reported`, without a deadline or expiry, until a
verdict. Before it releases the claim, the report reserves that verdict in the store; when it
cannot (16 pending verdicts in the store, 32 in the daemon, no scope revision left on the
item, or no room after every other promise), it refuses with `capacity` and the claim stays.
A reported item refuses start, edit, update, release, finish and another report with
`invalid_transition`; propose keeps it reported. Get shows the frozen `report` (summary,
references, scope revision, claim generation); views and list summaries show `report_seq`,
`report_consumer`, `verdict` and `verdict_seq`, and `lifecycle reported` filters a list.

Accept or reject names the current `report_seq`; an older one refuses with `stale_report`.
The reporting consumer is refused with `own_report`, and so is any consumer of the native
session that reported. Another native session may record the verdict with any consumer,
including a later holder of the same participant whose default consumer differs from the
report's. Accept finishes the item as completed and starts its 30-day retention. Reject
writes the corrected scope as a new revision and returns the item open with no claim; a
fresh start and report follow. A verdict clears the progress, checkpoint, next artifact and
blocker of the reported round, and its evidence replaces the item's references; get still
shows the frozen report with its own references. Report, accept and reject events stay in
the item's history. The verdict is the recording agent's data and
grants no authority.

List also accepts `created_by_me` (`--created-by-me`), matching the caller's consumer. These
current queries reveal retained work even after the continued memory cursor has passed its
original transition. A daemon work notice names the repository and changed head: query
current work, process memory deltas through that head, and acknowledge after handling.

Work notices coalesce per audience and use the existing inbox/provider lifecycle. Proposals
and changes reach current assignees; removed assignments reach previous assignees; reports
reach creators; verdicts reach the reported worker's participant or recorded native actor.
Current registry and cursor provenance resolve recipients. Status shows unresolved audiences
and the 256-row limit; full queries remain available when a notice cannot be queued.

List accepts `assigned_to_me` (`--assigned-to-me`), matching explicit consumer proposals or
the caller's actually held family/role. `after` (`--after WORK_ID`) pages in work-ID order.
Responses carry `more`, nullable `next_after`, and the compatible `truncated` flag. Page until
`more` is false; the limit remains 1–100 and each response remains bounded. These are current
queries, not a frozen inventory: use a fresh query and retained memory deltas for assignment
changes behind a page boundary. An already advanced participant cursor does not hide current
assignments and needs no reset. Until the discovery chunk ships, poll work and memory; this
increment does not emit work-change model notices.

The CLI checks unconditional required options before contacting the service. These
missing-option refusals return JSON with `ok: false`, `code: invalid_request` and exit 1;
missing options are named explicitly. The service independently checks required
wire fields. Conditional requirements below remain service-validated. A service refusal
carries its rule as `error` text, for example `renew_for is 60 to 3600 seconds` or `missing
required fields: deadline` for a `key` without its `deadline`; the CLI prints it in `detail`.

Other mutations accept optional paired key/deadline and reported author fields.
CLI references repeat `--reference`; start resources repeat `--path-resource` or
`--exact-resource`. JSON resources are `[kind, key]` pairs. Claim generation is a
durable writer token, distinct from the service-instance generation used for
endpoint targeting. Lease duration defaults to 900 seconds; `lease_seconds` and
`renew_for` are bounded to 60–3,600. Progress deadlines must be after server time and within one day; retry
deadlines follow the existing 24-hour horizon.

Results contain work ID, revision, sequence and duplicate flag. Start/update/renew
also return claim generation/revision/expiry; renewal's sequence is null. Replay
returns the original bounded result before mutable preconditions or reconciliation.
Transport service generation is excluded from fingerprints so retries can survive
restart; claim generation and all semantic request fields remain bound to the key.

## Transactions, claims and recovery

Work writes commit the item, scope/event history, durable counters and replay result in one
serialized store transaction. Only committed changes can wake readers. Renewal extends a
lease without reporting progress or writing a work event. A mutating operation may reconcile
a due transition before returning a revision refusal, so reread the item rather than assuming
that every refusal means the revision stayed unchanged.

Get/list return observed ownership and progress without renewing a lease. A claim is advisory:
it never authorizes work, a takeover or configuration changes. Before writing, obtain the
current revision and claim generation. Use both on update/release/finish, and use claim revision
on renewal. Read the retained checkpoint before restarting an expired claim under the current
native identity. Work history and replay retention are finite; see
[WORK-ITEMS-GO-STORAGE.md](WORK-ITEMS-GO-STORAGE.md).

Snapshot and delta readers use the memory operations in [USAGE.md](USAGE.md#shared-memory).
Page every frozen snapshot before acknowledging its ID; process deltas before acknowledging
`next_cursor`. Snapshot work views are frozen; later events arrive as deltas. Work text is not
part of note recall. Capacity refusals preserve data and cannot be bypassed by creating a new
store or rewriting counters. Refer to [PROTOCOL.md](../PROTOCOL.md#work-items) for the exact
response fields, refusal codes and maintenance bounds.

## One writer in a checkout

`koinon work checkout [--directory DIR]` derives an advisory resource locally, without a
daemon or caller. MCP `work_checkout` and `koinon work checkout status --as FAMILY:ID
[--directory DIR]` also report its current writer. The key is `checkout:v1:` plus the SHA-256
of the JSON pair of canonical Git common directory and worktree root. Subdirectories and
symlink spellings converge; linked worktrees have different keys in the same logical store.
Inherited Git environment overrides cannot change the identity. Bare repositories refuse.

Include the returned `["exact", KEY]` in `work_start.resources`, or pass the key through
`--exact-resource KEY`. Every cooperating writer in that checkout uses the same resource,
even when working on different items. An overlapping start conflicts atomically. Separate
worktrees can each have a writer. This is opt-in coordination: it installs no hooks and
cannot stop an agent that omits the resource or writes outside the agreement.

Checkout status returns `repository`, `directory`, `resource`, `observed_at`, `state` and
`writer`. The writer carries `work_id`, `generation` (the role token), `consumer`, the exact
native `peer` when addressable, the work `revision`, lease `expires_at`, `lease_valid` and
`checkpoint`. The resource plus work ID/generation identifies the role; it is observable
data, not a secret capability. `held` means a current advisory lease; `released` and `expired`
describe a retained former lease. `unclaimed` means no retained matching bundle, including
after bounded maintenance reclaims an inactive bundle. Work checkpoints remain on their
items; no status, expiry or notification proves the former writer stopped editing.

For a shared-checkout driver handoff:

1. The requester reads checkout status, then calls `work_checkout_request` with an optional
   `note`, or `koinon work checkout request --as FAMILY:ID [--directory DIR] [--note TEXT]`.
   This sends an ordinary inbox message to the observed writer's exact native peer, naming
   its work ID/generation; it changes no ownership. An unheld role refuses. A custom consumer
   with no native recipient refuses with `writer_unaddressable`; coordinate with its owner.
2. The writer reads the request, verifies the requester through peers and rechecks the role.
   After stopping edits and saving a checkpoint, it calls `work_release` with `handoff_to`
   set to that exact peer name and `checkout_resource` set to the key, along with its current
   revision, generation and checkpoint. CLI options are `--handoff-to NAME` and
   `--checkout-resource KEY`. Use paired `key`/`deadline` for safe retries. The whole work
   claim bundle is released; its checkpoint and the pickup message commit together. Aliases,
   foreign-repository peers, inactive recipients, stale tokens and resources outside the
   owned bundle refuse without a partial handoff. Adding a message uses ordinary storage
   admission; a capacity refusal can still be followed by a plain release and manual
   coordination using the existing funded release allowance.
3. The requester reads the notification, checkpoint and current checkout status, reconciles
   the stopped writer's work, then explicitly `work_start`s the assigned item with the same
   checkout resource. Only a successful start records the new writer and issues a new
   generation. The handback reserves nothing: another successful start may win first, and
   a delayed notification cannot override it. Acknowledge inbox messages after handling.

The requester may pick up the same item or its separately assigned item. Requests and
notifications do not assign work, change sandbox permissions or permit an automatic takeover.
Readers can inspect the checkout role without claiming it; writers renew the existing lease
and report progress through the usual work operations.
