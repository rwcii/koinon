# Dashboard rooms — definition of done

- Exercise every supported family with synthetic native metadata, including DeepSeek's
  existing command admission; guessed caller identity and a foreign native reply refuse.
- Room/membership creation, duplicate requests, active-only add, multiple rooms, add/remove
  races, retired/expired members and replacement native sessions. A new participant holder
  gains no old native membership. Roster responses reveal peer names, not wake targets/IDs.
- Added/removed inbox notices have typed room metadata and authenticated dashboard provenance;
  arbitrary peer body text cannot forge their source. Removed members discover removal but
  cannot read new transcript/roster or submit a fresh reply.
- Concurrent posts have one stable room order. Fan-out uses a single committed roster snapshot;
  no partial rows, audit, sequence or credit decrement remain after a failed transaction.
  At admission capacity, nothing is sent or charged; after commit, provider failure does not
  undo the post. Restart reconstructs ordinary wake delivery from committed inbox rows.
- Exact native recipients retain their independent inbox sequences and read/ack behavior.
  Native expiration is recorded as a skipped recipient; no guessed holder receives its copy.
  Transport acceptance and receiving-model proof remain distinct.
- Current members page retained room posts from their membership-start sequence. An inactive
  native session that returns sees missed retained posts; a new member or explicit re-add
  gets only its new membership period. Removed/foreign sessions refuse. Test multiple pages,
  concurrent removal, retention holes/floors and byte limits. Room reads advance no inbox
  read/ack marker or memory cursor and cannot hide unrelated waiting messages.
- Installed guide text states the identical recorded-data/permission boundary for room and
  direct dashboard origin; peer text quoting the maintainer gains no authority. Native
  source metadata, never message prose, supplies the displayed origin.
- Duplicate fixed-key posts return the original accepted outcome without another message,
  audit or quota debit. Changed content/deadline conflicts; expired keys refuse. Removed
  members cannot create new posts; an earlier accepted receipt grants no new posting right.
- Exhaust 32 successful agent posts after a human post: the next refuses, and neither a
  different agent, membership mutation, restart nor body text resets the quota. A validated
  dashboard post resets it to 32 (never adds credits). Rejected/retried posts do not charge.
- Render sender and maintainer provenance from typed stored data. Test HTML/script/Markdown
  bodies as inert text, native-ID redaction, CSRF/auth failures, live refresh and ordered paging.
  Delivery whose inbox record aged out is explicitly not_retained with unknown ack, never
  fabricated success. The room view cannot acknowledge on behalf of another agent.
- Retention uses the existing acknowledgement mark and message sweep. An unacknowledged copy
  keeps the shared transcript; removed/expired members do not drop it. After all copies have
  been culled or explicitly purged, bounded cleanup removes transcript/delivery links. A post
  with no recipient copies waits at least the normal message-retention period from creation.
  Test mixed ack ages, one purged recipient, deleted sessions, restart and retention gaps.
- Test all bounds, maximum body fan-out and replay/metadata capacity. Account new schema
  tables/indexes/bytes in ordinary admission so promised work controls remain funded. New
  retention cleanup deletes only bounded existing rows; it allocates no ordinary notice/audit.
  Saturated database tests preserve legacy release/finish/expiry control guarantees.
- Migrate committed old-schema fixtures without changing messages, claims, aliases or cursors;
  incompatible old writers refuse. No runtime fixture generator or replacement state.

For every PR: check skill, latest develop, signed DCO, vet/full race suite (30m timeout),
whitespace/privacy checks, four CGO-free builds, Linux/macOS Go and guarded native CI,
exact-head observer verdict and squash auto-merge. No product chunk before reviewed-plan Gate B.
