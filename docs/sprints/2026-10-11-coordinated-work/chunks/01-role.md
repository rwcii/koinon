# Chunk 01 — role routing and native ownership

Implements decision criteria 1–3 and the corresponding done tests.

Add optional `proposed_role: {family, role}` to create/propose and item views, mutually exclusive
with `proposed_assignee`. Validate the existing family/launcher-role grammar; empty role means
the default participant. It names a role in the caller's Git common-directory store, not a
native identity. Proposal never registers, starts or transfers a session/claim. Resolve current
holder through the participant registry and expose unheld/conflict state explicitly.

`work_list`/CLI list gains `assigned_to_me`; a current holder sees proposals matching its
actual family/role. Add bounded keyset pagination (`after`, `more`, `next_after`) in existing
work-ID order, retaining `truncated` compatibility. An inventory is complete only after paging.
Read state plus memory deltas handles changes between pages; it never resets a memory cursor.

A role-assigned start requires the verified current holder of that role, plus the existing
explicit start and exclusive resource checks. Routing eligibility is not external authorization.
Its ownership consumer is a bounded `native-work:` key derived from a SHA-256 digest of the
validated native family/ID. Store that native actor digest in the claim bundle and check it
on owner operations and keyed replays, inside the same transactions/participant serialization.
Do not accept a model-supplied actor digest. An explicit consumer must match the derived key.
Once acquired, the native binding lasts for the whole generation even if the proposal changes;
ownership does not transfer with the participant. An active original native owner can release
its own claim; a successor cannot use it. All external authorization rules remain in force.

Existing claims default to legacy mode on migration and retain their consumer/generation/
deadlines. Existing explicit-consumer proposals retain v1 semantics. New role claims are an
explicitly distinct path; participant inboxes and memory cursors still use their stable keys.
Use the next schema migration and existing tables; add no second lease engine or identity
source, and no indexes on ownership/credit flags. Include new bounded fields in image/usage
accounting and verify existing funded-control bounds before publishing.

Update WORK-ITEMS-V1.md/COMMANDS, PROTOCOL, guide and Unreleased. Test both continuation models,
scope/proposal changes, every owner write/replay, native identity continuity versus genuine
succession, role conflict/unheld state and complete current-assignment paging. No runtime setup.
