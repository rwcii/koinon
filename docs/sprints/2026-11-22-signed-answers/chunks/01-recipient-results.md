# Chunk 01 — Recipient results

Cites `../decision.md` (Scope decision items 1 to 6; acceptance 1 to 4), `../contract.md`
section 1 and `../definition-of-done.md` (Recipient results; Documentation and records).

## Outcome

A room post, signed or from the dashboard or `koinon mcp --maintainer`, answers each member's
result: sent, or not sent with its reason. A refused signed send keeps its reason in its
receipt. A repeat and `outcome` give the stored list. Nothing that was not sent is delivered
later. PDR 011 records the scope decision.

## Work

1. Room fan-out (`internal/core/room_posts.go`): for a member session that takes no copy, record
   the liveness reason (`inactiveReason` in `internal/core/liveness.go`) in place of
   `inactive`; keep `unheld` and `conflicting` for participants. Build the `recipients` list in
   the same transaction, in roster order, without the poster.
2. Results: the store gives the list to its maintainer callers, and `maintainerPost` carries
   `recipients`; `queued` and `skipped` stay. The agent route (`internal/core/room_routes.go`)
   keeps its counts-only answer: the list is not a field of the agent's JSON. The signed
   receipt keeps the whole maintainer result (`internal/core/signed_actions.go`).
3. Receipts of refusals keep `details` for `recipient_inactive` (`InactiveRecipient`), as for a
   `WorkRefusal`.
4. Dashboard: the room page and its post answer show the new reasons in words.
5. Records and docs: PDR 011, a new decision from `../decision.md` and the Decision 6 review
   condition; PROTOCOL.md; docs/USAGE.md, Rooms; CHANGELOG.md.

## Tests

The Recipient results tests of the definition of done, and a dashboard test for the reasons.
The existing counts-only test of an agent's post also checks that the agent route's JSON has
no `recipients`.

## Out of this chunk

Envelope version 2, signed answers and node claims (chunk 02).
