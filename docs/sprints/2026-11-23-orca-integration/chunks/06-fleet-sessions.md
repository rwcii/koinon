# Chunk 06 — Fleet Sessions

Cites `../decision.md` (The fleet board, items 4 to 8; acceptance 5) and
`../definition-of-done.md` (Fleet Sessions). Depends on chunk 05.

## Outcome

The Fleet Sessions page shows the sessions of every connected node and runs the Sessions
page's actions on any of them, each signed by the fleet board for one node and revision.

## Work

1. `internal/core/signed_sessions.go`: the version 2 operations `sessions`, `session_retire`,
   `session_purge`, `session_unpurge`, `inbox_clear` and `claims_release`, through the same
   checks as the dashboard actions, with the session revision, once-only receipts and the
   audit record with the key's user and client; forwarded over the link like the other
   signed operations.
2. Fleet Sessions page: the Sessions page layout, each row's node, a node filter, unreachable
   nodes shown as such, the actions with the admin password.
3. Records and docs: PDR 011; PROTOCOL (the operations); USAGE; CHANGELOG.

## Tests

For each operation: success, stale revision, unknown session, version 1 refused, the repeat,
forwarding to a paired node, and the board's check of the signed answer; the page's filter and
unreachable state.
