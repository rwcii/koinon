# Signed maintainer actions across linked computers (#693): decision and contract

Status: approved by Robert Capps on 2026-11-15 on the decision board, at 4e324d5; recorded as
PDR 011 Decision 6 (LINK-693-SIGNED). claude-koinon, codex-koinon, claude-koinon-voice and
codex-koinon-voice agree (rwcii/koinon-voice c85b9cb). Built in three chunks with Koinon Voice
(rwcii/koinon-voice#36).

## Source

Robert Capps, 2026-11-14, terminal:

- "yes, carry maintainer traffic over the links"
- "koinon-voice should create a keypair per user/install, and maintainer messages are signed
  before transmission, public keys can be registered with remote systems as authorized keys.
  this also gives us a way to tie auth to username. maintainer board decisions could gain
  identity"
- Relayed by claude-koinon-voice from its terminal: "I would rather voice just contain the
  parts needed to communicate with full koinon nodes. I don't want it to run koinon itself."
- Approved on the decision board of koinon-voice, 2026-11-14: Voice connects only to Koinon
  nodes and reaches paired computers through the links; its SSH connectors stay until this
  ships.

Intent: one maintainer client works the boards, inboxes, rooms and agents of all paired
computers through one Koinon node. A computer trusts a maintainer action because a key that
it registered signed it, not because of the connection it came over. Each verdict records the
user of that key.

This changes PDR 011 Decision 2 (LINK-670-WHAT), whose review condition is met: "the maintainer
wants to write to the other computer from one dashboard". The revision went to the decision
board; PDR 011 Decision 6 (LINK-693-SIGNED) records its approval.

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
3. Each computer keeps its own authorized keys. Each addition is one authorization. Its ID is
   the hex SHA-256 of the enrollment statement's signed bytes (Decision 3.5), so the client
   knows it without being told. It keeps the key ID, the public key, the user name, the client label, the
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
   each action, so an action that waits for the store after a removal is refused.
7. Each enrollment statement adds an authorization once on a computer: the target keeps the IDs
   of all authorizations that it ever added, and refuses a statement whose ID it has
   (`statement_used`). A key that is removed and enrolled again needs a new statement, so it has
   a new authorization ID. Every other envelope carries its authorization ID in `auth`, and the
   target runs it only under that current authorization (Decision 3.3). So an envelope signed
   under an earlier authorization never runs under a later one, whatever the clocks say, and
   receipts never cross authorizations (Decision 4.1).
8. A maintainer secret reset, Remote off and unpair do not remove keys. Remote off and unpair
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
| `changes` | read | versions (topic to the 20-digit version string it has), wait_seconds (0 to 30); on the node, also the topic `computers` |
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
  "auth": "<authorization ID: hex SHA-256 of the enrollment statement's signed bytes>",
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
   2^53-1, booleans, and objects of these (only `changes` `versions`, string to string). No
   floats, null or arrays. `created` is an integer of Unix seconds. The target refuses any other
   value (`signature_refused`, `version`).
3. The target checks in this order, and stops at the first refusal (`signature_refused` with a
   reason):
   1. one JSON object with exactly these fields, no duplicate key, `v` `koinon-maintainer-1`
      (`version`); `target` its own pin (`target`);
   2. the key has a current authorization (`key_unknown`, `key_removed`), and `auth` is its ID
      (`authorization_changed`, Decision 1.7); the signature (`signature`);
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
   unless `key` is its key ID, then checks the signature with it and stores it. The statement
   has no `auth`; `key add` refuses a statement that this computer added before
   (`statement_used`, Decision 1.7). `key add` takes it while `created` is within 10 minutes, because
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

auth (sha256 of the enroll signed bytes): 60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e

op: enroll
canonical: {"args":{"client":"koinon-voice","public_key":"ebVWLo/mVPlAeLES6KmLp5AfhTrmlb7X4OORC60ElmQ=","user":"maintainer"},"created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"enroll","target":"a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf","v":"koinon-maintainer-1"}
signed_bytes_hex: 6b6f696e6f6e2d6d61696e7461696e65722d310a7b2261726773223a7b22636c69656e74223a226b6f696e6f6e2d766f696365222c227075626c69635f6b6579223a22656256574c6f2f6d56506c41654c4553364b6d4c703541666854726d6c623758344f4f52433630456c6d513d222c2275736572223a226d61696e7461696e6572227d2c2263726561746564223a313739343639393030302c226b6579223a225348413235363a5a6259476339627469457677484377694c594b746f4851504b61777a566461704a636766462f52364a3767222c226f70223a22656e726f6c6c222c22746172676574223a2261306131613261336134613561366137613861396161616261636164616561666230623162326233623462356236623762386239626162626263626462656266222c2276223a226b6f696e6f6e2d6d61696e7461696e65722d31227d
sig_b64: 3mzi0yvfgymYoV3Wp6xVOwjOKuUQTP/9+hXAd23WytD8ykv/4vTF1Tpu3t4H8ZBkr7OtLJfr9NoNvVkhTKOqBQ==

op: decision_verdict
canonical: {"args":{"note":"ok <&> \"quoted\"","reference":"VEC-1","revision":3,"snapshot":"350f1e8dd2e447f6bf2594e780e0fc16544c0114cdfdf9cccbb1346a4a9860dc","verdict":"approved"},"auth":"60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e","created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"decision_verdict","request":"vec-request-000001","target":"a0a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4b5b6b7b8b9babbbcbdbebf","v":"koinon-maintainer-1"}
signed_bytes_hex: 6b6f696e6f6e2d6d61696e7461696e65722d310a7b2261726773223a7b226e6f7465223a226f6b203c263e205c2271756f7465645c22222c227265666572656e6365223a225645432d31222c227265766973696f6e223a332c22736e617073686f74223a2233353066316538646432653434376636626632353934653738306530666331363534346330313134636466646639636363626231333436613461393836306463222c2276657264696374223a22617070726f766564227d2c2261757468223a2236306636373537653138646538376266613439313861613962373634623530623336636264646639383264396534626562653232366666633531383336663665222c2263726561746564223a313739343639393030302c226b6579223a225348413235363a5a6259476339627469457677484377694c594b746f4851504b61777a566461704a636766462f52364a3767222c226f70223a226465636973696f6e5f76657264696374222c2272657175657374223a227665632d726571756573742d303030303031222c22746172676574223a2261306131613261336134613561366137613861396161616261636164616561666230623162326233623462356236623762386239626162626263626462656266222c2276223a226b6f696e6f6e2d6d61696e7461696e65722d31227d
sig_b64: IOGwy1JLMXL0Lizmp1LZe7skwiVP7FO1wkuATry177ta1FFcfdFZDXAdl1LqiPGneV/AmKPrgFUHk/rl1AKmDQ==
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
   with the target's clock `now` when there is none. An `outcome` envelope carries the `auth`
   of the original request, never the authorization that is current now. When that
   authorization is gone, the envelope is refused like any other (`signature_refused`, reason
   `authorization_changed`, Decision 3.3). Then:
   - that refusal: the key was enrolled again since the request, and the new authorization
     cannot read the old receipts, so the outcome is unknown. The client says so and does not
     send the request again; the maintainer reads the target's record (for a verdict, its
     history).
   - `now` more than 5 minutes after the request's `created`: final. The write did not run and
     cannot run (item 3.3 of Decision 3 refuses it inside the transaction); the client says so,
     and signs the action again under a new request ID only when the maintainer asks.
   - `now` within those 5 minutes: the envelope can still be in flight. The client sends the
     same envelope again; it runs once or answers from its receipt. If that answer is lost too,
     the client asks `outcome` again after the window, which is then final.
   - After the 7 days of a receipt, `outcome` cannot tell; the client reports the outcome as
     unknown.
   `outcome` looks up the receipt and reads `now` inside a transaction of the target's store,
   which runs one transaction at a time on its single connection (`internal/core/store.go`,
   `SetMaxOpenConns(1)`); a write's receipt lookup, time check and commit are one transaction. A write that passed its time check has then committed
   or rolled back before `outcome` reads, so an absent receipt with `now` past the window is
   final, and no commit can follow it.
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
   - `POST /v1/maintainer/signed/session` with a `session` envelope answers as the local
     session route does (Answer shapes).
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
6. Trust: the node is one of the maintainer's own computers, reached over the tailnet, and the
   client trusts it to show the target's answers as they are. A node cannot make an action run
   that the client did not sign, change it, or get a verdict for text that the client did not
   show (Decision 2.2). The target's answers are not signed in this deliverable, so a node that
   lies could show a false result; target-signed answers are #696.

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

## Answer shapes

Every answer has the form of today's maintainer routes (`internal/core/maintainer_mcp.go`): a
success is `{"ok": true, "result": …}`; a refusal is
`{"ok": false, "code": "…", "error": "…", "details": {…}}`, with `details` only when it has
fields. The `signed` tool and the Serve route return this object unchanged. The operations of
Decision 2 that exist today keep their `result`. The new ones:

```json
{"ok": true, "result": {"state": "recorded", "result": {"…": "the write's own result"}}}
{"ok": true, "result": {"state": "refused", "code": "stale_revision", "details": {"current_revision": 4}}}
{"ok": true, "result": {"state": "unknown_request", "now": 1794699400}}
```

`outcome`, above: its three states are answers, not refusals. A refusal of the `outcome`
envelope itself is a refusal, `signature_refused` with reason `authorization_changed`
included.

```json
{"ok": true, "result": {"versions": {"inbox": "00000000000000000012", "decisions": "00000000000000000040",
  "ROOM_ID": "00000000000000000003", "computers": "00000000000000000007"}, "changed": ["decisions"]}}
```

`changes`, above: `versions` has every topic that exists on the target (`computers` only on the
node), as today's route does with URIs; `changed` lists the topics whose version differs from
the request's, also a topic that only one side has, sorted; it is `[]` when the wait ended with
no change. A request with no `versions` answers at once with the current ones.

```json
{"ok": true, "result": {"node": {"pin": "…", "name": "desk"},
  "computers": [{"pin": "…", "name": "lab-1", "state": "connected", "reason": "", "since": 1794696630},
                {"pin": "…", "name": "lab-2", "state": "offline", "reason": "link_unreachable", "since": 1794696623}]}}
```

`computers`, above: `since` is Unix seconds of the state's start.

```json
{"ok": false, "code": "computer_offline", "error": "…", "details": {"target": "PIN", "last_seen": 1794696623}}
{"ok": false, "code": "outcome_unknown", "error": "…", "details": {"target": "PIN", "request": "…"}}
{"ok": false, "code": "signature_refused", "error": "…", "details": {"reason": "time"}}
```

The node's own refusals, above: `computer_offline` and `outcome_unknown` come from the node;
`signature_refused` comes from the target (or the node for its own pin), with its reason.

The Serve route (Decision 5.2):

```json
{"ok": true, "token": "…", "client": "key:koinon-voice", "idle_seconds": 86400, "absolute_seconds": 604800}
{"ok": false, "code": "maintainer_token_expired", "error": "…"}
```

1. `POST /v1/maintainer/signed/session` answers the first line, flat, as today's
   `/v1/maintainer/session` does (`internal/core/maintainer_mcp.go`): the token lasts
   `idle_seconds` without use and `absolute_seconds` at most.
2. `POST /v1/maintainer/signed` with an unknown, ended or missing bearer token answers the
   second line with HTTP 409, the status of every refusal today (`internal/core/server.go`),
   before the envelope is read or sent anywhere. `invalid_request` answers 400.
3. Requests are `Content-Type: application/json`, one JSON object, at most 512 KiB (a `send`
   or `room_post` body is at most 64 KiB of text, as today). An answer is at most 1 MiB; reads
   page with `limit` so that their answers fit.
4. The link operation `maintainer` carries one envelope and one answer in frames of up to
   1 MiB each, above the 128 KiB of other link operations (`internal/link/frame.go`); a larger
   one is refused (`too_large`).

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
   A write that commits, loses its answer, and whose key is then removed and enrolled again with
   a new statement: an envelope of the earlier authorization, replayed, is refused with
   `authorization_changed`; the same statement file added again is refused with
   `statement_used`, also within its 10 minutes.
   A write whose transaction is held open across the end of its window while `outcome` runs:
   `outcome` waits and then sees the receipt, or answers final when the write rolled back; it
   never answers final before a commit.
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

- PDR 011 Decision 2 revised (PDR 011 Decision 6, LINK-693-SIGNED), and a new PDR 011 decision for keys and
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
