# Koinon protocols

## Go daemon core

The Go daemon is currently a development runtime alongside Python. Its HTTP API uses a
private user secret from the selected Go state root, passed in `Authorization: Bearer SECRET`.
No API endpoint is anonymous. Each listener must be a literal loopback address; foreign
browser origins and non-loopback Host headers are refused. The API accepts no browser cookies.
The secret and state files are user-owned regular files, mode 0600; the state root is private.
Never put a secret, runtime session identifier or response body in a repository.

| Route | Operation |
| --- | --- |
| `GET /v1/status` | Daemon readiness, listener addresses, schema and counts of active/expired/retired sessions. |
| `GET /v1/sessions` | Up to 1,000 records ordered by family and ID; `truncated` reports more records. |
| `POST /v1/sessions/register` | Register or reactivate one session identified by `(family, id)`. |
| `POST /v1/sessions/renew` | Renew an active session's expiry. |
| `POST /v1/sessions/retire` | Mark an active session retired, retaining its record. |
| `POST /v1/peers` | Each session's `name`, held `alias`, `family`, `state` and `repository`, for an active caller; never a wake target, directory or session ID. |
| `POST /v1/messages/send` | Store one message for the session that a peer name or alias names. |
| `POST /v1/inbox/read` | Read the caller's own inbox after a sequence number. |
| `POST /v1/inbox/ack` | Acknowledge the caller's own inbox through a sequence number. |
| `POST /v1/messages/outcome` | Read the delivery and acknowledgement state of a message the caller sent. |
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
can omit `repository`; its stored repository is empty. Repository-dependent memory and work
operations arrive in later chunks. Wake targets are inert metadata until the wake adapter chunk.

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
  `created_at`, `delivery_state`, `delivery_reason` and `acknowledged`.
- Acknowledge takes `caller` and `through`. Acknowledgement only moves forward: an earlier
  sequence leaves `acked_through` unchanged, and a sequence beyond the last fails with
  `ack_beyond_last` (409). A message is acknowledged when its `seq` is at most `acked_through`.
- Outcome takes `caller` and `message_id` and returns `id`, `recipient`, `seq`, `delivery_state`,
  `delivery_reason`, `updated_at` and `acknowledged` (#82).

Each message has exactly one delivery state: `waiting`, `notified`, `uncertain`, or `failed` with
a reason. This version stores every message as `waiting`; wake adapters, which change the
state, are a later sprint chunk. Inboxes are independent of wake notices.

Replies include `ok`. Success returns `session` or `sessions`; failures report a fixed `code`:
`unauthorized` (401), `foreign_origin` (403), `invalid_request` (400), `session_not_found` (404),
`session_conflict` (409), the message codes above, or `storage_error` (500). Mutations commit
before replying. A lost reply does not prove rollback; read the retained record before
retrying. Wake and memory commands are later sprint chunks. The Python protocol below continues
to apply to Python.

Launch creation takes `family` (`codex`, `agy`, `opencode`), absolute `directory` and `cli`,
and positive `host_pid`. OpenCode also requires `address` (literal loopback with a nonzero
port) and `password` (64 hex characters). Other families refuse those credential fields.
The response returns `ok` and `launch_id`. The credential stays only in private launch storage;
session responses contain a `launch_id` reference and target metadata with no password field.
Launch records survive a
restart, and can bind successive native session identities from one CLI process, such as a
Codex context reset. They are inert state until the agent registers and later wake adapters
use its target. All launch calls use the existing bearer authentication, request limits and
origin checks; a launch ID is an association key, not a replacement authentication secret.

### Memory stores

The daemon holds one memory store per Git common directory: the store of a request is the
repository that the daemon recorded for the calling session, so every worktree and
subdirectory of a repository share one store. A session without a repository gets
`repo_unresolved`. Each request names its `caller`, which must be active, and an optional
`consumer` (1–128 characters, a stable cursor name that defaults to the caller's peer name; it
names a cursor, never an identity). Provenance is the caller's family and peer name
(`writer_family`, `writer_name`).

The operations keep the behaviour of the memory control protocol below: `record` is `note`
with the same fields, types, scopes, limits, supersession, revocation, `conflicts_with`,
idempotency key and deadline; `sync`, `ack`, `recall` and `status` take the same fields and
return the same results and codes, as `{"ok": true, "result": ...}`. Every result is record
format 2, and `record_format` is not a request field; unknown fields are refused. Recall is the
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

### MCP server

`koinon mcp` is a stdio MCP server: newline-delimited JSON-RPC 2.0 with `initialize` (protocol
versions `2025-06-18`, `2025-03-26` and `2024-11-05`; another requested version gets
`2025-06-18`), `ping`, `tools/list` and `tools/call`. Other methods get error -32601; a message
longer than 1 MiB gets -32700. Its tools are `peers`, `send` (`to`, `body`), `inbox` (`after`,
`limit`), `ack` (`through`) and `delivery` (`message_id`), and `memory_status`, `memory_sync`,
`memory_ack`, `memory_record` and `memory_recall` with the fields of the memory routes, and
`work_create`, `work_get`, `work_list`, `work_propose`, `work_edit`, `work_start`, `work_update`,
`work_release`, `work_finish` and `claim_renew` with the fields of the work routes, which call
the routes above. A tool
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
agent passes `KOINON_LAUNCH_ID` as `launch_id`. `KOINON_STATE_DIR` and `KOINON_DAEMON_ADDRESS`
select the state root and address.

### Dashboard and session observations

The daemon serves a read-only dashboard under `/dashboard/` on its loopback listeners. Pages are
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
  `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`
  and `Cross-Origin-Opener-Policy: same-origin`. The dashboard cookie never authorizes `/v1/`, and
  the bearer secret never authorizes `/dashboard/`.
- **Views.** `sessions` (100 per page in family and ID order, `after=FAMILY:ID` for the next page;
  names, family, repository and working directory, state and times, the observations below with
  their source and time, and the live claims held under the session key `FAMILY:ID`), `messages` (every inbox, newest first
  by message ID, 50 per page and about 1 MiB of bodies, `before=ID` for older pages, `to=NAME` for
  one recipient; delivery state and reason, acknowledgement), `memory` (per store: head, floor,
  entries and logical bytes against their ceilings, consumers, work debt and maintenance), `work`
  (per store, every unfinished item with its claim, resources, lease and progress state computed
  at read time) and `health` (start time, revision, schema, listeners, session counts, storage and
  the work maintenance sweep). Stores page by repository, 50 at a time. With `fragment=1` a view
  returns its list alone; the page script fetches it every 5 seconds while the page is visible.
  Views never write. Actions and their audit log are not part of this chunk.

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

| Group | Sources |
| --- | --- |
| Model | `claude_statusline`; `codex_rollout` (the session's own rollout under `CODEX_HOME`) and `codex_mcp_meta` (`x-codex-turn-metadata.model` of a tool call); `agy_hook` (`modelName` of the Stop hook) |
| Context | `claude_statusline` (`context_window_size`, `total_input_tokens`); `codex_rollout` (`model_context_window`, `last_token_usage.input_tokens`) |
| Activity | `claude_registry` (the parent Claude process's registry record for this session, `entrypoint: cli`); `codex_rollout` (task started and completed); `agy_hook` (idle at Stop) and `mcp_call` (an agy tool call); `opencode_status` (`GET /session/status` on a launched OpenCode server, with its password, cached 5 seconds) |
| Terminal | `tmux_env`: the MCP server's `TMUX` and `TMUX_PANE` and the pane's session name |

One MCP server can serve several sessions, so its environment proves nothing about a session.
The daemon accepts a terminal only for a session registered with a verified launch ID or a
Claude session, whose server is the child of that Claude process; any other terminal report is
refused with `terminal_unverified`, and the view shows that reason. `koinon mcp` checks its
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

The [work command interface](docs/WORK-ITEMS-COMMANDS.md) provides schema-5
work records, advisory claims and immutable events. Startup creates schema 5 or
atomically migrates schema 3/4 after validating the complete catalog. Transport remains
protocol 1; hello/status advertise `work_items_v1` and `memory_record_format_2`.
Every sync and ack requires integer `record_format: 2`, refused before maintenance or
cursor mutation if missing or incompatible. New readers retain legacy snapshot shapes.
[Maintenance](docs/WORK-ITEMS-MAINTENANCE.md) bounds reclamation and reports timestamped
diagnostics. [Policy and guidance](docs/WORK-ITEMS-POLICY.md) remain explicit opt-in.
Follow the [runtime upgrade procedure](docs/WORK-ITEMS-UPGRADE.md) for existing services.

The peer transport below was observed in Claude Code 2.1.267 on Linux and 2.1.268 on macOS. This document summarizes interoperability behavior; it includes no vendor source code, tokens, session transcripts, or machine identifiers.

## Platform differences

The wire protocol is identical on both platforms. The local facts around it are not, and each is handled in `koinon/platform_support.py`:

- **Peer identity.** Linux returns pid, uid and gid from one `SO_PEERCRED` getsockopt. macOS has no such option: `getpeereid` returns uid and gid only, and the peer pid comes from a separate `LOCAL_PEERPID` socket option. Both are required, and a failure to read them rejects the connection.
- **Process start marker.** Linux reads field 22 of `/proc/<pid>/stat`, a tick count. macOS reports an asctime string, and Claude writes it in **UTC**, so a reader must force `TZ=UTC` rather than inherit the local zone; a local-time port is six hours off in a US mountain zone.
- **Peer domain.** Linux peers publish `linux:<machine-id>:<pid-namespace>`; macOS peers publish the literal `darwin`.
- **Socket directory.** macOS resolves `/tmp` to `/private/tmp`. The registry's `messagingSocketPath` and the `uds:` address stay **unresolved**, because the peer key filename is derived from the literal path; resolving it silently breaks authentication.
- **Address length.** `sockaddr_un.sun_path` is 108 bytes on Linux and 104 on macOS, including the terminating NUL. A per-session state directory can exceed it, and an over-long bind fails with `AF_UNIX path too long`, so the control socket falls back to a short path in the peer socket directory.

## Transport

AF_UNIX stream sockets carrying UTF-8 newline-delimited JSON objects. This is not HTTP or JSON-RPC. Claude also accepts a final nonempty JSON fragment at EOF. Replies use a separate connection to the sender's listening socket.

## User message

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

The content is nonempty text. Priorities are `now`, `next`, and `later`. `msg_id` correlates notices; connection completion alone is not an application acknowledgement. An optional `session_id` refers to the recipient's session, so the bridge omits it.

### Sender envelope

A Claude receiver shows a sender name only from an envelope inside `content`, and the sending
session writes that envelope itself; the frame's `from` field is not shown. Observed in Claude
Code 2.1.283, the envelope is:

```text
<cross-session-message from="uds:/tmp/cc-socks/12345.sock" from-name="codex-sample-86">
BODY
</cross-session-message>
```

`Bridge.send` wraps every body this way, so a Claude receiver names a Codex or DeepSeek sender.
`from` is the bridge's own address. `from-name` is the per-thread name that the bridge's notifier
published in the registry; a holder of its checkout's alias is labelled `name (alias)`. Before
the notifier registers, the envelope has `from` only. The receiver reads the fields from the first
tag and accepts the envelope only when rebuilding it from its parts gives the same text, so a
body cannot change them. The fields are asserted by the sender, as in every Claude envelope;
only the receiver's kernel-checked peer pid is verified. The envelope is internal to Claude
Code: if it changes, a receiver shows the raw text, which still carries the name, and
`tests/test_bridge.py` pins the observed form. The outgoing ledger keeps the bare body.

Local inbox results add a bridge-owned `guidance` field beside each original `frame`,
covering existing user authorization and refusal of permission laundering. This does
not change stored envelopes or the wire format. Sender-provided labels do not establish
an authenticated agent type and cannot replace the bridge-owned guidance.

## Participant guidance

`koinon/participant_instructions.py` manages a small section for both Codex and DeepSeek
participants, with separate Koinon markers. The section is the same in every release: it
names the installed `session.py guide --agent <family>`, when to run it, and the authority
limits. The operating instructions come from `koinon/guidance.py` through that command, so an
upgrade replaces them with the runtime. `guide` writes nothing, needs no registration or
service, and reports each live observation as `observed`, `unavailable` or `unknown`; its
recipes are argument arrays that it never runs. The old `koinon/codex_instructions.py` import
remains a shim. Updates and removal recognize legacy markers and retain the legacy lock inodes
to exclude old updaters. Koinon supplies the peer-input guidance in the guide's `messages`
topic, each inbox result, and each queued notice. This does not depend on the participant
runtime adding its own peer framing.

The repository's `CLAUDE.md` includes `AGENTS.md` for agents working on Koinon itself;
these are separate from guidance installed into a participant's configuration.
Koinon installs no Claude instructions and adds no guidance field to outbound peer
frames. In observed Claude Code sessions, Claude's own runtime wraps incoming peer
messages with its peer-input framing. That is an observed internal behaviour, not a
compatibility guarantee or proof of equivalent safeguards. Koinon does not verify that
receiver-side framing, so a change to it would require a fresh compatibility review.

## Endpoint roles

Messaging validation rejects `control.sock` and `*-control.sock`, including short
control paths placed in the peer socket directory. These endpoints do not appear in
`bridge.py peers` and cannot be used by `bridge.py send`. A control client derives its
endpoint from an explicitly configured state root and checks the directory and socket
ownership and modes without creating directories. Control replies use the same bounded
JSON framing as peer messages; a missing reply does not prove that a mutation rolled back.
An endpoint that fails its permission or metadata checks reports `unsafe_service_endpoint`;
it is not treated as an absent memory service or a reason to start a replacement.
A bridge client whose connect call the kernel refuses with `EPERM`, as an agent sandbox does,
reports `sandboxed`: nothing was sent, and the bridge is not shown to be down. `bridge.py peers`
does not judge a registry record from another pid namespace of this machine, because its pid
names no process there; when such records hide every peer, `peers` reports `sandboxed`
instead of an empty list, and `send` by name reports it instead of `peer_not_found`. The
guide marks the Codex and DeepSeek bridge, status and memory recipes `needs_approval`.
Memory reuse likewise distinguishes `service_busy`, `service_unresponsive`,
`service_unavailable`, `service_refused` and `invalid_service_response` from an absent
listener. A connected service with another identity is `foreign_service`. These results
refuse a replacement start; only a missing or refused connection takes the absent-listener
path, which still requires the existing ownership checks before binding.
The memory CLI prints structured errors for these refusals. Busy/unresponsive/unavailable
services and capacity refusals exit 75 (retryable). Identity, ownership, permissions,
configuration and invalid-handshake refusals exit 78 (operator correction required).
Blocked storage also exits 78 because it requires an explicit `recover` operation.
Stopping and bounded idempotency/snapshot capacity exit 75. All locally raised recovery
codes have an explicit class checked by the tests. Internal software errors use exit 70. Locally generated error replies pass through a
checked classification boundary; an unclassified local outcome becomes `internal_error`.
Other request errors, ambiguous `no_reply` and unclassified wire errors retain exit 1. A lost stop reply remains ambiguous: stop observes
the selected generation before reporting its exit.

These path checks do not authenticate a service role. Memory-service reuse additionally
requires agreement between the connected kernel PID, the hello response and the owner
record, with a present matching process-start marker and a valid current generation.
Messaging addresses remain literal for peer-key lookup; service roots are filesystem
configuration and do not use peer tokens.

## Discovery

Claude scans process records in its configured `sessions` directory. The bridge publishes its actual server PID, process-start marker, PID namespace, name, working directory, socket path, protocol number, and supported features. It retains the compatibility entrypoint `codex-peer-bridge` after the project rename to Koinon.

**The registry's `messagingSocketPath` contains a bare filesystem path.** Only wire-message addresses use the `uds:` prefix. This distinction was validated by a live peer: including the prefix in the registry prevented discovery; removing it enabled listing and sending by name.

Only `reply_across_default_dirs` is advertised. Unsupported features such as idle notification and artifact yield are not advertised.

## Peer authentication

Claude may publish a peer key named `<pid>.<sha256(absolute-socket-path)>.key`. Its `peerToken` can be sent as the first frame:

```json
{"type":"auth","token":"AUTHORIZED_PEER_TOKEN"}
```

The client reads the key for the kernel-verified server PID and socket path without logging the token. Linux's inspected default permits same-user peer connections without a token, and macOS behaves the same way; this bridge uses that policy for inbound traffic. It does not read child tokens or bypass a recipient's required authentication.

## Controls

Observed controls include delivery statuses, rename, idle notices, and artifact coordination. The bridge stores control objects without performing their actions and does not notify Codex about them. It neither implements nor advertises their specialized semantics.

## Database execution

Bridge and memory database work runs on one owning thread per service. Initialization,
queries, mutations and close use that thread with SQLite thread checks enabled. Each
worker accepts at most 16 queued ordinary jobs, two queued status jobs, and one running
job. Status has priority over queued ordinary work, but cannot interrupt a transaction.
Stop is validated and handled on the event loop; shutdown settles accepted database work.

Control connections have eight pending frame slots with a separate two-second read
deadline. A full unclassified pool can refuse any operation, including status or stop;
no operation identity is known before its frame arrives. These slots are released after
parsing or expiry. Established ordinary requests do not occupy them. After parsing, each service admits
16 ordinary handlers and two separate status/stop handlers. Bridge peer connections use
the same ordinary allowance. Excess control requests return `capacity`; excess peer
connections close. These bounds do not promise a deadline for disk operations.

A timeout, disconnect or cancellation does not cancel an accepted database mutation and
is not proof of rollback. Use the existing memory idempotency contract for uncertain note
replies. Services stop accepting connections, drain handlers, then close the worker after
all accepted jobs settle. Socket waits cannot keep a database transaction open.

Unwrapped database failures return `storage_error`; programming failures return
`internal_error`, including programming errors wrapped by a storage recovery exception.
Expected storage recovery errors retain codes such as `write_failed` and `storage_blocked`
and also record an observed storage fault.
Status waits at most one second for a priority database read, then returns known process
identity and a lock-protected worker snapshot without waiting for that read to finish.
`database_status` is `ready`, `busy`, `capacity`, `closing` or an error class. When the read
cannot complete, bridge `inbox_count` is null; memory omits database-derived fields such
as `head` and sets `healthy` false. Unknown values are never replaced with zero.

`database_worker` reports the running flag, both queue counts and the closing flag.
`database_observed_fault` is null until a storage or programming failure is observed,
then records the last error class until restart. This is historical evidence; a
successful unrelated query does not clear it or prove recovery. Memory also sets
`healthy` false after an observed worker fault. Invalid requests and capacity refusals
do not set a historical fault. These fields are not a complete database integrity check
or the notifier's separate delivery-health record. A status fallback requires a parsed
request; it cannot bypass a full unclassified connection pool.

### Generation-bound session shutdown

Notifier private status also reports `bridge_generation`, the bridge instance it
observed. Session readiness joins this with both directly owned child identities;
a notifier process alone is insufficient evidence of the selected pair.

Bridge and notifier private status replies advertise
`control_capabilities: ["generation_bound_stop"]` independently of database health.
A supervisor must observe this capability before sending
`{"op":"stop-generation","protocol":1,"generation":GENERATION}` to either private control endpoint. The
32-character lowercase hexadecimal generation must equal that process's current
runtime generation; malformed requests return `invalid_request`, and stale targets
return `not_this_instance`, before stopping. Accepted replies contain
`{"stopping":true,"generation":GENERATION,"protocol":1}` in `result`. Missing
capability is not permission to fall back to an unguarded stop. The client must bind
capability and generation from the same status reply to the captured child PID and
kernel peer PID, with the captured process-start marker checked before each exchange.
The generation comparison at the receiving process remains the in-band target guard;
these process observations are not an atomic lock. Old bridge versions ignore extra fields on `stop`; therefore a
new field on that operation would silently lose the guard. The distinct operation
also protects against an old replacement appearing between status and stop: it
rejects the unknown operation. The shared client reports `guarded_stop_unsupported`
when capability is absent and `guarded_stop_unconfirmed` for an ambiguous reply.
Neither condition permits an unguarded retry. An accepted reply
means shutdown was requested, not that the process has exited.

Existing explicit operator requests containing only `{"op":"stop"}` remain
supported. This control protocol does not execute stored peer messages. The staged
native session runner will capture both child generations before using guarded stops;
this protocol addition alone does not enable native session activation.

## Memory control protocol

This section describes a protocol **this project defines**, unlike the rest of this document,
which records behaviour observed in another implementation. The memory service at `memory.py`
listens on its own private control socket and carries no peer traffic.

Transport is an AF_UNIX stream socket carrying one UTF-8 JSON request line and one JSON response
line, then close. Same-UID kernel peer credentials are the authentication policy, as for the
bridge's control socket. No token is published or accepted.

```json
{"op": "sync", "consumer": "session-a", "page_token": 0}
```

A response is `{"ok": true, "result": ...}` or `{"ok": false, "code": "...", "error": "..."}`.
The `code` names a recovery path and is the field a caller should branch on: `snapshot_expired`
and `stale_page_token` require restarting `sync`; `snapshot_incomplete` requires paging to the
end before acknowledging; `consumer_retired` requires a new consumer key; `capacity`,
`entry_too_large`, `idempotency_conflict`, `retry_deadline_expired`, `not_issued`,
`foreign_snapshot`, `snapshot_open` and `not_bootstrapped` describe a refused request that changed
nothing. There is no conflict code for competing revisions: a second replacement of the same entry
is a successful write whose result carries `conflicts_with`, naming the replacement it competes
with, and both remain live.

A `note` carrying `key` must also carry `deadline`, an absolute epoch second fixed before the first
send and repeated on every retry. Within it a repeat returns the original sequence with
`duplicate` true; past it the request is refused with `retry_deadline_expired` rather than appended,
because the service cannot tell whether the first attempt landed. The result echoes `deadline` and
reports `idempotency_horizon`, the longest deadline the service will accept.

A `sync` returns either `kind: snapshot` with `snapshot_id`, `entries`, `page_token`, `total` and
`more`, or `kind: delta` with `entries`, `cursor`, `next_cursor`, `head` and `more`. Continuing a
snapshot requires both `snapshot_id` and `page_token`. An `ack` carries `snapshot_id` once every
page has been issued, or `through` for a delta. `recall` and `status` return `more` with
`next_before` and `next_after` respectively, null when nothing remains.

Operations are `hello`, `note`, `sync`, `ack`, `recall`, `status` and `stop`. `hello` is the
reuse handshake and reports the service name, repository key, protocol and schema versions,
generation, process ID, health and search capability. `sync` returns either a `snapshot` page or
a `delta` batch and never advances a cursor; only `ack` does. Pages are bounded by encoded bytes
rather than by a row count, because a row limit multiplied by the maximum body size exceeds one
frame.

Sequence numbers come from a durable head that only ever advances. Reclaiming entries moves a
retained-history floor rather than the head, and a consumer below the floor is returned to a
fresh snapshot rather than handed a gap. The protocol field remains `floor`. This is a history
availability boundary; it does not imply summarization. A snapshot contains stored records,
not a generated summary.

## Limitations

Admission rules, deduplication, rate limits, and loop checks on Claude's side may reject a transported message. Return-path validation also depends on socket ownership and kernel process identity. A bridge that sends from a different process than its advertised listener can fail these checks; all outbound peer connections here originate in the listener process.

### Bridge startup and control failures

The bridge reserves its control and messaging socket paths before opening the inbox
database. It listens only after database initialization commits. An existing path
refuses startup without opening the inbox; `serve` reports `endpoint_unavailable`
and exits 78. No status probe or new advisory lock substitutes for this reservation.
Shutdown keeps the reservation until accepted database operations finish.

Control timeouts, lost replies, transport failures and invalid replies produce
structured CLI errors and exit 1. A failed reply does not establish whether a
mutation committed. The CLI does not automatically repeat that mutation.

On Linux, installed systemd bridge and session services do not restart on exit 70
(internal software error) or 78 (configuration refusal).
On macOS, the manual process exits and must be started again after correction; see
[macOS setup](docs/INSTALL.md#macos). For a leftover socket, follow [recovery from a killed instance](docs/INSTALL.md#recovering-from-a-killed-instance).
Remove a socket only after verifying that its owner is dead. Unsafe startup
directories also produce a structured ownership refusal with exit 78.

## Inbox schema 2 and journal activation

The bridge migrates its inbox in one explicit `BEGIN IMMEDIATE` transaction after
reserving both endpoints and before listening. Existing rows and AUTOINCREMENT
allocation are preserved. Rows gain `kind` (initially `peer`) and nullable `binding`.
A fixed-key `inbox_meta` table stores JSON values for `schema`, `ack_through`, and
`journal_activation`. Current initial values are 3, 0 and null. Schema 2 introduced
these fields; schema 3 adds binding observations as described below. No historical acknowledgement
is inferred. The inbox keeps its journal mode; migration does not enable WAL.

`ack` deletes rows and updates the monotone `ack_through` value in the same explicit
write transaction. The requested position is clamped to the allocated inbox head
read from `sqlite_sequence` within that transaction. A never-used inbox has head
zero. This prevents a large acknowledgement from covering future arrivals. Neither
peer receipt nor notification delivery acknowledges the inbox or a memory consumer.

Status includes a per-process 32-hex `generation`, `inbox_schema`, `ack_through`,
`journal_activation`, and `capabilities`. Implemented capabilities are
`inbox_ack_watermark`, `notification_journal_activation`, `inbox_subscription` and
`memory_binding`. Their advertisement
requires a successful read of validated committed metadata. Unavailable or corrupt
metadata produces database diagnostics, null database fields and no capabilities;
it is never substituted with legacy defaults. Incompatible startup metadata exits
78 as `incompatible_inbox`; other SQLite storage failures exit 78 as `storage_error`.
SQLite programming errors exit 70 as `internal_error`.

The control operation `activate-notification-journal` accepts `target_digest`
(64 lowercase hex characters) and `nonce` (32 lowercase hex characters). It commits
that pair before replying. Repeating the pair is idempotent. A different target is
always refused, and ordinary activation cannot replace a nonce.
`rebuild-notification-journal-activation` additionally requires
`expected_previous_nonce` and `accept_history_loss: true`; it replaces only the
matching target's expected nonce. Repeating the resulting pair is safe after a lost
reply. This is the bridge-side evidence operation, not a complete notifier rebuild.
The notifier journal and its maintenance command are not yet implemented.

The `memory_binding` table stores explicit bindings. Repository and state paths
are absolute and at most 4096 UTF-8 bytes; SQL constraints enforce these bounds.
Existing ordinary notification readers can still read the original inbox columns.
New notifier binding integration, capability negotiation and journal migration
remain pending; binding controls alone do not activate them.

CLI forms use the same operation names with `--target-digest` and `--nonce`.
Explicit activation replacement also requires `--expected-previous-nonce` and
`--accept-history-loss`. These controls are operator commands; stored peer controls
remain inert data and cannot invoke them.


## Change subscriptions

The bridge control socket accepts `{"op":"subscribe-inbox","protocol":1,
"generation":GENERATION}`. The memory control socket accepts `{"op":"subscribe",
"protocol":1,"generation":GENERATION,"repo":REPO_KEY,"consumer":CONSUMER_KEY}`.
Use the generation from that service's current status (or memory hello), and an
explicit service directory. Memory hello/status advertise `memory_subscription`.
Consumer keys are asserted provenance, not authorization; the service validates the
repository and generation. Binding-derived consumer keys remain a later component.

The first reply uses `ok/result` with `protocol`, `generation` and a random 32-hex
`subscription` identifier. Following frames contain only those three fields and
`event:"changed"`. No message body, sequence number or receipt is included. The
client sends no further frames. End of input closes the subscription.

Each service has at most 32 reserved subscription slots, including handshakes, and
at most eight handshakes awaiting a reply. These do not consume ordinary request or
reserved status/stop slots after request classification. The initial request read
has the existing two-second deadline; handshake replies and each subsequent frame
have a five-second write deadline. One queued hint coalesces further changes while
another frame may be in flight. A slow receiver is disconnected without blocking
other subscribers or a database transaction. There is no total connection lifetime.
An idle connection sends the same content-free hint after 30 seconds.

Database owners publish only after commit. Memory publishes when its durable head
changes; bridge insertion and acknowledgement publish after their transactions.
A hint is not durable evidence. The shared `subscriptions.watch_changes` helper
installs a subscription before rechecking durable state, repeats that sequence on
reconnect, and independently rescans every two seconds. Reconnect delays are 1, 2,
4, 8, 16, then 30 seconds. Its caller must verify service identity on each connection
and reconcile durable state. The notifier uses this helper for inbox delivery and
bound memory refresh. Memory contents still require explicit reads. Automatic memory
notices contain only a sync pointer. These are local service subscriptions, not
peer-bus subscriptions. Explicit binding and refresh controls are described below.


## Memory bindings and pointers

These bridge controls require explicit operator action. Session registration does
not bind memory automatically. `bind-memory` accepts only `repo_path` and
`memory_state_dir`; the CLI uses `--repo-path` and `--memory-state-dir`. Paths must
exist. The bridge derives the canonical Git common directory and repository key,
resolves the memory state root, and hashes the compact JSON array of repository key
and resolved state root with SHA-256 for the binding key. At most 16 bindings exist.
Each creation also assigns a durable random 32-hex `binding_instance`. An idempotent
bind preserves it; unbind followed by rebind changes it even when the key is the same.
Internal pointer rows retain this instance so later journal work can distinguish an
obsolete prior binding from unexpected missing data. The pointer frame stays binding-only.

Before binding or refresh, the bridge verifies the memory service name, protocol,
repository, recorded owner, kernel PID, process-start marker and generation.
Memory schema 5 is required; the durable 32-hex `store_id` is preserved from schema 4. An older live memory
service yields `memory_upgrade_required`; restart it with the new runtime. Missing
memory yields `memory_unavailable`; incompatible identity or unhealthy storage is
refused. Binding refusals include `recovery:"retry"` for transient failures or
`recovery:"operator_action"` for conditions such as an old runtime. These are
operation replies, not process exits. Expiry of the bridge request deadline
returns `binding_timeout`, `recovery:"retry"`, and `outcome:"unknown"`. A timeout
is not proof of rollback; reread durable state before retrying.
Binding operations retain ordinary admission slots but use a 19-second deadline:
three seconds for Git, five for hello, five for process identity, two for status,
two one-second socket cleanup allowances and two seconds of margin. The binding
CLI allows 23 seconds, including request-header, response and cleanup margin.
These values are derived from the inner policies; they are not hard disk-latency
bounds. Other ordinary bridge operations retain their six-second deadline.
Process identity probes run outside the event loop, with at most two accepted
probes per process. Cancellation does not release a probe slot before completion.
The macOS process query has a five-second subprocess timeout. Probe saturation
returns a retryable service-busy refusal and leaves status/stop admission intact. Automatic integration must not repeatedly
refresh an unchanged service that requires an operator action. These failures do
not change a binding observation or pointer. The bridge's
`memory_binding` capability describes implemented controls, not the readiness of
any optional memory service. Each service must independently pass verification.

`refresh-memory BINDING` accepts no caller-supplied head or message. It reads the
verified memory store ID and head outside the inbox transaction. In one transaction,
it compares that exact observation with the saved one, deletes the previous pointer,
inserts a new pointer with a new inbox sequence, and saves the observation. A concurrent
refresh that changed the saved observation causes `binding_observation_changed`;
a caller must reread rather than apply a stale observation. A recreated binding
instead returns `binding_instance_changed`, also requiring a fresh lookup. Identical observations
do nothing, even after inbox acknowledgement or bridge restart. The first refresh
also creates a pointer for an empty store so a consumer can bootstrap explicitly.

Pointer frames contain only `type:"memory-pointer"` and `binding`. They carry no
memory body, store ID, head or sequence range. Ordinary peer input cannot mint this
row kind. Inbox and CLI projections label them `kind:"memory-pointer"`, include
`source_service_pid` instead of `peer_pid`, and carry separate inert pointer
guidance. Ordinary frame fields cannot claim that internal provenance. Up to 16 pointer rows have their own allowance; the 1,000 ordinary-row
allowance is unchanged. `unbind-memory BINDING` atomically removes the binding and
its pointer as obsolete, without advancing inbox or memory acknowledgements.

`memory-bindings [--after BINDING]` returns a bounded page with `bindings`, `more`
and `next_after`. Each binding exposes its paths, repository key, binding instance, observed store ID
and head, and persistent `anomalies` flags: bit 1 means store replacement; bit 2
means head regression within the same store ID. Both cause a replacement pointer;
`ack-binding-health BINDING` clears these flags without deleting a pointer,
changing the observed head, or acknowledging either inbox or memory. A clone restored with identical store ID
and head is indistinguishable here; content verification is outside this mechanism.
The binding instance is intentional diagnostic metadata on this listing; it is
not part of a pointer frame.
`service_state` is `verified`, `refused`, or `unknown`, with a classified
`service_reason` on refusal. It is a bounded process-local observation, expires to
unknown after 30 seconds, and starts unknown after restart. Listing does not contact
memory or infer current health from a retained pointer. An unpageable single record
fails explicitly as `binding_row_too_large` rather than returning an empty loop.

No binding operation advances memory consumer cursors. The saved observation means
only that the bridge issued a pointer. The notifier owns per-binding subscriptions
and finite recovery checks. Each check refreshes the verified service observation.
An operator-action refusal suspends repeated remote work until owner metadata changes
or an explicit successful refresh changes the observed service state.


### Private control path aliases

Private service control endpoints use the resolved state directory for both the
socket-length decision and the fallback digest. All spellings of one state
therefore select one new endpoint. This rule does not apply to peer messaging
addresses or registry socket paths, which remain literal for key lookup.

A client can still use an existing legacy direct `control.sock` through the
original configured state path. A memory client can also use the old spelling
from its owner record, but only when it resolves to that state's direct socket.
The deterministic legacy hashed endpoint is also retained as a client route,
including when a long alias had selected it for a short canonical root. The normal
private-directory, socket-mode, kernel-PID and service-identity checks still apply. Two distinct old/new endpoints cause a refusal. New bridge and
memory startup refuses a retained distinct legacy endpoint before database startup;
it never removes that endpoint or starts a second listener beside it.


## Notifier controls and delivery health

The notifier owns a private control endpoint under `<bridge-state>/notifier`.
Same-user credentials and canonical private endpoint rules apply. Requests are
single JSON lines with `op` equal to `status`, `stop`, `retry`, or `ack-health`.
Only `retry` accepts another field: `sequences`, a nonempty bounded list of inbox
sequence numbers for retained exhausted work. A reply is an operation result,
not a peer receipt. `notifier_not_ready` means the journal is unavailable; it does
not identify a programming fault. Recovery values are `retry`, `capacity`,
`invalid_request`, `operator_action`, and `internal_error`. `retry` requires a new
observation or completion of accepted work; it is not permission to repeat an
ambiguous mutation without checking state. Rebuild reports `bridge_storage_unavailable`
with exit 75 before checking capabilities when the bridge cannot read its store.
A readable older bridge instead yields `bridge_upgrade_required` with exit 78. Control timeouts do not prove rollback.

Status reports lifecycle separately from `delivery_health`. The latter contains
`state` (`healthy`, `degraded`, or `unknown`), fixed reason codes and a bounded
journal summary. Pending work, exhausted attempts, ambiguous outcomes, compatibility
mode, accepted history loss, memory refusal and storage faults remain visible.
A journal query has a one-second observation deadline. No fresh result means unknown
journal values, not a reused healthy result. This deadline is not a disk I/O bound.

A separate worker publishes `notify-health.json` every two seconds. Readers require
a matching verified readiness owner and a snapshot no older than 15 seconds.
Missing, stale, malformed or mismatched evidence yields unknown delivery health.
Snapshot failure is reported in process memory and a content-free diagnostic;
readers do not assume a final failure snapshot could be written. A healthy process
can have degraded delivery health. A provider fault must not trigger a second notifier.

`retry` resets the current automatic budget for selected retained exhausted units.
It first reconciles source acknowledgements and obsolete pointers. `ack-health`
clears acknowledged diagnostic aggregates without acknowledging source records,
resetting budgets or clearing uncertainty on retained work. Rebuild is a stopped
owner operation, not a socket control; see [notifier recovery](docs/NOTIFIER.md).

Memory services advertise `memory_target_guard` when requests can carry both
`repo` and `generation`. A mismatch is refused before maintenance
or mutation. The exact-path memory CLI verifies the owner and uses this guard;
older services without it require an upgrade for this route.

## Local usage reports

`usage_report.py` emits versioned local JSON described in [USAGE.md](docs/USAGE.md).
It introduces no peer frames, remote transcript access, hooks, or model wakeups.
Selections and reports are local data and cannot grant authorization.

## Delivery ledger and presence

Inbox schema 4 and notification journal schema 2 provide the local-only
[delivery evidence contract](docs/DELIVERY.md). Private control operations `delivery`,
`handled`, and `record-notification` do not create peer frames. Deduplication is
bound to an observed sender process lifetime, recipient installation and message
ID, with a canonical payload fingerprint and bounded retention. Outgoing attempt
IDs and deadlines are local controls; no new native wire fields are introduced.

Service health and model activity have separate evidence and observation times.
Model, context and claimed work are reported beside presence from content-free status
records; they add no wire fields ([delivery contract](docs/DELIVERY.md#model-context-and-claimed-work)).
Unsupported activity remains unknown. The daemon omits registry activity instead
of claiming a permanent wait. Native status-omission discovery verification remains
an open release gate, as documented in the delivery contract.

Codex `presence.model_activity` may use source `codex_session_log`. Its `since_ms` is the
matched turn event time; `observed_at_ms` is the observation time. Model and context groups
retain their own source times. Log ownership loss or unreadable evidence produces unknown,
without changing delivery or adding any peer control operation.

### Claimed-work status

A `work-list` item includes its bounded `checkpoint` alongside the existing summary.
Peer and notifier status query this list by the associated owner in the writer-selected
repository store. `work.claims` contains only active-lease `{work_id, title, checkpoint}`
entries, with source `memory_work_list`, read/source times, and a `truncated` flag.
An empty successful query remains observed. No selected association and an unavailable
service report `work_association_missing` and `memory_unavailable` respectively.

## Guidance revision observations

Installation records `runtime_revision` (the digest of the shipped Python allowlist) and
`guidance_revision` (the catalog digest). Services capture their runtime digest once at
startup; CLI observation does not hash runtime files. These digests do not change or replace
service ownership fingerprints, process identities or generations. Older owners lacking the
optional runtime revision remain readable and report `unknown`.

`guide`, session `ensure`/`status`, and `peers` include `guide_revision` and `guide_stale`.
Null means unavailable evidence; true means the installed revision has not been acknowledged
by that session. Guide and session status additionally report a runtime observation, with
`match`, `mismatch`, `runtime_revision_unavailable`, or `upgrade_incomplete`. Revision metadata
is published after upgrade final readiness and `finish()`, with a retained completion receipt.
The gap before that receipt is committed remains `upgrade_incomplete`, even though the normal
installation marker has been cleared. A resume repeats the metadata publication safely.

`session.py guide-ack REVISION` compares the requested digest with the installed revision
under the installation lock. It atomically records owner-only `{revision, acknowledged_at}`
in the current session's `guidance-ack.json`. Claude uses
`guidance-ack-<session id>.json` beside participant status records instead. It refuses an
incomplete upgrade or a different revision. Reading a guide or delivering a notice does not
write this acknowledgement. Peer status reads expose only revision digests and staleness;
notifier-written observations expire with the participant status freshness window.

## Host process and terminal records

A Codex `session.py ensure` observes, from outside the agent sandbox, the host Codex process
and the tmux pane that the command runs in. It publishes two owner-only records in the
session's state directory, replaced on each `ensure`:

- `host.json`: `{state, pid, proc_start, source, observed_at_ms}` for the nearest ancestor that
  is the configured Codex CLI process itself. A session started by `codex_launch.py` carries its
  CLI's process ID in every command as `KOINON_CODEX_HOST` (through `-c
  shell_environment_policy.set`); the host then has `source: launcher`, and a value that is not
  that nearest CLI is `state: unknown, reason: launcher_mismatch`. Without the variable the host
  has `source: process_tree`. A host with another Codex CLI above it, such as a CLI that a
  session's command started, is `state: unknown, reason: host_in_codex`, because it shares the
  outer session's pane. A child of the CLI, such as its shell, never
  matches. A configured executable that is a Python or Node interpreter matches no
  process, because every script of the user runs in one. No match is
  `state: unknown, reason: host_not_found`. A walk that passes a Codex app-server process
  (`argv[1]` is `app-server`, such as the managed daemon of Codex CLI 0.157) is
  `state: unknown, reason: host_shared`: the daemon runs the commands of every CLI connected
  to it, so its CLI ancestor and the pane in the inherited environment can belong to another
  session. This holds also for the session whose CLI started the daemon, because a command's
  thread cannot be matched to its CLI from the process table. With Codex CLI 0.157 every
  session started without the launcher is `host_shared`. A CLI started with a `-c` override,
  as the launcher does, was observed to run its own app-server instead of joining the shared
  one, so its walk reaches its own CLI. Should a launched session run under another CLI's
  daemon, it is `host_shared` as well.
- `terminal.json`: `{state, socket, pane_id, session_id, observed_at_ms}` from
  `$TMUX` and `$TMUX_PANE`, accepted only when the pane's process is the host or one of its
  ancestors. Otherwise `state: unavailable` with `reason` `not_in_tmux`, `tmux_unavailable`,
  `tmux_unreadable`, `host_not_found`, `host_shared`, `host_in_codex`, `launcher_mismatch` or
  `pane_not_host`. Without an observed
  terminal, `ensure` renames no tmux session and titles no pane, and `rebind` needs
  `--user-authorized`.

Neither record holds a thread ID or a peer name. `ensure` and `status` report both as `host`
and `terminal`; `host` adds `live`, whether the recorded pid still has the recorded start
time. A registration without the records reports `state: unknown, reason: not_recorded`.
The host process is evidence only: one Codex process can switch between threads. DeepSeek
and Claude sessions have no such records.

## Stable alias of a Codex participant

Each checkout reserves one alias for its Codex participants, once, under the installation's
`names.lock`: `codex-<label>` (the per-thread name without its two-hex suffix), or, when that
name is reserved for another checkout or equals a saved or live name,
`codex-<label>-<first N hex of the checkout digest>` for the first free N in 4, 6, … 64. The
checkout digest is the SHA-256 of the absolute top folder of the checkout
(`git rev-parse --show-toplevel`). The worktrees of one repository share a Git common
directory, but each is its own checkout with its own alias, named after its own folder, so
their Codex participants never compete for one alias. A new per-thread name never equals a
reserved alias. DeepSeek and Claude sessions have no alias.

The lease `<state_root>/aliases/<alias>.json` (owner-only) is the only source of the holder:
`{alias, checkout, repository, holder, state, from, to, operation, changed_at_ms}`, with
`repository` the SHA-256 of the absolute Git common directory, `state` `held`, `publishing` or
`moving` and `operation` the `{pid, proc_start}` of the command that set a transient state. It
holds no inbox, checkpoint, claim or thread ID.

A lease of an earlier version has no `checkout`: it was keyed by the repository only, so one
alias served every worktree. The checkout of that repository whose per-thread name base equals
the alias adopts it at its next `ensure`, which writes its `checkout` and keeps its holder and
state. `ensure` of any checkout of that repository removes a lease that no checkout adopts,
while no live record publishes its alias and no registry record that cannot be removed carries
it. A notifier never publishes the alias of another checkout, even when the lease names it as
holder. When a key's live record still publishes the alias of another checkout, its `ensure`
restarts its service, so that it publishes its own name or alias. A holder of a `held` lease
whose saved repository is another checkout, and whose live record does not publish the alias,
does not keep it: the checkout's own participant takes it.

One-holder rule. A notifier publishes the alias as its registry `name` only when, at its start
and under `names.lock`, the lease names its own session key as holder in `held` or
`publishing`; otherwise it publishes its per-thread name. It never rewrites its record in
place. Every Koinon record also carries `koinonName` (the per-thread name) and, for a Codex
participant with a reserved alias, `koinonAlias`. The lease leaves a key only when that key
has no live record, checked under the same lock, and `names.lock` is never held while a
service starts, stops or waits. At most one live record therefore carries an alias.

`ensure` takes the alias when the lease has no holder, or when the holder is stopped and has no
live record; a transient state whose `operation` is live refuses (`alias_busy`), and a dead one
stays reserved for its recorded successor while that successor runs. Before a take from a dead
holder, `ensure` removes that holder's registry record only when it is Koinon's, of this user,
of a dead process, and carries the holder's last `bridgeOwner`; any other record named as the
alias refuses the take (`alias_occupied`). A missing registry directory refuses the take
(`registry_missing`), because a record carrying the alias cannot then be ruled out; the next
`ensure` after a notifier has created the directory takes it. When a restart's stop fails,
`ensure` reports `alias_restart_failed` with the stop's result and does not start the
service. When `ensure` did not take or hold the alias, its `alias` result adds `take` with the
`reason` (`alias_held_by`, `alias_busy`, `alias_occupied`, `registry_missing`,
`alias_unavailable`, `not_a_repository`) and, where they apply, `holder` and `paths`. A holder whose running notifier does not publish the
alias is restarted once, and `ensure` marks the lease `held` when the alias is live.

`bridge.py peers` adds `thread_name`, `alias` and `alias_holder`. `bridge.py send` accepts a
peer name as well as a `uds:` address: one live match sends; none is `alias_unheld` for a
reserved alias, else `peer_not_found`; more than one is `peer_ambiguous`.

## Rebind and retirement of a replaced Codex thread

After a Codex `/clear` (or a `/resume` to another thread) in one terminal, the new thread runs
`ensure`, then `session.py rebind --predecessor OLD`, through the approved run outside the
sandbox. The evidence is that both registrations recorded the same tmux server and pane
(`terminal.json`), with this thread's terminal observed again by the rebind itself; or
`--user-authorized`, the agent's statement that the user named that exact predecessor, which
skips only the terminal match. The same host process alone refuses (`host_only`), as do
`terminal_mismatch`, `terminal_not_recorded`, `same_thread`, `same_repository` and
`predecessor_unknown`. The rebind also refuses unless this thread's lifecycle is confirmed
`running` (`not_running`, with the observed lifecycle), and when its own terminal cannot be
observed and recorded now (`terminal_refresh_failed`); an older record never stands in. A
refusal stops nothing and leaves the lease unchanged.

When the predecessor holds the alias, each step is one lease transition under `names.lock`:
`moving` from the predecessor to this thread (no notifier publishes the alias at its start,
and a third thread's take refuses while the rebind runs); the predecessor's stop; `publishing`
with this thread as holder, once the predecessor has no live record; this thread's `ensure`,
which restarts its running service and marks the lease `held` when the alias is live. When the
predecessor does not hold the alias, the rebind still stops it, and the alias follows the take
rule: a free alias moves to this thread, and a live holder in another terminal keeps it.

The stop keeps the predecessor's inbox, checkpoint and claims; nothing moves. The result
reports the predecessor's name, `predecessor_stopped` or `already_stopped`, the number of
records left in its inbox (counted without reading a body), the evidence and the alias. A
killed rebind is completed by the next `rebind` or `ensure` of its successor; a `moving` lease
killed before the stop is cancelled by the predecessor's next `ensure` while it still
publishes the alias. A resumed predecessor registers again with `ensure`, and its own rebind
takes the alias back.

## Terminal name

After a Codex `ensure`, and so after `rebind`, the agent's own terminal is named after the
name its registry record publishes (the alias when it holds it, else its per-thread name). The
input is the terminal that this `ensure` has just observed and recorded; when that refresh
fails, nothing is named (`terminal_refresh_failed`), because an older record may name a
session that is no longer this agent's. When the process scan is incomplete, another agent
pane cannot be ruled out and nothing is named (`panes_unknown`). When the recorded tmux session holds no other
agent pane (a pane whose process tree holds a Codex CLI process or a live Claude registry
process), the session is renamed by its session ID and the name is read back; when another
session already has the name, nothing is renamed (`name_taken`). When the session holds another
agent's pane, only the own pane's title is set (`pane_titled`). The `tmux` result is one of
`renamed`, `unchanged`, `pane_titled`, `name_taken`, `rename_unconfirmed`, `tmux_unreadable`,
`panes_unknown`, `terminal_refresh_failed` or the terminal record's reason. No other session or pane is ever targeted.
