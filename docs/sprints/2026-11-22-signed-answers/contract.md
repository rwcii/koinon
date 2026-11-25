# Signed answers and recipient results (#695, #696) — contract

This contract implements `decision.md`. It changes the contract of
`docs/sprints/2026-11-14-signed-maintainer/decision.md` (here "v1"), cited by its decision
numbers, and keeps every v1 rule that it does not name. The Koinon Voice agents settle it with
Koinon before chunk 02 (rwcii/koinon-voice#41).

Terms as in v1. The **node pin** is the pin of the node that the client sends through (Koinon
Voice: the host's `node_pin`); the client does not need to enroll a key on the node for it.

## 1. Recipient results (chunk 01)

1. The maintainer's room post result, signed (both versions), on the dashboard and on
   `koinon mcp --maintainer`, keeps `room_id`, `seq`, `queued`, `skipped` and `duplicate`, and
   adds `recipients`: one entry for each member of the roster at the post other than the
   poster, in roster order.

   ```json
   {"ok": true, "result": {"room_id": "ROOM_ID", "seq": 12, "queued": 1, "skipped": 2,
     "recipients": [
       {"name": "agent-a", "state": "sent"},
       {"name": "agent-b", "state": "not_sent", "reason": "ended"},
       {"name": "review-lead", "state": "not_sent", "reason": "unheld"}]}}
   ```

   `name` is the member's label as the roster shows it (a peer name or a participant address).
   `sent` means that the target stored a copy in the member's inbox; read, wake and
   acknowledgement are separate, as on the dashboard. `reason` is present only for
   `not_sent`: `retired`, `ended`, `lapsed` or `no_liveness_evidence` for a member session
   (the liveness reasons of `internal/core/liveness.go`), `unheld` or `conflicting` for a
   participant member. A room has at most 32 active members, so the list is small.
2. An agent's `room_post` answer stays as it is: counts only, never who the members are.
3. A `send` result keeps its shape; a success means `sent`. A refusal keeps its code:
   `peer_not_found`, `alias_unheld`, or `recipient_inactive` with `details.reason` (the reasons
   above).
4. The target stores the whole result, or the code and details of a refusal, in the once-only
   receipt in the write's transaction (v1 Decision 4.1). The same request sent again, and
   `outcome`, answer that stored result; nothing is computed again. A receipt stored before
   this release has no list and answers as it was stored; no past roster is built again. The
   client then shows "recipient list not recorded".
5. Nothing that was not sent is stored for later delivery.

## 2. Envelope version 2 (chunk 02)

1. `v` is `koinon-maintainer-2`. The signed bytes are `koinon-maintainer-2\n` followed by the
   RFC 8785 bytes of the envelope without `sig`. Every other field, value rule, check and
   refusal is as in v1 Decision 3. The enrollment statement stays as in v1.
2. A target at this release takes both versions. A v1 envelope gets the v1 answer, unsigned,
   with the additive fields of section 1. A v2 envelope gets a signed answer (section 3).
3. Receipts are as in v1 Decision 4; the digest of a v2 request is the SHA-256 of its v2 signed
   bytes. A v1 and a v2 envelope with the same request ID have other digests: the second is
   `request_conflict`. `outcome` in v2 reads a receipt of a v1 or v2 request of the same
   authorization.
4. Request IDs: each read and each `outcome` has a new request ID. A write that is sent again
   is the same envelope, so it keeps its receipt.
5. `session`: on the Serve route (v1 Decision 5.2), a v2 `session` envelope, signed for the
   node's own pin, gets a signed answer with `signer` `target` and `pin` the node pin; its
   `answer` is today's flat token object (`{"ok": true, "token": ...}`, v1 "Answer shapes").
   The client checks it before it uses the token. The node still opens the session only for a
   key that it authorized (v1 Decision 5.1); the node pin only checks the answer. In the local
   form (`koinon mcp --maintainer --signed`, v1 Decision 5.1) the client starts the process on
   its own computer and no node is between them: the process takes a v2 `session` envelope and
   answers as today, unsigned.

## 3. The signed answer (chunk 02)

Each answer to a v2 envelope that the target or the node could parse (so that its digest is
known) is one JSON object, except the local `session` bootstrap (section 2.5) and the unsigned
answers of section 4:

```json
{"v": "koinon-answer-2", "signer": "target", "pin": "PIN", "public_key": "BASE64_SPKI",
 "request": "HEX_SHA256", "status": 200, "answer": "{\"ok\":true,\"result\":{}}", "sig": "BASE64"}
```

1. The object has exactly these eight fields, no duplicate key, and no other value types:
   `status` an integer from 200 to 599, the others strings. `v` is `koinon-answer-2`. `signer`
   is `target` or `node`. `pin` is the signer's pin. `public_key` is the standard base64, with
   padding, of the signer's link key as DER SubjectPublicKeyInfo; its SHA-256, in lower-case
   hex, is `pin`.
2. `request` is the lower-case hex SHA-256 of the request envelope's v2 signed bytes. `status`
   is the application status of the answer, the same on every transport: 200 for a success,
   else the status of the refusal on the maintainer routes (`internal/core/server.go`).
   `answer` is the exact JSON text of the answer object, in the form of v1 "Answer shapes"
   (`{"ok": true, "result": ...}` or `{"ok": false, "code": ..., ...}`; for `session`, the
   flat token object of section 2.5).
3. The signed bytes are `koinon-answer-2\n` followed by the RFC 8785 bytes of the object without
   `sig`. `sig` is the standard base64, with padding, of the Ed25519 signature of those bytes
   with the signer's link key.
4. The client accepts an answer only when: the object is as in item 1; for `signer` `target`,
   `pin` is the envelope's `target`; for `signer` `node`, `pin` is the node pin; the SHA-256 of
   `public_key` is `pin`; `request` is the digest of the envelope that it sent; `sig` checks;
   `answer` is one strict JSON object whose `ok` agrees with `status`; and, for `signer` `node`,
   `answer` is one of the claims of section 4. Then it uses `status` and `answer`, never the
   transport's status. A node-signed answer is never a target's success, refusal or outcome.
5. Any other answer is "answer not verified": the client shows no content from it, and for a
   write the outcome stays unknown until a verified signed `outcome`. An unsigned answer never
   confirms that a write did not run. The client never sends a v1 envelope again in place of a
   v2 one.
6. Transports carry the whole signed object, for a success and a refusal alike, and the node
   returns a target's signed object with its field values unchanged, `answer` text included.
   The outer JSON bytes can differ (spacing, escapes): the client checks the RFC 8785 bytes of
   the field values, not the bytes that it received. The Serve route answers it as the body, with
   `status` as the HTTP status. The `signed` MCP tool answers it as the text of its one content
   item. The link carries it in `maintainer-ok`. The client ignores the transport's own status
   and error flags for a signed object: only the verified `status` and `answer` count.
7. Size: the target measures the signed object as the link carries it, inside a `maintainer-ok`
   frame of at most 1 MiB (`signedAnswerLimit`, `internal/core/signed_forward.go`), with the
   escaping of `answer` counted. A read that does not fit is refused `too_large`, signed. Write
   results are small (section 1), and a test shows that the largest one fits, so a write that
   committed is never answered `too_large`.

## 4. The node's claims (chunk 02)

The node signs as `signer` `node`, with its own link key, the answers to a v2 envelope that it
makes itself. The client accepts only these, each with `ok` false:

| Code | When | The client shows |
| --- | --- | --- |
| `computer_offline` | the node had no connection to the target and sent nothing | the node says: computer offline, not sent |
| `outcome_unknown` | the node sent the envelope and the answer did not come | outcome unknown; read the outcome |
| `signature_refused`, reason `target` | the pin is neither the node nor a computer that it pairs with | the node says: unknown computer |
| `invalid_request` | a node operation (`session`, `computers`) signed for a paired computer's pin | the node says: invalid request |
| `too_large` | the envelope does not fit the link | the node says: too large, not sent |
| `link_operation_unknown`, `link_pair_not_found`, `link_remote_required`, `invalid_request`, `link_frame_too_large` | the target's link layer refused before it ran anything | the node says: the computer refused the link request |

For a room post to a computer that is off, the client shows the room as not sent and any member
list only as last known.

The node and the target do not sign an answer that they give before they can bind a request:
`maintainer_token_expired`, the limit of signed calls, a body that is not one JSON object, and
an envelope that does not parse as v2. Mixed releases:

| Node | Target | Answer to a v2 envelope |
| --- | --- | --- |
| this release | this release | signed by the target, or a node claim |
| this release | earlier | the target's `signature_refused`, reason `version`, unsigned |
| earlier | any | the node's `signature_refused`, reason `version`, unsigned |

The client shows an unsigned version refusal as "the node says: that computer refuses version 2
(not verified)". It never signs a new write in its place and never sends it as v1. For a write,
it can read a signed `outcome` when the target answers one; else the outcome stays unknown.

## 5. Test vectors (chunk 02)

Go produces them in a test of `internal/signed`, and Koinon Voice checks them. Synthetic keys:
the maintainer seed is bytes 0x01 to 0x20 (as v1); the target's link key seed is bytes 0x21
to 0x40; the node's link key seed is bytes 0x41 to 0x60.

Accepted: a v2 `room_post` envelope and its target answer with a recipient list; a signed
`outcome` for it with the stored list; a refused `send` with `recipient_inactive`; a v2
`session` answer; node claims `computer_offline` and `outcome_unknown`.

Refused: another `request`; another `pin`; `signer` changed; `status` changed; one byte of
`answer` changed; a `public_key` that does not hash to `pin`; a node-signed answer with `ok`
true; a node-signed code that section 4 does not list; a target answer signed with the node
key; a missing field; a duplicate field; an extra field; an unsigned version refusal.

Values (generated with Go `crypto/ed25519` and `internal/signed.Canonical`, checked with Python
`cryptography`). The envelopes use the v1 key ID and authorization ID of the maintainer seed.
The negative cases are mutations of these values; the chunk 02 test makes them.

```text
target_pin: c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091
target_spki_b64: MCowBQYDK2VwAyEA5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=
node_pin: 60ed0b151e1f088d443a953a5ccaebe7377b0df8b5ccd2228f0d49eb08ae9bc5
node_spki_b64: MCowBQYDK2VwAyEArcFAEfgtHFbZVqpPnXPYhYNhpgYEhSXg0Ixjjcdd2Mc=

envelope room_post
canonical: {"args":{"body":"a synthetic post","room":"0123456789abcdef0123456789abcdef"},"auth":"60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e","created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"room_post","request":"vec-request-000002","target":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","v":"koinon-maintainer-2"}
sig_b64: krYr9KjRLjQ6wZCMOlHtmAbYVjuY7RhInbEcnbZLShAKZAPEpnv//XL24RXaOCKUIQUr/+X1seuH6R/f1v0IBw==
request_digest: 100511c6f18a67f5894a17816913ad4120d4d49919a2f1589381df8a9803430e

answer room_post (target)
canonical: {"answer":"{\"ok\":true,\"result\":{\"room_id\":\"0123456789abcdef0123456789abcdef\",\"seq\":12,\"queued\":1,\"skipped\":2,\"recipients\":[{\"name\":\"agent-a\",\"state\":\"sent\"},{\"name\":\"agent-b\",\"state\":\"not_sent\",\"reason\":\"ended\"},{\"name\":\"review-lead\",\"state\":\"not_sent\",\"reason\":\"unheld\"}]}}","pin":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","public_key":"MCowBQYDK2VwAyEA5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=","request":"100511c6f18a67f5894a17816913ad4120d4d49919a2f1589381df8a9803430e","signer":"target","status":200,"v":"koinon-answer-2"}
sig_b64: M8eld1dxIE5M2mswvlQjd6vndkOsVERT5NlOxrEidfEGnQ2rfrt/cZOhXyHI1e93lwXh5M6WmnpTi1f1oHFaCA==

envelope outcome
canonical: {"args":{"created":1794699000,"request":"vec-request-000002"},"auth":"60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e","created":1794699060,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"outcome","request":"vec-request-000003","target":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","v":"koinon-maintainer-2"}
sig_b64: m9a7mhmyGMsXoGRVtON9GqV1eWNWSw5/Do4IODF4tt0M1WFz/B6FFn2MojvncJ1WPkRN4EO+/iBNA6/i6GrtDQ==
request_digest: eb1ceeb983d60e1da20e177429942a380dfb11c01d92b8181b55c1702b7f013a

answer outcome (target)
canonical: {"answer":"{\"ok\":true,\"result\":{\"state\":\"recorded\",\"result\":{\"room_id\":\"0123456789abcdef0123456789abcdef\",\"seq\":12,\"queued\":1,\"skipped\":2,\"recipients\":[{\"name\":\"agent-a\",\"state\":\"sent\"},{\"name\":\"agent-b\",\"state\":\"not_sent\",\"reason\":\"ended\"},{\"name\":\"review-lead\",\"state\":\"not_sent\",\"reason\":\"unheld\"}]}}}","pin":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","public_key":"MCowBQYDK2VwAyEA5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=","request":"eb1ceeb983d60e1da20e177429942a380dfb11c01d92b8181b55c1702b7f013a","signer":"target","status":200,"v":"koinon-answer-2"}
sig_b64: IzUnPbO2NR83W4syipbGSwcQQuUro8tEkBjgzNBoIEkbxlOdadHifWYWnJSuXbFQEh+H8uKGVJZxFuC8rhQaDA==

envelope send
canonical: {"args":{"body":"a synthetic message","to":"agent-b"},"auth":"60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e","created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"send","request":"vec-request-000004","target":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","v":"koinon-maintainer-2"}
sig_b64: dZEDmZRTg2oyrmd7YR+9LmC3L1I4Qy5FA9uY/9Ifam/cCvwrGkSsD0FFJmUgPFFKXlHzVudk5PEm69R23QNaAw==
request_digest: 4248c56e90d38545c8c2a11aa7ddf3130cf55f28496d36ef0a73743553133e9b

answer send refused (target)
canonical: {"answer":"{\"ok\":false,\"code\":\"recipient_inactive\",\"error\":\"the recipient session is expired or retired\",\"details\":{\"reason\":\"ended\"}}","pin":"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091","public_key":"MCowBQYDK2VwAyEA5/FioQvsVZr+oZXk3OhLaVaNXSywlj60RsBoXisX8vA=","request":"4248c56e90d38545c8c2a11aa7ddf3130cf55f28496d36ef0a73743553133e9b","signer":"target","status":409,"v":"koinon-answer-2"}
sig_b64: nOt9jwyTdnk7Bb7a3u/sfBTk7lm9SA4A5NX5x+Ayo+bKVULuZlTyJzSqC9w6WLGsIdNDzSDTSuMztCrp9tqZBA==

envelope session
canonical: {"args":{"client":"koinon-voice"},"auth":"60f6757e18de87bfa4918aa9b764b50b36cbddf982d9e4bebe226ffc51836f6e","created":1794699000,"key":"SHA256:ZbYGc9btiEvwHCwiLYKtoHQPKawzVdapJcgfF/R6J7g","op":"session","request":"vec-request-000005","target":"60ed0b151e1f088d443a953a5ccaebe7377b0df8b5ccd2228f0d49eb08ae9bc5","v":"koinon-maintainer-2"}
sig_b64: WQpbwJF1SS/qaxFOdO7/0CuKrboqtEmn1p58eg0AX+nOxXZu8JS1/wmC7BsO06nOB3bjitp9Cu043N1e42SaCw==
request_digest: 984f20d526d25165e10330fc689df72f9fe48b101447290fb82b3fd0c16ea728

answer session (node as target)
canonical: {"answer":"{\"ok\":true,\"token\":\"synthetic-token-000001\",\"client\":\"key:koinon-voice\",\"idle_seconds\":86400,\"absolute_seconds\":604800}","pin":"60ed0b151e1f088d443a953a5ccaebe7377b0df8b5ccd2228f0d49eb08ae9bc5","public_key":"MCowBQYDK2VwAyEArcFAEfgtHFbZVqpPnXPYhYNhpgYEhSXg0Ixjjcdd2Mc=","request":"984f20d526d25165e10330fc689df72f9fe48b101447290fb82b3fd0c16ea728","signer":"target","status":200,"v":"koinon-answer-2"}
sig_b64: 57WB+2tEcv/rl0OXO0tIdknrNJLoSf8Q3xZWwej86vxu1NU7kLIJKsawhmQAqva2rX3uribpRZylrR5H5cu1BQ==

answer computer_offline (node claim, for room_post)
canonical: {"answer":"{\"ok\":false,\"code\":\"computer_offline\",\"error\":\"the node could not reach that computer and sent nothing; the action did not run\",\"details\":{\"target\":\"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091\",\"last_seen\":1794696623}}","pin":"60ed0b151e1f088d443a953a5ccaebe7377b0df8b5ccd2228f0d49eb08ae9bc5","public_key":"MCowBQYDK2VwAyEArcFAEfgtHFbZVqpPnXPYhYNhpgYEhSXg0Ixjjcdd2Mc=","request":"100511c6f18a67f5894a17816913ad4120d4d49919a2f1589381df8a9803430e","signer":"node","status":409,"v":"koinon-answer-2"}
sig_b64: D+V3ABvL/jVVcABJ3hstFcag6JtCt5CLsau85f6hQzXRacQbbKh5YkV444RoyBc1+UFAx3wTQ9gK4QtOC03GAA==

answer outcome_unknown (node claim, for room_post)
canonical: {"answer":"{\"ok\":false,\"code\":\"outcome_unknown\",\"error\":\"the node sent the action and its answer did not come back; read its outcome\",\"details\":{\"target\":\"c4b2956e00f048ac8ac94746e6699cebebfdf682181a0011c8e3feea5ab7c091\",\"request\":\"vec-request-000002\"}}","pin":"60ed0b151e1f088d443a953a5ccaebe7377b0df8b5ccd2228f0d49eb08ae9bc5","public_key":"MCowBQYDK2VwAyEArcFAEfgtHFbZVqpPnXPYhYNhpgYEhSXg0Ixjjcdd2Mc=","request":"100511c6f18a67f5894a17816913ad4120d4d49919a2f1589381df8a9803430e","signer":"node","status":409,"v":"koinon-answer-2"}
sig_b64: 6TnLjMFTP115nmcKckpLOiP2uYxqYYmiXJtDRH7FyHBFiW5QX3DmlI1jqTmrxk7LYkAs+1xlRAef7oVl1Bq7Dw==
```
