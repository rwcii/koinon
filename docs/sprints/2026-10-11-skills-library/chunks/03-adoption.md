# Chunk 03 — scoped state and explicit adoption

Implements decision criteria 4, 5 and 6 and completes criterion 10. Depends on chunk02.
Use the corresponding tests and integration gate in definition-of-done.md.

## State query

Add `library_state` and `koinon library state`. It takes optional catalog item IDs and an
optional directory and target scope (`machine` or `project`), never arbitrary target paths
or a caller identity. Skills default to machine scope; project-only setup items require
project scope. Unsupported/unverified native roots permit list/read but return an explicit
inspection limitation for machine state. Inspect only the
manifest's target descriptors. With no item IDs, select items applicable to the chosen scope;
an explicitly selected inapplicable item reports unsupported scope rather than guessed state.
For project scope, the default repository directory is the session's registered
directory (the MCP/client directory at registration), not a guessed editor location. Resolve
an optional directory relative to that directory; require its Git common directory to match
the registered repository and its containing worktree to appear in that repository's
`git worktree list`. Reuse the existing checkout-tool validation boundary. Plain or foreign
directories are refused. Repository targets resolve against this verified selection while
the validated Git common directory remains the decision-store scope. The existing MCP process or command client performs bounded
read-only inspection; it has the caller's working directory and native environment, unlike
the daemon's global HOME. Inject filesystem/environment readers for synthetic tests.

For machine scope, derive the effective native root from trusted launch/client metadata,
not a model parameter or the daemon's own HOME. Codex uses effective CODEX_HOME/skills;
Claude uses its verified user skill root. Verify custom root semantics before accepting
them. Scope digests separate different native roots on the same machine. A machine-wide
query/decline follows the family/root across repositories; project decisions never suppress
a machine offer, and machine decisions never suppress a separately selected project offer.

The daemon supplies trusted catalog bytes and scope-specific decisions; the client returns
the combined result through the native identity path. Inspection observations are same-user
reported data, not an attestation or a permission grant. No model-controlled observation,
family, environment-root or target-path argument is exposed. Refuse symlink escapes and
non-regular file targets. Declared discovery-link targets are inspected with Lstat/Readlink;
require their literal target and resolved canonical root to stay inside the selected worktree.
Do not read a foreign destination or automatically replace a custom link. Report unavailable
roots and permissions explicitly, and read no more
than the per-item bound. Do not return private native paths or file bodies in catalog state.

An expected discovery-link path that is already a real directory is a distinct
`directory_conflict` inspection result, with no guessed content state. Preserve that directory
and every existing skill; do not read its contents, move, merge or delete it. Stop the link
adoption and report the conflict. Other independently approved canonical-file items remain
available; this sprint adds no implicit per-skill-link fallback.

Before either scope's adoption proposal, inspect known same-name native skills in both
the verified machine root and the selected verified project directory, using catalog-derived
bounded targets. Different bytes return `name_conflict` separately from content state;
identical bytes report duplicate discovery with matching content, without claiming which
copy a native invocation selects. Preserve both copies. A differing-content adoption stops
pending separately reviewed conflict resolution; a skill-content approval alone is not
permission to replace a project's workflow. Recheck after upgrades for a stale machine copy.
No implicit delete/rename or source replacement. The inspection covers the calling project,
not an unbounded machine scan. Unreadable/unverified roots report a limitation, not no conflict.
Link-item directory_conflict remains non-traversing; collision inspection may read only its
independently declared, bounded regular-file targets, never enumerate that directory.

Report sidecar state alongside the selected skill's content state. Inspect the fixed
`.koinon/skills/NAME.md` in the selected verified worktree, even for a machine skill.
A missing project context is unavailable, not a guessed sidecar path. Sidecars report no-sidecar-needed when no required facts and no local overrides exist,
present-valid, missing-required, incomplete (required headings/facts absent), invalid schema/skill ID,
or inspection error. Their local facts are never compared with a canonical embedded body;
only the declared schema is part of the catalog. Thus `skill present, required sidecar missing` is explicit; an absent optional sidecar
does not prevent using the canonical skill/defaults. Supplied optional overrides still
need bounded validation; malformed/conflicting data is never silently ignored. A changed local fact remains local; never call it a catalog
update or replace it from this repository's facts. Catalog read/list carries no sidecar body.

Only absent required facts stop their dependent repository-specific step. A sidecar
proposal is separate from the skill file proposal: draft from repository evidence,
show the exact fact file, ask about missing/conflicting facts, obtain exact approval and then
write with normal editing tools. A machine-skill approval does not approve any project file.
Record approved sidecar schema/content digests as scoped project evidence, without copying
its fact body into the catalog or machine decision. Repeating approved matching facts is a
no-op; a missing/edited sidecar still requires a fresh proposal and approval. Connection
recommendations can name the missing sidecar without creating it or recording a decision.

Report content state separately from adoption decision:

- present: target content/marked fragment matches the current catalog digest;
- missing: verified absent target/fragment;
- outdated: unchanged bytes match a previously adopted catalog digest, but the current
  catalog digest differs;
- edited: existing bytes match neither current catalog nor the recorded adopted digest.

Without a prior receipt, nonmatching content is edited, never automatically outdated. An
inspection failure reports an error/reason with no guessed content state. Read-only state
does not record an observation, advance a memory cursor or suppress an offer durably.

For a collection item, return per-target states and an aggregate; a verified unchanged prior
collection is outdated when the new catalog adds a wrapper, even though that new target is
absent. An edited target wins over an automatic update recommendation. Bound each collection
to 32 declared targets and the existing item-size ceiling.

## Scope-specific decisions

Project decisions reuse the calling repository's existing memory store and
`memory_record`/`memory_recall` interfaces. Bounded versioned JSON `decision` entries have
an explicit library marker, item ID, family, target scope digest, catalog digest,
pre-edit/result digests, adopted or declined decision, and an inert approval/evidence
reference. Project scope is the Git common directory, so linked worktrees share decisions
while inspected file bytes may differ. Page all relevant live receipts, validate the
schema, preserve fixed retry deadlines and the existing supersession/conflict semantics.
A competing live receipt is a conflict, not approval. Neither a record body nor a named
author supplies trusted caller identity. A decline receipt retains the last adopted target
digests so superseding an earlier receipt cannot lose the unchanged-old-content baseline.

Machine decisions need their own scope in the existing daemon database, not a synthetic
repository or another repository's memory. Add `library_decision` / `koinon library decision`
for machine decision recording and read the current machine record through `library_state`.
Native identity/family follows the existing admission path. The client supplies its verified
native-root digest through internal metadata, never a model-controlled filesystem path.
The record is keyed by family, root digest and catalog item ID. Add one ordinary table and
its primary-key index, at most 256 rows total and 8192 bytes per decision body, with at most
2 MiB of body data. All row/index/byte/page usage is included in ordinary storage accounting;
recording has no funded control or reserved-storage exemption. A schema migration preserves
existing state and makes incompatible older writers refuse. Unknown catalog/family/scope
values refuse before writing; reaching row, byte or page capacity refuses without eviction.

The bounded record retains adopted digests separately from decline status, so declining an
update does not lose the old baseline used to distinguish outdated content from local edits.
Write with a compared revision in one transaction. Repeating the exact current decision
returns it without a write; changed revision/content returns a conflict and leaves the
current record intact. Reconcile a lost reply by reading the current record: matching outcome
is data evidence, while an intervening different decision is a conflict, never inferred
approval. This is compare-and-set state, not the repository memory keyed-write API; do not
promise replay of an overwritten historical machine decision. Persist writer provenance
internally, but do not return private native IDs in catalog state. No arbitrary author or
approval flag can authorize a file edit.

Collection receipts retain at most 32 typed target IDs and digests, never arbitrary paths.
Only catalog-derived safe targets can be inspected when comparing prior contents. Unknown
or malformed descriptor data reports an inspection limitation and never triggers an arbitrary
read. Records contain no file bodies, secrets or absolute native-home paths. A decline
persists for its item/scope across reconnects and releases until explicitly reconsidered;
a changed item can be listed as changed while still declined. Preserve records for removed
items as inert data, and never silently reset decisions when a record is unavailable,
capacity is refused or history has been revoked. Receipts assert outcomes; they are not
attestations of human approval.

## Adoption procedure

The agent reads an item and state, produces the exact diff and explains optional native
pointers separately, explicitly selects machine or project scope, then obtains approval under the maintainer's
rules. Project-local installation always requires approval of the exact change; a machine
recommendation never implicitly authorizes project changes. It applies
only that approved item using normal editing tools. A changed target since review requires a
fresh diff and approval; existing local edits are not overwritten. It then verifies content
and records the outcome through the scope's decision interface above. If recording fails, preserve the
applied files and report an unrecorded outcome; do not pretend the operation was atomic.

A decline records no file edits. Repeating an adopted item with matching bytes is a no-op.
Fragment adoption preserves surrounding instructions and requires separate approval for an
ignore rule or discovery pointer. Neither the daemon nor a new adoption command writes project
or native runtime files. This choice keeps the approval decision in the agent's conversation.

After state inspection exists, initialize/guide recommend only missing/outdated skills
needed by currently available features for the verified calling family; other catalog
skills are optional. Declined/edited/unverified items are not offered as automatic updates.
With no verified native root or caller scope, mention read-only catalog access rather than
guessing installation state. Inspection is bounded/read-only; connection offers never
record a decision. An inability to read decisions is an explicit limitation, not missing.

Update initialize/guide to offer state and explain adoption, update protocol/usage/install
docs and Unreleased. Exercise an old/new synthetic catalog, private temporary project and
native roots, shared-store worktrees, all receipt pages, conflicts and unchanged files under
refusal. Test a machine decline followed by connection from another repository, a separate
project choice, custom verified native roots, decision conflicts, lost replies, and row/byte/
page capacity. Preserve all existing funded controls; the new machine table is ordinary data.
