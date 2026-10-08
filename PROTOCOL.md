# Koinon protocols

## Go daemon core

The Go daemon is the Koinon runtime. Its HTTP API uses a
private user secret from the selected Go state root, passed in `Authorization: Bearer SECRET`.
No API endpoint is anonymous. Each listener must be a literal loopback address; foreign
browser origins and non-loopback Host headers are refused. The API accepts no browser cookies.
The secret and state files are user-owned regular files, mode 0600; the state root is private.
Never put a secret, runtime session identifier or response body in a repository.

| Route | Operation |
| --- | --- |
| `GET /v1/status` | Daemon readiness, listeners, schema, session counts, storage and content-free wake health. |
| `GET /v1/sessions` | Up to 1,000 records ordered by family and ID; `truncated` reports more records. |
| `POST /v1/sessions/register` | Register or reactivate one session identified by `(family, id)`. |
| `POST /v1/sessions/renew` | Renew an active session's expiry. |
| `POST /v1/sessions/retire` | Mark an active session retired, retaining its record. |
| `POST /v1/peers` | Each session's `name`, held `alias`, `family`, `state` and `repository`, for an active caller; never a wake target, directory or session ID. |
| `POST /v1/peers/status` | Resolve a published peer name or held alias and read public identity plus observed model, context and activity for an active caller. |
| `POST /v1/messages/send` | Store one message for the session that a peer name or alias names. |
| `POST /v1/inbox/read` | Read the caller's own inbox after a sequence number. |
| `POST /v1/inbox/ack` | Acknowledge the caller's own inbox through a sequence number. |
| `POST /v1/messages/outcome` | Read the delivery and acknowledgement state of a message the caller sent. |
| `POST /v1/wake/agy-stop` | Offer a due notice for this Antigravity conversation at its native Stop boundary. |
| `POST /v1/launches` | Retain a launcher target and return its generated launch ID. |
| `POST /v1/memory/record` | Record an entry in the caller's repository memory store. |
| `POST /v1/memory/sync` | Read a snapshot page or a delta batch; never moves a cursor. |
| `POST /v1/memory/ack` | Acknowledge a fully issued snapshot or a delta through a sequence. |
| `POST /v1/memory/recall` | Find live entries whose body contains a query, newest first. |
| `POST /v1/memory/status` | Report the store's head, floor, usage, limits, lifetimes and consumers. |
| `POST /v1/sessions/observe` | Record allowlisted observations (model, context, activity, terminal) for an active session; see "Dashboard and session observations". |
| `POST /v1/dashboard/links` | Issue a one-time dashboard login link (`path`, `expires_in`). |
| `POST /v1/storage/recover` | Prove the write-ahead log empty again, return free pages and clear a blocked state. |

POST requests use `Content-Type: application/json`, at most 16 KiB, with unknown fields refused.
Registration fields are `family` (`claude`, `codex`, `deepseek`, `agy`, `opencode`), `id` (1–256
bytes), an absolute `directory` path, an optional absolute `repository` path,
optional `wake_target` JSON (up to 8 KiB),
optional `launch_id`, and optional `ttl_seconds` (60–3,600, default 900). A `launch_id` selects
the daemon-held target and refuses a simultaneous `wake_target` override. Its family and
canonical directory must match the registering session. The daemon derives the canonical absolute
Git common directory when a repository is selected; the working directory must then belong
to that repository. Worktrees share the repository identity. A session in a plain directory
can omit `repository`; its stored repository is empty. Memory and work operations require a
selected repository. Wake targets are local registration metadata used by the adapters below.

Renew/retire take `family`, `id`, and `if_revision`; renew also accepts `ttl_seconds`. A stale
revision, expired session or retired session is refused with `session_conflict`. Re-register
to return an expired/retired session to active without deleting its retained data. Concurrent
registrations update one row, never create two records for the same key. Times are Unix
milliseconds. Expiry is computed from the current wall clock; clock jumps can change effective
expiry, never remove records or imply a model stopped. Restart preserves records and revisions.

Each session record carries its peer `name` and, while it is active and holds one, its `alias`.
The daemon gives each session a permanent peer name `<family>-<label>-<2 hex>`. The label is the
repository directory name (the folder that holds `.git`, or a bare `NAME.git` without `.git`),
or the working directory name for a session without a repository: lower case, characters other
than `a-z`, `0-9` and `-` replaced with `-`, at most 32 characters, `session` when empty. The two
hex digits start at the first byte of SHA-256 of `family NUL id` and take the next free value;
when all 256 are taken, the name takes 4, 6, … hex digits of that digest. A renewal or a new
registration of the same key keeps the name. Each family and repository reserves one alias
`<family>-<label>`; when another name already uses it, `<family>-<label>-<first N hex of SHA-256
of the Git common directory>` for N = 4, 6, …. The reservation is permanent. Peer names and
aliases share one namespace, so a peer name never equals an alias. The alias names at most one
holder: an active session of that family whose repository is the alias's repository. When the
holder expires, retires or registers for another repository, the next registration or renewal
of the same family and repository takes it. A session without a repository has no alias.

Message calls name their `caller` as `{"family": ..., "id": ...}`; the caller must be an active
session, or the call fails with `caller_inactive`. A caller reads and acknowledges only its own
inbox and reads only the outcome of its own messages; another sender's message reads as
`message_not_found`.

- Send takes `caller`, `to` (a peer name or alias) and `body` (1–65,536 bytes of UTF-8; the
  request may be up to 6 × 64 KiB + 4 KiB, for JSON escaping). The message, the sender's session
  and peer name, and the next sequence number of the receiving inbox are stored in one
  transaction; sequence numbers per inbox are gapless and ordered. The reply's `message` carries
  `id`, the receiving `recipient` peer name, `seq` and `delivery_state`. Errors:
  `peer_not_found` (404) for an unknown name, `alias_unheld` (409) for an alias with no active
  holder, `recipient_inactive` (409) for an expired or retired recipient.
- Read takes `caller`, `after` (default 0) and `limit` (1–100, default 50). The reply's `inbox`
  holds `messages` in sequence order, `last_seq`, `acked_through` and `more`; a page stops after
  about 1 MiB of bodies. Each message has `id`, `seq`, `sender_family`, `sender_name`, `body`,
  `created_at`, `delivery_state`, `delivery_reason`, `acknowledged` and, for an acknowledged
  message, `acknowledged_by`: `recipient` or `maintainer`.
- Acknowledge takes `caller` and `through`. Acknowledgement only moves forward: an earlier
  sequence leaves `acked_through` unchanged, and a sequence beyond the last fails with
  `ack_beyond_last` (409). A message is acknowledged when its `seq` is at most `acked_through`.
- Outcome takes `caller` and `message_id` and returns `id`, `recipient`, `seq`, `delivery_state`,
  `delivery_reason`, `updated_at`, `acknowledged` and `acknowledged_by` (#82). For a message that
  retention or a purge deleted, it returns `ok: true` with `id` and `delivery_state` `deleted`;
  the other fields are empty, zero or false. Any issued ID that the daemon no longer holds reads as `deleted`, also when another
  session sent it; a held message of another sender stays `message_not_found`.

The daemon has one built-in session, family `maintainer` and peer name `maintainer` (schema 7).
It has an inbox, never expires, has no repository, alias or wake target, and is never woken.
`peers` lists it last, so agents can send to `maintainer`; the session list and the session
counts leave it out. No `/v1/` caller can act as it, register it, renew it or retire it
(`invalid_request`); only the dashboard sends, reads and acknowledges as the maintainer.

Each message has exactly one delivery state: `waiting`, `notified`, `uncertain`, or `failed` with
a reason. Messages are stored as `waiting`; the adapters below update delivery independently
of inbox storage and acknowledgement.

Replies include `ok`. Success returns `session` or `sessions`; failures report a fixed `code`:
`unauthorized` (401), `foreign_origin` (403), `invalid_request` (400), `session_not_found` (404),
`session_conflict` (409), the message codes above, or `storage_error` (500). Mutations commit
before replying. A lost reply does not prove rollback; read the retained record before
retrying.

Launch creation takes `family` (`codex`, `agy`, `opencode`), absolute `directory` and `cli`,
and positive `host_pid`. OpenCode also requires `address` (literal loopback with a nonzero
port) and `password` (64 hex characters). Other families refuse those credential fields.
The response returns `ok` and `launch_id`. The credential stays only in private launch storage;
session responses contain a `launch_id` reference and target metadata with no password field.
Launch records survive a
restart, and can bind successive native session identities from one CLI process, such as a
Codex context reset. They are inert state until the agent registers and wake adapters
use its target. All launch calls use the existing bearer authentication, request limits and
origin checks; a launch ID is an association key, not a replacement authentication secret.

### Wake delivery

Schema 6 adds durable attempt counts, retry deadlines and fixed wake reason codes to messages
in the same atomic migration as `user_version`, preserving sessions, inboxes, memory and work.
Unknown future schemas are refused before migration.

The worker polls once per second and handles at most 16 due inboxes per pass. Before any
provider side effect, it commits `uncertain` with `attempt_in_progress`. Busy or unreachable
results become `waiting`; confirmed queue acceptance becomes `notified`; ambiguous results
remain `uncertain`. Busy receivers are rechecked every 3 seconds without increasing the failed
attempt count. Other waiting and uncertain messages retry after 1, 2, 4, ... seconds, capped at
5 minutes. Attempts survive restart; a backward clock jump cannot strand a retry more than
the maximum backoff ahead. Acknowledgement and withdrawal share the submission boundary:
an acknowledgement committed before submission is never included. Provider calls have a
3-second deadline bounded by session expiry. No peer body or provider output enters a notice.

Each notice names only the quoted native family/ID inbox and its sequence range. Codex uses
the configured absolute CLI with `queue --thread ID --message NOTICE`. Claude requires a
same-user registry record matching its native session ID and CLI entrypoint, the status
`idle` or `shell` (the prompt with a background shell task running; the session takes the
`next`-priority notice at its turn boundary), an owned private socket/directory, and the
connected kernel UID/PID. The statuses `busy` (a turn in progress) and `waiting` (an approval
or question prompt) are a busy receiver. Key filenames hash the literal unresolved socket
path. The daemon's private same-process reply listener accepts no commands and needs no
registry entry. A Claude write remains `uncertain` because the socket
protocol provides no verifiable queue receipt; a duplicate notice is possible until inbox ack.

DeepSeek validates a canonical HTTP(S) loopback authority and pins all resolved addresses
before reading its private same-user browser-session credential. An authority-bound signed
cookie accompanies `session/prompt`, `mode: queue`, for the exact session. No proxy or redirect
is permitted. Only `ok: true` with `value.accepted: true` confirms queue acceptance. OpenCode
uses the launcher's private password on its literal loopback server. A fresh `/session/status`
read precedes `/session/ID/prompt_async`; an omitted status counts as idle only when read-only
`GET /session/ID` confirms this exact ID exists. Busy/retry and unknown sessions are not woken.

`POST /v1/wake/agy-stop` takes `caller` with family `agy` and the hook's native conversation ID.
It offers a due unacknowledged notice at that turn boundary, leaving delivery uncertain. The
Stop hook returns `{"decision":"continue","reason":NOTICE}` only for that offer; otherwise `{}`,
including when the daemon is unavailable. `GET /v1/status` and dashboard health expose `wake`
counts, grouped fixed reason codes and a storage fault, without message bodies or credentials.
Queue acceptance does not prove model processing. Expired/retired sessions keep their inboxes,
until retention deletes them, and receive no new wake until registered again.

### Retention

The maintenance loop runs a retention sweep after each work sweep (#216). Schema 9 adds three
session columns for it: `ack_mark` and `ack_mark_at`, the sweep's acknowledgement mark, and
`purge_at`, the time of the maintainer's purge mark (0 when unmarked). The periods are fixed:

- **Acknowledged messages: 30 days.** When an inbox has acknowledged messages and no mark waits,
  the sweep records a mark: every message at or below the acknowledged sequence was
  acknowledged by that time. Thirty days after the mark, the sweep deletes those messages. A
  message is therefore deleted between 30 and 60 days after its acknowledgement, never sooner.
  An unacknowledged message is never deleted by retention.
- **Inactive sessions: 30 days** after their activity ended: the retirement time of a retired
  session, otherwise the expiry time (0 for an imported session without one). The sweep deletes
  such a session with its peer name when it holds no message and no active claim bundle owned
  by its consumer key `FAMILY:ID`. A session with unacknowledged messages stays until they are
  acknowledged, by the recipient or by the dashboard `clear`, and their own retention has
  passed. The deleting transaction reads every condition again, so a session that registered
  again meanwhile stays. The alias that it held stays reserved for its repository and goes to
  the next active session of that family and repository. Its released peer name is free at once,
  so a later session can receive the same name; a default memory consumer, which is the peer
  name, then continues the earlier cursor.
- **Purge.** A session that the maintainer marked for purge is deleted by the next sweep with its
  whole inbox, unacknowledged messages included, and its names, without a retention period.
  Its live claims are released first, each as the owner's release with the checkpoint
  `Released by the maintainer's purge of the session` and a work event that names family and
  name `maintainer`. A claim whose lease expired waits for the work sweep's reconciliation; a
  claim that cannot be released keeps the session until a later sweep. Removing the mark stops
  the purge: the sweep reads the mark again before it releases each claim, under the lock that
  removing a mark also takes, and in each transaction that deletes messages or the session.
  Claims released and messages deleted before the removal stay so.

A send to a deleted peer name is `peer_not_found`. Each deletion is a control write (it may use
the storage reserve) in its own transaction: at most 500 messages per transaction, 10,000
messages per sweep for both rules together and 100 sessions per rule and sweep, so a sweep holds the storage boundary no longer
than the work sweep does. Memory entries, work items and audit records keep their own retention.

`GET /v1/status` (and `koinon status`) and dashboard health report `retention`:
`message_retention_days`, `session_retention_days`, `marked_for_purge`, `last_sweep` (Unix
milliseconds), `last` with the counts of the last completed sweep (`messages_by_retention`,
`messages_by_purge`, `sessions_by_retention`, `sessions_by_purge`, and the sessions past their
period that it kept: `held_unacknowledged`, `held_claims`), and a `fault` code with `fault_at`
when the last sweep failed.

### Memory stores

The daemon holds one memory store per Git common directory: the store of a request is the
repository that the daemon recorded for the calling session, so every worktree and
subdirectory of a repository share one store. A session without a repository gets
`repo_unresolved`. Each request names its `caller`, which must be active, and an optional
`consumer` (1–128 characters, a stable cursor name that defaults to the caller's peer name; it
names a cursor, never an identity). Provenance is the caller's family and peer name
(`writer_family`, `writer_name`).

The operations are `record`, `sync`, `ack`, `recall` and `status`. Entry fields, types, scopes,
limits, supersession and revocation follow contract 4 of
[PARITY-MEMORY-DESIGN.md](docs/PARITY-MEMORY-DESIGN.md). A response is `{"ok": true, "result":
...}` or `{"ok": false, "code": ...}`, and the `code` names the recovery path:
`snapshot_expired` and `stale_page_token` require restarting `sync`; `snapshot_incomplete`
requires paging to the end before acknowledging; `consumer_retired` requires a new consumer
key; `capacity`, `entry_too_large`, `idempotency_conflict`, `retry_deadline_expired`,
`not_issued`, `foreign_snapshot`, `snapshot_open` and `not_bootstrapped` describe a refused
request that changed nothing. A second replacement of the same entry is a successful write whose
result carries `conflicts_with`, naming the replacement it competes with; both stay live.

A `record` carrying `key` must also carry `deadline`, an absolute epoch second fixed before the
first send and repeated on every retry. Within it a repeat returns the original sequence with
`duplicate` true; past it the request is refused with `retry_deadline_expired`, because the
daemon cannot tell whether the first attempt landed. The result echoes `deadline` and reports
`idempotency_horizon`, the longest deadline the daemon accepts.

A `sync` returns either `kind: snapshot` with `snapshot_id`, `entries`, `page_token`, `total` and
`more`, or `kind: delta` with `entries`, `cursor`, `next_cursor`, `head` and `more`, and never
advances a cursor; only `ack` does. Continuing a snapshot requires both `snapshot_id` and
`page_token`. An `ack` carries `snapshot_id` once every page has been issued, or `through` for a
delta. `recall` and `status` return `more` with `next_before` and `next_after`, null when nothing
remains. Pages are bounded by encoded bytes. Sequence numbers come from a durable head that only
advances; reclaiming entries moves the retained-history `floor`, and a consumer below the floor
gets a fresh snapshot instead of a gap. A snapshot holds stored records, not a summary.

Every result is record format 2, and `record_format` is not a request field; unknown fields are refused. Recall is the
complete substring scan (`indexed: false`); there is no full-text index. Idempotency rows carry
the scheme of their fingerprint and are compared only by that scheme, so rows imported from
another runtime keep their own. A committed head change calls one content-free hook with the
repository only; synchronization, acknowledgement and a rolled-back write never call it.

The per-repository socket service of the Python runtime (`hello`, `stop`, owner records,
generations, the subscription socket) has no equivalent: the daemon serves every store. Its
storage bound applies to the daemon's one database instead. The database runs with 4,096-byte
pages, a write-ahead log with exclusive locking (no shared-memory file), cache spill off,
in-memory temporary storage, incremental auto-vacuum and full synchronization; every setting is
read back at start and a mismatch refuses the database. Every write of the daemon first proves
the log empty (a truncating checkpoint reports nothing busy and nothing left, and the log file
is absent or empty), so the log never holds more than one transaction. The database stays below
a page ceiling derived from 1 GiB for the database and its log; ordinary writes (registration,
messages, launches, memory records, consumers and snapshots) stop 2,048 pages earlier, and that
reserve serves progress and withdrawal (renewal, retirement, acknowledgement, delivery state,
page issuance, supersession, revocation and expiry). A write the ceiling refuses rolls back
whole as `capacity`. A failed proof, or a reserved write that the engine still refuses, blocks
writes with `storage_blocked` while status and reads keep working, until `koinon recover`
(`POST /v1/storage/recover`) proves the log empty again. `GET /v1/status` reports `storage`:
pages, ceilings, the log size, the work debt and the blocked state.

### Work items

The memory store of a repository also holds its work items, with the contract of
`docs/WORK-ITEMS-V1.md` and the commands of `docs/WORK-ITEMS-COMMANDS.md`. Each wire operation
is a route: `POST /v1/work/work-create`, `work-get`, `work-list`, `work-propose`, `work-edit`,
`work-start`, `work-update`, `work-release`, `work-finish` and `claim-renew`. A request names
its `caller` and the fields of the commands table; unknown fields are refused, and types are
strict (a boolean is not an integer, times are finite numbers). The optional `consumer` (1–128
characters) names a stable key; without it the consumer is the caller's session key
`FAMILY:ID`, not its peer name, which a successor can inherit, so a replacement session must
respect its predecessor's lease. Results are `{"ok": true, "result": ...}`; a refusal carries
its code and, for `claim_conflict` (holder, generation, expiry, resource) and
`revision_conflict` (current revision), bounded `details`.

Work IDs are 32 lowercase hexadecimal characters from a per-store counter that never
decreases. Every observable mutation and due transition writes, in one transaction, the item,
its scope history when the scope changes, one stream entry of type `work-event` (empty body,
scope target the work ID, the caller's provenance), the immutable event payload (the item view
at that moment), the advanced head and the replay result, and calls the change hook after the
commit. Renewal writes no event. Deltas carry `event_kind`, `work_id` and `payload`; a snapshot
adds one frozen `work-item` view per retained item, ordered by ID after the notes. Recall and
note snapshots exclude work entries, and a note cannot supersede or revoke one. Replay rows
are kept apart from note idempotency rows, carry their fingerprint scheme, and return the
original result with `duplicate: true` before any precondition or due transition.

At a statically valid request for a known target, the daemon first records at most one due
transition of that target (`lease-expired` wins over `progress-overdue`); malformed requests,
unknown targets and replays never cause one. A maintenance sweep runs at start and 30 seconds
after each sweep ends, never overlapping: per store at most 32 due transitions, then at most one
inactive claim bundle and one finished item past its 30-day retention. Memory status reports
`work_maintenance` (`enabled`, `last_successful_sweep`, `observed_at`, `pending_due`,
`expired_items`, `inactive_bundles`, `fault`, `fault_at`, `skipped_submissions`) and
`work_debt`.

The limits are those of the implementation design per store (128 items, 16 claim bundles, 64
scope revisions per item and 1,024 in all, 2,048 events, 12 MiB of work usage inside the shared
32 MiB, work entries counted in the 5,000 entries) and at most 32 retained claim bundles across
the daemon. Every retained bundle holds an overdue and an end credit until it spends them; the
pages, logical bytes, entry, event and replay slots that those credits reserve
(`docs/WORK-ITEMS-GO-STORAGE.md`) are kept free by every other write of the daemon, so a promised
release, finish or expiry can still commit when ordinary writes refuse. A mutation's item view
must leave 1 KiB of its 16 KiB bound free, so that its later due events and observations always
fit. Replay compares the decoded request, so equivalent JSON spellings are one request. The
daemon refuses a database whose header schema format is not 4, or whose schema objects (tables,
indexes, triggers, views) differ from those it creates for the database's schema version, checked
before and after a migration.

Checkout coordination uses the existing exact-resource lease engine. A read-only local
`koinon work checkout [--directory DIR]` derives `checkout:v1:SHA256`, hashing the JSON pair
of canonical Git common directory and worktree root with inherited Git overrides removed.
The authenticated `POST /v1/work/checkout-status` takes `caller` and nonempty `directory`,
which must resolve inside the caller's recorded repository. Its `result` carries
`repository`, `directory`, `resource: ["exact", KEY]`, `observed_at`, `state` (`held`,
`released`, `expired`, `unclaimed`) and nullable `writer`. A writer carries `work_id`,
`generation`, `consumer`, optional exact native `peer`, work `revision`, `expires_at`,
`lease_valid` and `checkpoint`. Reads create no store or claim and reconcile no expiry.
Inactive bundles are bounded and reclaimable; `unclaimed` does not prove an old writer
stopped editing. Writers explicitly include the resource in `work-start`.

`POST /v1/work/checkout-request` takes the same fields plus optional string `note` (up to
1,024 UTF-8 bytes). It sends an ordinary inert `checkout_writer_request` JSON message to
the currently observed native writer, including resource, work ID and generation; its
result carries `checkout` and `message` outcome. It changes no ownership. An unheld role
refuses with `checkout_unheld`; an unaddressable custom consumer with `writer_unaddressable`.
Request delivery follows the existing content-free wake and acknowledgement protocol.

`work-release` additionally accepts paired `handoff_to` (exact same-repository peer name)
and `checkout_resource` (derived key). It verifies that the live owner/generation holds
that resource, releases the entire bundle and saves its checkpoint, and inserts one
`checkout_writer_handoff` JSON message in the same transaction. The result adds
`role_released: true` and a `message` outcome. Paired key/deadline retries replay that outcome
without a second message. Aliases/foreign peers refuse with `invalid_recipient`; a missing
resource with `checkout_not_held`; inactive recipients and stale claims retain their
existing refusals. This message-adding release uses ordinary admission; a plain release
keeps its funded control allowance. Notification is an offer to explicitly pick up, not a
reservation or ownership transfer. The recipient reads the checkpoint and current status,
then `work-start`s with the same resource; a successful start issues a new token. No new
lease engine, schema, filesystem enforcement or permission grant is introduced.

### MCP server

`koinon mcp` is a stdio MCP server: newline-delimited JSON-RPC 2.0 with `initialize` (protocol
versions `2025-06-18`, `2025-03-26` and `2024-11-05`; another requested version gets
`2025-06-18`), `ping`, `tools/list` and `tools/call`. Other methods get error -32601; a message
longer than 1 MiB gets -32700. Its tools are `peers`, `send` (`to`, `body`), `inbox` (`after`,
`limit`), `ack` (`through`) and `delivery` (`message_id`), and `memory_status`, `memory_sync`,
`memory_ack`, `memory_record` and `memory_recall` with the fields of the memory routes, and
`work_create`, `work_get`, `work_list`, `work_propose`, `work_edit`, `work_start`, `work_update`,
`work_release`, `work_finish` and `claim_renew` with the fields of the work routes, which call
the routes above. `work_checkout` (optional `directory`, default MCP working directory)
and `work_checkout_request` (also optional `note`) call the checkout routes. Relative
directories resolve against the MCP working directory and must share its Git common
directory. They take no model-supplied consumer or caller identity. A tool
error is a result with `isError` and a JSON text `{"ok": false, "code": ...}`: the daemon's code,
`daemon_unavailable`, `identity_unavailable`, `invalid_arguments` or `unknown_tool`, with the
`details` of a work refusal. The server
writes only protocol messages to stdout and never logs a secret, session ID or message body.

The calling session comes from the agent on every call, never from model-supplied arguments; a
call whose arguments hold `caller`, `family`, `id`, `as`, `session` or `session_id` is refused:

| Family | Identity source |
| --- | --- |
| Codex | `_meta.threadId`, from a client whose name starts with `codex`; one server serves each thread as its own session. |
| Antigravity | `_meta["antigravity.google/conversation_id"]`. |
| Claude | `CLAUDE_CODE_SESSION_ID` in the server's environment, only when `initialize` names the client `claude-code` and the server's parent process is a Claude Code executable. |
| OpenCode | The `koinon_session` argument that the Koinon plugin sets from the calling session's ID; accepted only from the `opencode` client. |

DeepSeek has no MCP identity source yet and uses the `koinon` commands with `--as`. The server
registers a session at its first call, with the working directory and, inside Git, its
repository, and registers it again after five minutes or when the daemon reports it inactive.
Every five minutes it renews the session it served last. Registration carries wake data for the
wake adapters: Claude `{"claude_pid": PID}`; Codex `{"cli": PATH}` from `launchers.json`, else
the parent Codex executable, never a `PATH` search; a launched Codex, Antigravity or OpenCode
agent passes `KOINON_LAUNCH_ID` as `launch_id`. A Claude session that `koinon claude` started
registers with `claude_pid` like any Claude session; its launch record documents the start, and
the launcher keeps the caller's `CLAUDE_CONFIG_DIR` but removes the other `CLAUDE_` variables and
`CLAUDECODE` of the session that ran it. In a new tmux session, whose environment comes from the
tmux server, it carries the caller's value, or its absence, as the session variable
`KOINON_CLAUDE_CONFIG_DIR`, which the pane's launcher turns back into `CLAUDE_CONFIG_DIR`. `KOINON_STATE_DIR` and `KOINON_DAEMON_ADDRESS`
select the state root and address.

### Dashboard and session observations

The daemon serves a dashboard under `/dashboard/` on its loopback listeners. Pages are
rendered on the server and their CSS and script are embedded in the binary; nothing is fetched
from elsewhere. Peer message bodies appear here, escaped, and nowhere else outside the inboxes.

- **Login.** `koinon dashboard [--state-dir DIR] [--address ADDR] [--no-open]` asks
  `POST /v1/dashboard/links` for a link, prints `http://ADDR/dashboard/login?token=TOKEN` and
  opens it when a browser is available (macOS `open`, Linux `xdg-open` with `DISPLAY` or
  `WAYLAND_DISPLAY`; never in an SSH session). A token has 256 random bits, works once within 60
  seconds, and at most 8 are outstanding. Its use starts a session: the cookie `koinon_dashboard`
  is `HttpOnly`, `SameSite=Strict`, `Path=/dashboard/` and has no `Domain`. A session ends after 30
  idle minutes, after 12 hours, at logout, when 16 newer sessions exist, or when the daemon
  restarts. The daemon keeps only SHA-256 hashes of tokens and session IDs, in memory.
- **Request protection.** Every dashboard request must name the literal address of the listener
  that accepted it in `Host` (`127.0.0.1:PORT` or `[::1]:PORT`; `localhost` is refused); an
  `Origin`, when present, must be `http://` plus that host. A request other than `GET` or `HEAD`
  needs that `Origin` and the session's CSRF token in `X-Koinon-CSRF` or the form field `csrf`.
  The token is HMAC-SHA256 of the session ID under a key made at each daemon start; pages carry it
  in a hidden field, so the cookie stays unreadable to scripts. A wrong host, origin or token is
  403; no session is 401. Every response carries a `default-src 'none'` content security policy
  that allows only the dashboard's own script, style and fetches, `frame-ancestors 'none'`,
  `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: same-origin` (under `no-referrer` a browser sends
  `Origin: null` with a same-origin `POST`, which the origin check refuses), `Cache-Control: no-store`
  and `Cross-Origin-Opener-Policy: same-origin`. The dashboard cookie never authorizes `/v1/`, and
  the bearer secret never authorizes `/dashboard/`.
- **Views.** `sessions` (100 per page, with the counts of active, expired and retired
  sessions; names, family, repository and working directory, state and times, the
  observations below with their source and time, and the live claims held under the session
  key `FAMILY:ID`; `q=TEXT`, at most 256 bytes, lists only the sessions whose peer name,
  alias, family, repository, directory or state holds TEXT, ignoring ASCII case, with `%` and
  `_` matched literally), `messages` (every inbox, 50 per page and about 1 MiB of bodies,
  `to=NAME` for one recipient, a peer name or a held alias; delivery state and reason,
  acknowledgement; the send form suggests the names and held aliases of at most 1,000 active
  sessions with their family and state, and with `peers=all` of every session, active first,
  never a session ID or directory), `memory` (per store:
  head, floor, entries and logical bytes against their ceilings, consumers, work debt and
  maintenance, 50 stores per page; with `store=REPOSITORY`, that store's entries, newest first,
  50 per page with `before=SEQ`: sequence, time, type, scope, path, writer, author, body, state
  `live`, `superseded`, `revoked` or `expired`, and the supersession, revocation and conflict
  links; work events are not listed. `q=TEXT` (at most 256 bytes) matches the body or path as a
  substring, ignoring ASCII case; `type=TYPE` filters; without `all=1` only live entries are
  listed), `work` (per store, every unfinished item with its claim,
  resources, lease and progress state computed at read time; stores page by repository, 50 at a
  time, with `after=REPOSITORY`; `lifecycle=open|active|blocked|finished|all` filters, and
  finished items are listed while their retention lasts; `store=REPOSITORY&item=WORK_ID` opens
  one item with its full scope, checkpoint, progress, references, outcome and its newest 200
  events with their writers), `audit` (the audit log, 100 per page) and `health` (start time,
  revision, schema, listeners, session counts, storage and the work maintenance sweep). With
  `fragment=1` a view returns its list alone; the page script fetches it every 5 seconds while the
  page is visible, with the page's own query. The search and recipient filter forms sit outside
  the list, so a refresh never resets them. A refresh also waits while a disclosure in the list is
  open or a form field in it has focus, and drops an answer that arrives once one is, so an
  open confirmation or edit form stays as it is.
  Views never write.
- **Sorting and paging.** Each table header with a sortable value links to `sort=COLUMN&dir=asc`
  or `dir=desc`; the current column carries `aria-sort` and its link reverses the direction.
  Columns: sessions `state` (default, ascending: active, then expired, then retired, each with
  the latest renewal first, so active sessions are on the first page however many expired
  sessions are kept), `name`, `family`, `repository` (then directory), `registered`,
  `renewed`, `expires`; messages `id` (default, descending), `to`, `seq`,
  `from`, `sent`, `delivery`, `acknowledged`; memory `repository` (default), `store`, `head`,
  `entries`, `logical`, `consumers`, `debt`; work `work` (default), `title`, `lifecycle`,
  `proposed`, `owner`, `lease`, `progress`, within each store; audit `id` (default, descending),
  `time`, `action`, `target`, `result`. Observations, bodies, resources and actions do not sort.
  An unknown column gives the default order; a column is only ever a key into a fixed list of SQL
  expressions. Every order ends with a unique tie-breaker (sessions family and ID, messages and
  audit ID, stores repository, work items work ID). Pages follow a keyset: `after` is an opaque
  cursor of the last listed row's sort values, so a page neither repeats nor skips a row when
  rows are added; a malformed cursor is 400. Next-page links keep `sort`, `dir`, `q`, `to` and `peers`.
- **Actions.** Each action is a `POST` under `/dashboard/actions/` with the request protection
  above, and answers `303` to the view it changed with a fixed `notice` code, so a reload never
  repeats it. Forms are limited to 4 KiB; the send form takes up to 3 × 65,536 + 4,096 bytes of
  URL-encoded input, and its decoded body must be 1–65,536 bytes of UTF-8.
  - `retire` (`family`, `id`, `revision`): the existing retirement; a changed revision is
    `revision_changed`, a session that is not active `session_not_active`, and the maintainer
    session `maintainer_session`.
  - `purge` (`family`, `id`, `revision`, `confirm`): marks an agent session for purge (see
    Retention). The page shows the consequences behind a Purge disclosure, and its Confirm purge
    button sends `confirm=purge`; without it the action is `confirmation_required`. An active
    session is retired in the same write. A changed revision or an existing mark is
    `revision_changed`; the maintainer session is `maintainer_session`. The sessions view shows
    a marked session with its mark time.
  - `unpurge` (`family`, `id`, `revision`): removes the mark before the sweep; the session stays
    as it is (a session that the mark retired stays retired). A changed revision or a session
    without a mark is `revision_changed`.
  - `release` (`repository`, `work_id`, `revision`, `generation`, `consumer`): the work release
    of the claim's owner, with the checkpoint `Released by the maintainer from the dashboard`;
    the work event names family and name `maintainer`. Stale values get the work refusal codes.
  - `acknowledge` (`family`, `id`, `through`) and `clear` (`family`, `id`): acknowledge any
    existing inbox, also of an expired or retired session, through a sequence or through the
    newest sequence at the request. Both only move forward and share the wake submission
    boundary, so no notice is sent for a sequence they cover. The messages they newly
    acknowledge carry `acknowledged_by: maintainer`. They delete nothing; retention deletes the
    acknowledged messages later.
  - `memory-record` (`repository`, `type`, `scope`, optional `scope_target` and `path`, `body`,
    optional `supersedes`, `key`, `deadline`): records a memory entry in an existing store as
    writer family and name `maintainer` (consumer `maintainer`), through the same path, limits,
    idempotency and capacity refusals as an agent's `record`. With `supersedes` it is an edit: a
    new entry that replaces the old one, which stays readable with its link. An edit of an entry
    that another writer replaced or revoked meanwhile is recorded with `conflicts_with` and
    answers `memory_conflict`. Each form of a rendered page carries its own idempotency key and
    a deadline one minute inside the idempotency horizon, so a resubmitted form is a duplicate.
    A repository without a store is `store_not_found`.
  - `memory-revoke` (`repository`, `seq`, `reason`, `key`, `deadline`): revokes an entry with a
    revocation of the same type and scope whose body is the reason; an empty reason is
    `reason_required`.
    The form is up to 3 × 8,192 + 4,096 bytes for `memory-record`. The audit target names the
    repository, the type or the entry replaced or revoked, and the body size, never the body.
  - `work-create` (`repository`, `title`, `criteria`, `non_goals`, optional `proposed_assignee`,
    `references` one per line, `key`, `deadline`), `work-propose` (`repository`, `work_id`,
    `revision`, `proposed_assignee`, empty to clear, `key`, `deadline`), `work-edit` (the same
    with `title`, `criteria`, `non_goals`) and `work-finish` (the same with `outcome`
    `completed` and `references`, or `withdrawn` and `reason`): the work operations as consumer
    `maintainer`, through the same revision checks, limits, idempotency and refusal codes as an
    agent's request; each work event names family and name `maintainer`. An edit of an item
    with a live claim is refused with `work_claimed`: release the claim first. A finish of a
    live claim runs on behalf of its owner, as `release` does; an unclaimed item is claimed by
    `maintainer` and finished in one transaction, with one audit record, so a refused finish
    changes nothing. The same finish form again repeats its first request under that request's
    consumer, so it replays the first result whatever the item's state is now; a form key is at
    most 250 bytes. A store that does not exist is
    `store_not_found`. Each form of a rendered page carries its own idempotency key.
  - `send` (`to`, `body`): a message from `maintainer`; the recipient's wake works as for any
    message.
  - `launch` (`family` `claude`, `codex`, `agy` or `opencode`; `directory`; optional `name`): the daemon runs
    its own executable as the launcher with `--tmux-session`, so the agent starts detached in a
    new tmux session with its configured CLI. The directory must be an existing absolute path; the
    name uses letters, digits, `_` and `-`, and defaults to `FAMILY-FOLDER-XXXX`. No other
    argument is accepted. The action never waits for the agent to register.
- **Audit log.** A request that fails the session, host, origin or CSRF check writes no record.
  After those checks, each action writes one record: time, action, target, result (`accepted`,
  `refused`, `started` while a launch runs, or `unknown`) and a fixed reason code. A record never
  holds a message body, a credential or a secret; a send records the peer name of the session
  that received it, the body size, the message ID and, for a send to an alias, `via ALIAS`. An
  accepted change and its record commit in the transaction that makes the change, in its write
  class; maintenance writes that run first in the same request (memory expiry, work
  reconciliation) never carry the record. A record that does not fit refuses the change. A
  refusal is recorded only when storage can hold it. A launch writes its record first and sets
  the result when the launcher returns; when that update cannot be written, the notice says
  `launched_unrecorded` or `launch_refused_unrecorded`, and the maintenance loop writes the
  result later without starting anything again. A daemon start sets every launch record still
  `started` to `unknown` with `daemon_restarted`. The maintenance sweep keeps 90 days and at
  most 10,000 records.

Observations are memory-only, allowlisted values: a model ID (at most 128 printable characters),
context limit and used tokens, an activity state (`busy`, `idle` or `waiting`) and a tmux socket,
pane (`%N`) and session name, each with its source and the time the source recorded it. The
daemon keeps the newest report per group for active sessions, with the time it last received a
report that confirms it: a newer report replaces the value, a report of the same content
confirms it and keeps its source time, and an older, different report changes nothing. A value
that no report confirmed within its window shows `unknown` with `observation_stale`: 2 minutes
for activity and terminal, 30 minutes for model and context. Polled sources are confirmed every
minute while they stay readable; hooks and tool calls confirm only by their next event, so an
agy session's idle state and a Claude session's status-line values go stale when no new event
comes. A session that is not active shows `unknown` with `session_expired` or
`session_retired`. A group without a value shows `no_source` when the family has none, else
`not_observed`.

Agents read these same observations without a dashboard login through `peer_status` with
`peer: NAME`, or `koinon peer-status --as FAMILY:ID NAME`. The authenticated
`POST /v1/peers/status` request takes `caller` and `peer` and returns `ok: true` with a `peer`
object: the discovery fields (`name`, held `alias`, `family`, registration `state`,
`repository`), `observed_at` in Unix milliseconds, and `model`, `context`, `activity`.
Each group has `known` and `stale_after_ms`; an unknown group has `reason` and no value fields.
A known group carries `source`, source timestamp `at`, last confirmation timestamp
`confirmed_at`, and its allowlisted value fields: model `id`; context `limit_tokens`,
`used_tokens`, `usage_available` when reported; activity `state`. Source time may be old while
confirmations remain fresh. Activity expires after 120,000 ms without confirmation; model
and context after 1,800,000 ms. Reads share the dashboard projection and a two-second budget
for provider reads. No native session ID, terminal target, wake metadata or credentials are
returned. An expired or retired peer name remains readable with unknown observations;
an unheld alias refuses with `alias_unheld`, an unknown name with `peer_not_found`.
After a daemon restart the memory-only reports are unknown until fresh reports or provider
reads arrive. Registration `active` means a current lease; it says nothing about busy/idle
activity. Message delivery and acknowledgement do not establish activity or inbox readership.

| Group | Sources |
| --- | --- |
| Model | `claude_statusline`; `codex_rollout` (the session's own rollout under `CODEX_HOME`) and `codex_mcp_meta` (`x-codex-turn-metadata.model` of a tool call); `agy_hook` (`modelName` of the Stop hook) |
| Context | `claude_statusline` (`context_window_size`, `total_input_tokens`); `codex_rollout` (`model_context_window`, `last_token_usage.input_tokens`) |
| Activity | `claude_registry` (the parent Claude process's registry record for this session, `entrypoint: cli`; `shell`, the prompt with a background shell task running, is idle); `codex_rollout` (task started and completed); `agy_hook` (idle at Stop) and `mcp_call` (an agy tool call); `opencode_status` (`GET /session/status` on a launched OpenCode server, with its password, cached 5 seconds) |
| Terminal | `tmux_env`: the MCP server's `TMUX` and `TMUX_PANE`, the pane's session name and the last terminal naming result |

One MCP server can serve several sessions, so its environment proves nothing about a session.
The daemon accepts a terminal only for a session registered with a verified launch ID or a
Claude session, whose server is the child of that Claude process; any other terminal report is
refused with `terminal_unverified`, and the view shows that reason.

For those same sessions, `koinon mcp` names the terminal after the session's published name
(the alias while the session holds one, else the peer name) after a registration or a renewal
in which that name changed. `TMUX` and `TMUX_PANE` only select the tmux server and a candidate
pane. The pane counts as the session's own only when its process is the session's host
process from the wake target (the Claude process, or the launched CLI) or an ancestor of it,
with no Claude Code, Codex, `agy` or OpenCode process between them (`pane_not_host`,
`nested_agent`). When another pane of the same tmux session holds an agent process, only this
pane is titled (`pane_titled`); otherwise the session is renamed by its ID (`renamed`), unless
it already has the name (`unchanged`) or another session has it (`name_taken`), and the name is
read back (`rename_unconfirmed`). A tmux or process-table failure (`tmux_unreadable`,
`process_table_unreadable`, `panes_unknown`) changes nothing and is tried again at the next
renewal; the other results wait for the next change of the published name. Every tmux call
has a one-second timeout, and naming never delays or fails a tool call. The terminal
observation carries the last result as `naming`. `koinon mcp` checks its
pulled sources every 5 seconds for the 8 sessions it registered last and reports a group when it
changed, or once a minute to confirm it. It reads metadata only: a rollout's first record must
name the session, at most 4 MiB are read per check, records longer than 1 MiB are discarded in
pieces across checks, and no conversation text is kept or sent.

`koinon hook claude-status [--command COMMAND]` is the Claude Code `statusLine` command. It runs
`COMMAND` through `/bin/sh -c` with the same input and returns its output and exit status, or
prints the model's display name without one, and reports the model and context of the input's
`session_id`. `koinon setup claude` makes it the `statusLine` entry, keeping the entry's other
fields and wrapping its previous command. The previous entry is saved once, before the settings
change, in `claude-statusline.json` in the Go state root. `koinon setup claude --remove-status-line`
restores it while the entry is still Koinon's, and keeps an entry the user changed. Only the
`statusLine` member changes; member order and the file mode are kept; a symbolic link, a file
not owned by the user, a file over 1 MiB or invalid JSON is refused, and a file that changes
while Koinon edits it is a `settings_conflict` that keeps the other change.

### Import of Python-era state

Schema 8 adds the `imports` table. Each Python-era source that a database imported has one row
there, with these columns:

- `source`: the database path;
- `kind`: `inbox` or `memory`;
- `digest`: the SHA-256 of the mapped rows;
- `cutoff`: the head sequence and counters at capture;
- `counts`;
- `at`: the import time.

`koinon import` and `koinon upgrade --from-python` write the records of each source in the
representation below. They read every source back in that representation before the record
commits. A source with a recorded digest is skipped. A different digest is `source_changed`.
A row that collides with an imported row is `source_conflict`. A read-back mismatch is
`verify_failed`, and a database or memory ceiling is `capacity`. The capture, the staging and
the recovery rules are in [the installation guide](docs/INSTALL.md#import).

**Inbox.** An inbox becomes a session and its messages.

- **The session.** Its family is the Python agent (`codex` or `deepseek`) and its ID is the
  thread or session ID. A legacy single-thread inbox is the Codex thread that its notifier
  checkpoint names. The session imports expired, with no repository, directory or wake target.
  Its times are the newest message time, so a capture of the same source maps to the same rows.
  Its next registration with the same family and ID gets the same inbox.
- **The peer name.** The Python peer name is kept. When two sources hold one name, the first
  source by path keeps it, and the other session gets a new name at registration (`renamed`).
- **The sequences.** `last_seq` is the inbox's AUTOINCREMENT head, and `acked_through` is its
  `ack_through`. A Python acknowledgement deleted the rows it covered, so an imported inbox
  holds only unacknowledged messages. It can have gaps where Python held control frames or
  memory pointers. Each message keeps its `seq`.
- **The body.** The body is `frame.message.content`, unchanged. When the content is a sender
  envelope whose fields rebuild it exactly, the envelope's `from` and `from-name` give the
  sender. Otherwise the sender is the frame's `from`.
- **The sender family.** It is `legacy`: a Python-era sender is identified only by what it
  asserted, and no session has that family.
- **Times.** `created_at` is `received` in milliseconds.
- **The delivery state.** It is `notified` when the delivery ledger recorded a delivered notice,
  or when the notifier checkpoint covers the sequence. It is `uncertain` when the outcome was
  unknown, and `waiting` otherwise. An unacknowledged `waiting` message is woken after its
  session registers again.

**Memory store.** A store becomes the memory and work rows of its repository, keyed by its
Python `store_id`.

- **The store row.** `head`, `floor`, `store_id`, `work_id_counter` and `claim_generation` keep
  their source values in `memory_stores` (`work_counter` and `claim_counter`). They are never
  rebuilt from the kept records.
- **Entries.** Entries keep every column. `writer_family` is `legacy`, and `writer_name` is the
  reported `author`, otherwise the consumer. A missing consumer becomes the empty string.
- **Snapshots.** A pending snapshot's `acked = NULL` becomes `acked = 0`. Its consumer continues
  and acknowledges it as before.
- **Replays.** A note replay key `repository NUL consumer NUL key` is split into its columns. A
  work replay key is a digest of the repository, the consumer and the key, so it is kept with an
  empty consumer. Both get the fingerprint scheme `py1`. They hold their place in the
  idempotency window until their deadline, and no Go request matches them.
- **Work.** Work items, scope revisions, claim bundles, resources and events map column for
  column. Leases and consumer strings are unchanged.

**Not imported.** The report counts these by kind as skipped:

- control frames and memory pointers;
- the notifier journal, migration and health records;
- the delivery ledger's internals;
- memory bindings;
- alias leases, which registration creates again;
- the full-text index;
- supervisor and service records, locks and sockets.

## Claude socket compatibility

The peer transport below was observed in Claude Code 2.1.267 on Linux and 2.1.268 on macOS.
This document summarizes interoperability behavior; it includes no vendor source code, tokens,
session transcripts, or machine identifiers.

The Claude wake adapter uses the same-user socket protocol below. This is a provider
transport, not a second Koinon coordination service. Socket paths remain literal so that
registry key filenames continue to match their unresolved paths.

## Claude socket framing

```json
{
  "msgV": 1,
  "msg_id": "12345678-1234-4123-8123-123456789abc",
  "type": "user",
  "priority": "next",
  "from": "uds:/tmp/cc-socks/12345.sock",
  "message": {"role": "user", "content": "Hello"}
}
```

The content is nonempty text. Priorities are `now`, `next`, and `later`. `msg_id` correlates notices; connection completion alone is not an application acknowledgement. An optional `session_id` refers to the recipient's session, so the wake adapter omits it.

### Sender envelope

A Claude receiver shows a sender name only from an envelope inside `content`, and the sending
session writes that envelope itself; the frame's `from` field is not shown. Observed in Claude
Code 2.1.283, the envelope is:

```text
<cross-session-message from="uds:/tmp/cc-socks/12345.sock" from-name="codex-sample-86">
BODY
</cross-session-message>
```

The Go Claude wake adapter builds an envelope with `from-name="koinon"` around its
content-free notice. `from` names the daemon's private reply listener; it accepts no
commands. The outer frame carries `type: "user"`, `priority: "next"` and a fresh `msg_id`.
Those are Claude protocol fields, not maintainer instructions. The notice identifies only
the native inbox and sequence range. No message body or memory content is transmitted.
The receiving socket's UID/PID is verified against the selected native Claude session.
Sender-provided envelope labels alone are not authentication or permission. See
`internal/core/wake_providers.go` and its synthetic receiver tests for the maintained wire contract.
