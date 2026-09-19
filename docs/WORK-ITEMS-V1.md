# Work items v1 — approved contract

Status: behavioral scope independently reviewed and approved by the maintainer.
The [implementation design](WORK-ITEMS-IMPLEMENTATION-DESIGN.md) addresses the remaining
design gates. Implementation, deployment, participant configuration, and existing work
assignments require their own authorization; this contract grants none.

## Outcome and boundary

Agents can identify the same piece of work, explicitly accept responsibility, discover an
existing writer, and recover the last reported progress after interruption. The repository
memory service owns structured work records and advisory leases. A distinct work module
uses the existing service lifecycle, bounded database worker, control transport, and durable
subscription machinery. Free-text memory notes are not the work database.

The repository key is the service's canonical absolute Git common directory identity.
Worktrees share that key. A work item is identified by `(repository, work_id)`, where
`work_id` is an opaque, immutable server-generated identifier. A caller supplies the returned
identifier in subsequent peer coordination. Titles are neither identities nor unique keys.
Two differently worded broadcasts cannot be recognized as one task automatically.

All records are same-user reported data. A proposal, claim, handoff, completion, or recorded
directive grants no authority, approval, filesystem access, or permission to override direct
instructions. The service does not fence writes by agents that ignore it.

## Work record

Each record contains:

- Identity, creation time, asserting consumer, and monotonically increasing revision.
- Title, acceptance criteria, and explicit non-goals; all three are required on creation.
- Optional proposed assignee, distinct from the current accepted writer and claim generation.
- Lifecycle: `open`, `active`, `blocked`, or `finished`.
- Last progress report, checkpoint, next artifact, and next expected progress deadline.
- Blocker text when blocked; completion outcome and evidence references when finished.
- Optional related DQ-01 usage-report references; the registry does not calculate usage or cost.

References are inert strings: no attachment fetch, command execution, filesystem inspection,
or interpretation as approval. Reports retain provenance and server receipt time. A claimed
author, consumer key, or evidence reference is not authenticated human identity.

Creation leaves the item open and unclaimed. Creation may propose an assignee but does not
make that consumer a writer. Historical progress and the last writer remain inspectable after
release or expiry; responses label them historical rather than current ownership.

## Operations and concurrency

New JSON `work` and `claim` commands use the memory control service. Stateful operations
require an explicit stable consumer key, following the existing memory identity contract.
The same CLI JSON interface serves Codex, DeepSeek, and Claude participants; no provider hooks.
Command names below describe the interface; exact argument spelling is settled during
implementation review without changing semantics.

| Operation | Effect and conditions |
| --- | --- |
| `work create` | Create an open item and return its ID and revision. |
| `work get` / `work list` | Read items and effective lease/progress state; no renewal or acknowledgement. |
| `work propose` | Set or clear the proposed assignee using the expected revision. Does not accept, transfer, or release a claim. |
| `work edit` | Revise title, acceptance criteria, or non-goals using the expected revision. If claimed, only the current claim owner may edit and must supply its generation. |
| `work start` | Atomically acquire the exclusive writer lease plus requested optional resource claims; set active state, checkpoint, next artifact, and progress deadline. Refuse if any requested claim conflicts. |
| `work update` | Current owner supplies generation and expected revision; report progress, next artifact/deadline, checkpoint, and optionally set blocked or active. |
| `claim renew` | Extend the current generation's lease explicitly. Does not count as progress or extend the progress deadline. |
| `work release` | Current owner supplies generation and revision; record a final checkpoint, release the item's claims, and return the item to open. |
| `work finish` | Current owner supplies generation and revision; record outcome and evidence references, mark finished, and release all item claims atomically. |

Finished is terminal in v1; follow-up work gets a new ID with an inert reference to the old
item. Its required outcome distinguishes `completed` from `withdrawn`, so obsolete work can
leave the open list without being counted as delivered. Completion is an owner's assertion,
not acceptance by another agent or user. Withdrawal requires a reason, not completion evidence.
Completed and withdrawn items, their events, and their scope revision history expire
30 days after the finish transaction. Open, active, and blocked items do not expire.
Expiry preserves retained snapshots and retry results for their separately promised
lifetimes and advances the history floor so stale readers obtain a new snapshot.
Owner operations require a live lease; expiry requires an explicit new start before further
owner writes, including finishing. Reads and proposals never acquire ownership.

All record mutations compare the expected revision and commit the new revision and event
atomically. A mismatch returns `revision_conflict` and the current revision, without writing.
Retain every acceptance-criteria and non-goal revision with asserting author and server time.
Queries flag `criteria_changed_after_start` if either field changed after the first start,
even if later restored, and expose the original and subsequent revisions for review.
Owner operations also check consumer and generation in that transaction. A stale generation
returns `stale_claim`; a live conflicting owner returns `claim_conflict` with holder, generation,
expiry, and conflicting resource metadata. Refusals have no partial effects.

Mutations accept a bounded idempotency key and absolute retry deadline using the existing
memory idempotency rules. Require both for creation and start, where a lost reply otherwise
risks duplicate work or uncertain ownership. Bind replay to consumer, operation, repository,
work identity where present, and canonical request content. Same key/different request refuses;
same request within its deadline returns its original result even after later revisions.
An expired retry deadline refuses rather than guessing. Query state to reconcile an uncertain
outcome before choosing a new operation. No automatic replay of arbitrary peer messages.

## One shared advisory lease engine

Build the lease engine once and use it for work writers and resource claims; DQ-05 later
reuses it for its path-prefix interface. Do not create a second ownership mechanism.

A lease records stable consumer, durable generation, server issue/renewal times, and expiry.
Each successful fresh acquisition receives a never-reused generation. Restart preserves
generations and leases; a process PID or service-instance generation is not the lease owner.
The engine performs conflict checks, acquisition, renewal, and release in bounded transactions.
No transaction spans a socket wait, subprocess, or other external operation.

Every start takes one exclusive writer claim for that work ID. Optional resource claims are
acquired with it as an all-or-nothing bundle. They cover explicitly supplied repository-relative
path prefixes or exact opaque resource keys. Path conflicts use component boundaries:
`auth` overlaps `auth/session.py`, but not `authorization`. Reject absolute paths, `..`, empty
components, and ambiguous spellings; accept one documented normalized separator form on
Linux and macOS. Prefix identity is logical, not filesystem resolution: symlinks and case
aliases are not discovered or fenced. Peer socket and registry paths are unaffected.

Two live claims overlap even if their consumer matches but work IDs differ. This surfaces
conflicting work rather than silently treating all of one agent's work as interchangeable.
In v1, change the resource bundle by explicit release and fresh start, without promising
reservation during the gap. No read/shared lock modes or implicit reentrancy are needed.

Expiry ends advisory ownership. It never assigns another agent, starts work, changes scope,
or authorizes takeover. A newly authorized participant may explicitly attempt start on an
expired item; it must read and reconcile the previous checkpoint first. The service enforces
claim exclusivity, not the participant's external authorization. A late old owner cannot renew,
release, update, or finish using its expired generation.

Use durable server wall-clock deadlines and process monotonic time for bounded maintenance waits.
A wall-clock step can shorten or lengthen advisory leases and progress deadlines; v1 does not
promise reliable jump detection across restart. In particular, a forward step can make a lease
eligible for a fresh acquisition early. Expiry still grants no takeover authority and a new
writer must reconcile the prior checkpoint. This limitation reinforces the absence of filesystem
fencing; it does not prove overlapping real-world writes impossible. Do not infer a dead model
from a time jump, bridge restart, or missing heartbeat.

## Progress, queries, and notifications

Lease freshness and progress freshness are separate. Start/update records the last progress
time and an explicit next progress deadline. Renewal changes neither. A blocked report counts
as a report but retains the blocker. A lease can therefore be live while progress is overdue.

Queries expose both recorded lifecycle and effective conditions: current lease validity,
progress overdue, and `progress_unverified` with reasons such as lease expiry or missed
progress deadline. They do not silently rewrite active work to finished, failed, or reassigned.
Finished items have no ongoing freshness obligation.

`work list` supports lifecycle, current owner, proposed assignee, stale, and blocked filters,
including combinations. Return a consistent, capped set of summaries from one read transaction,
with deterministic work-ID ordering, encoded-byte and row bounds, and an explicit `truncated`
flag. Full records are obtained by ID. A truncated result is not a complete inventory or proof
of absence; callers can narrow filters. V1 adds no frozen list sessions or pagination, and
listing never advances the memory consumer's durable acknowledgement cursor.

Work mutations and due/expiry transitions enter the repository's durable change stream after
commit and wake existing scoped subscriptions through content-free, coalesced pointers.
Use the existing commit-before-wake, subscribe-then-recheck, reconnect/catch-up, and bounded
recovery rules. No separate peer broadcast or per-heartbeat model notification is introduced.
Extend the existing expiry maintenance machinery to record one overdue/expired transition per
relevant deadline/generation as an ordinary change event, not a repeating alert. The current
expiry check runs at request boundaries; its 30-second rate limit is not an autonomous timer.
To notify idle consumers, add one bounded periodic maintenance sweep through the existing worker,
without per-claim timers or overlapping sweeps. Reconcile due transitions on restart. Queries
compute overdue state even before a delayed sweep publishes its event. Owner renewal alone
must not create routine
model notices. Coalescing must not lose the ability to query the latest record and missed
change history under the existing retention contract.

## Capacity, compatibility, and recovery

All strings, record counts, claim bundles, live leases, history, idempotency records, sweep
batches, and query results have explicit finite limits. The implementation proposal must include a
numeric budget and worst-case storage derivation before code approval; this draft does not
claim the existing note budget automatically accommodates work storage.

Reserve storage for release, finish, and expiry bookkeeping so ordinary create/update load
cannot prevent relinquishing ownership. On exhaustion refuse new work without dropping live
items, checkpoints, claims, or recovery information. V1 adds no general history pruning.
Safe reclamation follows existing expiry rules and the explicit 30-day finished-work
retention policy above. DQ-05 pruning remains separate.
Finite capacity can be exhausted before finished history becomes eligible for expiry,
including by a long-running item's event history. Refuse ordinary growth explicitly,
preserve promised end operations, and report the retention-based recovery path. The
implementation design must state the expected workload and limits; it must not imply
indefinite sustained reporting or silently introduce early history trimming.

Schema migration is versioned and transactional, preserves existing notes/subscriptions and
consumer cursors, and rejects incompatible older writers. Service restart recovers accepted
work and leases without inventing progress. Use existing repository lifecycle/ownership checks;
no new daemon or per-participant memory database is introduced. Freeze detailed numeric limits,
schema/version negotiation, and change-event integration in the implementation design.

## Participant opt-in and bootstrap on this repository

Installing the product does not enable this workflow for every agent. Configuration must
explicitly select participants and repository scope before managed participant guidance is
changed. Preserve other guidance and direct user instructions. The opt-in rule asks an agent
to look up or create the work item, explicitly claim before writing, propagate its work ID,
and report the holder on conflict. It never instructs an agent to obtain missing permissions.

Before the registry exists, use explicit bridge coordination: agree one writer, identify a
single draft artifact and frozen revision/hash, record review findings, and retain a private
checkpoint. This is a manual bootstrap, not evidence of implemented registry enforcement.

After contract approval, implementation, validation, and separately authorized deployment:

1. Enable repository-scoped opt-in for the explicitly selected participants.
2. Create one work item for each agreed work block, with acceptance criteria and non-goals;
   send its ID with the assignment rather than asking two agents to independently create it.
3. The writer reads the item and explicitly starts it with the required resource bundle;
   the reviewer confirms the artifact/revision and performs read-only review. If the reviewer
   needs to write, establish a distinct non-conflicting work item or explicitly hand off first.
4. At meaningful checkpoints, report the artifact, next deliverable, and next expected update.
   Update before a planned interruption; after an unexpected crash, query the record and
   reconcile the last checkpoint and any uncertain operation before acquiring anew.
5. Finish with evidence references. User acceptance, merge, and deployment stay separate actions
   under their own authorization; a finished registry entry cannot trigger them automatically.

Bootstrap success is two participants observing the same work ID and writer, a refused second
writer, separate review progress, and recovery of an interrupted checkpoint without duplicate
implementation. First demonstrate these cases with synthetic participants; an authorized live
trial confirms adoption, not stronger enforcement guarantees.

## Acceptance and exclusions

Test on Linux and macOS with synthetic peers: competing atomic starts; all-or-nothing bundle
conflicts; path boundary cases; proposals without acceptance; stale revision/generation refusal;
lease renewal without progress; overdue blocked and active work; expiry/reacquisition with late
old-owner writes; restart and wall-clock steps; lost replies and idempotency conflicts; consistent
capped listings and explicit truncation; catch-up after lost wakes; idle due-event publication
and deduplication; capacity refusal with successful release/finish; schema upgrade preserving
existing data; and scoped opt-in
preserving unrelated configuration. Verify completion never implies user approval.

No scheduler, dependencies, dashboards, auto-assignment, forced takeover, automatic budget
enforcement, semantic duplicate classification, filesystem fencing, semantic memory
consolidation, or new provider usage acquisition. Scope discipline is visible through the
acceptance criteria, non-goals, progress and usage references; it is not judged automatically.

Remaining gates: resolve the explicitly named numeric, migration, and change-event
integration details in the implementation design before coding.
