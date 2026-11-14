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
3. Each computer keeps its own authorized keys. Each addition is one authorization with a new
   random authorization ID, the key ID, the public key, the user name, the client label, the
   added time, how it was added (`dashboard` or `secret`) and its removed time. A user name
   matches `^[a-z][a-z0-9._-]{0,31}$`; a client label matches `^[a-z0-9][a-z0-9-]{0,31}$`.
4. Enrollment, on each computer, with that computer's own maintainer login or secret:
   1. On the target, `koinon link status` prints its pin as `Fingerprint`; Settings shows it too.
      This is the only source of a pin for enrollment, so the pin is read on the computer itself.
   2. The client signs an enrollment statement for that pin (Decision 3.5), for example
      `koinon-voice key enroll --target PIN --user NAME`.
   3. On the target, `koinon maintainer key add --statement FILE` (or Settings → Keys) checks the
      statement and stores the authorization. `koinon maintainer key list` and
      `koinon maintainer key remove KEY_ID` complete the set.
   A signed action never adds, removes or changes a key.
5. A group join or introduction (PDR 011 Decision 5) carries no keys. The maintainer enrolls the
   key on each computer.
6. A removal is stored before the command returns, and it is checked inside the transaction of
   each action, so an action that waits for the store after a removal is refused. A key that is
   removed and added again gets a new authorization ID, and none of the earlier
   authorization's receipts (Decision 4.1).
7. A maintainer secret reset, Remote off and unpair do not remove keys. Remote off and unpair
   stop the transport only.

## Decision 2: which actions cross

A signed action names one target and one operation:

| Operation | Kind | Arguments signed |
| --- | --- | --- |
| `computers` | read | none (target: the node) |
| `board_list` | read | filter, after, limit |
| `decision_get` | read | reference |
| `decision_history` | read | reference, after, limit |
| `decision_verdict` | write | reference, revision, snapshot, verdict, note |
| `inbox` | read | after, limit |
| `ack` | write | through |
| `send` | write | to, body, reply_to |
| `room_list` | read | after, limit |
| `room_read` | read | room, after, limit |
| `room_post` | write | room, body |
| `peers` | read | none |
| `peer_status` | read | peer |
| `changes` | read | versions (topic to version), wait_seconds (0 to 30); on the node, also the topic `computers` |
| `outcome` | read | request, created |
| `session` | session | client (target: the node) |

1. `decision_verdict` takes every verdict that the item's kind takes (`kindVerdicts`), with the
   target's checks: `stale_revision` with `current_revision`, `not_found`, `decided`,
   `invalid_verdict`, `feedback_required`, `hidden_command`.
2. `snapshot` is the hex SHA-256 of the item's snapshot at that revision: the RFC 8785 bytes of
   the object `{"reference", "revision", "kind", "title", "where", "context", "recommendation",
   "agent_text", "commands"}`, with the values that `decision_get` returns: `revision` an integer,
   `commands` an array of strings in their order (`[]` when there is none), and each other field
   a string (`""` when it is empty or absent). This object is the only place where v1 has an
   array; it is hashed, never sent as `args`. The client computes
   it from the fields that it shows the maintainer, and never copies a digest from an answer.
   The target computes it from its own row and refuses a difference (`snapshot_changed`). So a
   node that showed other text than the target holds cannot get a verdict for it.
3. `peers` and `peer_status` for another computer give only what PDR 011 Decision 2 item 2 lets
   cross (name or address, family, role, repository label, activity and its time) and the stall
   signal that the target's own maintainer view has; no native session ID, folder path, process
   data or credential.
4. `computers` gives the node's own pin and name, and for each pair its pin, name, connection
   state (`connected`, `offline`), reason and time, as `koinon link status` shows them.
5. No other operation crosses. Pairing, unpairing, keys, the maintainer secret, Remote, room
   membership and settings stay with the computer's own login. Each operation keeps its local
   note and credential filters, recipient resolution and audit.

## Decision 3: the signed envelope

```json
{
  "v": "koinon-maintainer-1",
  "key": "SHA256:…",
  "target": "<target pin>",
  "op": "decision_verdict",
  "args": {"reference": "…", "revision": 3, "snapshot": "<hex>", "verdict": "approved", "note": ""},
  "request": "<16-128 of [A-Za-z0-9_-]>",
  "created": 1794699000,
  "sig": "<base64>"
}
```

1. The signed bytes are `koinon-maintainer-1\n` followed by the RFC 8785 bytes of the envelope
   without `sig`. `sig` is the standard base64 (with padding) Ed25519 signature of those bytes.
   The key ID is `SHA256:` and the standard base64 of the SHA-256 of the raw 32-byte public key,
   without padding, as OpenSSH writes fingerprints.
2. Values in v1: object keys are ASCII; values are strings, integers between -(2^53-1) and
   2^53-1, booleans, and objects of these (only `changes` `versions`, string to integer). No
   floats, null or arrays. `created` is an integer of Unix seconds. The target refuses any other
   value (`signature_refused`, `version`).
3. The target checks in this order, and stops at the first refusal (`signature_refused` with a
   reason):
   1. one JSON object with exactly these fields, no duplicate key, `v` `koinon-maintainer-1`
      (`version`); `target` its own pin (`target`);
   2. the key has a current authorization (`key_unknown`, `key_removed`); the signature
      (`signature`);
   3. for a write, inside its store transaction: a receipt for this authorization and request
      (Decision 4) is answered now, whatever `created` is; else `created` within 5 minutes of the
      target's clock at that moment (`time`), then the operation and its own checks. The time
      check runs in the same transaction as the commit, so a write that waited for the store
      past its window does not run;
   4. for a read: `created` within 5 minutes of the target's clock (`time`), then the read.
4. The user name and client label come from the target's authorization, never from the envelope.
5. The enrollment statement is an envelope with `op` `enroll`, `args`
   `{"user", "client", "public_key"}`, the target's pin and no request. `public_key` is the
   standard base64 (with padding) of the raw 32-byte Ed25519 public key; `key add` refuses it
   unless `key` is its key ID, then checks the signature with it and stores it. `key add` takes it while `created` is within 10 minutes, because
   it is carried to the target by hand; this is the only 10-minute window.

Test vectors (synthetic key and target; generated with Go `crypto/ed25519`, checked with
Python `cryptography`). The seed is bytes 0x01 to 0x20; the snapshot is an example item.

```text
seed_hex: 0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20
public_key_hex: 79b5562e8fe654f94078b112e8a98ba7901f853ae695bed7e0e3910bad049664
public_key_b64: ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=
key_id: SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g

snapshot_canonical: {"agent_text":"","commands":[],"context":"- Line one\n- Line two","kind":"decision","recommendation":"Approve.","reference":"VEC-1","revision":3,"title":"Approve the test vector","where":"docs/test"}
snapshot_sha256: 350f1e8dd2e447f6bf2594e780e0fc16544c0114cdfdf9cccbb1346a4a9860dc

snapshot2_canonical: {"agent_text":"","commands":["koinon version","git status --short"],"context":"- Line one\n- Line two","kind":"decision","recommendation":"Approve.","reference":"VEC-1","revision":3,"title":"Approve the test vector","where":"docs/test"}
snapshot2_sha256: b6c12882582116eae7810fd6cb251045973eea8411b8d800c2c7e0b780d2b5ba

op: enroll
canonical: {"args":{"client":"koinon-voice","public_key":"ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=","user":"maintainer"},"created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"enroll","target":"a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf","v":"koinon-maintainer-1"}
signed_bytes_hex: 6b6f696e6f6e2d6d61696e7461696e65722d310a7b2261726773223a7b22636c69656e74223a226b6f696e6f6e2d766f696365222c227075626c69635f6b6579223a22656256574c6f2f6d56506c41654c4553364b6d4c703541666854726d6c623758344f4f52433630456c6d513d222c2275736572223a226d61696e7461696e6572227d2c2263726561746564223a313739343639393030302c226b6579223a225348413235363a5a6259476339627469457677484377694c594b746f4851504b61777a566461704a636766462f52364a3767222c226f70223a22656e726f6c6c222c22746172676574223a2261306131613261336134613561366137613861396161616261636164616561666230623162326233623462356236623762386239626162626263626462656266222c2276223a226b6f696e6f6e2d6d61696e7461696e65722d31227d
sig_b64: 3mzi0yvfgymYoV3Wp6xVOwjOKuUQTP/9+hXAd23WytD8ykv/4vTF1Tpu3t4H8ZBkr7OtLJfr9NoNvVkhTKOqBQ==

op: decision_verdict
canonical: {"args":{"note":"ok <&> \"quoted\"","reference":"VEC-1","revision":3,"snapshot":"350f1e8dd2e447f6bf2594e780e0fc16544c0114cdfdf9cccbb1346a4a9860dc","verdict":"approved"},"created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"decision_verdict","request":"vec-request-000001","target":"a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf","v":"koinon-maintainer-1"}
signed_bytes_hex: 6b6f696e6f6e2d6d61696e7461696e65722d310a7b2261726773223a7b226e6f7465223a226f6b203c263e205c2271756f7465645c22222c227265666572656e6365223a225645432d31222c227265766973696f6e223a332c22736e617073686f74223a2233353066316538646432653434376636626632353934653738306530666331363534346330313134636466646639636363626231333436613461393836306463222c2276657264696374223a22617070726f766564227d2c2263726561746564223a313739343639393030302c226b6579223a225348413235363a5a6259476339627469457677484377694c594b746f4851504b61777a566461704a636766462f52364a3767222c226f70223a226465636973696f6e5f76657264696374222c2272657175657374223a227665632d726571756573742d303030303031222c22746172676574223a2261306131613261336134613561366137613861396161616261636164616561666230623162326233623462356236623762386239626162626263626462656266222c2276223a226b6f696e6f6e2d6d61696e7461696e65722d31227d
sig_b64: 5RIpFZ7G+PMlGqvRQizOOmOaPK637OhJX9dCifNKOjIOXjvG4drxmGRLZVaViYYundattt/zTlJOHDiq6zeKCQ==
```

## Decision 4: once only, outcome by request

1. The target keeps one receipt per (authorization ID, request ID) for each write: op, digest of
   the signed bytes, state, code, result and created time, for 7 days, in the same store as the
   remote control v2 receipts (`internal/core/operation_receipts.go`). Another authorization,
   including the same key added again, finds no receipt of this one.
2. A write and its receipt are recorded in one transaction. The same request with the same
   digest gives the first result again, also after a restart or a lost answer. The same request
   with another digest is refused (`request_conflict`).
3. `outcome` with `{"request", "created"}` (the request's own `created`) answers the receipt of
   that authorization: `recorded` with the result, `refused` with the code, or `unknown_request`
   with the target's clock `now` when there is none. Then:
   - `now` more than 5 minutes after the request's `created`: final. The write did not run and
     cannot run (item 3.3 of Decision 3 refuses it inside the transaction); the client says so,
     and signs the action again under a new request ID only when the maintainer asks.
   - `now` within those 5 minutes: the envelope can still be in flight. The client sends the
     same envelope again; it runs once or answers from its receipt. If that answer is lost too,
     the client asks `outcome` again after the window, which is then final.
   - After the 7 days of a receipt, `outcome` cannot tell; the client reports the outcome as
     unknown.
4. Reads carry a request ID and `created`, so an old read is refused; they keep no receipt.
5. A signed action is not queued (settled point 1; #695). The node answers:
   - `computer_offline`, with the target's last-seen time, only when it had no connection to the
     target and sent nothing; the action did not run;
   - `outcome_unknown` when it sent the envelope and the answer did not come back; the client
     uses item 3 to find the outcome. A reply lost after the commit never comes back as
     `computer_offline`.

## Decision 5: how a client reaches a node

1. **A node on the client's computer**: the client runs `koinon mcp --maintainer --signed`. The
   first line of its standard input is a signed `session` envelope, in place of the secret, with
   `target` the node's pin, `args` `{"client"}` and a request ID. The node checks it as in
   Decision 3.3 and opens a maintainer session with the lifetime of today's maintainer token
   (24 hours idle, 7 days at most, ended by a daemon restart or a key removal): then
   `maintainer_token_expired`, and the client signs a new `session`. Refusals:
   `signature_refused` with its reason. The node's own pin for the session comes from
   enrollment on the node (Decision 1.4).
2. **No node on the client's computer**: the client's computer is on the maintainer's tailnet,
   and the client reaches a node at its Remote address (Tailscale Serve, HTTPS, PDR 010) on
   `/v1/maintainer/signed`. The route takes only signed envelopes and serves nothing else; the
   agent API stays on loopback (PDR 011 acceptance 9).
   - `POST /v1/maintainer/signed/session` with a `session` envelope answers
     `{"token", "expires"}`.
   - `POST /v1/maintainer/signed` with `Authorization: Bearer TOKEN` and one envelope answers
     the target's JSON result or `{"code", "error", ...}` with the HTTP status of the local
     maintainer route.
   - `changes` waits up to its `wait_seconds` and then answers, as the local route does.
3. **The MCP form** (case 1): one tool, `signed`, with `{"envelope"}`, answers the target's
   result, as the Serve route does. It has no resources and no notices: the client learns of
   changes with signed `changes` waits (Decision 6), in the same way on both forms.
4. In both cases the node carries out an action whose target is its own pin, and forwards any
   other on the paired link with that pin, as a new link operation `maintainer`, and returns the
   target's answer unchanged. The node adds nothing to the envelope and cannot change it without
   breaking the signature.
5. The client acts only on pins that it enrolled; it takes names and states for them from
   `computers`.

## Decision 6: notices and state

1. The client signs every read itself, `changes` included; the node never makes or repeats a
   read, because it holds no key and an envelope is valid for 5 minutes only (Decision 3).
2. For each target that it shows, the client keeps one `changes` wait at a time: it signs a new
   `changes` envelope with the versions it has and `wait_seconds` up to 30, the target answers
   when a topic version passes them or the wait ends, and the client reads the topics that
   changed and signs the next wait. The topics are `inbox`, `decisions` and each room ID
   (`internal/core/maintainer_changes.go`); versions are stored and survive restarts.
3. `changes` on the node's own pin also has the topic `computers`, whose version changes when a
   paired computer's state changes, a pair is added or removed, or a name changes. The client
   then reads `computers`.
4. A target that cannot be reached answers `computer_offline` (Decision 4.5): the client shows it
   offline, keeps its other targets, and waits on the node's `computers` topic for it to come
   back. Then it reads each topic again from its own versions.
5. When the session ends (`maintainer_token_expired`, a node restart, a dropped connection), the
   client signs a new `session` and goes on from the versions that it holds.

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
   snapshot that differs from the target's text, an unknown or removed key, an envelope older
   than 5 minutes that has no receipt, an unknown field, and an agent message that names a
   maintainer family.
3. One request ID gives one verdict across a lost answer and a target restart: a reply dropped
   after the commit gives `outcome_unknown`, and `outcome` or the same envelope gives the first
   result, with one verdict recorded. Another payload under the same ID is `request_conflict`.
   A key removed and added again cannot read or replay the earlier authorization's receipts.
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
| R11 | Decision 3; the client is the authorization's client label, one key per install |
| R12 | Decision 3 `target` is the pin; Decision 1.4 and Decision 2.4 (`computers`) give pins with names |
| R13, R14, R15 | Decision 4 (receipts per authorization, `request_conflict`, `outcome`, `computer_offline` against `outcome_unknown`); no "queued here" for signed actions (#695) |
| R16 | Not queued: settled point 1; the queue is #695 |
| R17, R18, R19, R21 | Decision 6: signed `changes` waits per target, the node's `computers` topic; waits, not polling |
| R22, R23, R24, R25 | Decision 5 |
| R26 | Decision 1.4 and 1.5 |

## Records

- PDR 011 Decision 2 revised (card LINK-693-SIGNED), and a new PDR 011 decision for keys and
  identity.
- ADR 002: the signed envelope, the `maintainer` link operation and the Serve route.
- PROTOCOL.md: the envelope, the routes and the errors.
- Koinon Voice (koinon-voice#36): ADR 004 (new), ADR 001 modified, PDR 001, PDR 002, and PDR 004
  Decision 2 (the per-computer external-inference opt-in, policy unchanged, kept by pin).

## Settled with the maintainer

Robert Capps, 2026-11-14, terminal, answering two questions:

1. Signed actions are not queued for an offline computer (Decision 4.5): he chose "No queue for
   now". Koinon Voice had proposed queuing `send` and `room_post` (R16). Then: "file an issue to
   track any item we push off, so it's not lost." The queue is #695.
2. A client with no node on its computer reaches another computer's Remote address on a route
   that accepts only signed actions (Decision 5.2): he chose "Remote address, signed-only". The
   option of speaking the link protocol on port 47672 as a client was not taken.
