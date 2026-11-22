# Increment 3: Transactional action receipts

See [decision](../decision.md), [definition of done](../definition-of-done.md)
and the original `docs/REMOTE-CONTROL-V2-SPEC.md`.

Support remote send, room-post and decision verdict only. Use one host-wide client
operation ID with original timestamp, session attribution, type, target and digest.
The accepted receipt and mutation share the exact main-database transaction.
Persist supported revision-conflict results. Concurrent same-ID calls create one
mutation; changed content conflicts. Reauthentication never changes the namespace.

Authenticated origin-checked lookup returns authoritative found/missing/expired
results. Retain seven days; do not evict a live receipt to admit a new action.
Pending browser metadata contains no message bodies unless drafts were selected.
Only authoritative absence permits explicit same-ID retry in its original window.
Keep expired/future-dated IDs visible for inspection; never retry automatically.
Accepted is distinct from delivery/ack. Preserve text after refused actions.
Launch, terminal input, stop/restart and remote disable remain outside scope.

Checks: ACTION-1, duplicate/concurrent IDs, changed payload, dropped result,
reauthentication, expired lookup, refused revision, admission-blocked replay and
failed receipt write rolling back its mutation. Verify unchanged-schema previous
reader can open/retain v2 namespaced records.
