# Signed maintainer actions across linked computers (#693): decision and contract

Status: draft for the maintainer, codex-koinon and the Koinon Voice pair
(rwcii/koinon-voice#36). Nothing here is built.

## Source

Robert Capps, 2026-11-14, terminal:

- "yes, carry maintainer traffic over the links"
- "koinon-voice should create a keypair per user/install, and maintainer messages are signed
  before transmission, public keys can be registered with remote systems as authorized keys.
  this also gives us a way to tie auth to username. maintainer board decisions could gain
  identity"
- Relayed by claude-koinon-voice from its terminal: "I would rather voice just contain the
  parts needed to communicate with full koinon nodes. I don't want it to run koinon itself."
- Card kv-693-ssh (koinon-voice board), approved 2026-11-14: Voice connects only to Koinon
  nodes and reaches paired computers through the links; its SSH connectors stay until this
  ships.

Intent: one maintainer client works the boards, inboxes, rooms and agents of all paired
computers through one Koinon node. A computer trusts a maintainer action because a key that
it registered signed it, not because of the connection it came over. Each verdict records the
user of that key.

This changes PDR 011 Decision 2 (LINK-670-WHAT), whose review condition is met: "the maintainer
wants to write to the other computer from one dashboard". The revision goes on the decision
board as card LINK-693-SIGNED.

## Terms

- **Client**: a maintainer client install, such as Koinon Voice on one computer, for one user.
- **Node**: the Koinon daemon that the client connects to. It is on the client's computer when
  one runs there, else on another computer of the maintainer.
- **Target**: the computer whose daemon carries out an action. It is the node itself or a
  computer paired with the node.
- **Pin**: a daemon's link key fingerprint (`link-key.pem`, `internal/link/identity.go`). It is the
  stable identity of a computer. A computer's name can change (PDR 011 Decision 3); its pin
  does not.

## Decision 1: keys and users

1. The client creates one Ed25519 key pair per user and install. The private key stays with the
   client (Koinon Voice: the macOS Keychain). Koinon never holds it.
2. The key ID is `SHA256:` and the base64 SHA-256 of the 32-byte public key.
3. Each computer keeps its own authorized keys: key ID, public key, user name, client label,
   added time, added by (`dashboard` or `secret`), and removed time. A user name matches
   `^[a-z][a-z0-9._-]{0,31}$`.
4. Only that computer's own maintainer login or secret adds or removes a key:
   `koinon maintainer key add --user NAME --client LABEL --statement FILE`,
   `koinon maintainer key remove KEY_ID`, `koinon maintainer key list`, and the same in Settings.
   The statement is the enrollment statement of Decision 3, signed by the key, so a mistyped
   or unusable key cannot be added. A signed action never adds, removes or changes a key.
5. A group join or introduction (PDR 011 Decision 5) carries no keys. The maintainer adds the
   key on each computer.
6. Removal is stored before the command returns, and it is checked inside the transaction of
   each action, so an action that waits for the store after a removal is refused. A key that is
   removed and added again gets no earlier request back (Decision 4).
7. A maintainer secret reset, Remote off and unpair do not remove keys. Remote off and unpair
   stop the transport only.

## Decision 2: which actions cross

A signed action names one target and one operation. The operations are the maintainer tools of
`internal/mcp/maintainer.go` that Koinon Voice needs:

| Operation | Kind | Arguments signed |
| --- | --- | --- |
| `board_list` | read | filter, after, limit |
| `decision_get` | read | reference |
| `decision_history` | read | reference, after, limit |
| `decision_verdict` | write | reference, revision, snapshot digest, verdict, note |
| `inbox` | read | after, limit |
| `ack` | write | through |
| `send` | write | to, body, reply_to |
| `room_list` | read | after, limit |
| `room_read` | read | room, after, limit |
| `room_post` | write | room, body |
| `peers` | read | none |
| `peer_status` | read | peer |
| `changes` | read | topic versions, wait seconds |

`peers` and `peer_status` for another computer give only what PDR 011 Decision 2 item 2 lets
cross (name or address, family, role, repository label, activity and its time), and the stall
signal that the target's own maintainer view has; no native session ID, folder path, process
data or credential. No other operation crosses. Pairing, unpairing, keys, the maintainer secret, Remote, room
membership and settings stay with the computer's own login. Each operation keeps its local
checks: revision, state, kind, recipient resolution, note and credential filters, audit.

The snapshot digest is the SHA-256 of the item's revision snapshot as `decision_get` returned
it. The target refuses a verdict whose digest differs from its own (`snapshot_changed`), so a
node that showed other text than the target holds cannot get a verdict signed for it.

## Decision 3: the signed envelope

```json
{
  "v": "koinon-maintainer-1",
  "key": "SHA256:…",
  "target": "<target pin>",
  "op": "decision_verdict",
  "args": {"reference": "…", "revision": 3, "snapshot": "<hex>", "verdict": "approved", "note": ""},
  "request": "<16-128 of [A-Za-z0-9_-]>",
  "created": 1794699000
}
```

1. The signed bytes are `koinon-maintainer-1\n` followed by the envelope without `sig`, in the
   JSON Canonicalization Scheme (RFC 8785). `sig` is the base64 Ed25519 signature.
2. The target refuses an envelope with an unknown field, a duplicate key, a version other than
   `koinon-maintainer-1`, a target other than its own pin, an unknown or removed key, a bad
   signature, or `created` more than 5 minutes from its clock (`signature_refused` with one of
   these reasons: `version`, `target`, `key_unknown`, `key_removed`, `signature`, `time`).
3. The enrollment statement is the same envelope with `op` `enroll`, `args` `{"user", "client"}`
   and the target's pin. It is valid for 10 minutes.
4. The user name comes from the target's key record, never from the envelope.

## Decision 4: once only, outcome by request

1. The target keeps a receipt per request ID, as the remote control v2 receipts do
   (`internal/core/operation_receipts.go`): key ID, op, digest of the signed bytes, state,
   code and result, 7 days.
2. A write is recorded in the same transaction as its receipt. The same request ID with the
   same digest gives the first result, also after a restart or a lost answer. The same request
   ID with another digest is refused (`request_conflict`).
3. `outcome` (a read, signed like any other, with `args` `{"request"}`) gives a write's
   receipt for reconciliation.
4. Reads also carry a request ID and `created`, so an old read is refused, but they keep no
   receipt.
5. A signed action is not queued. When the target cannot be reached, the node answers at once
   with `computer_offline` and the target's last-seen time. The client says that and tries
   again when the maintainer asks. Agent messages keep their queue (PDR 011 Decision 4).

## Decision 5: how a client reaches a node

1. **A node on the client's computer**: the client runs `koinon mcp --maintainer`, as today,
   and logs in with a signed `enroll`-style session statement (`op` `session`) in place of the
   maintainer secret. The node checks it against its own authorized keys.
2. **No node on the client's computer**: the client reaches a node over the tailnet, at that
   node's Remote address (Tailscale Serve, HTTPS, PDR 010), on a new route
   `/v1/maintainer/signed`, which takes only signed envelopes and a signed `session`. The route
   serves nothing else; the agent API stays on loopback (PDR 011 acceptance 9).
3. In both cases every action is a signed envelope. The node carries out an action whose target
   is its own pin, and forwards any other on the paired link of that pin, as a new link
   operation `maintainer`, and returns the target's answer unchanged. The node adds nothing to
   the envelope and cannot change it.
4. The node lists its computers for the client: its own pin and name, and for each pair the
   pin, the name, the connection state with its reason and time (`koinon link status`).

## Decision 6: notices and state

1. `changes` per target gives the topic versions (`internal/core/maintainer_changes.go`):
   `inbox`, `decisions`, each room. The node keeps one long wait per connected target over the
   link and tells the client, per pin, which topics changed; the client reads again.
2. A target's restart or a lost link marks that computer disconnected for the client; the
   client's connection to the node stays. After the link is back, the client reads each topic
   again from its own cursor.
3. A node restart ends the client's session; the client signs a new `session`.

## Decision 7: identity on the record

1. A verdict, a maintainer message, a room post and an ack from a signed action record the
   user name, the key ID and the client label from the target's key record, and `via`
   `key:CLIENT`. The record keeps them after a key is removed or a user is renamed.
2. `decision_history`, the dashboard and the audit show `USER (CLIENT)` for these.
3. Actions with the dashboard login or the secret keep `via` `dashboard` or `mcp:NAME`, with no
   user.

## Acceptance

1. A Voice install with a key registered on two computers reads both boards through one node and
   records a verdict on the other computer; the verdict shows the user and the client.
2. Refused, with nothing recorded: a swapped target pin, a changed argument or revision, a
   changed snapshot, an unknown, removed or re-added key, an envelope older than 5 minutes, an
   unknown field, and an agent message that names a maintainer family.
3. One request ID gives one verdict across a lost answer and a target restart; another payload
   under the same ID is `request_conflict`.
4. A key removed while an action waits for the store refuses that action.
5. With no node on the client's computer, the same works through another computer's Remote
   address; the route answers nothing but signed envelopes.
6. A target that is off gives `computer_offline` at once; its notices resume after it is back.
7. Evidence: Linux and macOS, a Voice build and two Koinon nodes, live.

## Koinon Voice requirements (koinon-voice 6a0a8ea, R1-R26)

| R | Here |
| --- | --- |
| R1, R5, R6, R20 | Decision 2 (`board_list`, `decision_get`, `inbox`, `room_list`, `room_read`, paged as now) |
| R2, R3, R4 | Decision 2 `decision_verdict`, every verdict that the item's kind takes; the target's checks answer `stale_revision` with the current revision and `not_found` |
| R7 | Decision 2 (`decision_history`, `ack`, `send`, `room_post`, `peers`, `peer_status`) |
| R8 | Decision 2, the `peers` paragraph |
| R9, R10 | Decision 1; the private key stays with the client |
| R11 | Decision 3; the client is the key record's client label, one key per install |
| R12 | Decision 3 `target` is the pin; Decision 5.4 lists pins with names |
| R13, R14, R15 | Decision 4 (receipts, `request_conflict`, `outcome`); no "queued here" for signed actions (#695) |
| R16 | Not queued: settled point 1; the queue is #695 |
| R17, R18, R19, R21 | Decision 6 and Decision 5.4 |
| R22, R23, R24, R25 | Decision 5 |
| R26 | Decision 1.4 and 1.5 |

## Records

- PDR 011 Decision 2 revised (card LINK-693-SIGNED), and a new PDR 011 decision for keys and
  identity.
- ADR 002: the signed envelope, the `maintainer` link operation and the Serve route.
- PROTOCOL.md: the envelope, the routes and the errors.
- Koinon Voice: its ADR 001 and PDRs 001 and 002 (koinon-voice#36).

## Settled with the maintainer

Robert Capps, 2026-11-14, terminal, answering two questions:

1. Signed actions are not queued for an offline computer (Decision 4.5): he chose "No queue for
   now". Koinon Voice had proposed queuing `send` and `room_post` (R16). Then: "file an issue to
   track any item we push off, so it's not lost." The queue is #695.
2. A client with no node on its computer reaches another computer's Remote address on a route
   that accepts only signed actions (Decision 5.2): he chose "Remote address, signed-only". The
   option of speaking the link protocol on port 47672 as a client was not taken.
