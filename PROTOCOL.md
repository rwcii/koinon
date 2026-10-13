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
| `GET /v1/status` | Daemon readiness, `pid`, listeners, schema, session counts, storage and content-free wake health. |
| `GET /v1/sessions` | Up to 1,000 records ordered by family and ID; `truncated` reports more records. |
| `POST /v1/sessions/register` | Register or reactivate one session identified by `(family, id)`. |
| `POST /v1/sessions/renew` | Renew an active session's expiry. |
| `POST /v1/sessions/retire` | Mark an active session retired, retaining its record. |
| `POST /v1/peers` | Each session's `name`, held `alias`, `family`, `state`, `repository`, optional `role` and `address`, true-only `holds_address` and `subagent`, and optional `succession` and fresh `naming`, for an active caller; never a wake target, directory or session ID. |
| `POST /v1/peers/status` | Resolve a published peer name or held alias and read public identity plus observed model, context and activity for an active caller. |
| `POST /v1/messages/send` | Store one message for the session that a peer name or alias names. |
| `POST /v1/inbox/read` | Read session and held participant inboxes after their independent sequences. |
| `POST /v1/inbox/ack` | Acknowledge handled session and held participant messages through independent sequences. |
| `POST /v1/messages/outcome` | Read the delivery and acknowledgement state of a message the caller sent. |
| `POST /v1/wake/agy-stop` | Offer a due notice for this Antigravity conversation at its native Stop boundary. |
| `POST /v1/launches` | Retain a launcher target and return its generated launch ID. |
| `POST /v1/launches/job` | Record a pending Claude background launch's native job ID once. |
| `POST /v1/launches/retire` | Delete a pending background launch with no recorded job ID. |
| `POST /v1/memory/record` | Record an entry in the caller's repository memory store. |
| `POST /v1/memory/sync` | Read a snapshot page or a delta batch; never moves a cursor. |
| `POST /v1/memory/ack` | Acknowledge a fully issued snapshot or a delta through a sequence. |
| `POST /v1/memory/recall` | Find live entries whose body contains a query, newest first. |
| `POST /v1/memory/status` | Report the store's head, floor, usage, limits, lifetimes and consumers. |
| `POST /v1/library/list` | Page embedded skill/setup catalog metadata for the active caller's family. |
| `POST /v1/library/read` | Read one catalog item's canonical content and family-specific metadata. |
| `POST /v1/sessions/observe` | Record allowlisted observations (model, context, activity, terminal) for an active session; see "Dashboard and session observations". |
| `POST /v1/dashboard/links` | Issue a one-time dashboard login link (`path`, `expires_in`). |
| `POST /v1/storage/recover` | Prove the write-ahead log empty again, return free pages and clear a blocked state. |

Wake health keeps `fault` as the current wake-pass error class, empty after a successful
pass. `last_fault` retains the latest failure's `class`, fixed operation `stage`, Unix-ms
`at`, bounded `error_types` and optional numeric `sqlite_code` until daemon restart.
The daemon logs `wake_fault` when these diagnostics change and `wake_recovered` on recovery;
repeated identical failures update the health time without flooding logs. Diagnostics hold
no raw error/provider text, message body, credential or native session ID. Normal shutdown
cancellation creates no wake fault. Provider delivery outcomes remain in the existing counts
and reason codes, separately from pass failures.

POST requests use `Content-Type: application/json`, at most 16 KiB, with unknown fields refused.
Registration fields are `family` (`claude`, `codex`, `deepseek`, `agy`, `opencode`, `grok`), `id` (1–256
bytes), an absolute `directory` path, an optional absolute `repository` path,
optional `wake_target` JSON (up to 8 KiB),
optional `launch_id`, optional `ancestors` (at most 256
process IDs), optional `subagent` (boolean),
and optional `ttl_seconds` (60–3,600; without it, the lifetime setting, 900 by default). Claude,
Codex, Antigravity, OpenCode and Grok register as plain peers when `launch_id` is absent. They get
a peer name and can send/read/ack their own inbox and use repository memory/work, with no
participant address, role, succession or terminal naming. A supplied launch ID must be known
and match the registering session's family and canonical directory; an unknown, malformed or
mismatched association returns `not_launched` (409), without falling back to plain mode.
A foreground launch's `host_pid` must occur in `ancestors`; Claude also requires
`wake_target: {"claude_pid": PID}` with that same PID. A plain Claude registration also carries
its native worker PID, without recording a launch host. Other native families accept no
`wake_target` override except an empty object. The stored wake target comes from the launch,
with Claude's worker PID added and any OpenCode password omitted.
A Grok launch with a recorded `session_id` admits only that native ID until a session has
registered through it, and refuses another ID with `identity_unavailable` (409); later
sessions under the same host, such as after `/clear`, are not limited.
A background Claude launch instead admits only the session whose native ID equals its
recorded full job ID, or the full session ID whose first eight characters are its recorded
short job ID. Until that job ID is recorded, admission returns `launch_pending` (409)
and registers nothing. DeepSeek retains registration without a launch ID and rejects a
launch ID or `subagent: true`. The daemon derives the canonical absolute
Git common directory when a repository is selected; the working directory must then belong
to that repository. Worktrees share the repository identity. A session in a plain directory
can omit `repository`; its stored repository is empty. Memory and work operations require a
selected repository. Wake targets are local registration metadata used by the adapters below.

Renew/retire take `family`, `id`, and `if_revision`; renew also accepts `ttl_seconds`. A stale
revision, expired session or retired session is refused with `session_conflict`. When a renewal
names a session that is no longer active, the refusal's `details` give its `state` (`expired`
or `retired`) and `fenced` (true when a holder change fenced it), whatever revision it named. Re-register to return an expired/retired session to
active without deleting its retained data.

Liveness keeps a running session without renewals. Every 30 seconds the daemon reads the host
process of each active, launched session that is not a sub-agent and has a host record. When
that process still has the recorded start value, the daemon records the time, and when less
than half of the lifetime setting, or less than two sweep intervals (60 seconds), remains it
moves `expires_at` to that lifetime from now (900 seconds by default, so at 450 seconds left). This change keeps the
revision, so the session's own renewals still apply. The daemon reads the hosts outside its
write and checks the clock and the row again inside it: a session that expired meanwhile, or
whose revision changed (renewed, registered again, as a sub-agent too, or retired), is left as
it is. A session whose host ended (no process,
or another start value) or cannot be read, a sub-agent, and a session without a host record
(DeepSeek, or one registered before host records) expire at their deadline. The daemon never
revives an expired session. A session record shows `liveness`: `host` or `none`, and
`host_verified_at`, the last time that the daemon read the host as running (kept in memory, so
it is empty until the first check after a daemon start). Concurrent
registrations update one row, never create two records for the same key. Times are Unix
milliseconds. Expiry is computed from the current wall clock; clock jumps can change effective
expiry, never remove records or imply a model stopped. Restart preserves records and revisions.
Sessions without a launch association renew as plain peers, including records retained from
older installations. Renewal releases any former participant address and clears launch role,
host and succession metadata, retaining inboxes, acknowledgements, memory cursors, work claims
and persistent participant fences. DeepSeek renewals keep their command path and participant.

Each session record carries its peer `name` and, while it is active and holds one, its `alias`.
Sub-agent records additionally carry `subagent: true`; false is omitted. Schema 10 adds
`sessions.subagent` with false as the default for retained sessions. A sub-agent gets a
peer name and never acquires an alias on registration or renewal. A session stays a sub-agent
once registered as one; a session that held the alias before it was marked gives it up.
The daemon gives each session a permanent peer name `<family>-<label>-<2 hex>`. The label is the
repository directory name (the folder that holds `.git`, or a bare `NAME.git` without `.git`),
or the working directory name for a session without a repository: lower case, characters other
than `a-z`, `0-9` and `-` replaced with `-`, at most 32 characters, `session` when empty. The two
hex digits start at the first byte of SHA-256 of `family NUL id` and take the next free value;
when all 256 are taken, the name takes 4, 6, … hex digits of that digest. A renewal or a new
registration of the same key keeps the name.

### Participants and roles

A participant is a family, Git common directory and optional role. Its reserved address is
`<family>-<label>` without a role and `<family>-<label>-<role>` with one. Worktrees share
participants. The maintainer sets a role at launch with `koinon FAMILY --role ROLE`; the
role has 1–24 lower-case letters, digits or hyphens, starts with a letter, and cannot consist
only of hexadecimal digits and hyphens. Registration takes the role from the launch record,
never from a tool argument. DeepSeek's command registration has no role. A native plain peer,
a sub-agent or a session without a repository has no participant.

Peer names and participant addresses share one namespace. A taken address uses the existing
4, 6, … hexadecimal digest suffix fallback, hashing the common directory (and `NUL role` for
a role). Address reservations persist. Schema 11 adds `sessions.role`, `names.role`,
`names.conflict` and `participant_events`; existing aliases retain their addresses and holders
as participants without a role. Migration keeps inboxes, acknowledgements, memory cursors and
work claims unchanged.

An active holder keeps the address when another session registers or renews, except for
verified succession below. When no active
holder remains, registration or renewal counts the active, non-retired, non-sub-agent sessions
of that participant, excluding native plain peers. Exactly one qualifier takes it; multiple qualifiers leave it unheld,
record their peer names as `conflict`, and wait for the maintainer's choice or for only one
qualifier to remain.
An expired holder that re-registers follows these same rules. Renewal order cannot break a
conflict. Sending to an unheld address returns `alias_unheld`.

Session records carry optional `role` and `address`, true-only `holds_address`, and a
`conflict` list when present. `address` identifies the participant even for a non-holder;
`alias` is present only on its active holder. MCP and CLI `peers` expose role, address and
holds-address alongside their existing fields. Participant events record holder and conflict
changes with daemon time, former/new peer names, reason and actor, under the audit log's
retention bounds. The dashboard shows the conflict and last event. Its bounded participant
lookup contains at most 1,000 participants; sessions outside that lookup still show their
role, address and holder flag.

Schema 12 gives each participant the key `participant:<address>`. An address send enters
that participant's inbox, represented by an internal `sessions` row with that ID which never
expires and is excluded from agent listings, counts and qualifiers. Native IDs with the
`participant:` prefix are refused. Exact peer-name sends keep their native recipient.
Holder changes retire an active former holder and record a persistent fence in the same
transaction. A fenced session can register again as a native peer but is never a qualifier;
only the maintainer's dashboard choice removes its fence. A holder defaults to the
participant consumer for memory and work. Every participant call validates the current
native holder, family and repository, including explicit consumer keys, read paths and
stored retries, before returning a replay or writing. Refusals use `stale_holder` (409).
Split memory/work calls serialize against holder changes, and each write transaction checks
its native caller again. The successor continues participant inbox acknowledgements, memory
cursors and live claims with their existing generations and deadlines.
Pre-upgrade messages, acknowledgements, peer-name cursors and native-session claims keep
their original owners; migration does not transfer them to the participant.

### Session timing settings

Schema 14 adds the `settings` table. The maintainer sets three values on the dashboard
**Settings** page; a value without a row has its default:

| Setting | Default | Range | Use |
| --- | --- | --- | --- |
| `lifetime_seconds` | 900 | 60–3,600 | The lifetime of a registration or renewal without `ttl_seconds`, and of a liveness extension. |
| `renewal_seconds` | 300 | 30 to half the lifetime | How often `koinon mcp` renews; register and renew replies carry it as `renewal_seconds`. |
| `guard_seconds` | 30 | 30–600 | How long after the holder's last tool call same-host succession waits. |

The action `settings` checks the three values together and stores them in one transaction
with its audit record (target: the three values); a refused change stores nothing and is
audited as refused. A change applies from its commit, without a restart: the next
registration, renewal, liveness sweep and succession check read it, and each `koinon mcp`
takes the interval from its next reply. A stored set that is not valid as a whole is not
applied at start, and the defaults apply.

### Verified succession

Schema 13 adds a private host record to each session: host PID, process start value,
tmux socket and pane, plus its last `succession` result. Existing records start with no host
evidence; their owners, messages, acknowledgements, cursors and claims remain unchanged.
The daemon reads the host start through `internal/platform.ProcessStart`: Linux `/proc` field
22 or macOS `kern.proc.pid`, without cgo. A PID with a different start identifies a different
process; an inspection error proves nothing. The host is the launch's process, or the Claude
background job's process reported by its MCP server.

Registration accepts `tool_call` (default false), `clear` (default false) and optional
`tmux: {socket, pane}`.
The socket is an absolute clean path without control characters, at most 4,096 bytes; the
pane matches `%` followed by 1–9 digits. Invalid values return `invalid_request`.
`koinon mcp` sets `tool_call` for registrations triggered by native tool calls and reports a
pane only after the terminal-naming ancestor check proves it holds the host, without a
nested agent between them. Neither identity, host nor pane comes from tool arguments.

Only a tool-triggered registration can succeed another active holder of the same family,
repository and role. It requires readable host records and either the same host PID and
start value, or the same tmux socket and pane with the former host proven ended or replaced.
A candidate whose host already holds another participant is refused. Same-host succession
also requires the guard setting (30 seconds by default) without a tool call from the holder. The daemon stamps actual tool
routes, including reads, using its own clock; observation, renewal, wake and maintenance
requests do not count. Stamps are kept in memory; a daemon restart conservatively counts as
a call for the first guard period. The guard reads the stamp in the registration transaction.

`clear` is accepted only for Codex and only with `tool_call`; otherwise registration returns
`invalid_request`. `koinon hook codex-session-start` sets it. Codex runs that SessionStart
hook as a child of its host process at the first turn of the thread that `/clear` started,
with `source: "clear"`; the hook registers that thread through its own launch and ancestors,
writes nothing and always exits 0. A `clear` registration with the holder's host PID and
start value succeeds at once with `host_cleared`, also within the guard: the host itself no
longer shows the holder. Every other rule applies unchanged. Codex runs no hook at `/clear`
itself, so until the new thread's first turn the cleared thread stays the holder (#275).

The session and `peers` expose `succession: {result, reason, address, holder, at,
retry_after_ms}` when present; `at` is daemon Unix milliseconds and `holder` is the former or
retained holder's peer name. Success reports `result: succeeded` with `same_host`,
`host_cleared` or `same_pane`. Refusal reports `result: refused` with `no_host`, `host_running`, `other_pane`,
`subagent`, `holder_active`, `fenced` or `other_participant`. `no_host` covers missing host
records; `host_running` includes a former host that cannot be proven ended; `other_pane`
includes missing pane evidence. A refused succession still registers the native peer.

For `holder_active`, the daemon returns a remaining `retry_after_ms`. MCP waits that duration
from receipt, rather than comparing client and daemon wall clocks, then registers at the
next native tool call even within its normal five-minute registration cache. A later call
from the holder can cause another refusal and wait. Renewals preserve the pending wait and
never initiate succession; other refusals have no scheduled retry. A fenced session cannot
succeed automatically even after the guard; only the maintainer's dashboard choice lifts it.
Each change and each new refusal records a participant event with candidate, evidence,
refusal reason, host PID and pane; repeated refusals for the same reason and holder do not
repeat the event. A successful change retires and fences the former holder atomically and
continues the participant's inbox, cursor and claims. Every holder change, including the
maintainer's choice, also returns the participant's unacknowledged `notified`, `uncertain` and
`failed` messages to `waiting` with no attempts and no notices, and sets its `read_through` back
to `acked_through`, so the new holder gets a wake: a notice accepted for the former holder,
such as a Codex thread after `/clear`, never reaches it, and what the former holder read it
did not handle. The notice budget of a participant inbox belongs to its holder.

Registration and renewal replies carry `host_holder: true` when another active session with
the same host PID and start value holds a participant address. `koinon mcp` then leaves the
terminal name to that session, and a sub-agent never names its terminal. Listings never carry
the flag.

Message calls name their `caller` as `{"family": ..., "id": ...}`; the caller must be an active
session, or the call fails with `caller_inactive`. A caller reads and acknowledges only its own
native inbox and its currently held participant inbox. It reads only outcomes of messages
its native session sent; another sender's message reads as
`message_not_found`.

- Send takes `caller`, `to` (a peer name or alias) and `body` (1–65,536 bytes of UTF-8; the
  request may be up to 6 × 64 KiB + 4 KiB, for JSON escaping). The message, the sender's session
  and sender name (participant address while held, otherwise peer name), and the next
  sequence number of the receiving inbox are stored in one
  transaction; sequence numbers per inbox are gapless and ordered. The reply's `message` carries
  `id`, the receiving `recipient` peer name or participant address, `seq` and `delivery_state`. Errors:
  `peer_not_found` (404) for an unknown name, `alias_unheld` (409) for an alias with no active
  holder, `recipient_inactive` (409) for an expired or retired recipient, with `details.reason`:
  `retired`; `ended` (its host process ended); `no_liveness_evidence` (no host record, a
  sub-agent, or a host that cannot be read, and its lifetime passed); or `lapsed` (its host
  runs, but its registration expired before a check kept it, as while the daemon was stopped;
  its next tool call registers it again).
- Read takes `caller`, `after` and optional `participant_after` (both default 0), and
  `limit` (1–100, default 50) per inbox. The reply's `inbox` has `messages`, `last_seq`,
  `acked_through` and `more` for the session; while a participant is held it adds
  `participant: {address, last_seq, acked_through, more}`. Messages are ordered within each
  inbox, session first then participant, and tagged `inbox: session|participant`. Each inbox
  page stops after about 1 MiB of bodies. Track and page both independent sequences;
  an explicit `participant_after` without a held participant returns `stale_holder`.
  Each message has `id`, `seq`, `sender_family`, `sender_name`, `body`,
  `created_at`, `delivery_state`, `delivery_reason`, `acknowledged` and, for an acknowledged
  message, `acknowledged_by`: `recipient` or `maintainer`. From schema 15 an acknowledged
  message also has `acknowledged_session` (the peer name of the session that acknowledged
  it, or `maintainer`) and `acknowledged_via` (`ack`, `participant_ack` or `dashboard`).
  A read records the highest sequence that it returned from each inbox as that inbox's
  `read_through`, when the read started at or below `max(read_through, acked_through)`; a
  read that skips unread messages records nothing (see Handled wakes).
- Acknowledge takes `caller`, `through` (default 0) and optional `participant_through`.
  The reply adds `participant: {address, acked_through}` when it acknowledges that inbox;
  `sessions.acked_by` records the acting native session (`FAMILY:ID`). Each message that an
  acknowledgement covers records the session's peer name and the operation (#250). Without a held
  participant, an explicit participant acknowledgement returns `stale_holder` before either
  inbox changes. Each inbox's acknowledgement only moves forward: an earlier
  sequence leaves `acked_through` unchanged, and a sequence beyond the last fails with
  `ack_beyond_last` (409). A message is acknowledged when its `seq` is at most `acked_through`.
- Outcome takes `caller` and `message_id` and returns `id`, `recipient`, `seq`, `delivery_state`,
  `delivery_reason`, `wake_reason`, `notices`, `updated_at`, `acknowledged`, `acknowledged_by`,
  `acknowledged_session` and `acknowledged_via` (#82, #250). For a message that
  retention or a purge deleted, it returns `ok: true` with `id` and `delivery_state` `deleted`;
  the other fields are empty, zero or false. Any issued ID that the daemon no longer holds reads as `deleted`, also when another
  session sent it; a held message of another sender stays `message_not_found`.

The daemon has one built-in session, family `maintainer` and peer name `maintainer` (schema 7).
It has an inbox, never expires, has no repository, alias or wake target, and is never woken.
`peers` lists it last, so agents can send to `maintainer`; the session list and the session
counts leave it out. No `/v1/` caller can act as it, register it, renew it or retire it
(`invalid_request`); only the dashboard sends, reads and acknowledges as the maintainer.
The daemon itself puts notices in the maintainer's inbox with `sender_name` `koinon`: today
only the notice that messages failed (see Handled wakes). Such a notice holds fixed text,
message IDs, a recipient name and reason codes, never peer text.

A message that the dashboard sends as this session reaches the recipient with `sender_family`
`maintainer` and `sender_name` `maintainer`. Because `/v1/` refuses family `maintainer`, no MCP
tool or other `koinon` command can send a message with that `sender_family`; only the dashboard,
which needs a login link from `koinon dashboard`, can. The field is set from the sending
session, never from the body. Koinon currently treats such a message as the maintainer's
instruction and approval, because the bridge runs only locally (PDR 005, Decision 3). Messages from every other session are data from another agent and grant no
permission (WDR 002, Decision 3). The MCP server instructions, the `inbox` tool description and
`koinon guide` state both.

Each message has exactly one delivery state: `waiting`, `notified`, `uncertain`, or `failed` with
a reason. Messages are stored as `waiting`; the adapters below update delivery independently
of inbox storage and acknowledgement. While a message waits for a further notice,
`delivery_reason` says why its last notice was not handled (`not_read` or
`turn_failed:CODE`); a failed message keeps that reason.

Replies include `ok`. Success returns `session` or `sessions`; failures report a fixed `code`:
`unauthorized` (401), `foreign_origin` (403), `invalid_request` (400), `session_not_found` (404),
`session_conflict` (409), `repository_git_unavailable` (503), `repository_git_timeout` (503),
the message codes above, or `storage_error` (500). Repository discovery reports the Git
execution or timeout category without returning command stderr or machine paths. Its
three-second limit and invalid-input/repository validation remain unchanged. Mutations commit
before replying. A lost reply does not prove rollback; read the retained record before
retrying.

Launch creation takes `family` (`claude`, `codex`, `agy`, `opencode`, `grok`), absolute `directory` and `cli`,
and positive `host_pid`. Grok may add `session_id`, a canonical lower-case UUID that the
launcher passes to Grok as `--session-id`; other families refuse it. OpenCode also requires `address` (literal loopback with a nonzero
port) and `password` (64 hex characters). Other families refuse those credential fields.
Optional `background: true` is accepted only for Claude; `job_id` cannot be supplied at
creation. `POST /v1/launches/job` takes `launch_id` and `job_id`, recording the native job ID
after startup: eight lower-case hex characters, or a canonical lower-case full session ID.
Repeating the same ID succeeds; changing an already recorded ID returns `session_conflict`. `POST /v1/launches/retire` takes `launch_id` and deletes only a background
launch with no recorded job ID; a recorded job prevents deletion with `session_conflict`.
Unknown launch IDs return `session_not_found`. These routes do not stop native jobs.
Optional `nested` lists at most 64 repositories inside the directory that are not part of its
repository, each `{"path", "kind"}`: a clean relative path of at most 256 bytes with no control
character, and `kind` `submodule`, `worktree` or `repository`. Optional `nested_incomplete`
says that the launcher's scan stopped early. The session target carries both; they never
change the session's repository.
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

Each notice names only the quoted family/ID inbox and its sequence range: the first run of
due unhandled messages, which stops before a message whose read wait has not ended. A participant
notice names `participant:<address>` and is delivered to its current active holder's native
wake endpoint; without one its messages wait. Codex uses
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

Grok (#254) types the notice into the session's own tmux pane: the socket and pane that
`koinon mcp` proved at the launched session's registration to hold its host, never a path or
pane from model input. Before each wake the daemon proves the pane again: the host process
still has its recorded start value, and the pane's process is the host or an ancestor of it
with no other agent process between them. Otherwise the wake waits with
`grok_target_unavailable` and sends nothing. It then reads the pane and types only when Grok
is idle: every line of the last input box (`│ ❯` and its bordered continuation lines) is
empty, no line is the running-turn status (a spinner glyph that ends with `[stop]`), and no
line below the box shows a cancel shortcut (Grok Build 1.0.50 keeps the box empty during a
turn; conversation text that mentions `[stop]` does not count). Otherwise it waits with `receiver_busy`. It sends the notice with `send-keys -l` and
then Enter, nothing else, and records `notified` only when a second read shows the box empty
and the notice as a submitted prompt (`❯ NOTICE`); otherwise `uncertain` with
`pane_submission_unconfirmed`. A directly started Grok session has no host record and gets
no pane wake. Leader mode stays refused.

`POST /v1/wake/agy-stop` takes `caller` with family `agy` and the hook's native conversation ID.
It offers a due unhandled notice at that turn boundary, leaving delivery `uncertain` with
`stop_offered`; the offer counts as a notice (see Handled wakes). The
Stop hook returns `{"decision":"continue","reason":NOTICE}` only for that offer; otherwise `{}`,
including when the daemon is unavailable. `GET /v1/status` and dashboard health expose `wake`
counts (`waiting`, `uncertain`, `notified`, `failed`), grouped fixed reason codes and a storage
fault, without message bodies or credentials. The counts cover messages that are neither
acknowledged nor read. Queue acceptance does not prove model processing.

#### Handled wakes

Schema 15 (#250) adds `sessions.read_through` and, on messages, `wake_notices`, `ack_session`
and `ack_via`. A message is handled when its `seq` is at most `acked_through` or
`read_through`; the worker wakes only for messages that are not handled. A read therefore
ends the notices for what it returned, even before the agent acknowledges it.

A result that may have put the notice in front of the agent counts as a notice: `notified`,
or `uncertain` other than `attempt_in_progress` and `invalid_adapter_result`, including an
Antigravity Stop offer. Each message counts its own notices; a notice whose range spans a
message that is not yet due leaves that message's count and wait unchanged. After its 1st, 2nd, 3rd and 4th notice it waits 2, 4, 8 and 16
minutes for a read. When the wait ends and it is still not handled, it gets `delivery_reason`
`not_read` and a new notice; the adapters' busy check defers that notice without counting it.
Results that never reach the agent (`waiting` reasons) keep the backoff above and do not count.

A Codex turn that fails reports the fixed code of `codex_error_info` from the rollout on the
idle activity observation as `turn_error`: `server_overloaded`, `usage_limit_exceeded`,
`unauthorized`, `response_too_many_failed_attempts`, `context_window_exceeded`,
`internal_server_error`, `http_connection_failed`, `response_stream_connection_failed`,
`response_stream_disconnected`, `bad_request`, `sandbox_error`, `cyber_policy`,
`misalignment_policy_violation`, or `other`. The error's message text is never read. A failure
observed after a message's last notice sets `delivery_reason` to `turn_failed:CODE` at once,
in the session's own inbox and in the participant inbox that it holds; each reported failure
is applied once, for every Codex session;
the next notice keeps the wait, so a capacity outage does not use the notices up in a minute.
Other families report no turn result; for them the read wait applies.

A message that is still not handled when the wait after its 4th notice ends, or that has a
failed turn after its 4th notice, becomes `failed` with that reason, and its notices stop. This
applies in every family's inbox, Antigravity's included, and to a participant inbox with no
active holder. A read that a holder change overtook does not advance the new holder's read. In
the same transaction the daemon puts one notice in the maintainer's inbox naming the message
IDs, the recipient, the notice count and the reasons. The recipient still finds the message
unread. Wakes stay content-free: a further notice never carries peer text and never changes
the recipient's model or settings. Expired/retired sessions keep their inboxes,
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
  again meanwhile stays. The address that it held stays reserved for its participant; the
  holder rules apply at a later registration or renewal. Its released peer name is free at once,
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
`consumer` (1–128 characters). A holder defaults to `participant:<address>`; a non-holder
keeps its peer-name cursor. Explicit participant consumers also require their current native
holder; other custom keys remain asserted provenance. Entries record the caller's family and
peer name (`writer_family`, `writer_name`). Participant cursors additionally retain `actor`
(`FAMILY:ID`) for their latest caller mutation, shown with each consumer in `status`.

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
(`POST /v1/storage/recover`) proves the log empty again. A proof that ends because its request
was canceled or timed out proves nothing and writes nothing: it refuses only that request,
records no block, and the next write proves the log again. An outside read-only reader of the
database, such as a backup tool, does not block or delay writes. `GET /v1/status` reports `storage`:
pages, ceilings, the log size, the work debt and the blocked state.

### Work items

The memory store of a repository also holds its work items, with the contract of
`docs/WORK-ITEMS-V1.md` and the commands of `docs/WORK-ITEMS-COMMANDS.md`. Each wire operation
is a route: `POST /v1/work/work-create`, `work-get`, `work-list`, `work-propose`, `work-edit`,
`work-start`, `work-update`, `work-release`, `work-finish`, `claim-renew`, `work-report`,
`work-accept` and `work-reject`. A request names
its `caller` and the fields of the commands table; unknown fields are refused, and types are
strict (a boolean is not an integer, times are finite numbers). The optional `consumer` (1–128
characters) names a stable key. For legacy claims, a holder defaults to `participant:<address>`; a non-holder
uses its native `FAMILY:ID`. Only the current holder may use a participant consumer, including
an explicit one, for any operation or keyed retry. A successor continues the same participant
claim generation and deadlines; existing native-session claims keep their owners and leases. Results are `{"ok": true, "result": ...}`; a refusal carries
its code and, for `claim_conflict` (holder, generation, expiry, resource) and
`revision_conflict` (current revision), bounded `details`.

Each work event records `actor` (`FAMILY:ID`) next to its consumer, stored in
`work_events.actor` and returned with work events in memory deltas. Historical events and
daemon maintenance without a native caller keep an empty actor.

Schema 16 adds mutually exclusive typed `proposed_role: {family, role}` and consumer
proposals; empty role targets the default participant. Current item/list views report the
proposal and `role_resolution` (`held`, `unheld`, `conflict`, optional address/held peer).
The current native holder accepts a role proposal using the existing lease engine and
exclusive resources. Its bounded `native-work:` consumer is derived from SHA-256 of the
JSON tuple `[family, native ID]`; explicit consumers must match. The native actor digest is
stored on the claim bundle and its ownership replay rows, checked before owner writes/replays
inside participant serialization and the existing transactions, and retained for the entire
generation even if the proposal changes. A different native session starts only after
release/expiry with its own consumer and generation. Claim views label `ownership` as
`native` or `legacy`. Migration defaults old claims/replays to legacy, preserving consumers,
generations, deadlines, inboxes and cursors. See [PDR 004, Decision 1](docs/product/pdr/004-work-results-role-routing.md).
The authenticated dashboard and explicit purge retain their existing maintainer release and
finish controls. Their ownership replay rows keep the claim's native
binding for agent callers. Retention recognizes the same digest derived from recorded native
session keys, including in its deleting transaction; participant claims keep their continuation.

Schema 17 adds reported results and verdicts (PDR 004, Decisions 2 and 3). `work-report`
(`claim_generation`, `if_revision`, `summary` up to 2,048 bytes, one to eight `references`,
`key`, `deadline`) is an owner write in the ordinary write class. In one transaction it
reserves the verdict, releases the claim bundle and its resources, clears the progress
deadline and sets lifecycle `reported`; its `reported` event payload freezes the summary,
the references (as the item's `references`), the scope revision and the claim generation.
The item keeps `report_seq`, `report_consumer` and the reporting native session's digest,
which views do not show. `work-get` adds the frozen `report`; views and list summaries carry
`report_seq`, `report_consumer`, `verdict` and `verdict_seq`. A reported item does not expire
and refuses start, edit, update, release, finish and another report with
`invalid_transition`; `work-propose` keeps it reported. `work-accept` and `work-reject`
(`if_revision`, the current `report_seq`, one to eight `references`, `key`, `deadline`;
reject also `criteria`, `non_goals` and optional `title`) are control writes that spend the
item's verdict credit. Accept finishes the item as `completed`, and its 30-day retention
starts. Reject writes a new scope revision and returns the item open with no claim. A
verdict ends the reported round: it clears the item's `progress`, `checkpoint`,
`next_artifact` and `blocker`, and its evidence replaces the item's `references`, as a finish
does; the report keeps its own in its frozen event. Each writes an `accepted` or `rejected`
event whose payload adds `verdict_evidence` (`report_seq`). A stale `report_seq` refuses with `stale_report`; a verdict on
an item that is not reported with `invalid_transition`. The reporting consumer, and every
consumer of the native session that reported, refuse with `own_report` before a replay and
again in the committing transaction; another native session's consumer may record the
verdict, including a later holder of the same participant.

A report that cannot fund its verdict refuses with `capacity` and keeps its claim: at most 16
pending verdicts per store and 32 across the daemon, a free scope revision on the item, and
the verdict's pages, slots and bytes after every other promise. Each pending verdict holds
1,024 pages of the page debt and one entry, event, replay and scope revision slot and 64 KiB
of logical bytes in the store's ceilings (`docs/WORK-ITEMS-GO-STORAGE.md`). A verdict with
the maximum scope and eight 512-byte references fits its event bound; references whose JSON
encoding grows (escaped characters) can refuse with `record_too_large`, as for a finish.

Schema 18 adds durable work-notice audiences. `work-list` also accepts `created_by_me`,
matching the caller's validated consumer. Creator and assignment queries page the current
inventory independently of a continued memory cursor. Frozen snapshots and deltas retain
their existing read/acknowledgement and retention-floor contract.

The daemon binds the post-commit memory-change hook to work-notice reconciliation, also
running it at startup, after registration or holder changes, and every 30 seconds. Retained
work events reconstruct relevant audience heads: assignment, scope and daemon changes reach
current assignees, assignment removal reaches the previous assignee, reports reach creators, and verdicts reach the
reported worker. A report freezes the reporting native session's `participant_role` when
it has a participant; an older report without that metadata uses its recorded event actor.
Role recipients follow the same store's current holder. Custom consumer recipients use
verified registry/cursor or creator-actor provenance; a claimed consumer is never parsed
as a native ID. Unresolved audiences remain queryable.

A worker's own start, progress, release, finish and report do not advance the assignment
audience, so recording its response to a notice does not create another wake. Routing-history
reads decode only relevant fields outside the memory operation lock; cleanup rechecks the
observed store head in its transaction before removing an unused row.

Each store retains at most 256 `work_audiences` rows, containing the audience, relevant head,
last notice head and one pending message reference. A pending notice coalesces later changes.
Inbox read and acknowledgement remain distinct; after acknowledgement, a later head queues
the next notice. At saturation no extra row/notice is allocated and no pending audience is
evicted. A row is reclaimed only after its retained relevant work and pending notice are gone.
Reconciliation retries omitted audiences after reclamation. `memory_status` exposes
`work_notifications` (audiences, limit, pending, unresolved, missing_audiences, saturated,
observed_at); usage includes audience rows and their logical bytes.

Work notices have sender family `daemon`, sender name `koinon-daemon`, and a body containing
only the repository work-head/query pointer. They reuse provider wake and task-failure retry
handling. Transport acceptance does not establish receiving-model handling. Enqueueing and
advancing a notice is an ordinary transaction after the work event commits. A refusal leaves
the work transition and its funded controls intact for later reconciliation. Audience rows
use ordinary storage and logical ceilings, with no notification allocation in a funded
report/verdict transaction.

`work-list` accepts `assigned_to_me`, `created_by_me` and keyset `after`. Responses retain `truncated`, add
`more` and nullable `next_after`, and keep their 100-item/48-KiB bounds. Matching proposals
are discovered through the current consumer or the actual held family/role in this store;
no unheld/conflicting role is assigned by guessing. Page to the end, then use current queries
plus retained memory catch-up for changes between pages, without resetting a cursor. This
chunk adds no production work-change model notices.

Work IDs are 32 lowercase hexadecimal characters from a per-store counter that never
decreases. Every observable mutation and due transition writes, in one transaction, the item,
its scope history when the scope changes, one stream entry of type `work-event` (empty body,
scope target the work ID, the caller's provenance), the immutable event payload (the item view
at that moment), the advanced head and the replay result, and calls the change hook after the
commit. Renewal writes no event. Deltas carry `event_kind`, `work_id` and `payload`; a snapshot
adds one frozen `work-item` view per retained item, ordered by ID after the notes. Recall and
note snapshots exclude work entries, and a note cannot supersede or revoke one. Replay rows
are kept apart from note idempotency rows, carry their fingerprint scheme, and return the
original result with `duplicate: true` after participant/native actor validation and before
mutable preconditions or due transitions.

At a statically valid request for a known target, the daemon first records at most one due
transition of that target (`lease-expired` wins over `progress-overdue`); malformed requests,
unknown targets and replays never cause one. A maintenance sweep runs at start and 30 seconds
after each sweep ends, never overlapping: per store at most 32 due transitions, then at most one
inactive claim bundle and one finished item past its 30-day retention. Memory status reports
`work_maintenance` (`enabled`, `last_successful_sweep`, `observed_at`, `pending_due`,
`expired_items`, `inactive_bundles`, `fault`, `fault_at`, `skipped_submissions`) and
`work_debt` (`overdue_credits`, `end_credits`, `verdict_credits`).

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
the current native writer, including resource, work ID and generation. A participant
consumer resolves only to the active holder of that address in the same repository; an
unheld participant has no peer. The holder is re-read inside the send transaction, and the
current holder's own request is refused with `invalid_request`. Its
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

### Skill and setup catalog

The daemon retains one catalog built from the binary's embedded canonical sources at startup.
It requires no runtime checkout or files beside the installed binary. `catalog_digest`
identifies the complete canonical catalog and matches the same binary's version report;
destination metadata in query results is filtered to the validated caller's family.

`POST /v1/library/list` takes `caller` (an active registered session), optional `after` (the
previous page's catalog ID) and `limit` (1–32, with 0 or omission selecting 16). It returns
`{ok: true, library: {version, catalog_digest, recommended_scope: "machine",
available_features, items, more, next_after?}}`. Each item contains metadata without
`content`: stable ID, kind, name, description, purpose, requiring release, content digest,
available feature requirements, candidate destinations and any repository-fact schema.
Items are ordered by ID; pages also have a 1 MiB metadata bound and can return fewer than
`limit` items. While `more` is true, continue with `next_after`. If an upgrade
changes the digest between pages, start again for the new catalog.

`POST /v1/library/read` takes `caller` and `item_id`. It returns
`{ok: true, library: {version, catalog_digest, item}}`; `item` also contains the exact
canonical UTF-8 `content`, whose bytes match `content_digest`. A well-formed unknown ID
returns `library_item_not_found` (404). Invalid IDs or paging bounds return
`invalid_request` (400); IDs are catalog names such as `skill.pickup`, never paths or URLs.
An inactive caller returns `caller_inactive` (403). Both routes use the existing bearer
authentication, request decoding and response size limits.

Machine-wide skills are the recommended default; project-local adoption is an explicit
alternative. Native destination metadata describes the catalog's verification evidence;
it does not inspect the current machine's roots. Repository-fact schemas are metadata,
with no repository-specific values. List/read change no project/native files, adoption
decisions, memory cursors or acknowledgements. MCP registration/renewal still occurs under
the existing session rules. Catalog calls count as tool activity for succession.
The content is reference data; adoption uses an exact reviewed diff approved by the
maintainer (PDR 008).

`library_state` runs bounded filesystem reads in the existing native MCP/command client,
using the daemon's trusted catalog and current decisions. It accepts optional `item_ids`
(at most 32), `scope` (`machine`, default, or `project`) and `directory`. Without IDs it
selects skills for machine scope and all catalog items for project scope. Family, effective
native root and caller identity are native metadata, not tool arguments. Project selection
must match the registered Git common directory and a listed worktree. Machine decision
scope hashes the resolved native root; project decision scope hashes the common directory.
Linked worktrees can therefore inspect different bytes while sharing decisions.

The response has version, family, catalog digest, scope, item states and explicit limitations.
Each item has typed target IDs/hashes, aggregate and target `present`, `missing`, `outdated`
or `edited` states, reasons, collision state, scoped decision and its revision or memory
sequence. Outdated requires a retained adopted baseline. Edited content wins over update
recommendation; a read failure has no guessed aggregate state. Collections have at most
32 targets and a 64 KiB aggregate read bound. Facts are a separate fixed, bounded project
sidecar with schema digest and validation state; local facts are not canonical catalog bytes.
No response contains adopted file bodies, resolved private home paths or native provenance IDs.
Read-only state records no decision and moves no memory cursor.

Known same-name catalog-derived files in machine/project discovery roots return
`name_conflict` for differing bytes or `duplicate_discovery` for matching bytes, independently
of selected content state. Unavailable roots remain limitations.
Adoption comparison also includes the incoming catalog bytes when another independent
same-name file exists; an absent selected target does not conceal a conflict. Discovery
aliases of one physical file count once. A directory at a declared
discovery-link path reports `directory_conflict` and is not traversed by link inspection;
independently declared regular skill targets stay inspectable. Rooted reads reject symlink
escapes and nonregular targets. Codex's effective CODEX_HOME is the primary adoption root;
its shared HOME/.agents skills and both project discovery roots are collision-only descriptors
verified with Codex 0.162.0 metadata. Unverified families still support catalog reads and
project inspection. Recommendations select only verified missing/outdated skills required
by available features, with no decline, collision or decision-inspection limitation.

Machine `library_decision` accepts item ID, adopted/declined choice, inert evidence,
catalog/item digests, typed pre-edit/result target digests and compared revision. The client
fills native family/root metadata and calls `POST /v1/library/decision` internally;
`POST /v1/library/decision/read` returns current state. `POST /v1/library/context` supplies
the registered directory/common-directory context internally and is not a model tool.
Schema 21 adds one ordinary `library_decisions` table and its primary-key index, keyed by
family, root digest and item. It has at most 256 rows, 8,192 bytes per body and 2 MiB bodies
in all, with native writer digest/time retained privately. Existing ordinary page admission
preserves funded work controls. Capacity refuses without eviction. An identical current
canonical outcome is a no-write success, including after a lost reply; a differing compared
revision returns `decision_conflict` with current data. It promises no historical replay.
Adoption updates the adopted baseline; decline keeps it. Records for removed items remain inert.

Project decisions use existing `memory_record`/`memory_recall`, with their body limit,
fixed retry deadline, replacement/conflict and retention semantics. A repo-scope `decision`
body is strict version-1 JSON marked `library: "koinon-library-decision"`: item/family/scope,
scope/catalog/item digests, kind, choice, inert evidence, and typed pre-edit/result/adopted
digests. `repository-facts` kind additionally names the schema digest and fixed target ID
`facts`; it is always project-scoped. Native writer family and repository are checked
independently of body/author fields. A decline superseding a receipt must preserve its adopted
baseline. State pages every relevant live receipt; multiple live receipts or a retained
replacement conflict stay conflicts. Literal `memory_recall` of the library marker also
returns bounded `library_history` absence reasons for revoked/expired receipts, retained floor
and an incomplete flag. This metadata revives nothing and moves no cursor.
The retained floor is library-specific: expiry raises `memory_stores.library_floor` only
for a removed library-marked decision; unrelated memory/work retention does not suppress offers.
This is a scalar in the existing memory store, with no new table or index. Unavailable
history never silently resets a decline. Receipts are reported outcomes, not approval attestations.

The approved #145 procedure has the agent present an exact scoped diff, apply the approved
item with normal editing tools, verify hashes and then record its outcome. Sidecars and
discovery pointers are separate proposed changes. A failed receipt leaves the files applied
and an explicitly unrecorded outcome. No state/decision interface writes adopted files.

### Rooms

Schema 19 adds dashboard rooms (#217, chunk 01). The maintainer creates a room and changes its
roster in the dashboard. Schema 20 adds room posts (chunk 02, below). A room has an opaque 32-character
`room_id`, a display `name` of 1–128 bytes of UTF-8 (names may repeat), a roster `revision`,
the agent-post `refill` setting (a non-negative integer, default 32; PDR 005, Decision 2) and
an `allowance` that starts at 0. A refill change applies at the next maintainer post and does
not change the allowance. Room posts use the allowance (chunk 02).

A member is the participant address that the selected session holds, otherwise that exact
native session. A participant membership follows the participant's verified holder (PDR 001,
Decision 6): a successor reads the roster, and a fenced or replaced former holder does not.
Native expiry does not end a membership; the roster shows `active`, `inactive` (a native
member), `unheld` or `conflicting` (a participant). Removal keeps the membership row with
`current` false, so the former member's `room_list` reports `removed`; a re-add starts a new
membership period and keeps the member's ordinal. Each row records its membership revision
and a membership-start sequence for room posts (0 until chunk 02). The daemon holds at most
64 rooms, 32 active members per room and 128 current and former membership rows per room.
A limit refuses (`room_limit`, `room_member_limit`, `room_history_limit`) and evicts nothing.

An add or remove commits the roster revision, the membership, the audit record and one inbox
message to the member in one ordinary write. The message is from `maintainer` and carries
typed `room` metadata `{room_id, revision, event}` with `event` `added` or `removed`; inbox
reads return it on that message only. Body text never supplies it. A participant member's
message goes to the participant's inbox while a session holds the address; an inactive or
unheld member gets no message and sees the change in its next `room_list`. The wake is the
ordinary content-free wake. At the ordinary storage ceiling the change is refused with
`capacity` and nothing is written. Room rows, their indexes and the message are ordinary pages
under the existing admission; a membership row is less than 1 KiB.

`POST /v1/rooms/list` takes `caller`, optional `after` (the previous page's `room_id`) and
`limit` (1–100; 0 or omission selects 100). It returns `{ok: true, result: {rooms, more,
next_after?}}` with one entry per room in which the caller's exact session or the participant
whose address it holds has a membership: `room_id`, `name`, `revision`, `status` (`member` or
`removed`), `via` (`participant` with its `address`, or `session`) and `membership_revision`.
A current membership is reported in preference to a removed one.

`POST /v1/rooms/members` takes `caller`, `room_id`, optional `after` (the previous page's last
`ordinal`) and `limit` (1–100). Only a current member can read it: the exact native member or
the current holder of a member address. Any other caller, also for an unknown room, gets
`not_room_member` (409). It returns `{ok: true, result: {room_id, name, revision, members,
more, next_after?}}`; each current member has `ordinal`, `member` (`participant` or `session`),
`family`, `address` (a participant) or `peer_name` (the session, or the participant's current
holder), `state`, `current` and `revision`. No route returns a native session ID or wake
target. An inactive or guessed caller gets `caller_inactive` (403), invalid fields
`invalid_request` (400). Both routes use the existing bearer authentication and request and
response bounds; a full page is far below the one-MiB inbox envelope.

#### Room posts

A post has the room's next sequence, its sender (family and the address that the sender holds,
else its peer name), a typed `origin` and its body (1–65,536 bytes of UTF-8). The origin is
`maintainer` only for a post from the dashboard's `room-post` action; every native post has
`member`, and no request field or body text sets it. One ordinary write stores the post,
snapshots the current members, and for each member except the poster's own memberships
stores either an inbox copy with a delivery row (`queued`) or a delivery row `skipped` with
the reason `inactive`, `unheld` or `conflicting`. A participant's copy goes to the
participant's inbox. The write also sets the room's allowance, stores the poster's retry
record and, for a dashboard post, the audit record. It commits completely or not at all;
wakes follow through the existing queue and are content-free. A copy is an ordinary inbox
message from the poster with the full body and `room: {room_id, event: "post", seq, origin}`.

The allowance starts at 0. A dashboard post sets it to the room's refill setting at that
moment (PDR 005, Decision 2); each new member post uses one, and at 0 a member post refuses
with `exchange_limit` until the maintainer posts again. A refill change, a membership change,
a restart or body text never refills. Koinon does not write replies.

`POST /v1/rooms/post` takes `caller`, `room_id`, `body`, `key` (1–256 bytes) and `deadline`
(Unix seconds, after now and at most 24 hours ahead). The caller must be active and a current
member, checked before its retry record, so an accepted earlier post grants nothing after
removal. The same key with the same room, body and deadline returns the first receipt with
`duplicate: true` and writes nothing; other input is `idempotency_conflict` (409), an
expired deadline `retry_deadline_expired`. It returns `{ok: true, result: {room_id, seq,
queued, skipped, allowance, duplicate?}}`; the receipt names no recipient. The daemon
retains at most 10,000 posts and 10,000 retry records (`room_post_limit`,
`room_replay_limit`); a post has at most 32 delivery rows. The request may be up to
6 × 65,536 + 4,096 bytes, as for `send`.

`POST /v1/rooms/read` takes `caller`, `room_id`, optional `after` (a room sequence) and
`limit` (1–100). A current member reads the posts after its membership start, which an add
or re-add sets to the room's post head; expiry, re-registration and a participant holder
change keep it. It returns `{ok: true, result: {room_id, start, floor, head, posts, gaps,
more, next_after?}}`: posts in order with `seq`, `sender_family`, `sender_name`, `origin`,
`body` and `created_at`, about one MiB of bodies per page, `gaps` with the `from`–`to`
sequences in the page's range that retention no longer holds, and `floor`, the highest
sequence below the room's oldest retained post (the head when none is retained). Membership,
floor and posts come from one read snapshot. A read moves no inbox read or acknowledgement marker and no memory cursor.

Retention: message culling stays the clock. A post stays while any of its inbox copies is
retained, also after its member was removed or expired. The maintenance sweep then deletes it
with its delivery rows; a post without copies waits the message retention period from its
creation. Expired retry records are deleted too. These are bounded control deletions with no
message or audit record. One post at maximum fan-out (32 copies of a 65,536-byte body) uses
about 540 pages of 4 KiB under the ordinary admission.

### MCP server

`koinon mcp` is a stdio MCP server: newline-delimited JSON-RPC 2.0 with `initialize` (protocol
versions `2025-06-18`, `2025-03-26` and `2024-11-05`; another requested version gets
`2025-06-18`), `ping`, `tools/list` and `tools/call`. Other methods get error -32601; a message
longer than 1 MiB gets -32700. Its tools are `peers`, `send` (`to`, `body`), `inbox` (`after`,
`participant_after`, `limit`), `ack` (`through`, `participant_through`) and `delivery` (`message_id`), and `memory_status`, `memory_sync`,
`memory_ack`, `memory_record` and `memory_recall` with the fields of the memory routes, and
`work_create`, `work_get`, `work_list`, `work_propose`, `work_edit`, `work_start`, `work_update`,
`work_release`, `work_finish`, `claim_renew`, `work_report`, `work_accept` and `work_reject` with the fields of the work routes, which call
the routes above. `work_checkout` (optional `directory`, default MCP working directory)
and `work_checkout_request` (also optional `note`) call the checkout routes. Relative
directories resolve against the MCP working directory and must share its Git common
directory. `library_list` (`after`, `limit`) and `library_read` (`item_id`) call the catalog
routes, using native caller identity and accepting no target path or family override.
`library_state` (`item_ids`, `scope`, `directory`) and `library_decision` (item, choice,
evidence, catalog/item digests, pre-edit/result arrays and `if_revision`) combine catalog,
decisions and bounded reads in that same native client.
`room_list` (`after`, `limit`), `room_members` (`room_id`, `after`, `limit`), `room_post`
(`room_id`, `body`, `key`, `deadline`) and `room_read` (`room_id`, `after`, `limit`) call the
room routes.
They take no model-supplied consumer or caller identity. A tool
error is a result with `isError` and a JSON text `{"ok": false, "code": ...}`: the daemon's code,
`daemon_unavailable`, `identity_unavailable`, `grok_leader_mode`, `invalid_arguments` or `unknown_tool`, with the
`details` of a work refusal. When the daemon or the MCP server states the rule that refused the
call, such as `renew_for is 60 to 3600 seconds` or `missing argument work_id`, the result also
carries it as `error` (at most 1 KiB). The tool schemas state the lease bounds (60 to 3,600
seconds), work/message list limits (1 to 100) and catalog list limit (maximum 32). Every
initialize response offers the catalog and its query paths; this informational offer
performs no query, inspection, registration or adoption. The server
writes only protocol messages to stdout and never logs a secret, session ID or message body.

The calling session comes from the agent on every call, never from model-supplied arguments; a
call whose arguments hold `caller`, `family`, `id`, `as`, `session` or `session_id` is refused:

| Family | Identity source |
| --- | --- |
| Codex | `_meta.threadId`, from a client whose name starts with `codex`; each thread is its own session. `x-codex-turn-metadata.thread_source` in the call metadata marks a non-empty value other than `user` as `subagent`; turn metadata may be an object or a JSON string. Missing metadata does not mark a sub-agent. |
| Antigravity | `_meta["antigravity.google/conversation_id"]`. |
| Claude | `CLAUDE_CODE_SESSION_ID` in the server's environment, only when `initialize` names the client `claude-code` and the server's parent process is a Claude Code executable. |
| OpenCode | The `koinon_session` argument that the Koinon plugin sets from the calling session's ID; accepted only from the `opencode` client. |
| Grok | `GROK_SESSION_ID` in the server's environment, only when `initialize` names the client `grok-shell-koinon` and the server's parent process is a Grok terminal session: the `grok-native` executable (started as `grok`) whose arguments do not begin with `agent leader`. A parent that is Grok's leader process (`grok-native agent leader`) returns `grok_leader_mode`: Koinon supports Grok's default mode only, because a leader-mode server has the first terminal's directory and pane. A Grok sub-agent's call carries its parent's `GROK_SESSION_ID` with no per-call marker and acts as the parent session (PDR 001, Decision 2). |

DeepSeek has no MCP identity source yet and uses the `koinon` commands with `--as`. For the
other families, the server registers the native session at its first call, with the working
directory and, inside Git, its repository, and registers it again after the renewal interval,
when the daemon reports it inactive, or at once when a Codex thread is first marked as a
sub-agent. At the renewal interval it renews the session it served last. The interval is the
daemon's renewal setting from the `renewal_seconds` of its last register or renew reply (30
to 1,800), and five minutes before a reply carries one. A renewal that fails because the
daemon is unreachable, its storage is blocked or the call times out is tried again at the next
interval. A refusal that ends the registration (`session_conflict`, `session_not_found`,
`not_launched`, `caller_inactive`, `stale_holder` or `invalid_request`) stops the renewals
until the next tool call registers the session again. A `session_conflict` whose `details.state`
is `expired` and whose `details.fenced` is false, such as after an outage longer than the
session lifetime, instead registers the session again at once, with `tool_call` false: it takes back its participant address only when
no other session holds it, and a retired or fenced session stays dropped. The session stays in
the renewal set until that registration succeeds or is refused for good, so a transient failure
is tried again at the next interval. Registration passes
`KOINON_LAUNCH_ID` as `launch_id` and the server's process ancestors, nearest first, from the
native process table. Claude also passes its parent's `claude_pid`; the daemon binds the
foreground host or background job as described above. A server without a launch ID registers
a plain peer with the same native identity checks. A supplied launch that does not match
returns `{ok: false, code: "not_launched", launcher: "koinon FAMILY", message: ...}`.
`tools/list` remains available. Failed pending background admission retries at the next tool
call. A plain peer's renewal or reconnect can recover an expired registration with the same
peer name. Direct Codex starts have no launcher CLI wake target, and direct OpenCode starts
have no launcher wake endpoint; their inboxes remain available through tool calls. A direct
Grok start has no host record and no pane wake; its inbox remains available through tool calls.

`koinon grok` adds `--session-id` with a new UUID, recorded in the launch, unless its
arguments hold `--session-id`, `--resume`, `-r`, `--continue` or `-c`.

`koinon codex` uses `--no-daemon`, making the launched CLI the host of its MCP servers, and
overrides `mcp_servers.koinon.env_vars` to forward `KOINON_LAUNCH_ID`, `KOINON_STATE_DIR`,
`KOINON_DAEMON_ADDRESS`, `TMUX`, `TMUX_PANE` and `CODEX_HOME`, preserving the configured
server's other fields. The launcher keeps the caller's `CLAUDE_CONFIG_DIR` but removes the
other `CLAUDE_` variables and `CLAUDECODE` of the session that ran it. In a new tmux session, whose environment comes from the
tmux server, it carries the caller's value, or its absence, as the session variable
`KOINON_CLAUDE_CONFIG_DIR`, which the pane's launcher turns back into `CLAUDE_CONFIG_DIR`. `KOINON_STATE_DIR` and `KOINON_DAEMON_ADDRESS`
select the state root and address.

`koinon claude --bg` creates a background launch and passes its environment through
`<state>/launches/<launch ID>.json`, a private mode-0600 settings file containing `env`.
The native `claude --bg` invocation has inherited `KOINON_` variables removed so a service
it starts cannot inherit another job's launch association. Its returned job ID is then
recorded with the launch. Failed startup or unusable job output retires the pending launch
and removes the settings file; successful jobs retain the file. Koinon owns `--bg` and
`--settings` for this path and refuses conflicting native arguments.

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
  never a session ID or directory), `rooms` (every room with its refill setting, allowance,
  revision and current and former members by address or peer name and state, with Remove
  buttons; the add form suggests the peer names of active sessions; `room=ROOM_ID` opens a
  room with its post form and 50 posts in order, the newest by default, with `after=SEQ`
  links and the retention gaps), `memory` (per store:
  head, floor, entries and logical bytes against their ceilings, consumers, work debt and
  maintenance, 50 stores per page; with `store=REPOSITORY`, that store's entries, newest first,
  50 per page with `before=SEQ`: sequence, time, type, scope, path, writer, author, body, state
  `live`, `superseded`, `revoked` or `expired`, and the supersession, revocation and conflict
  links; work events are not listed. `q=TEXT` (at most 256 bytes) matches the body or path as a
  substring, ignoring ASCII case; `type=TYPE` filters; without `all=1` only live entries are
  listed), `work` (per store, every unfinished item with its claim,
  resources, lease and progress state computed at read time; stores page by repository, 50 at a
  time, with `after=REPOSITORY`; `lifecycle=open|active|blocked|reported|finished|all` filters, and
  finished items are listed while their retention lasts; `store=REPOSITORY&item=WORK_ID` opens
  one item with its full scope, checkpoint, progress, references, outcome, frozen report and
  verdict (a reported item offers no edit or finish action, and the dashboard records no
  verdict) and its newest 200
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
  - `participant-holder` (`address`, `family`, `id`, `revision`): chooses the named active
    session as holder of its participant address. A changed session revision returns
    `revision_changed`; an inactive session returns `session_not_active`; a sub-agent or a
    session of another participant returns `not_participant`. Invalid fields return
    `invalid_request`. The choice is a control write, records `maintainer_choice` with the
    former/new holders, and is audited. It retires an active former holder, persistently
    fences it and lifts the chosen session's fence in the same transaction. Participant
    inboxes, cursors and claims retain their state. The success notice is `participant_chosen`.
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
  - `room-create` (`name`, `refill`, `key`), `room-refill` (`room_id`, `refill`), `room-add`
    (`room_id`, `peer`) and `room-remove` (`room_id`, `ordinal`): the room operations (see
    Rooms). `refill` is a non-negative integer; other input is `invalid_request` and changes
    nothing. The create form carries its own key: the same key with the name and refill that it
    first sent returns the room it created, also after a refill change, and with other input
    is `idempotency_conflict`. `room-add` resolves the
    peer name inside its write and needs an active session (`peer_not_found`,
    `recipient_inactive`); adding a current member changes nothing. The audit target names the
    room, the peer name and the address or `session`, never a native ID. The notices are
    `room_created`, `room_refill_saved`, `room_member_added` and `room_member_removed`.
  - `room-post` (`room_id`, `body`, `key`, `deadline`): a post with origin `maintainer` (see
    Room posts). It reaches every active member and sets the allowance. The form takes up to
    3 × 65,536 + 4,096 bytes, as the send form does, and each rendered form has its own key, so
    a resubmitted form repeats the first post. The notice is `room_posted`.
  - `send` (`to`, `body`): a message from `maintainer`; the recipient's wake works as for any
    message.
  - `launch` (`family` `claude`, `codex`, `agy`, `opencode` or `grok`; `directory`; optional `name`): the daemon runs
    its own executable as the launcher with `--tmux-session`, so the agent starts detached in a
    new tmux session with its configured CLI. The directory must be an existing absolute path; the
    name uses letters, digits, `_` and `-`, and defaults to `FAMILY-FOLDER-XXXX`. No other
    argument is accepted. The action never waits for the agent to register. The launcher's
    `--tmux-session` result adds `nested` and `nested_incomplete` when it found nested
    repositories; the sessions view shows the launch record's list with each session registered with it.
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
| Terminal | `tmux_env`: the MCP server's `TMUX` and `TMUX_PANE`, and the pane's session name |
| Naming | `koinon_mcp`: the last terminal naming result and the published name it was computed for; no socket or pane required |

One MCP server can serve several sessions, so its environment proves nothing about a session.
The daemon accepts a terminal only for a session registered with a verified launch ID or a
Claude session, whose server is the child of that Claude process; any other terminal report is
refused with `terminal_unverified`, and the view shows that reason.

For sessions registered with a verified launch record, `koinon mcp` names the terminal after
the session's published name (the participant address while held, else the peer name) after a
registration or renewal when that name changed or the previous result permits a retry.
Claude also requires its MCP server to be a child of its Claude process. `TMUX` and
`TMUX_PANE` only select the tmux server and a candidate
pane. The pane counts as the session's own only when its process is the session's host
process from the wake target (the Claude process, or the launched CLI) or an ancestor of it,
with no Claude Code, Codex, `agy`, OpenCode or Grok process between them (`pane_not_host`,
`nested_agent`). When another pane of the same tmux session holds an agent process, only this
pane is titled (`pane_titled`); otherwise the session is renamed by its ID (`renamed`), unless
it already has the name (`unchanged`) or another session has it (`name_taken`), and the name is
read back (`rename_unconfirmed`). `name_taken`, `rename_unconfirmed` and a tmux or process-table failure (`tmux_unreadable`,
`process_table_unreadable`, `panes_unknown`) changes nothing and is tried again at the next
renewal; the other results wait for the next change of the published name. Outside tmux,
`not_in_tmux` changes nothing. Every tmux call has a one-second timeout, and naming never
delays or fails a tool call.

A Claude background launch uses the pane of its `claude attach SHORT_JOB_ID` client instead
of the background host. The short ID is the first eight characters of its registered job ID.
Naming searches socket entries in `$TMUX_TMPDIR/tmux-<uid>`, else `/tmp/tmux-<uid>`, kept
unresolved, and the process trees of each server's panes. Exactly one matching client applies
the same ancestor and naming rules to its pane. No match reports `attach_pane_not_found`
and retries at the next renewal; multiple matches report `attach_pane_ambiguous`, rename
nothing and wait for a change of published name. Servers outside that directory are not searched.
Only an `attach` command with the job's short ID qualifies; option values and prompts do not.
If a pane's process tree cannot be completely checked within the scan bound, naming reports
`panes_unknown`, renames nothing and retries at the next renewal.

The independent `naming` observation carries `source: koinon_mcp`, `at`, allowlisted `naming`
result and `target` (the published name). It contains no socket or pane, so a session outside
tmux or a background job with no client can report its result. Older servers' `terminal.naming`
is still accepted but no longer displayed. Naming reports do not count as tool-call activity.
The daemon's last confirmation controls freshness (two minutes), and a report for a target
that is no longer the session's published name is unknown with `naming_outdated`.
The dashboard shows the result, target, source time and fixed reason text; `peers` and
`koinon peers` include optional `naming: {result, reason, target, at}` only for a fresh result
of an active session's current published name. An unverified session reports
`terminal_unverified` in the dashboard. Reports remain in memory and become unknown after
a restart. `koinon mcp` checks its
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
The adapter wakes only the registry entry of the session's `claude_pid` with entrypoint `cli`.
Its `sessionId` must be the registered session's, or, after `/clear` started a new transcript
in the same process (the Koinon session continues, as the MCP server keeps its first session
ID), the process must still be the session's recorded host: the host record names that PID
and the process has the start time that the daemon read at registration. A reused process
ID, another PID, a session without a host record, or a missing or malformed entry returns
`claude_identity_mismatch` or `claude_target_unavailable`.
Sender-provided envelope labels alone are not authentication or permission. See
`internal/core/wake_providers.go` and its synthetic receiver tests for the maintained wire contract.


### Claude observations after a transcript clear

Claude can keep its MCP process and first environment session ID across `/clear`, while the
native registry and status-line input use a new transcript ID. The MCP activity reader accepts
that changed ID only while the parent retains the process start value pinned at the first
Claude initialization. A reused PID or unreadable changed-ID proof reports no activity.

When a Claude status-line hook's direct observation names an unregistered transcript, it can
retry with `claude_reporter: {pid, start}` from its own live process. Only `claude_statusline`
model/context reports accept that optional evidence. The daemon reads actual process ancestry,
requires one active Claude host with its recorded start value, and stops at a foreign or
unregistered nested agent. An ambiguous host or a known caller from another session is refused
with `observation_host_unverified`. The reporter is rechecked before accepting the report.

This resolves observation values to the existing session only. It creates no registration and
moves no participant, claim or cursor. Reporter evidence is transient and is excluded from
peer status, dashboard values, stored observations and logs. Hook output and the user's status-
line command's exit result are unchanged. These observations remain subject to the existing
source validation and freshness windows.
