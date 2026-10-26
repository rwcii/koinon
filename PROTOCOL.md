# Koinon protocols

## Go daemon core

The Go daemon is the Koinon runtime. Its HTTP API uses a
private user secret from the selected Go state root, passed in `Authorization: Bearer SECRET`.
No API endpoint is anonymous. Each listener must be a literal loopback address; foreign
browser origins and non-loopback Host headers are refused. The API accepts no browser cookies.
The daemon listens on `127.0.0.1` and `[::1]` at the port that the state root's `port` file
records, 47671 when there is none; `koinon install --port N` writes that file, one decimal
port and a newline, and `koinon serve --listen` or `--listen-v6` replaces one listener. A
client takes the daemon address from `--address`, else from `KOINON_DAEMON_ADDRESS` when
`KOINON_STATE_DIR` (or, unset, the default) names the same state directory, else
from `127.0.0.1` and the recorded port; a `port` file that holds no valid port is refused
with `port_invalid` (docs/INSTALL.md).
The secret and state files are user-owned regular files, mode 0600; the state root is private.
Secrets, runtime session identifiers and response bodies stay in the state directory, outside
repositories.

| Route | Operation |
| --- | --- |
| `GET /v1/status` | Daemon readiness, the release `version`, `pid`, listeners, schema, session counts, storage and content-free wake health. |
| `GET /v1/sessions` | Up to 1,000 records ordered by family and ID; `truncated` reports more records. |
| `POST /v1/sessions/register` | Register or reactivate one session identified by `(family, id)`. |
| `POST /v1/sessions/renew` | Renew an active session's expiry. |
| `POST /v1/sessions/retire` | Mark an active session retired, retaining its record. |
| `POST /v1/peers` | Each session's `name`, held `alias`, `family`, `state`, `repository`, optional `role` and `address`, true-only `holds_address` and `subagent`, and optional `succession` and fresh `naming`, for an active caller; never a wake target, directory or session ID. The caller's own row has true-only `self`, matched by its family and session; when the list is truncated, that row follows the listed rows, before the maintainer row. |
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
| `POST /v1/maintainer/secret` | Set, generate or clear the maintainer secret (`action`, `secret`); ends every dashboard login and maintainer token. |
| `POST /v1/maintainer/session` | Exchange the maintainer secret for a maintainer token (`secret`, `client`); see Maintainer mode. |
| `POST /v1/maintainer/ROUTE` | The maintainer's reads and actions with a maintainer token: `board-list`, `decision-get`, `decision-history`, `decision-verdict`, `inbox`, `ack`, `send`, `room-list`, `room-read`, `room-post`, `room-create`, `room-add`, `room-remove`, `peers`, `peer-status` and the long poll `changes`; see Maintainer mode. |
| `GET /v1/participants` | Each participant's `address`, `family`, `repository`, optional `role`, active `holder` peer name, `conflict` and `last_event`, at most 1,000 with `truncated` (`koinon participant list`, #398). |
| `POST /v1/participants/holder` | The maintainer's holder choice from the command line (`koinon participant holder`, #398): `address`, `peer` and an optional `if_revision`, the session's current revision when absent. It makes what the dashboard's **Make holder** makes and is audited as `participant-holder` by `cli`. It returns `holder` and `former`; refusals are `invalid_request`, `peer_not_found`, `not_participant`, `session_not_active` and `revision_changed`. |
| `POST /v1/storage/recover` | Prove the write-ahead log empty again, return free pages and clear a blocked state. |

Wake health keeps `fault` as the current wake-pass error class, empty after a successful
pass. `last_fault` retains the latest failure's `class`, fixed operation `stage`, Unix-ms
`at`, bounded `error_types` and optional numeric `sqlite_code` until daemon restart.
The daemon logs `wake_fault` when these diagnostics change and `wake_recovered` on recovery;
repeated identical failures update the health time without flooding logs. Diagnostics hold
no raw error/provider text, message body, credential or native session ID. Normal shutdown
cancellation creates no wake fault. Provider delivery outcomes remain in the existing counts
and reason codes, apart from pass failures.

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

### Context policy

The managed context handoff (#196, PDR 006) is configured per family and switched on per
session; chunk 01 adds the policy, the Enable switch and the trigger selection, and no cycle
runs yet. The `settings` table holds `context_mode_FAMILY` (0 `observe`, 1
`native_compaction`, 2 `handoff_cycle`) and `context_threshold_FAMILY` (1–100 percent of the
context limit) for each supported family. Defaults: Claude `handoff_cycle`, Codex
`native_compaction`, every other family `observe`; threshold 75. `native_compaction` is
Codex's only; it leaves compaction to the client, and no mode edits a native setting. A
stored pair that is not valid keeps the family's default, and the Settings page names the
error. The action `context-family-settings` (`family`, `mode`, `threshold`) stores one family
in one transaction with its audit record and applies after the commit; it enables no present
or future session.

Schema 22 adds `context_enables`, one private row per session with its Enable switch and
revision and the session revision, participant address, host and tmux pane it was set for;
none of these leave the store. The action `context-enable` (`peer`, `revision` of the session
as shown, `if_revision` of the Enable, `enabled` `on` or `off`) changes one session with its
audit record. Enabling needs an active session whose family adapter's Probe passes for its
pinned host and pane (the cycle adapters below); otherwise it refuses with
`context_cycle_unavailable` and writes nothing. Disabling is always
accepted. A changed session refuses `revision_changed`, a changed Enable
`context_enable_changed`. Deleting a session deletes its Enable.

Schema 27 (#391) adds `context_enables.threshold`: a session's own threshold, 1 to 100, or 0
for its family's threshold. The trigger, the admission and the Sessions page use the session's
threshold while it is set. Changing it keeps the Enable and its revision, so an admitted cycle
continues; for a session without an Enable row it adds a row that is off, pinned as an Enable
would be. A verified reset moves the threshold to the successor with the Enable. The dashboard
action `context-threshold` (`peer`, `threshold` 0 to 100) sets it.

The same changes have `/v1` routes for the command line and MCP (#391). Each runs the
dashboard action's store path and writes its audit record, whose target ends with
` by cli`, or with ` by agent PEER` when the request names a `caller` (the MCP server adds the
calling session). Without `peer`, a request with a caller names the caller's own session.

- `POST /v1/context/show` (`context_show`, `koinon context show`) takes an optional `after` (a
  peer name) and returns `families` (mode, threshold, error) and up to 100 `sessions` by peer
  name after it, with `next_after` when a full page may continue (`koinon context show` reads
  every page). Each is an active agent session's `peer`, `family`, `enabled`,
  `enable_revision`, `mode`, `threshold`, `threshold_source` (`session` or `family`),
  `available`, `unavailable`, `eligible`, `reason`, and its newest cycle's `cycle_id`,
  `cycle_revision`, `cycle_state`, `cycle_reason` and waiting native `dialog` (`lines`,
  `options`, `digest`). The table shows the cycle's reason after its state.
- `POST /v1/context/set` (`context_set` with `target_family`, `koinon context set`) takes
  `family`, `mode` and `threshold`, as `context-family-settings`.
- `POST /v1/context/enable` (`context_enable`, `koinon context enable|disable`) takes `peer`
  and `enabled` (boolean) and applies them at the session's and the Enable's current revisions.
- `POST /v1/context/threshold` (`context_threshold`, `koinon context threshold`) takes `peer`
  and `threshold` 0 to 100, as `context-threshold`.

No route, command or MCP tool answers a native dialog: the Sessions page does, or keys in the
session's own pane (#391).

The trigger selection reads the session's state, Enable, family policy, cycle availability
and its stored context observation. It is eligible when used tokens reach the threshold
percentage of a positive limit (equality and use above the limit included), the context
source is at most 120 seconds old and its last confirmation at most 90 seconds old, and the
observation's native epoch has not been selected already. A source or confirmation time later
than the daemon's clock is `observation_future`. Otherwise it names one reason:
`session_inactive`, `mode_observe`, `mode_native_compaction`, `not_enabled`,
`context_cycle_unavailable`, `context_unknown`, `context_counts_unavailable`,
`context_counts_invalid`, `epoch_unverified`, `observation_future`, `source_stale`,
`confirmation_stale`, `below_threshold` or `already_selected_for_epoch`. A new epoch or a
fall below the threshold clears the selected epoch. Selection writes nothing; the Sessions
page shows its result and the source and confirmation ages. After a restart the observations
are unknown, so no session is eligible until fresh native reports arrive.

### Managed context cycles

Chunk 02 adds the durable cycle. A context observation that makes an enabled session
eligible admits a cycle when the family has a registered cycle adapter, the session still has
the host and tmux pane that its Enable pinned, it has no unfinished cycle (nor has its host),
and fewer than 16 cycles are unfinished. Chunk 03 registers the Claude Code adapter and chunk
04 the Codex adapter; the other families have none, so their Enable is refused. Admission is one ordinary transaction:
the cycle row, its first event `requested admitted`, an audit record and the typed request in
the session's own inbox. The request's sender is the reserved system identity `overwatch`
(`sender_family` and `sender_id` `overwatch`, `sender_name` `overwatch`), with `system_kind`
`context_cycle_request`, `cycle_id` and `cycle_enable_revision`; its fixed body names the
cycle and its revision and asks the session to pause at the next safe stopping point, write its
handoff, report it with `context_cycle_ready`, then end its turn and stay idle (#431). A wake
whose range holds the request of an unfinished cycle ends its notice with ` Managed
context-cycle request ID.`, after any decision verdict suffix; a finished cycle's request adds
nothing (#445). No session, alias or participant can take the name `overwatch`, it has no
inbox, and no tool or command can send as it.
Inbox reads return `system_kind`, `cycle_id` and `cycle_enable_revision` with the message. A
name record called `overwatch` retained from before schema 23 is kept unchanged; while one
exists no cycle is admitted, and the session's trigger reason is `overwatch_name_conflict`.

Schema 23 adds `context_cycles` (state, compared revision, the pinned session, host, pane,
participant, Enable revision and pre-reset epoch, the policy snapshot, deadlines, artifact
metadata, adapter version, catalog digest, successor, outcome, the private pickup report, the
remaining page credit and whether the requester held its participant address) and `context_cycle_events` (at most 16 per cycle, keyed by cycle and ordinal,
with fixed stage, result and reason codes), and three message columns for the typed request.
The states are `requested`, `waiting_ready`, `ready`, `clear_intent`, `clear_received`,
`reset_verified`, `pickup_intent`, `waiting_pickup`, `waiting_prompt`, `prompt_intent`, and the terminal
`complete`, `cancelled`, `failed` and `uncertain`. The last event slot is kept for the
terminal outcome; a stage that would take it fails the cycle with `event_limit`.

Schema 24 rebuilds `context_cycles` with two more states, `bootstrap_intent` and
`bootstrap_verified`, copying every row unchanged. They belonged to the Claude loader's
interactive bootstrap, which #406 removed: no cycle enters them now, a cycle that an earlier
binary left in `bootstrap_intent` ends `uncertain` (`bootstrap_unreceipted`), and one left in
`bootstrap_verified` goes on to its clear.

The session reports through three own-cycle calls. The native client names the caller and
attaches its current native epoch: Claude's transcript session from the registry record of
its pinned host process, Codex's thread, a DeepSeek session's own ID. No argument names a
caller, epoch, host, pane, socket or another session.

- `POST /v1/cycles/status` (`context_cycle_status`, `koinon cycle-status --as deepseek:ID`)
  returns the caller's unfinished or newest cycle, as requester or managed successor, with its
  history. It reads only and moves no inbox, memory or work cursor.
- `POST /v1/cycles/ready` (`context_cycle_ready`) takes `cycle_id`, the `revision` shown and the
  handoff file name. The requester under its pre-reset epoch moves a `waiting_ready` cycle to
  `ready` when the file is `<main checkout>/<handoff directory>/<name>`. The handoff directory
  is the first word of the Handoff path fact in the main checkout's `.koinon/skills/handoff.md`,
  parsed with the catalog's facts schema for `skill.handoff`, else its default
  `_handoff/<agent>/`; `<agent>` is the family, with `-ROLE` for a launch with a role. It must
  be a clean relative path inside the checkout that names `<agent>`, and each of its directories
  a real directory, not a link. The file is a canonical
  timestamped name of at most 128 bytes, a regular file of 1 byte to 1 MiB modified after the
  current request (after a repeat, the repeated one), ignored by Git, in a directory where Git tracks nothing. The main checkout is the
  first worktree that Git lists for the registered common directory. The daemon reads only
  the file's metadata and, of the checkout's files, the handoff facts as one bounded read of an
  opened regular file of at most 64 KiB, through a root opened at the main checkout, and keeps its device, inode,
  size and modification time; a change to any of them is a changed file. The
  registered directory must itself be a worktree that Git lists. A session registered without
  a repository has its registered directory in place of the main checkout: the same handoff
  directory, facts file and file checks apply there, and Git checks nothing (#485). Each
  refused report at the current `waiting_ready` revision is kept in the cycle's
  `ready_refusals`, the newest 8, with its revision, time, code and text; it takes no revision
  and no event. The newest refusal's code and revision are also kept in the cycle's own
  fixed-width slot, which is written in place even when ordinary storage is full
  (docs/CONTEXT-CYCLE-STORAGE.md). A successful report also
  returns `next`, the instruction to end the turn and stay idle, because the clear waits for the
  idle prompt.
- `POST /v1/cycles/pickup` (`context_cycle_pickup`) takes `cycle_id`, `revision`, the same file
  name, `outcome` `resumed` or `paused` and a `report` of 1 to 1,024 UTF-8 bytes. A
  `waiting_pickup` cycle completes when the caller runs under a new epoch (the epoch that the
  adapter proved) and is the requester itself or a managed successor: an active session of the
  same family in the same host and pane that holds the cycle's participant address. The
  `revision` is that of one of the cycle's `waiting_pickup` stages. The report is also accepted
  in a prompt wait that began in `waiting_pickup` (`waiting_prompt` or `prompt_intent` with
  `prompt_from` `waiting_pickup`), because the call that the prompt approves can be the report
  itself; the wait and its return change the revision, and the approved call can arrive after
  either, so the report keeps the `waiting_pickup` revision that it read. A
  successor's completion moves the Enable to it. The report is private runtime data; status,
  audit and the dashboard show its size only.

Ready and pickup check in their write transaction that the cycle is before its stage and cycle
deadlines, that the Enable that admitted it is unchanged, and that the caller is still in the
pinned host and pane: the requester active with the same participant address, still holding it
if it held it at admission, or a managed successor as above.

An identical retry of an accepted report returns the current cycle without a write: a ready
retry while the reported file is unchanged, a pickup retry with the same successor, epoch,
file, outcome and report. A stale or conflicting one is refused with `cycle_changed` (HTTP 409) and the current cycle in
`details`. Another session's call is `cycle_not_found`, an epoch that does not fit
`cycle_epoch_unverified`, a file that fails a check `cycle_artifact_invalid`, whose `error`
names the check that failed and, for the directory, the directory it expects, with each path
cut to 240 bytes so the text stays within the 1024 bytes a client passes on (#468). The
request message names the session's directory and its ready deadline as a UTC time, the stage
deadline: 15 minutes after the request, or the end of the cycle's lifetime when that comes first
(#467). A message, an
acknowledgement or a peer's report advances no cycle.

The controller runs once a second. Each external input holds the wake lock and is preceded
by its recorded intent: on a fresh idle inspection a `ready` cycle records `clear_intent`,
clears and records `clear_received`; a proven new epoch in the same host and pane gives
`reset_verified`; an idle inspection then records `pickup_intent`, enters pickup and records
`waiting_pickup`. Right before the pickup input the adapter must read the proven new epoch
again; another one, or none, fails the cycle with `epoch_changed`, so a second clear never gets
the pending pickup. A recognized prompt records `waiting_prompt` and its preceding stage, in
`waiting_pickup` too (a native approval of the successor's first calls); the response is
entered after its recorded `prompt_intent`, and one that is read back returns to that stage. A
dashboard answer whose approved call completed the cycle first counts as received. Just before the clear or the pickup input, the daemon checks the reported file
again: a changed or missing file returns a `ready` cycle to `waiting_ready`
(`artifact_changed`) under the current request's readiness deadline (that of the newest
repeat, #431), and fails a `reset_verified` one. An input that fails or is not received is `uncertain`. An intent found without its
receipt, as after a restart, is never sent again: a clear is reconciled only by a proven new
epoch, otherwise it is `uncertain`. Deadlines: 15 minutes from the request for
`waiting_ready`, two minutes for each other stage (`waiting_pickup` included, for the report),
ten for a prompt, and 30 minutes for the whole cycle; no stage deadline passes the cycle's. A
report at or after a deadline is `cycle_changed`. While a cycle is in `ready` through
`waiting_pickup`, ordinary wakes to its pane wait, still due and not delivered, so peer
messages after the ready report start no turn (#417). A `ready` stage whose deadline passes
while the pane was read and was not idle asks again (#431): in one ordinary transaction the
cycle returns to `waiting_ready` with reason `not_idle.<code>`, drops the reported file, gets a
readiness deadline 15 minutes from the repeat (within the cycle's), and `overwatch` puts a new
typed request with the new revision in the session's inbox; `waiting_ready` does not hold the
pane, so its wake goes out. The codes are `status_busy` (a turn in progress), `status_waiting`
(a native wait), `status_unknown`, `input_not_empty` (a draft), `input_line_not_found`,
`turn_running` and `turn_not_ended` (Codex), or `pane_not_ready`. A repeat refused because the
cycle changed, its Enable or pin ended, or the store is full ends the cycle as a timeout; the
lifetime, the event limit (`event_limit`) and the page credit end repeated requests. A `ready`
stage whose pane could not be read or proven, and a `bootstrap_verified` or `reset_verified`
stage, that times out names what the pane last showed after a dot: `ready_timeout.home_unknown`,
`.rollout_unreadable`, `.tmux_unavailable` (tmux could not run, #418), `.pane_unproven` or
`.pane_unreadable`, and for the later stages also the not-idle codes above, `.native_dialog`
and `.status_thread_differs`. After a daemon restart no inspection has run, so the timeout reads the pane
once without sending a key; a pane in copy mode then gives `.pane_in_mode`. The ready report
holds the wake lock, so a wake already being submitted ends before `ready` commits. The reason
is shown on the
Sessions page, in `koinon context show` and in the audit record. A `waiting_ready` timeout
says whether the session read or acknowledged its inbox through the newest request:
`waiting_ready_timeout.read` or `waiting_ready_timeout.unread` (#445). When the daemon refused a
ready report at the stage's revision, the reason names the newest refusal's code instead, for
example `waiting_ready_timeout.refused.cycle_artifact_invalid`, and a `cycle_timeout` in
`waiting_ready` adds the same `.refused.CODE` (#485). The Sessions page lists the refused
reports with their text; the audit record keeps the code. A timeout of another stage keeps its
plain `<stage>_timeout`.

A cycle's admission extends the session's live work claims in its repository's store to 60
seconds past the cycle deadline, in the admission's own transaction (#489): the claims whose
consumer is the session's peer name, its `FAMILY:ID` or its role-bound native work actor, and
those of its participant address when it held the address at admission. The claim's revision,
generation, renewal time and progress do not change, and an expired claim stays expired. While
the cycle is unfinished, `claim-renew` of such a claim, by the session or by its managed
successor, keeps the longer of the new and the current expiry. A longer expiry stays after the
cycle ends; the pickup procedure renews the claims as it reconciles them.

A new native epoch of the requester before the managed clear (`waiting_ready`, `ready`, or a
stored `bootstrap_intent` or `bootstrap_verified`) cancels the cycle (`epoch_changed`; uncertain
in `bootstrap_intent`, whose input is unproven). Disable, a family mode without the cycle, retiring or purging the
requester or successor, deleting it and the maintainer's holder choice for its address end
the cycle in their own transaction. A cycle that ends after an input whose effect is not
proven (an intent, or `clear_received` without a proven reset) is `uncertain`, with the
reason of whatever ended it. Failed, cancelled and uncertain cycles end the session's Enable; a
later cycle needs a new Enable and a new handoff. Admission funds every remaining transition:
a credit of 4,411 pages covers 14 stages of 280 pages and a terminal outcome of 491. Each
stage, and each audit record it writes, is a control write that debits 280 pages; the terminal
outcome and these controls release the rest. The page debt includes every unfinished cycle's
remaining credit ([derivation](docs/CONTEXT-CYCLE-STORAGE.md)). Finished cycles are kept for 30 days, at most
the newest 1,024, with their events; unfinished cycles are never removed.

### Claude Code cycle adapter

Chunk 03 adds the adapter for Claude Code (version `claude-tmux-1`). It acts only in the
session's own tmux pane, on the stored socket and pane ID. Before every key it proves the pane
again (the host's PID and start, the pane's process ancestry, no other agent between them) and
rechecks, in one read transaction, that the cycle is still at its revision and stage, before its
deadlines, under the Enable that admitted it. A failed check stops the input before that key.
Typed text that is not submitted is erased again with one Backspace per typed character (the
input line was empty before). The erase passes the same proof and fence and runs only while the
input line still holds exactly that text: after a rejected fence, or over another draft, the
input line stays as it is. A failed step is recorded as
the cycle's reason (`typed_not_read_back`, `escape_changed_input`, `submit_not_read_back`,
`pickup_entry_not_listed`, `erase_not_read_back`, `pane_unproven`, `tmux_unavailable`).

Probe decides whether a session's cycle can run, for Enable, at admission and before each
input. It needs Linux or macOS (`platform_unverified` elsewhere); the host's
own Claude Code registry record (`sessions/<pid>.json` under the host's `CLAUDE_CONFIG_DIR`, else
`~/.claude`, read from the host's initial environment) for the same process start
(`native_registry_unavailable`; on macOS the record's start is `ps -o lstart` text in UTC, compared
to the whole second), with any Claude Code version (PDR 006, Decision 5; the native checks ran with 2.1.296);
the pane (`pane_unproven`, or `tmux_unavailable` when tmux could not run); and the
cycle's pickup entry, the Koinon-owned skill `koinon-pickup` (#406):
`skills/koinon-pickup/SKILL.md` under the host's configuration root. It must be byte-equal to
the binary's skill: a missing file is `pickup_entry_missing`, another content
`pickup_entry_differs`, a file that cannot be read `pickup_entry_unreadable`, and an unknown
host home `pickup_entry_unverified`. Each of the first three comes with the step "run koinon
install to install the koinon-pickup skill at PATH", which the Sessions page and `context show`
(`unavailable_step`) show beside the reason. Personal and project `pickup` skills, and legacy
commands of that name, are not read and take no part in the cycle. The Sessions page shows
Probe's reason, or `pickup through koinon-pickup`.

The skill's body only reads `skill.pickup` with `library_read` and requires its catalog digest
to equal the cycle's `catalog_digest`, which `context_cycle_status` returns; it holds no
procedure of its own. The daemon writes it at each start, from its own environment
(`$CLAUDE_CONFIG_DIR`, else `~/.claude`, and `~/.agents/skills`, and OpenCode's and agy's
configuration directories when they exist), when it is missing or holds other content, and
`koinon install` writes it for the person who runs it; neither writes into a repository (see
[INSTALL.md](docs/INSTALL.md)). The install report lists it in `pickup_skills` and the other
skill entries in `team_skills`, one row per file with `family`, `name` (for an entry), `path`
and `result`: `created`, `replaced` or `unchanged` for an install, `removed` or `absent` for an
uninstall, and `kept` for a file with other content at uninstall or a symbolically linked entry
directory or file, which neither writes through. A write the daemon cannot make is logged
(`pickup_skill`, `write_failed`) and does not stop it. `koinon claude` adds no directory to a launch,
and the cycle adds none to a running session.

The adapter is ready for input when the registry `status` is `idle` or `shell` and the input
line (`❯` followed by a no-break space) is empty. `shell` is the prompt with a background shell
task running, such as a monitor of the session's inbox; Claude Code 2.1.303 takes `/clear`
there without a dialog, and the task, with its `shell` status, stays through the clear (#417).
`busy` (a turn in progress) and `waiting` (a native wait) are not ready; text drawn dim there is a placeholder or a suggestion,
any other text a draft, and the cycle waits. A pane in copy mode is returned to its prompt
first. The clear and the pickup type the command, read it back (for `/koinon-pickup`, the
menu's first entry must have the skill's description, with or without a scope such as
`(user)` after it), close the menu with Escape, wait 400 ms (an escape byte
followed at once by Enter reaches Claude Code as Alt+Enter, which does not submit), read back
that the input line holds exactly the command with the menu closed, and submit with one Enter. The reset is proven by a new registry `sessionId` for the same host process; Koinon
registration is unchanged, because the MCP process keeps its caller across `/clear`.

The adapter answers no dialog itself. A dialog (`native_dialog`) puts the cycle in
`waiting_prompt`, and the Sessions page shows its text and
numbered options. Before entering a dashboard option the adapter proves the pane, passes the
fence and reads the dialog again. The session's native permission settings, or the maintainer in the session,
may answer it; the cycle returns to its stage once it is gone, or the prompt wait ends it. The
dashboard action `context-cycle-answer` (`cycle_id`, `revision`, `digest` of the dialog as shown,
`option` 1 to 9) records `prompt_intent` with the action's audit record, enters the option only
while the pane still shows the same dialog, and records the return to the cycle's stage. A
changed dialog before any key returns the cycle to `waiting_prompt` (`cycle_dialog_changed`); an
unproven answer is uncertain. Enable starts the managed cycle; the native permission interface
handles native permission choices.

### Codex cycle adapter

Chunk 04 adds the adapter for Codex (version `codex-tmux-1`), on the facts of Codex CLI 0.162.0
([chunk 04](docs/sprints/2026-10-12-context-handoff/chunks/04-codex-cycle.md)). It acts only in
the session's own tmux pane. Before every key it proves the pane and that the pane's terminal
gives input to the host (as the Codex pane wake below), and passes the cycle's fence. Typed
text that is not submitted is erased with Backspace while the composer still holds exactly it.

Probe needs Linux or macOS (`platform_unverified` elsewhere); the session
thread's own rollout under the host's `CODEX_HOME` (else `~/.codex`), whose first record is the
thread's `session_meta` from the interactive CLI (`native_registry_unavailable`), with any
`cli_version` (PDR 006, Decision 5; the native checks ran with 0.162.0, 0.162.1 and 0.163.0); the pane
(`pane_unproven`, or `tmux_unavailable`); the session's participant address held (`participant_unheld`: the thread
after `/clear` is a new session that reports only as the address holder); and the pickup
entry, the `koinon-pickup` skill in the host's `~/.agents/skills` (#406), a root that Codex
lists for every session. It is checked as for Claude Code (`pickup_entry_missing`,
`pickup_entry_differs`, `pickup_entry_unreadable`, each with the install step). Other skills,
a project's own `pickup` included, are not read. Codex turns a skill off only through
`skills.config` entries, so Probe reads the effective entries of the host's configuration as
TOML, from the lowest layer to the highest: `/etc/codex/config.toml`, `$CODEX_HOME/config.toml`,
the profile that the host's `-p`/`--profile` selects (`$CODEX_HOME/<name>.config.toml`, on top
of the user file; a selected profile without its file is `pickup_entry_unverified`), the
host's own `-c`/`--config` arguments and `/etc/codex/managed_config.toml`. Tables merge, and any
other value of a higher layer replaces the lower one. A project's `.codex/config.toml` loads, but
its `skills.config` does not change the skills that the launched Codex lists, so it takes no
part. An effective entry, or an entry
in `/etc/codex/requirements.toml`, whose
`enabled` is not `true` and that names `koinon-pickup`, the checked file or its directory, or
a path that does not resolve, gives `pickup_entry_unverified`, as does another value on that
path; a layer that does not parse gives `pickup_entry_unreadable`. The Sessions page shows
`pickup through koinon-pickup`.

The adapter is ready for input when the composer (`›`) holds no text, no running turn shows,
and the current thread is proven idle from its own rollout, read at that moment; each input
step reads this again right before its first key, and a draft that someone started is left as
it is: before the
clear the requester's last turn ended; after it the pane's `/status` card names the cycle's
successor, which has no rollout and no prompt after the card, or whose last turn ended. The
clear types `/clear`, reads it back and submits it with one Enter; the cleared screen is the
receipt. In the same step it enters `/status` the same way: Codex writes nothing for a new
thread before its first turn, and the card names it. The reset is proven by that card's
`Session:` thread, read from the pane and its history without input, below the latest
banner with nothing of a turn before it. The pickup checks the skill file again, types
`$koinon-pickup` and submits it only when the open skill menu is complete and lists exactly
one `koinon-pickup` row, so no other entry of that name can run; then Escape, a 400 ms wait,
the read-back and one Enter. A dialog (an MCP tool approval or a command approval) is never answered by the
adapter: the cycle shows it in `waiting_prompt`, and a dashboard option is one digit, entered
only while the same dialog shows.

### Verified succession

Schema 13 adds a private host record to each session: host PID, process start value,
tmux socket and pane, plus its last `succession` result. Existing records start with no host
evidence; their owners, messages, acknowledgements, cursors and claims remain unchanged.
The daemon reads the host start through `internal/platform.ProcessStart`: Linux `/proc` field
22 or macOS `kern.proc.pid`, without cgo. A PID with a different start identifies a different
process; an inspection error proves nothing. The host is the launch's process, or the Claude
background job's process reported by its MCP server.

Registration accepts `tool_call` (default false), `clear` (default false) and optional
`tmux: {socket, pane, executable}`.
The socket is an absolute clean path without control characters, at most 4,096 bytes; the
pane matches `%` followed by 1–9 digits. The optional `executable` (#418) is the absolute tmux
that the session's own terminal runs, with the same path rules as the socket. Invalid values
return `invalid_request`.

Schema 32 adds the table `change_versions` of the maintainer mode's change notices (PDR 009,
Decision 3): one row each for `inbox` and `decisions` and one for each room by its ID, each with
a 20-digit counter as text. The upgrade starts a row at `00000000000000000001` for the
maintainer's inbox, the decision board and each existing room; a room's creation inserts its
row and its deletion deletes it. Each write that changes what the maintainer's read of a topic
shows moves its counter in the same transaction: an inbox message to the maintainer, the daemon's
notices, a work notice to the maintainer, an acknowledgement of the maintainer's inbox and the
retention of its messages; a decision create, edit (also a reopen), verdict, close, restore and
the move to the deep archive; a room's add, remove, post and post retention. A write that rolls
back, a retry that writes nothing and a refused write move nothing. The counter has a fixed
width, so an increment overwrites it in place and allocates no page, also in a control write.

Schema 31 adds `decision_verdicts.via`, the maintainer client that gave each verdict
(`dashboard` or `mcp:NAME`, PDR 009, Decision 4); a verdict from before it has `''`.

Schema 29 adds the table `maintainer_secret` (see Dashboard and session observations, PDR
009); an upgrade starts with no secret set.

Schema 28 adds `sessions.tmux_path`, which stores `executable` with the socket and pane and is
cleared with them; existing sessions start with `''`. It is a tool path, not identity: an Enable
and a cycle pin the socket and pane, and read the session's `tmux_path` when they act. `koinon
mcp` looks `tmux` up on its own `PATH`, which is the agent's, and reports the absolute path
with the pane; a path that is not absolute and clean is left out. The daemon runs it for the
session's pane proof, pane wake and cycle input only after it resolves every symbolic link to
a regular file with an execute bit, owned by root or the daemon's user and not writable by its
group or others. It reads no shell configuration and guesses no installation directory. Without
a recorded path it runs `tmux` from its own `PATH`. A recorded path that fails the check, a tmux
that is not found, and a tmux that cannot start give the fixed reason `tmux_unavailable`, apart
from a pane that tmux answered for but that does not hold the host. `koinon mcp` sends the
path at each registration: the session's first Koinon tool call, a tool call after its record
expired or was retired, and the renewal of an expired record. An ordinary renewal carries no
pane. A session whose `koinon mcp` started before the upgrade has no path until it is restarted
(docs/INSTALL.md, System mode on macOS).
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
longer shows the holder.

A managed cycle's reset links the holder to one successor (#196, chunk 04). After the fenced,
sub-agent, host and other-participant checks and before the guard, a candidate succeeds with
`cycle_reset` only when, in the same transaction, an unfinished cycle of this address has the
holder as its requester, is pinned to the candidate's host PID, start value, tmux socket and
pane, has proven the candidate's ID as its new native epoch (a Codex thread's ID is its epoch),
is in `reset_verified`, `pickup_intent` or `waiting_pickup` (or a prompt wait that began in
one of them), still has the Enable that admitted it and is before its stage and cycle
deadlines. This is not a general address move: every other candidate keeps the rules above. The hook has no terminal, so a cleared thread that registers without
a pane keeps the holder's recorded tmux socket, pane and tmux path (each wake proves it again). Every
other rule applies unchanged. Codex runs no hook at `/clear` itself, so until the new thread's
first turn the cleared thread stays the holder (#275). On Linux and macOS a participant notice
for a launched holder with a tmux pane is typed into that pane (see the Codex pane wake
below), so it runs as the new thread's first turn.

The session and `peers` expose `succession: {result, reason, address, holder, at,
retry_after_ms}` when present; `at` is daemon Unix milliseconds and `holder` is the former or
retained holder's peer name. Success reports `result: succeeded` with `same_host`,
`host_cleared`, `cycle_reset` or `same_pane`. Refusal reports `result: refused` with `no_host`, `host_running`, `other_pane`,
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
  it, or `maintainer`) and `acknowledged_via` (`ack`, `participant_ack`, or `maintainer` for
  an acknowledgement by the maintainer). Agents do not see the maintainer's client (PDR 009,
  Decision 4); the dashboard and the maintainer mode show it as `dashboard` or `mcp:NAME`.
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
(`invalid_request`); only the dashboard and a maintainer token (see Maintainer mode) send,
read and acknowledge as the maintainer.
The daemon itself puts notices in the maintainer's inbox with `sender_name` `koinon`: today
only the notice that messages failed (see Handled wakes). Such a notice holds fixed text,
message IDs, a recipient name and reason codes, never peer text.

A message that the dashboard or a maintainer client sends as this session reaches the
recipient with `sender_family` `maintainer` and `sender_name` `maintainer`. Because `/v1/`
refuses family `maintainer` as a caller, no agent MCP tool or other `koinon` command can send a
message with that `sender_family`; only the dashboard, after a login, and `koinon mcp
--maintainer`, with the maintainer secret, can. The field is set from the sending
session, never from the body. Koinon currently treats such a message as the maintainer's
instruction and approval, because the bridge runs only locally (PDR 005, Decision 3). A message
from every other session comes from a teammate agent: the recipient does the work it asks for
and replies through Koinon (#487). The MCP server instructions, the `inbox` tool description and
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
execution or timeout category without returning command stderr or machine paths. Its Git
calls for one request share a six-second limit. A `repository_git_timeout` refusal gives
`details.calls_ms`, the time of each Git call of the request in order, and `details.limit_ms`.
Invalid-input/repository validation remains unchanged. Mutations commit before replying. A lost reply does not prove rollback;
read the retained record before retrying.

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
Codex context reset. They are stored state until the agent registers and wake adapters
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
the configured absolute CLI with `queue --thread ID --message NOTICE`, except for the pane
wake below. Claude requires a
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
`grok_target_unavailable` and sends nothing; when tmux could not run (see Verified succession),
it waits with `tmux_unavailable`. It then reads the pane and types only when Grok
is idle: every line of the last input box (`│ ❯` and its bordered continuation lines) is
empty, no line is the running-turn status (a spinner glyph that ends with `[stop]`), and no
line below the box shows a cancel shortcut (Grok Build 1.0.50 keeps the box empty during a
turn; conversation text that mentions `[stop]` does not count). Otherwise it waits with `receiver_busy`. It sends the notice with `send-keys -l` and
then Enter, nothing else, and records `notified` only when a second read shows the box empty
and the notice as a submitted prompt (`❯ NOTICE`); otherwise `uncertain` with
`pane_submission_unconfirmed`. A directly started Grok session has no host record and gets
no pane wake. Leader mode stays refused.

Codex (#275): after `/clear`, `codex queue --thread` reaches the cleared thread, which the host
no longer shows. On Linux and macOS, a participant notice for a session started with
`koinon codex` and a recorded tmux socket and pane is typed into that pane instead. Before each
key the daemon proves the pane as for Grok and proves that the pane's terminal gives input to
the host: the host shares the pane process's terminal and is in its foreground process group,
and every other member of that group is an ancestor of the host. A child of the host that holds
the foreground is refused. Otherwise the wake waits with `codex_target_unavailable`, or with
`tmux_unavailable` when tmux could not run (#418); the message stays queued and is tried again. Codex CLI
0.162.0 shows no busy marker while a reply streams, so the wake also requires the session's
observed activity to be `idle`. An unknown or busy activity waits with `receiver_busy`, as does
a screen that shows the running-turn status (`Working (… esc to interrupt)`) or whose input
area holds text: any text after the mark (`›`) other than the dim placeholder, or on any row
from there down to the footer, the last run of at most two rows after a blank row. A screen
without that footer counts as busy. Each attempt sends the notice once with `send-keys -l`,
reads the input back, and sends Enter once, only when the input holds exactly the notice. It
records `notified` only when a later read shows the input empty and the notice as the latest
submitted prompt; otherwise `uncertain` with `pane_submission_unconfirmed`. The read wait and
retries above apply unchanged: a message still unread after its read wait gets a new attempt. A
session inbox notice, a session without a pane and a direct start keep the queue.

The `codex queue` command has 3 seconds, or less when the session expires sooner. Exit 0 is
`accepted`. A normal non-zero exit is `waiting` with `queue_failed`: the queue did not take the
notice, so it is not counted as a notice and is retried with backoff. The time bound is
`uncertain` with `queue_timeout`, output still held 100 ms after the exit `queue_drain`, and any
other wait error, a signal among them, `queue_unconfirmed`; these may have queued the notice and
count under the notice limit, and an exhausted message keeps that code in its wake reason. Each
failure writes a `codex_queue` daemon log event with `reason`, `exit_status` (-1 when there is
none) and `detail`: the first line of the command's stderr, cut to 256 bytes. The notice (also
escaped) becomes `<notice>` and the thread ID `<thread>`. Of the other words, only a plain word
of lower-case letters (one capital first, joined by `-`, `/` or `_`) and a number of up to six
digits are kept; any other word becomes `<private>`. A word that names a credential, such as
`password`, `client_secret`, `token`, `key` or `Bearer`, ends the kept text: the rest of the
line becomes one `<private>`. No provider text is stored (#466).

Before a `codex queue` wake of a launched Codex session that holds its participant address, the
daemon checks that the host still shows that thread (#275). Codex CLI 0.162.0 with
`--no-daemon`, as `koinon codex` starts it, starts a new `koinon mcp` server under the host at
each `/clear`, before the new thread's first turn, and for a sub-agent thread. The daemon reads
the children of the session's recorded host process (PID and start value), keeps those whose
executable is `koinon` with first argument `mcp`, and reads when each started (Linux:
`/proc/<pid>/stat` start ticks against `/proc/uptime`; macOS: `p_starttime`). A server that
started after the holder's latest proof, at the same time or up to 1 second before it (the
start read can be early by that much), makes the wake wait with `codex_thread_unproven`, so no
turn runs in a thread that the host may no longer show. The proof is the latest of the holder's
first registration, its last tool call, and its last `codex_rollout` activity event less 2
seconds (a turn that `/clear` ends can record its end at about the time the new server starts).
While the wait lasts, a cleared thread runs no turn, so a later event or call proves the
holder; a new thread that takes the address (`host_cleared` or same-host succession) ends the
wait too. A process table, host, child command or server start that cannot be read also waits,
unless the child has ended. A sub-agent's server starts within the holder's turn, while the
wake waits for the turn anyway; one started in the last 2 seconds of that turn delays the wake
until the holder's next call or turn. Tool-call stamps and observations are kept in memory:
after a daemon restart the proof is the first registration until the session's server reports
the rollout again. A session that holds no participant address keeps the queue.

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

The command line marks and changes sessions in bulk (#388). `POST /v1/cleanup/sessions` takes
`action` (`list`, `purge`, `unpurge`, `retire` or `clear`), a selection (`names` as peer
names, `state` of `active`, `expired`, `retired` or `purge-marked`, `family`,
`inactive_for_seconds` by the retention rule) and `apply`. A change without a selection is
`invalid_request`; an unknown name is `peer_not_found`; the maintainer session is never
selected. It returns at most 1,000 `sessions` (`name`, `family`, `id`, `state`,
`inactive_since`, `unacknowledged`, `live_claim`, `purge_marked`, `revision`) with
`truncated`, narrowed to those the action applies to (retire: active; purge: unmarked;
unpurge: marked). With `apply`, each runs the dashboard action's store path at its revision
and writes its audit record by `cli`; `results` lists each `name`, `result` and refusal
`code`. `POST /v1/cleanup/claims` (`held_by` peer names, `apply`) lists at most 1,000 live claims
held by expired or retired sessions, by the sweep's live-claim rule (`holder`, `repository`,
`work_id`, `title`, `revision`, `generation`, `consumer`); a participant's claims are not
listed. With `apply`, each runs the dashboard's `release` as the owner, with the checkpoint
`Released by the maintainer from the command line`, audited by `cli`.
`POST /v1/cleanup/rooms` (`room_ids`, `apply`) lists the current members whose session is
expired or retired (`room_id`, `room`, `ordinal`, `label`, `state`); a participant member is
not listed. With `apply`, each is removed as the dashboard's `room-remove`, audited by `cli`.
`POST /v1/rooms/delete` (`room_id`) deletes a room as the dashboard's `room-delete`, audited
by `cli`; an unknown room is `room_not_found`.
`POST /v1/maintenance/sweep` runs the work, liveness and retention sweeps once, never
alongside the scheduled one, and returns the retention `counts` and `marked_for_purge`.

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
plus retained memory catch-up for changes between pages, without resetting a cursor.
Work-change notices are coalesced (Work notices, above); after a notice, reconnect or missed
wake, current queries and memory catch-up recover the changes.

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
1,024 UTF-8 bytes). It sends an ordinary `checkout_writer_request` JSON message to
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
The agent adopts an item with its normal editing tools (PDR 008).

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

Machine `library_decision` accepts item ID, adopted/declined choice, evidence text,
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
Adoption updates the adopted baseline; decline keeps it. Records for removed items stay as they are.

Project decisions use existing `memory_record`/`memory_recall`, with their body limit,
fixed retry deadline, replacement/conflict and retention semantics. A repo-scope `decision`
body is strict version-1 JSON marked `library: "koinon-library-decision"`: item/family/scope,
scope/catalog/item digests, kind, choice, evidence text, and typed pre-edit/result/adopted
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
history never silently resets a decline. Receipts record reported outcomes.

In the #145 procedure the agent applies the item with normal editing tools, verifies hashes
and then records its outcome. Sidecars and
discovery pointers are separate proposed changes. A failed receipt leaves the files applied
and an explicitly unrecorded outcome. No state/decision interface writes adopted files.

### Rooms

Schema 19 adds dashboard rooms (#217, chunk 01). The maintainer creates a room and changes its
roster in the dashboard. Schema 20 adds room posts (chunk 02, below). A room has an opaque 32-character
`room_id`, a display `name` of 1–128 bytes of UTF-8 (names may repeat) and a roster
`revision`. Members post without an allowance (PDR 005, Decision 2, deprecated by #493). The
`rooms` table keeps its `refill`, `allowance` and `create_refill` columns from schema 19;
nothing reads them.

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
participant's inbox. The write also stores the poster's retry record and, for a dashboard post, the audit record. It commits completely or not at all;
wakes follow through the existing queue and are content-free. A copy is an ordinary inbox
message from the poster with the full body and `room: {room_id, event: "post", seq, origin}`.

A member posts without an allowance and without a maintainer post between rounds (#493).
Koinon does not write replies.

`POST /v1/rooms/post` takes `caller`, `room_id`, `body`, `key` (1–256 bytes) and `deadline`
(Unix seconds, after now and at most 24 hours ahead). The caller must be active and a current
member, checked before its retry record, so an accepted earlier post grants nothing after
removal. The same key with the same room, body and deadline returns the first receipt with
`duplicate: true` and writes nothing; other input is `idempotency_conflict` (409), an
expired deadline `retry_deadline_expired`. It returns `{ok: true, result: {room_id, seq,
queued, skipped, duplicate?}}`; the receipt names no recipient. The daemon
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

### Decision items

Schema 25 adds three ordinary tables (#206, chunk 01): `decision_items`, `decision_recipients`
and `decision_replays`, with the indexes `(state, kind, id)`, `(state, id)` and
`(state, archived_at, id)` on items and `(name, item_id)` on recipients. An item is an
agent-written record on the maintainer's decision board. Agents get decision status only
through these routes, never from the dashboard page; every reply is a JSON record, and a page's
work depends on its limit, not on the number of stored items. There is no limit on the number
of items. Reads move no inbox read or acknowledgement marker and no memory cursor.

- `POST /v1/decisions/create` takes `caller`, `reference` (1–32 characters of `A-Z`, `a-z`,
  `0-9` and `-`, unique), `title` (1–256 bytes), `kind` (`decision` or `fyi`), optional
  `order` (integer), `where` (256 bytes), `agent_text` (8 KiB), `context` (16 KiB),
  `recommendation` (8 KiB), `commands` (at most 8) and `recipients` (at most 32 names of at
  most 256 bytes), and `key` (1–256 bytes) with `deadline`. A new item is `active` at
  revision 1. A retry with the same key and request before the deadline returns the first
  result with `duplicate: true`, also when the store refuses new writes; the same key with
  another request is `idempotency_conflict`, a passed deadline `retry_deadline_expired`, and a
  deadline beyond the idempotency period `invalid_request`. A used reference is
  `reference_in_use`. The result has `reference`, `revision`, `state` and the recipients'
  resolution. Expired retry records are deleted by the maintenance sweep in bounded control
  deletions.
- `POST /v1/decisions/edit` takes `caller`, `reference`, `if_revision` and at least one
  field to change; a list field replaces the stored list. Another revision is
  `stale_revision` with `current_revision`. An edit of an `in_progress` or `archived` item
  reopens it: the item becomes `active`, its current verdict gets `superseded_at`, in the
  same transaction; the verdict's deliveries do not change. The result is the full item at
  its new revision.
- `POST /v1/decisions/list` takes `caller`, `filter` (`waiting`: active `decision` items;
  `active`, `in_progress`, `archived`; or `mine`: items with a recipient name that is now the
  caller's peer name or the participant address it holds, also when the name did not resolve
  at the write), optional `after` (an item ID) and
  `limit` (1–100, default 100). Items come in item ID order, keyset-paged: a page holds at
  most `limit` summaries and stops before the summary that would take the serialized reply,
  with its envelope and JSON escaping, past 64 KiB; it always holds at least one. It returns
  `items` (`reference`, `title`, `kind`, `state`, `revision`, `order`, and `verdict` when
  the item has a current one), `more` and
  `next_after`. An item does not appear twice in a walk; an item that matches during the
  whole walk appears once; an item whose match changes during the walk appears at most once.
- `POST /v1/decisions/get` takes `caller` and `reference` and returns every field with
  `state`, `revision`, `author_family`, `author_name`, times, the last close (`closed_by`,
  `closed_at`, `outcome_note`), the current `verdict` with its snapshot and deliveries (or
  `null`) and `verdicts`, the number of verdicts in its history. A deep-archived reference
  is `deep_archived` with `details` `moved_at` and `restore`. An unknown reference is
  `not_found`. Recipients are resolved again on each read: `kind` `participant` (an address)
  or `peer` (a peer name), or `resolved: false` with `reason` `unknown_name`.

Each request field is checked on every write: valid UTF-8 within its byte limit; no control
character in `title`, `where`, `reference`, `key` or a recipient; in `agent_text`, `context`
and `recommendation` no control character other than newline and tab. A terminal-copy
command is 1–512 bytes of printable ASCII (0x20–0x7E), so a line break, tab, DEL, zero-width,
bidirectional or non-ASCII look-alike character is refused; a stored command that fails this
rule on read is left out of `commands` and counted in `hidden_commands`. Any other field,
such as a verdict, dismissal, audit or agent-row field, is `invalid_request`. Known
credential formats are refused in every text field with `credential_refused`: a
`-----BEGIN ... PRIVATE KEY-----` block, GitHub tokens (`ghp_`, `gho_`, `ghu_`, `ghs_`,
`ghr_` with 36 or more characters, `github_pat_`), AWS access keys (`AKIA` or `ASIA` and 16
characters), Slack tokens (`xoxa-`, `xoxb-`, `xoxp-`, `xoxr-`, `xoxs-`, `xapp-`) and
Anthropic-style (`sk-ant-`) and OpenAI-style (`sk-`) API keys with 20 or more characters.
The daemon cannot find all credentials or personal data; authors leave them out.

Schema 26 adds verdicts, deliveries and the deep archive (#206, chunk 02): the tables
`decision_verdicts` (index `(item_id, id)`), `decision_deliveries` (index
`(name, outcome, verdict_id)`) and `decision_deep_archive` (keyed by reference), the column
`messages.decision_verdict`, and the item columns `closed_by`, `closed_at`, `outcome_note`,
`mark_revision`, `mark_file`, `mark_offset` and `verdict_count`. `verdict_count` holds the
number of verdicts in the item's history, so `decision_get` reads it and the current verdict
(the latest verdict, unless it is superseded) without a scan of the history.

**Verdicts.** The maintainer gives a verdict on the dashboard (see Dashboard actions) or with a
maintainer token (`decision-verdict`, see Maintainer mode); the agent routes, agent MCP tools
and commands have no verdict. The verdict stores the client that gave it as `via`. Approve and Decline apply to a `decision` item, Dismiss to an
`fyi` item; another combination is `invalid_verdict`, a revision other than the one the page
showed is `stale_revision`, an item that is not `active` is `decided`, and an item with a
hidden command cannot be approved (`hidden_command`); Decline and Dismiss still apply. An optional note is Markdown of at
most 4 KiB with the credential check. One ordinary write stores the verdict, a snapshot of
the item's title, context, recommendation and commands at that revision, the item's new
state (`in_progress` after Approve; `archived` after Decline or Dismiss), and for Approve and
Decline one delivery per named recipient, resolved now, and the audit record with target
`decision REF revision N VERDICT verdict ID` (no card text); a failure of any part stores
nothing. A delivery to a participant address is `queued` with one message in the
participant inbox, read by its current holder also after a holder change; to an active
peer it is `queued` with one message in that session's inbox. Otherwise it is `skipped`
with `reason` `unknown_name`, `maintainer` or `recipient_inactive`. The message comes from
`maintainer` with `system_kind` `decision_verdict`, the typed `decision_verdict` ID and the
fixed body `Decision REF has a verdict (ID). Read it with decision_get.` A wake whose range
holds such messages ends its notice with ` Decision verdicts ID, ID.`: at most 8 IDs in
sequence order, then ` +N more`; target selection, coalescing, retries and inbox reads and
acknowledgements do not change.

- `POST /v1/decisions/history` takes `caller`, `reference`, optional `after` (a verdict ID)
  and `limit` (1–16, default 16) and returns `verdicts` (`verdict_id`, `revision`,
  `verdict`, `note`, `at`, `superseded_at`, `snapshot`) in verdict ID order with `more` and
  `next_after`. It never returns `via` (PDR 009, Decision 4). A page stops before the verdict that would take the serialized reply past
  256 KiB and always holds one: the largest verdict, every snapshot and note field at its
  limit in characters with the longest JSON escape, serializes below that.
- `POST /v1/decisions/pending` takes `caller`, optional `after` (a verdict ID) and `limit`
  (1–100) and returns the caller's `queued` deliveries (its peer name and the participant
  address it holds) as `deliveries` (`verdict_id`, `reference`, `verdict`, `revision`, `at`,
  `superseded_at`) with `more` and `next_after`, also after the inbox message failed or aged
  out. A reopen does not acknowledge a delivery; `superseded_at` shows it.
- `POST /v1/decisions/ack` takes `caller` and `verdict_ids` (1–32) and sets the caller's
  queued deliveries of those verdicts `acknowledged` with the time; it returns
  `acknowledged`, the IDs it changed. A repeated or late acknowledgement changes nothing. It
  is an ordinary write: at the ordinary storage ceiling it is `capacity` and the deliveries
  stay queued. Delivery and acknowledgement record only that.
- `POST /v1/decisions/close` takes `caller`, `reference`, `if_revision`, optional `note`
  (Markdown, 4 KiB), `key` and `deadline`, and moves an `in_progress` item to `archived` with
  the caller's peer name, the time and the note; another state is `not_in_progress`. A retry
  with the same key returns the item as this close returned it, also after a later edit or
  verdict. Create and close keep separate key spaces; create keys are unchanged from schema
  25, so a create receipt from before the upgrade still answers its retry.

**Deep archive.** Nothing deletes a decision record. 30 days after an item was archived,
once every delivery is `acknowledged` or `skipped`, the maintenance sweep moves its whole
record (item, recipients, verdicts with snapshots, deliveries) to one JSON line in
`STATE/decisions/YYYY-MM.jsonl` (mode 0600, the month of the move). A pass moves at most 64
items and reads at most 256 candidates from a cursor that passes items still waiting for an
acknowledgement. Only the sweep writes the log. It marks the item with its revision, file
and offset, appends and syncs the line, then in one transaction deletes the rows and writes
the locator (`reference`, `file`, `offset`, `length`, `moved_at`); the delete runs only while
the item still has the marked revision and mark, and an edit or verdict clears the mark. A
file that does not end in a newline gets one before the next line, so a partial line from a
crash is a line that readers skip; no byte of the log is removed. After a crash a marked item
is checked against its line: a complete line finishes the delete, otherwise the line is
written again at the end. The mark and the delete are ordinary writes; at the ceiling the
item waits for a later pass. A moved reference stays reserved: `decision_create` refuses it
with `reference_in_use`.

- `POST /v1/decisions/restore` takes `caller` and `reference`, reads the one line at the
  locator, checks its reference and length, and inserts the record again as `archived` with
  a new 30-day period, removing the locator, in one transaction; the line stays in the log.
  An unknown reference is `not_found`; a line that does not match is `decision_unavailable`.

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
`decision_create`, `decision_edit`, `decision_list`, `decision_get`, `decision_history`,
`decision_pending`, `decision_ack`, `decision_close` and `decision_restore` take the fields of the
decision routes, refuse any other argument by name and call those routes.
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

The `initialize` response declares `capabilities.tools.listChanged` and gives the release as
`serverInfo.version` (`dev` for a build without one); a successful `peers` result adds it as
`server_version`. The server checks the path it was started by, made absolute (through `PATH`
for a bare name), every 5 seconds once `initialize` has been answered, with the daemon's test:
the path resolves to another regular executable file than at the start. It does nothing while
a request is being answered, while its read buffer holds a byte or while standard input has one
to read; a byte that arrives later stays in the pipe for the new program. It then waits for its
renewal and observation loops to finish their current daemon call, resolves the path's links to
a file and runs `<file> version` for at most 10 seconds. Only when that prints
`"mcp_resume": "1"` and the file is still the same file does it execute that file in its own
process with the same arguments and environment and `KOINON_MCP_RESUME`, a JSON object with
`v` 1, the agreed `protocol_version`, the `client` name and `capabilities`, the registered
`sessions` (`family`, `id`, `revision`, `retry_after_ms` left of a `holder_active` wait),
`latest`, `renewal_seconds`, `subagents` and the `claude_host_start` that `initialize` pinned. The new program removes the variable from its
environment, restores that state, renews the latest session at once and sends
`notifications/tools/list_changed` before it reads a request. State that it cannot read, or
that has no protocol version, starts it as a new server with one line on standard error. A
version command that fails or reports another `mcp_resume`, or an exec that fails, keeps the
running program with one line on standard error and its loops running again; that file is not
tried again, and the next change is. `koinon version` reports the state version that a program
reads as `mcp_resume`.

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
  seconds, and at most 8 are outstanding. Its use starts a session: the cookie
  `koinon_dashboard_PORT`, named after the port in `Host` so that each port keeps its own login
  (#454), is `HttpOnly`, `SameSite=Strict`, `Path=/dashboard/`, has no `Domain` and has
  `Max-Age=604800` (7 days), so it survives a browser restart. A session ends after 24 hours without a request,
  7 days after it started, at logout, when 16 newer sessions exist, or when the daemon restarts
  (#435). The daemon keeps only SHA-256 hashes of tokens and session IDs, in memory.
- **Maintainer secret** (PDR 009). `koinon maintainer secret set|generate|clear` sends
  `POST /v1/maintainer/secret` with `{"action": "set"|"generate"|"clear", "secret": TEXT}`;
  `secret` is given only for `set` and is valid UTF-8 of at least 12 characters and at most 1024
  bytes, else `invalid_request`. The reply is `{"ok": true, "action": ..., "logins_ended":
  true}`, and for `generate` also `secret`, 32 random bytes as base64url without padding, which
  nothing else returns. `clear` without a secret is `maintainer_secret_unset`. Schema 29's
  table `maintainer_secret` holds at most one row: `pbkdf2-sha256`, a 16-byte random salt, the
  iteration count (600,000) and the 32-byte PBKDF2-HMAC-SHA256 key. Each accepted change writes
  the audit record `maintainer_secret` with `set by cli`, `generate by cli` or `clear by cli` in
  its transaction, then ends every dashboard session. With a secret set, `GET
  /dashboard/login` without `token` shows a form with one password field `secret`; without
  one, it says how to get a link or set a secret. `POST /dashboard/login` takes that form, at
  most 4,096 bytes, with the request protection below but no CSRF token, since no session
  exists yet. The daemon checks one secret at a time and compares keys in constant time. A
  match writes the audit record `maintainer_login dashboard accepted`, then starts the same
  session and cookie as a link (303 to `/dashboard/sessions`); a session is started only after
  its record is written. A wrong secret is 401 and writes `maintainer_login dashboard refused
  secret_invalid`; no secret set is 401. The fifth wrong secret in a row starts a wait of 1
  second, and each later wrong secret doubles it, up to 60 seconds; a check during the wait is
  429 with `Retry-After`, computes no key and does not move the wait. A match resets the
  count. The count is in daemon memory, for the whole daemon. The one-time link works during
  the wait. No page, reply, record or log line repeats a presented secret.
- **Maintainer mode** (PDR 009, Decisions 3 and 4). `POST /v1/maintainer/session` takes
  `{"secret": TEXT, "client": NAME}` with the bearer secret; `NAME` matches
  `[a-z0-9][a-z0-9-]{0,31}`, else `invalid_request`. It checks the secret as the login form
  does, with the same wait and the audit record `maintainer_login mcp:NAME`, and returns
  `{"ok": true, "token": TOKEN, "client": "mcp:NAME", "idle_seconds": 86400,
  "absolute_seconds": 604800}`. Refusals: `maintainer_secret_unset`, `maintainer_secret_invalid`,
  and during the wait `maintainer_secret_backoff` (429, `Retry-After`, `details:
  {retry_after_seconds, retry_after}`). The token is 64 hex characters; the daemon keeps only
  its SHA-256 hash, in memory with the dashboard sessions and bound to `mcp:NAME`. It has the
  limits of a dashboard session: 24 hours without an authenticated request, 7 days in all, at
  most 16 sessions and tokens together, and it ends when the secret is set, generated or
  cleared and when the daemon restarts. A token is no dashboard cookie, and a cookie is no
  token. Each route `POST /v1/maintainer/ROUTE` takes `token` with its fields, refuses unknown
  fields, and refuses an unknown or ended token with `maintainer_token_expired`; an accepted
  request moves the token's idle limit. A reply is `{"ok": true, "result": ...}`.
  - `board-list` (`filter` `waiting` (default), `active`, `in_progress` or `archived`;
    `after`; `limit`): `counts` (`waiting`, `in_progress`, `open_fyi`, `archived`) and a page
    of summaries as `decision_list` returns it.
  - `decision-get` (`reference`) and `decision-history` (`reference`, `after`, `limit`): as
    the agents' routes, without a caller; each history verdict adds `via`.
  - `decision-verdict` (`reference`, `revision`, `verdict` `approved`, `declined` or
    `dismissed`, optional `note`): the dashboard's verdict, with its refusals
    (`stale_revision` with `current_revision`, `not_found`, `decided`, `invalid_verdict`,
    `hidden_command`). The result is the verdict with `via`.
  - `inbox` (`after`, `limit` 1–100) reads the maintainer's own inbox as an agent's inbox read
    pages its own; `ack` (`through`) acknowledges it (`ack_beyond_last`).
  - `send` (`to`, `body`): the dashboard's send (`peer_not_found`, `alias_unheld`,
    `recipient_inactive`).
  - `room-list` (each room's `room_id`, `name`, `revision`, `created_at` and `members`);
    `room-read` (`room_id`, `after`, `limit`) of any room; `room-post` (`room_id`, `body`,
    `key`, `deadline`), whose receipt has `room_id`, `seq`, `queued` and `skipped`;
    `room-create` (`name`, `key`); `room-add` (`room_id`, `peer`); `room-remove` (`room_id`,
    `ordinal`): the dashboard's room actions with their refusals (`room_not_found`,
    `peer_not_found`, `room_member_not_found`, `idempotency_conflict` and the room limits).
  - `peers` lists the agent sessions without the maintainer; `peer-status` (`peer`) reads one
    as `peer_status` does (`peer_not_found`, `alias_unheld`).
  - `changes` (`wait_seconds` 1–30, optional `cursor`) is the long poll of the change notices.
    Its result is `{"cursor": {URI: VERSION}, "changed": [URI]}`: the version of
    `koinon://maintainer/inbox`, `koinon://maintainer/decisions` and each
    `koinon://maintainer/rooms/ROOM_ID`, read in one read transaction, with no content. A
    deleted room has no entry. Without `cursor` it returns at once with `changed` empty. With
    the `cursor` of an earlier result it reads the versions every 500 ms, without holding a
    transaction or the write lock while it waits, and returns as soon as one differs from the
    cursor, with each such URI in `changed`, or after `wait_seconds` with `changed` empty. The
    cursor is compared for equality, entry by entry, never by order, so two changes at the same
    time are both seen. A token that ends during the wait ends the poll with
    `maintainer_token_expired`, and a daemon stop ends the wait. Each poll is an authenticated
    request and moves the idle limit, so a client that polls keeps its token. The server's write
    deadline is 10 seconds; this route's reply gets `wait_seconds` plus 5 seconds, and the
    client waits as long. Ordinary requests keep 10 seconds.

  Each action writes its audit record as the dashboard's does, also when it is refused, with
  the dashboard's action name (`decision-approve`, `decision-decline`, `decision-dismiss`,
  `acknowledge`, `send`, `room-post`, `room-create`, `room-add`, `room-remove`) and a target
  that ends with ` by mcp:NAME`. A read writes no record. `koinon mcp --maintainer --client NAME
  --secret-stdin|--secret-file PATH` reads the secret from the first line of standard input,
  one byte at a time so that the MCP stream after it stays unread, or from a regular file that
  is no symbolic link, belongs to the user and has mode 0600 or 0400; the line has the bounds
  of `koinon maintainer secret set`. It exchanges the secret before any MCP output, and on a
  refusal writes the code to standard error and exits with status 1. It registers no session,
  renews and observes nothing, and is not in `peers`. Its tools are `board_list`,
  `decision_get`, `decision_history`, `decision_verdict`, `inbox`, `ack`, `send`, `room_list`,
  `room_read`, `room_post`, `room_create`, `room_add`, `room_remove`, `peers` and
  `peer_status`, each calling the route of its name with `-` for `_`; each schema has
  `additionalProperties: false`, and an unknown or missing argument is `invalid_arguments`.
  When a call answers `maintainer_token_expired`, the server writes that result and exits with
  status 1, so the client starts a new process. A call while no daemon answers is
  `daemon_unavailable`, and the process stays.

  The maintainer mode's `initialize` gives the capability `resources: {"subscribe": true}`
  (the agent mode has no resources). `resources/list` lists `koinon://maintainer/inbox`,
  `koinon://maintainer/decisions` and `koinon://maintainer/rooms/ROOM_ID` for each room, with
  `mimeType` `application/json`. `resources/read` (`uri`) returns one text content with that
  `mimeType` whose text is the JSON of the tool that reads the resource: `inbox`, `board_list`
  with `filter` `waiting`, or `room_read`. An unknown URI is the JSON-RPC error -32002 with
  `data: {"code": "unknown_resource"}`; an unknown room is -32002 with `room_not_found`; another
  refusal is -32603 with its code, text and details in `data`. `resources/subscribe` (`uri`)
  reads the current versions before it answers, so a change after the answer is noticed, then
  writes one notice for the URI, so a change between the client's last read and the
  subscription is read too; it refuses a room that does not exist with `room_not_found`; `resources/unsubscribe` (`uri`)
  ends one subscription. While at least one resource is subscribed, one goroutine runs
  `changes` with `wait_seconds` 25. A changed version of a subscribed URI writes
  `{"jsonrpc": "2.0", "method": "notifications/resources/updated", "params": {"uri": URI}}`,
  with no title, body or count; a deleted room is a change, and reading it then gives
  `room_not_found`. Notices and replies share one serialized output. The last unsubscribe, the
  end of standard input and a cancelled context end the poll and wait for it, so no poll starts
  and no notice is written after the unsubscribe's reply. When the poll reads
  `maintainer_token_expired`, the server writes the reply of a request in progress, writes the
  code to standard error and exits with status 1, also while standard input is open and idle.
  A poll that no daemon answers is tried again after 1 second, doubling to at most 30 seconds.
- **Request protection.** Every dashboard request must name a literal loopback IP with a port in
  `Host` (`127.0.0.1:PORT` or `[::1]:PORT`; `localhost` and other names are refused). PORT may
  differ from the listener's, as through an SSH tunnel from another local port (ADR 001,
  Decision 4). An `Origin`, when present, must be `http://` plus that host. A request other
  than `GET` or `HEAD` needs that `Origin` and the session's CSRF token in `X-Koinon-CSRF` or the
  form field `csrf`.
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
  never a session ID or directory), `rooms` (every room with its
  revision and current and former members by address or peer name and state, with Remove
  buttons; the add form suggests the peer names of active sessions; `room=ROOM_ID` opens a
  room with its post form and 50 posts in order, the newest by default, with `after=SEQ`
  links, the retained floor and the retention gaps; each post lists its recipients as they
  were at the post, by address or peer name and family: queued with the inbox copy's delivery
  state and acknowledgement, skipped with the reason, or `not retained` with unknown
  acknowledgement once the copy is deleted; the view reads one snapshot and acknowledges
  nothing), `decisions` (the counts of waiting, in-progress, open fyi and archived items; the
  agent table; 25 active and in-progress cards per page, `ACTION FOR YOU` items first, then
  item order and reference, with `cards=CURSOR` links; 20 archived items per page with
  `archived=ID` links; `ref=REFERENCE` shows one item at full width above the agent table,
  with its verdict history 16 verdicts at a time through `history=ID`: a waiting, in-progress
  or open fyi item as its card with the card's verdict form, which the cards below then skip,
  and an archived item read only. The daemon removes the spaces around `ref`. When no item has
  `ref` as its exact reference, the view lists the references of the items and the deep
  archive that contain it, ignoring ASCII case, 20 per page in reference order with
  `found=REFERENCE` links, each with its title, kind and state (#505). A verdict's redirect
  keeps `ref`, so it returns to the lookup or the search with the notice on that item. While
  `ref` is set, a Clear link returns to the view without it.
  Each count links to `status=waiting`,
  `in_progress`, `fyi` or `archived`, which lists only the cards of that status, or only the
  archive for `archived`; an `all` link removes it. The counts stay totals under a filter, the
  current filter's link carries `aria-current="true"`, and the lookup form, reference and page
  links and a verdict's redirect keep it. An unknown `status` is 400), `memory` (per store:
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
  - `bulk` (`action` of `retire`, `purge`, `unpurge` or `clear`, up to 100 `s` values
    `family:id:revision`, and `confirm=purge` for a purge): applies the single action to each
    selected session through its store path, with its own audit record and refusal code
    (`revision_changed`, `maintainer_session`). The notice is `bulk_applied`, or `bulk_partial`
    when any session was refused; an unknown action or more than 100 selections is
    `invalid_request`, a known action without a selection is `no_selection` (#442), and a
    purge without the confirmation is `confirmation_required` and changes nothing (#388). The sessions view has a box before each agent session's name.
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
  - `room-create` (`name`, `key`), `room-add` (`room_id`, `peer`), `room-remove` (`room_id`,
    `ordinal`) and `room-delete` (`room_id`, `confirm=delete`): the room operations (see Rooms). `room-delete` deletes the room with its
    members, posts and delivery records; copies of its posts already in inboxes stay until
    their retention (#388). The create form carries its own key: the same key with the name
    that it first sent returns the room it created, and with another name is
    `idempotency_conflict`. `room-add` resolves the
    peer name inside its write and needs an active session (`peer_not_found`,
    `recipient_inactive`); adding a current member changes nothing. The audit target names the
    room, the peer name and the address or `session`, never a native ID. The notices are
    `room_created`, `room_member_added` and `room_member_removed`.
  - `room-post` (`room_id`, `body`, `key`, `deadline`): a post with origin `maintainer` (see
    Room posts). It reaches every active member. The form takes up to
    3 × 65,536 + 4,096 bytes, as the send form does, and each rendered form has its own key, so
    a resubmitted form repeats the first post. The notice is `room_posted`.
  - `decision-approve` and `decision-decline` on a `decision` item and `decision-dismiss` on an
    `fyi` item (`reference`, `revision`, optional `note`): the maintainer's verdict on the
    revision the page showed (see Decision items). The form takes up to 3 × 4,096 + 4,096
    bytes. The verdict is the maintainer's instruction and approval (PDR 005, Decision 3). The
    notices are `decision_approved`, `decision_declined` and `decision_dismissed`; refusals
    are `stale_revision`, `decided`, `invalid_verdict` and `hidden_command`.
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
  After those checks, each action writes one record: time, action, target (which ends with
  ` by dashboard`, PDR 009, Decision 4), result (`accepted`,
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
A context report may carry `epoch`, the native transcript or thread it was read from; the
daemon keeps it for the context policy and returns it in no view.
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
| Context | `claude_statusline` (`context_window_size`, `total_input_tokens`; epoch: the transcript `session_id`); `codex_rollout` (`model_context_window`, `last_token_usage.input_tokens`; epoch: the rollout's thread) |
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

`koinon setup FAMILY` also reports, in `permissions`, the family's recommended permission
rules of docs/PERMISSIONS.md: `family`, `settings_file`, `outcome` and, by outcome, `missing`,
`added`, `different`, `reason` or `note`. `outcome` is `present`, `missing`, `added`,
`different` (the file sets a rule's key to another value; nothing is added), `unreadable` or
`manual` (Grok, DeepSeek). Only a yes at a terminal adds rules, with the same refusals as the
status line. `koinon install` at a terminal also reports `review`, one entry per family with
`family`, `cli` and `configured` before its questions, and `permissions` for each family that
is set up. For a family that got the permission question, its `review` and `agents` entries
report `permissions` after the question, as `koinon setup` does, and a yes also lists the rules
in its `agents` entry's `changed`.

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
Those are Claude protocol fields. The notice identifies only
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
