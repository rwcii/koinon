# Board link — review

## Scope review

codex-koinon, 2026-11-18, on the options before BOARD-LINK-746 revision 1: favored the copy
option, because it keeps the card readable while the origin is offline after receipt, and
asked for these boundaries, which the plan holds: one authority at the origin; read-only copies
with their origin; references and verdict IDs scoped by a stable pair identity; receipt only
after a durable commit and before the wake; an acknowledgement that reaches the origin; no
duplicate or earlier revision on retry; withdraw, close, recipient removal and unpair
outcomes; queued, offline and unsupported outcomes; a full card that is never cut short. Its
test list (disconnected recipient, origin offline after receipt, duplicate and reordered
retry, holder change, same reference and ID on both computers, acknowledgement lost across a
restart) is in the definition of done.

## Plan review

Pending: Codex peer review of the plan commit.
