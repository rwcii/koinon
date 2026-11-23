# Live check — signed answers (#696)

The live check of chunk 02 (`chunks/02-signed-answers.md`, Work item 7), run on 2026-11-23
between 02:56 and 03:02 UTC with a Go v2 test client and two test nodes, one on Linux and one on
macOS. Private runtime values (secrets, tokens, authorizations, pair IDs) are left out.

## Decision: the Voice part follows its v2 client

- **Decided by:** Robert Capps, 2026-11-23.
- **Context:** the chunk 02 Outcome asks for the live check with a Koinon Voice build. No Voice
  v2 client exists: its client side is rwcii/koinon-voice#41, which has not started. The options
  were a live check now with a Go v2 test client and the Voice part later, or no live check
  until Voice has a #41 build, with #787 open until then.
- **Decision:** run the live check now with a Go v2 test client on one Linux and one macOS
  node, on the Serve route, on each node's own pin and through the node for the other pin, with
  the node's claims and a v1 client. Run the Voice part when the #41 build exists (#789); its
  result goes into this file.
- **Reason and scope:** chunk 02 needs a live check, and the Voice client does not exist yet.
  The test client is test code in a scratch worktree; it does not ship.
- **Review when:** rwcii/koinon-voice#41 has a build (#789).

## Build and nodes

- Head: `9eca21c9eb6d7354351972f3b72148fa7e509c7e` of #787, after its peer review and refusal
  review passed and its full `go test -race -timeout 30m ./...` passed on a frozen worktree.
- Each computer built that head from a clean worktree with `CGO_ENABLED=0`, with the commit
  embedded. `koinon version`: version `dev`, that commit.
- Each node is a test daemon with its own state directory, started with `koinon serve
  --state-dir DIR --listen 127.0.0.1:47900`, with Remote on HTTPS port 10443 and the link on TCP
  port 47673: `blackhole-test` on Linux (linux/amd64) and `mac-studio-test` on macOS
  (run by claude-koinon-voice). The installed daemons and their Serve ports (443,
  47672) stayed unchanged. For the check, the maintainer allowed TCP 47673 and 10443 between the
  two computers in the tailnet policy.
- Pairing: `koinon link invite` on `blackhole-test`, `koinon link join` on `mac-studio-test`
  (this build reads the invitation from standard input). Both nodes showed each other
  `connected`.
- One maintainer key, enrolled on each node with a version 1 `enroll` statement and the node's
  secret (`koinon maintainer key add --statement`).
- Fixtures on each node: one registered test session, one message from it to the maintainer,
  four decision cards that name it, and one room with it as member.

## Test client

A Go program that imports `internal/signed`. It opens a version 2 session on the node's own pin
through the node's Remote address (`POST /v1/maintainer/signed/session`), then signs each
envelope with `koinon-maintainer-2` and posts it to `POST /v1/maintainer/signed` with the
session token. It checks every answer with `signed.VerifyAnswer`, with the digest of the
envelope that it sent, the envelope's target pin and the node's pin, and then checks the signer,
the status, the code and that the transport status equals the signed status.

## Each operation, both pins, through each node

Through each node: the session, then for the node's own pin and for the other computer's pin
`send`, `board_list`, `decision_get`, `decision_get` of a missing card (`not_found`),
`decision_history`, `inbox`, `room_list`, `room_read`, `peers`, `peer_status`, `changes`,
`outcome` of the send, `ack`, `room_post`, `send` to an unknown peer (`peer_not_found`),
`computers` (own pin only), `decision_verdict` (approved, with the snapshot digest), an envelope
with a changed argument (`signature_refused`, reason `signature`), and a version 1
`decision_get`.

| Through | Result |
| --- | --- |
| `blackhole-test` (02:56:39) | 38 of 38 pass. Every v2 answer is signed by its target with the target's link key and verifies: the Linux node's own answers, and the macOS computer's answers through the link. The v1 answer is `ok` and is not a signed object. |
| `mac-studio-test` (02:56:48) | 38 of 38 pass, the same with the roles reversed. |

## The node's claims

| Claim | Through | Result |
| --- | --- | --- |
| `computers` signed for the paired pin | each node | 400 `invalid_request`, signed by the node. |
| `session` signed for the paired pin, on the session route | each node | 400 `invalid_request`, signed by the node. |
| A pin that is neither the node nor a paired computer | each node | 409 `signature_refused`, reason `target`, signed by the node. |
| A lost answer: a `changes` wait of 25 s for the Linux pin, with `blackhole-test` paused (SIGSTOP) during the wait | `mac-studio-test` | 409 `outcome_unknown` with the request ID, signed by the node, after 40.0 s (the wait and the 15 s exchange bound). |
| A computer that is off: `send` with `blackhole-test` paused before the call | `mac-studio-test` | 409 `computer_offline`, reason `link_unreachable`, signed by the node, after 3.0 s. Afterwards the target's `outcome` for that request is `unknown_request`: the send did not run. |
| A computer that is off: `send` with `blackhole-test` stopped | `mac-studio-test` | 409 `computer_offline`, signed by the node, after 3.0 s. |

A version 1 session on each node's own pin still opens, and its answer is not signed.

## The definition of done's cases

On a new Linux test node of the same head (03:01 UTC), on its own pin through its Remote
address, with two test sessions in one room; one session was then retired:

| Case | Result |
| --- | --- |
| A signed room post with sent and not-sent members | 200, signed by the target: `queued` 1, `skipped` 1; the active member `sent`, the retired member `not_sent` with reason `retired`. Its `outcome` reads the same result, `recorded`. |
| A signed send to an ended session | 409 `recipient_inactive`, reason `retired`, signed by the target. |
| A target that is off | The two `computer_offline` rows above. |

A node that changes an answer is shown "answer not verified": tests only
(`TestSignedAnswersEachOperation` and the answer refusals of `internal/signed`), because the
live run cannot change bytes.

## Found during the check

- #788: `koinon link name` with a port that Serve already uses answers `storage_error` and saves
  the refused port. The installed daemon and its Serve mapping stayed unchanged.

## Cleanup

Both test nodes are stopped, `koinon remote disable` removed their Remote and link Serve ports,
and their state, keys and build worktrees are deleted on both computers. `tailscale serve status`
shows only the installed daemons' ports 443 and 47672 on each computer. The removal of the
tailnet rule for 47673 and 10443 is a task for the maintainer.
