# Chunk 03 — scoped state and explicit adoption

Implements decision criteria 4, 5 and 6 and completes criterion 9. Depends on chunk02.
Use the corresponding tests and integration gate in definition-of-done.md.

## State query

Add `library_state` and `koinon library state`. It takes optional catalog item IDs and an
optional directory, never arbitrary target paths or a caller identity. Inspect only the
manifest's target descriptors. The default repository directory is the session's registered
directory (the MCP/client directory at registration), not a guessed editor location. Resolve
an optional directory relative to that directory; require its Git common directory to match
the registered repository and its containing worktree to appear in that repository's
`git worktree list`. Reuse the existing checkout-tool validation boundary. Plain or foreign
directories are refused. Repository targets resolve against this verified selection while
the validated Git common directory remains the decision-store scope. The existing MCP process or command client performs bounded
read-only inspection; it has the caller's working directory and native environment, unlike
the daemon's global HOME. Inject filesystem/environment readers for synthetic tests.

The daemon supplies trusted catalog bytes and repository-scoped decisions; the client returns
the combined result through the native identity path. Inspection observations are same-user
reported data, not an attestation or a permission grant. No model-controlled observation,
family, environment-root or target-path argument is exposed. Refuse symlink escapes and
non-regular file targets. Declared discovery-link targets are inspected with Lstat/Readlink;
require their literal target and resolved canonical root to stay inside the selected worktree.
Do not read a foreign destination or automatically replace a custom link. Report unavailable
roots and permissions explicitly, and read no more
than the per-item bound. Do not return private native paths or file bodies in catalog state.

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

## Decisions in existing memory

Reuse the calling repository's existing memory store and `memory_record`/`memory_recall`
interfaces, not a new database or schema. Adoption decisions are bounded versioned JSON
`decision` entries with an explicit library marker, item ID, family, target scope digest,
catalog digest, observed pre-edit digest, result digest, decision (adopted or declined) and
an inert approval/evidence reference. Collection receipts additionally retain bounded typed
target IDs and digests, not arbitrary paths; only registry-derived safe targets can be inspected
when comparing an old collection. Unknown or malformed descriptor data reports an inspection
limitation and never causes an arbitrary filesystem read. They contain no private identity, file content, secret
or absolute native-home path. Repository items use the Git common-directory scope, so a
decline is shared by worktrees even when their inspected bytes differ. A native-runtime item,
when supplied, uses a native-root scope digest without publishing that path. Memory's recorded caller provenance remains authoritative
for who reported the decision; the body never proves human approval.

Retrieve every relevant live receipt page and validate its schema and catalog namespace.
Supersede the prior decision using the existing memory revision/conflict behavior; unresolved
competing live decisions are reported as conflicting and never treated as approval. A decline
persists for the item/scope across reconnects and releases until the maintainer explicitly
reconsiders it. A changed catalog item can be listed as changed while still declined; it is
not reoffered as a newly approved adoption. Do not silently reset decisions when a record is
unavailable, capacity is refused or history has been revoked: report the condition.

Use existing fixed idempotency deadlines. Before repeating a completed record, inspect the
current matching decision and return it without a write. A lost reply is reconciled using
the original key and deadline; passing the deadline is not permission to invent a replacement
approval. Existing memory limits, retention, revocation and same-user data boundaries apply.

## Adoption procedure

The agent reads an item and state, produces the exact diff and explains optional native
pointers separately, then obtains approval within the adopting project's rules. It applies
only that approved item using normal editing tools. A changed target since review requires a
fresh diff and approval; existing local edits are not overwritten. It then verifies content
and records the outcome through existing memory tools. If recording fails, preserve the
applied files and report an unrecorded outcome; do not pretend the operation was atomic.

A decline records no file edits. Repeating an adopted item with matching bytes is a no-op.
Fragment adoption preserves surrounding instructions and requires separate approval for an
ignore rule or discovery pointer. Neither the daemon nor a new adoption command writes project
or native runtime files. This choice keeps the approval decision in the agent's conversation.

Update initialize/guide to offer state and explain adoption, update protocol/usage/install
docs and Unreleased. Exercise an old/new synthetic catalog, private temporary project and
native roots, shared-store worktrees, all receipt pages, conflicts and unchanged files under
refusal. Carry the existing storage guarantees forward; no new tables or funded controls are
introduced.
