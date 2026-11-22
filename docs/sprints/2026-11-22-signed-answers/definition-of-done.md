# Signed answers and recipient results — definition of done

Every chunk cites `decision.md`, `contract.md` and this file. Tests are synthetic and run in CI
on Linux and macOS (WDR 004, Decision 3). Each guarantee is proven once, in the chunk that adds
it, at the boundary where Koinon enforces it. Two daemons in one test use separate private
state directories and the link test harness of #670, with real TLS between them. No test
reaches a real agent, a real board or another computer.

## Recipient results

- A signed `room_post` answers the `recipients` list of `contract.md` section 1.1, in roster
  order, without the poster. The counts `queued` and `skipped` agree with the list.
- Each reason comes from the state at the post: a retired, ended, lapsed or unproven member
  session, and an unheld or conflicting participant. The fan-out records the liveness reason
  for a member session in place of today's generic `inactive`, in `room_deliveries` too.
- A signed `send` refused as `recipient_inactive` keeps `details.reason` in its receipt, so
  `outcome` and a repeat give it.
- The list is stored with the receipt in the write's transaction. A repeat and `outcome` give
  the same list after a member joins, leaves, retires or returns, and after a daemon restart.
- A member that was not sent a post gets no inbox copy and no wake later. `room_read` still
  pages the posts since it joined.
- The dashboard room post and the `koinon mcp --maintainer` room post answer the same list. An
  agent's `room_post` answer keeps its counts and names no member.
- A receipt stored before this release answers as it was stored, without a list.
- Tests: a room with one active session, one ended session, one retired session, one unheld
  participant and one conflicting participant; a repeat and `outcome` after the roster
  changes and after a restart; a `send` to each refused kind; no later copy or wake for the
  members that were not sent; an agent's post answers counts only; a receipt seeded in the
  earlier form answers unchanged.

## Envelope version 2

- `internal/signed` parses, checks and canonicalizes `koinon-maintainer-2` with the v1 rules
  and its own domain line. A v1 envelope works as before, with its unsigned answer.
- Tests: a v2 envelope signed with the v1 domain line, and the reverse, are refused
  `signature`; a v1 and a v2 request with the same ID give `request_conflict`; a v2 `outcome`
  reads the receipt of a v1 request; a v2 `session` on the Serve route answers a signed token
  object, and the local `koinon mcp --maintainer --signed` takes a v2 `session`.

## Signed answers

- The target signs each answer to a v2 envelope with its link key, as `contract.md` section 3
  says: reads, writes, refusals of the envelope, `outcome`, `changes`, and `session` on the
  Serve route (its flat token object, signed by the node as target). The local
  `koinon mcp --maintainer --signed` bootstrap and the unsigned answers of section 4 stay
  unsigned.
- The node signs its own claims for a v2 envelope (`contract.md` section 4) and returns a
  target's signed answer with its field values unchanged, on the MCP `signed` tool, the Serve
  route and the link (`contract.md` section 3.6). The node's link check takes the v1 form for a
  v1 envelope and the signed object for a v2 envelope; for a v2 envelope it also passes an
  earlier target's unsigned `signature_refused`, reason `version`, unchanged and unsigned, and
  the client shows it as not verified. A test runs that answer from a target of the earlier
  form through a node of this release.
- A Go verifier in `internal/signed` checks an answer for an envelope, a target pin and a node
  pin, as a client does (`contract.md` section 3.4).
- Tests: each operation of v1 Decision 2 over v2 on a node's own pin and on a paired pin,
  verified, on the MCP `signed` tool and the Serve route; each node claim of `contract.md`
  section 4 verified as the node's; a node that changes the answer, the status, the signer,
  the pin or the request, or puts a target answer under a node signature, fails the check; a
  node-signed success fails; a target answer whose `answer` text has quotes, backslashes,
  control characters and non-ASCII text goes through the link and the node and still verifies
  on each transport; the mixed-release cases of section 4 answer unsigned
  `signature_refused`, reason `version`; a read near the 1 MiB link limit, with escaped
  content, fits or is refused `too_large`, signed; the largest room post result (32 members,
  longest labels) fits.
- The shared vectors of `contract.md` section 5 are a Go test that produces and checks them;
  Koinon Voice checks the same values.

## Documentation and records

- PDR 011: the scope decision of `decision.md` as a new decision, and the review condition of
  Decision 6 updated (chunk 01); the signed answers shipped (chunk 02).
- ADR 002: envelope v2 and the signed answer (chunk 02).
- PROTOCOL.md: the recipient list (chunk 01); v2 and the signed answer (chunk 02).
- docs/USAGE.md, Rooms: the post's recipient list for signed and MCP posts (chunk 01).
- CHANGELOG.md: each chunk.

## Checks and evidence

- The project's checks (`check` skill) on each chunk, and the four `CGO_ENABLED=0` builds for
  chunk 02.
- Live check in chunk 02 (`verification.md`): a Koinon Voice build and two Koinon nodes, on the
  computers and build that the maintainer names: a signed room post with sent and not-sent
  members, a signed send to an ended session, and a target that is off, each verified by the
  client; a node that changes an answer is shown "answer not verified" (test only, when the
  live run cannot change bytes).
