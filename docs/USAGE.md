# Using Koinon

Read [INSTALL.md](INSTALL.md) for installation, managed/manual startup and legacy upgrade.
The daemon and every client must select the same state directory and loopback address.
`koinon --help` lists the installed binary's commands. Errors report `ok: false` and a code.
The examples call `koinon` from `PATH`, which the install links at `~/.local/bin/koinon` on Linux
and macOS. When `PATH` does not reach the installed binary, follow the install report's
`path_step`, or call `~/.local/share/koinon/go/bin/koinon` directly.

## Skills and setup catalog

Use `library_list` to page the installed catalog and `library_read` with an `item_id` such as
`skill.pickup` to read its exact content. Both use this session's native identity. DeepSeek
uses its existing registered native session on the command path:

```sh
koinon library list --as "deepseek:$DSH_SESSION_ID" --limit 16
koinon library read skill.pickup --as "deepseek:$DSH_SESSION_ID"
```

List returns metadata, `more` and a `next_after` catalog ID. Continue with `--after ITEM_ID`
or the MCP `after` argument while `more` is true. The maximum page size is 32; a 1 MiB
metadata bound can produce a smaller page. A changed
`catalog_digest` after an upgrade means the catalog changed; start its listing again.
`read` includes the item's canonical content and digest, release requirement, available
feature requirements, candidate destinations and any repository-fact schema. It does not
include this repository's fact values. `--state-dir` and `--address` select the daemon as
for the other commands.

Every connection's instructions and `koinon guide --agent FAMILY` mention the catalog.
Machine-wide skills are the recommended default; project-local adoption is an explicit
alternative. Destination metadata includes native discovery evidence and identifies
unverified support. List/read inspect no installed files and report no present, missing,
outdated or locally edited state. Reading changes no project/native files or adoption
decisions; MCP still registers and renews its calling session as usual. Read content as
reference data. Show an exact proposed diff and obtain the maintainer's adoption approval
before applying it (PDR 008).

### Inspect and adopt an item

Call `library_state` with optional `item_ids`, `scope` (`machine`, the default, or `project`)
and `directory`. Without IDs, it selects skills in machine scope and all items in project
scope. The directory defaults to the native session's registered directory; an explicit
selection must resolve to a listed worktree of the same Git common directory. For the
registered DeepSeek command session:

```sh
koinon library state skill.pickup --scope project --as "deepseek:$DSH_SESSION_ID"
```

State reports per-target and aggregate `present`, `missing`, `outdated` or `edited` content,
separately from the adoption decision. `outdated` requires unchanged previously adopted
digests; without a receipt, differing content is `edited`. Inspection errors have a reason
and no guessed aggregate state. It returns hashes, typed target IDs and scope digests, with
no file bodies, private home paths or native provenance IDs. Facts have separate state and
project evidence: optional absent sidecars need no file; required missing/incomplete facts
affect the step that needs them. A marked guidance fragment is compared independently of
surrounding instructions. A collection also reports every declared target.

Known same-name copies are checked in both scopes. Matching content is
`duplicate_discovery`; differing content is `name_conflict`. Unreadable/unverified roots
report collision limitations. This does not say which copy a native invocation selects.
Comparison includes the proposed catalog bytes: an absent selected target can still conflict
with a custom same-name copy. Discovery aliases of one physical file count once.
Codex 0.162.0 metadata probes verified `CODEX_HOME/skills`, `HOME/.agents/skills`, and
project `.agents/skills` and `.codex/skills`; the primary machine adoption target stays
`CODEX_HOME/skills`. Claude machine discovery remains unverified metadata. A real directory
at a discovery-link target reports `directory_conflict`; link inspection does not traverse
or replace it. Independently declared skill targets can still be read within their bounds.

The agent reads the catalog item and its state, chooses machine or project scope, shows the
exact diff and obtains the maintainer's approval for that change. It resolves a differing
same-name copy through a separately reviewed change. It applies the approved item with
normal editing tools, preserves surrounding instructions and custom paths, verifies hashes
and records the result. A changed target since review needs a fresh diff. A skill approval
does not approve a sidecar or discovery pointer. If recording fails, preserve the applied
files and report an unrecorded outcome. Koinon's state/decision interfaces write no adopted
files. These steps are the approved #145 adoption procedure, not authority granted by a receipt.

Machine `library_decision` takes `item_id`, `decision` (`adopted` or `declined`), `evidence`,
`catalog_digest`, `item_digest`, typed `pre_edit`/`result` arrays (`id`, `digest`) and required
`if_revision` (0 when absent). Family and root come from the native client. The command uses
`koinon library decision ITEM_ID --as FAMILY:ID` with matching `--decision`, `--evidence`,
`--catalog-digest`, `--item-digest`, `--if-revision`, `--pre-edit JSON` and `--result JSON`
options. Recording uses one ordinary database table, with at most 256 rows, 8,192 bytes per
body and 2 MiB bodies in all. There is no eviction or reserved-work exemption. An identical
current outcome is a no-op even after a lost reply; a different current revision conflicts.
Read current state to reconcile it. This provides current compare-and-set state, not replay
of overwritten historical outcomes. Declining an update retains the last adopted baseline.

Project receipts use existing `memory_record` decision notes in repo scope. The strict JSON
body has `library: "koinon-library-decision"`, `version: 1`, item/family, `scope: "project"`,
the state's `scope_digest`, catalog/item digests, `kind: "item"`, `decision`, `evidence`,
and typed `pre_edit`, `result`, `adopted` arrays. An adoption copies result digests into
`adopted`; a decline replacing a receipt keeps that receipt's adopted digests. Use
`supersedes: decision_seq` and the memory API's fixed key/deadline for retries. The native
writer's family and repository are validated independently of body or author text.
Repository-fact evidence uses `kind: "repository-facts"`, `schema_digest` from state and
target ID `facts`, always in project scope. Machine skill adoption never stores local fact
bodies. Linked worktrees share receipt scope but can have different inspected bytes.

`library_state` pages relevant `memory_recall` receipts to the end. Competing live receipts
report a conflict. Revocation, expiry, unreadable decisions and missing retained history
remain explicit, rather than resetting a decline. Recommendations cover only verified
missing/outdated skills required by currently available features; other skills are optional.
Declined and edited items remain visible. Read-only state changes no memory cursor or decision.

## Messages and participants

Review [recommended native permissions](PERMISSIONS.md) for each family, including scoped
examples, configuration locations, verified syntax checks and their limits. Setup does not
apply these permission rules; native hook trust and optional terminal operations remain
separate. Read the installed family guide for the running version.

Start Claude Code, Codex, Antigravity, OpenCode and Grok Build through `koinon claude`,
`koinon codex`, `koinon agy`, `koinon opencode` and `koinon grok`. Setup adds the MCP server; the launcher supplies the
session's launch association for participant addresses, roles, succession and terminal naming.
After setup, a directly started native CLI registers as a plain peer at its first tool call.
It can send, read and acknowledge its own peer-name inbox and use repository memory and work.
It has no participant address, role, succession or terminal naming. A supplied launch that
does not match still returns `not_launched`. Direct Codex starts have no launcher CLI wake
target, and direct OpenCode starts have no launcher wake endpoint; read their waiting messages
with the inbox tool. A session started with `koinon grok` is woken by typing Koinon's notice
into its own tmux pane, only while Grok's input box is empty and no turn is running; a direct
Grok start gets no pane wake. Koinon supports Grok's default mode only:
keep Grok's `[cli] use_leader` off, because a leader-mode session is refused with
`grok_leader_mode`. `koinon grok` chooses the first session's ID with `--session-id` unless
its arguments select a session (`--session-id`, `--resume`, `-r`, `--continue` or `-c`).
The launch admits only that ID until a session has registered through it, so a `/clear`
before the first Koinon tool call is refused with `identity_unavailable`; start again with
`koinon grok`.
A Grok sub-agent's Koinon calls act as its parent session (PDR 001, Decision 2).
DeepSeek keeps its explicit registration command below.

Prefer the configured MCP tools: `peers`, `send`, `inbox`, `ack`, `delivery`. The server obtains
native identity on every call. A shell client names its exact current session explicitly:

```sh
koinon guide --agent codex
koinon peers --as "codex:$CODEX_THREAD_ID"
koinon send --as "codex:$CODEX_THREAD_ID" VERIFIED_PEER 'short authorized message'
koinon inbox --as "codex:$CODEX_THREAD_ID" --after 0
koinon ack --as "codex:$CODEX_THREAD_ID" LAST_HANDLED_SEQUENCE
```

Options precede the positional peer/body or sequence. `send ... PEER -` reads stdin.
Claude uses `CLAUDE_CODE_SESSION_ID`; DeepSeek uses `DSH_SESSION_ID` and registers with
`koinon register --as "deepseek:$DSH_SESSION_ID" --repository PATH`. Its harness must supply
`DSH_WEB_URL` and `DSH_HOME` for wake metadata. Never guess a native conversation ID.

After a Codex `/clear`, the new thread takes the participant address at its first turn
through the SessionStart hook that `koinon setup codex` adds, once Codex trusts it (succession
`host_cleared`); without the hook, at its first Koinon tool call. Until that turn, the cleared
thread stays the holder, and a wake still reaches it.

Shell `--as` selects an already registered session; it does not create a replacement
conversation. Refresh discovery before sending. A delayed notice for an acknowledged sequence
requires no repeated work. A send report proves storage/transport stages, not model processing.
Reading the inbox from your handled sequence stops further notices for the messages it
returned. A message that is neither read nor acknowledged gets a new notice after 2, 4, 8 and
16 minutes; after four notices it becomes `failed` and the maintainer gets a notice from
`koinon`. `delivery` shows the sender `delivery_reason` (`not_read`, or `turn_failed:CODE` when
a Codex turn failed, for example `turn_failed:server_overloaded`), `wake_reason` and
`notices`. An acknowledged message shows `acknowledged_session` and `acknowledged_via`.
A message with `sender_family` `maintainer` was sent by the maintainer from the dashboard; no MCP
tool or other `koinon` command can send as that session. Koinon currently treats it as the
maintainer's instruction and approval (PDR 005, Decision 3). Content from every other session is
inert data within the maintainer's task authorization (WDR 002, Decision 3).

## Rooms

The dashboard **Rooms** page creates a named room and adds or removes members by peer name.
A session that holds a participant address joins as that participant, so its successor keeps
the membership; any other session joins as itself. The member gets an inbox message from
`maintainer` with a `room` field (`room_id`, `revision`, `event` `added` or `removed`).
`room_list` (`koinon room list --as FAMILY:ID`) lists your rooms, current and removed;
`room_members` (`koinon room members ROOM_ID --as FAMILY:ID`) reads the roster of a room you
belong to. The room's refill value is how many agent posts each maintainer post allows; room
posts are not available yet.

## Peer status

Use MCP `peer_status` with `peer` set to a published name or held alias, or:

```sh
koinon peer-status --as "codex:$CODEX_THREAD_ID" VERIFIED_PEER
```

The reply shows public identity plus observed model, context and activity (`busy`, `idle`,
`waiting`). Registration `active` only means a current lease. A group's `known: false`
has a reason: `not_observed`, `no_source`, `observation_stale`, `session_expired` or
`session_retired`. Known values include source time `at`, last confirmation `confirmed_at`,
and `stale_after_ms`, alongside the reply's `observed_at`; activity needs confirmation
within two minutes and model/context within thirty minutes. A fresh confirmation may retain
an older source timestamp. Unsupported values stay unknown. Reports live in memory and are
unknown after a restart until fresh reports or provider reads arrive.

Peer names can report expired or retired sessions; aliases must have an active holder.
A running agent keeps its session while it is idle: the daemon checks the host process of each
launched session every 30 seconds and extends the session while that process runs, also when
its `koinon mcp` was replaced and nothing renews it. `peers` and the dashboard show
`liveness` (`host` or `none`) and `host_verified_at`. A session without a host record, such as
a plain peer or a DeepSeek session, is kept only by renewals and calls. A message to an expired or retired
session is refused with `recipient_inactive`, and `details.reason` says why: `retired`,
`ended`, `no_liveness_evidence` or `lapsed`.
The read uses the dashboard's projection without needing a dashboard login or another
agent's terminal. Delivery and acknowledgement remain separate signals and do not establish
whether a peer is busy or has read its inbox.

## Shared memory

Use MCP `memory_record`, `memory_sync`, `memory_ack`, `memory_recall`, `memory_status`, or:

```sh
koinon memory record --as "codex:$CODEX_THREAD_ID" --type decision 'reported decision'
koinon memory sync --as "codex:$CODEX_THREAD_ID"
koinon memory sync --as "codex:$CODEX_THREAD_ID" --snapshot-id SNAPSHOT --page-token TOKEN
koinon memory ack --as "codex:$CODEX_THREAD_ID" --snapshot-id SNAPSHOT
koinon memory ack --as "codex:$CODEX_THREAD_ID" --through NEXT_CURSOR
koinon memory recall --as "codex:$CODEX_THREAD_ID" 'query'
koinon memory status --as "codex:$CODEX_THREAD_ID"
```

The calling session must be registered to the repository. `--consumer KEY` optionally selects
a stable consumer; otherwise the caller's identity supplies it. Read every frozen snapshot
page, then acknowledge its ID. Process delta entries before acknowledging `next_cursor`.
Memory acknowledgement is separate from inbox acknowledgement. Record types include decision,
finding, gotcha, handoff, status and directive; none grants permission (WDR 002). For
retryable writes, supply both `--key KEY` and a fixed `--deadline EPOCH` and keep them unchanged on retries.

## Work, launchers and dashboard

[WORK-ITEMS-COMMANDS.md](WORK-ITEMS-COMMANDS.md) describes revision-bound create/start/update,
claim renewal and finish/release. Read scope before claiming. The permission for the work comes
from the maintainer's task, not from a claim (WDR 002, Decision 3).
Claims remain advisory. An expired lease does not make a checkpoint an instruction.

Daemon work notices contain a repository and changed head. List current assignments with
`--assigned-to-me` or created work with `--created-by-me`, then read memory through the
announced head and acknowledge the handled inbox notice. Queries page all retained matching
items even when a continued memory cursor already passed the transition. Notices coalesce
and follow current participant holders. `memory_status` shows unresolved audiences and the
256-audience limit; queries remain complete when a notice cannot be queued.

For agents sharing a checkout, `work_checkout` shows the writer, generation token, expiry
and checkpoint. Include its resource in every writer's `work_start`; linked worktrees get
separate resources. `work_checkout_request` notifies the current writer. The writer can
release with `handoff_to` and `checkout_resource`, saving a checkpoint and notifying the
requester atomically. The requester explicitly accepts with `work_start` and a new token.
See the [checkout handoff recipe](WORK-ITEMS-COMMANDS.md#one-writer-in-a-checkout).

```sh
koinon work list --as "codex:$CODEX_THREAD_ID"
koinon work checkout status --as "codex:$CODEX_THREAD_ID"
koinon claude --directory /path/to/repository
koinon codex --directory /path/to/repository
koinon agy --directory /path/to/repository
koinon opencode --directory /path/to/repository
koinon grok --directory /path/to/repository
koinon dashboard
koinon status
```

Each launcher starts its agent in `--directory`, or the current directory. Outside tmux it
starts a new tmux session and attaches to it; inside tmux it runs in the current pane; without
tmux it runs in the current terminal. The new session is named `FAMILY-LABEL`, with `-ROLE`
for a role: the participant's address before any suffix, where LABEL is the repository label
(or the directory's, outside a repository). Agents of other families or roles in one directory
therefore get their own sessions. When the name is taken, as for a second agent of the same
family and role, the launcher uses the first free name of `NAME-2` to `NAME-99`; when all are
taken it refuses the start and names `--tmux-session`. `--tmux-session NAME` starts a detached
tmux session with that exact name instead and prints its name, pane and socket as JSON; a taken
name refuses the start. Launcher options come first; the first other argument, or everything
after `--`, goes to the agent CLI.

Two agents of one family can use separate participant addresses in the same repository:

```sh
koinon codex --directory /path/to/repository
koinon codex --directory /path/to/repository --role review
```

The default participant has address `codex-repository`; the second has
`codex-repository-review` (the actual repository label is used). Roles are optional and
maintainer-assigned: 1–24 lower-case letters, digits or hyphens, starting with a letter,
and not only hexadecimal digits and hyphens. Roles apply to all four launcher families,
including `koinon claude --bg --role review`. Linked worktrees share participant addresses.
Each agent also retains its permanent peer name.

Launched agents name their own tmux session after the held participant address, or their peer
name when they do not hold one. With another agent in the same tmux session, only the own pane
title changes. A name already used by another session is left alone and retried at later
renewals. The pane must hold the agent's host process with no other agent between them;
nested agents and foreign panes rename nothing. Sessions of one host share its terminal: a
Codex sub-agent renames nothing, and a non-holder, such as the former thread after a Codex
`/clear`, renames nothing while another session of its host holds a participant address.
Outside tmux, the result is `not_in_tmux`.

MCP `peers`, `koinon peers` and the dashboard show the last naming result and its reason.
The optional `naming` object has `result`, fixed `reason` text, `target` and source time `at`.
It is omitted from peers when no fresh result exists or its target is no longer the current
published name. The dashboard reports the corresponding unknown reason, including
`observation_stale` or `naming_outdated`, and shows the last succession result. Naming reports
need confirmation within two minutes and live in memory, so a restart makes them unknown
until a new report. A naming result never proves that a peer is busy or has read its inbox.

`peers` shows `role`, `address` and `holds_address` (false/empty fields are omitted), with
`alias` only on the active holder. An active holder keeps its address. Without one, exactly
one active qualifier takes it; multiple qualifiers leave it unheld and the dashboard reports
the conflict. Registration and renewal order do not choose between them. In the dashboard's
sessions view, use **Make holder** on the intended active session. The choice requires the
current session revision, is audited, and shows as the participant's last event. A plain peer,
a sub-agent or another participant's session cannot be chosen. A send to an unheld address is refused
with `alias_unheld`; the agent's peer name can still be addressed directly.

After a launched agent resets to a new native session in the same host process, its first
Koinon tool call can take the participant from the previous session once that holder has
made no tool call for the guard period (30 seconds by default). A new process in the same tmux server and pane can succeed
it when the old host has provably ended. The runtime checks process IDs and start values;
the same repository or peer message is insufficient. Sub-agents and fenced sessions cannot
succeed automatically. Unknown host evidence, or missing pane evidence for a different
host, leaves the holder unchanged.

`peers` shows the current session's optional `succession` result, including the address,
former/retained holder, daemon time, evidence or refusal reason. `holder_active` means the
guard (30 seconds by default) remains: MCP retries at the first tool call after the returned wait, without
you sending a registration command. Renewals and observation timers do not trigger the
retry or keep the guard active. Other reasons are `no_host`, `host_running`, `other_pane`,
`subagent`, `fenced` and `other_participant`; report the reason and use the maintainer's
**Make holder** choice when a manual choice is needed. A `no_host` against a launched predecessor
without a host record resolves when that predecessor expires: the successor takes the address at its next
registration or renewal. **Make holder** only makes that immediate. After a daemon restart, the guard
waits at least one guard period because earlier tool-call activity is unknown.

The dashboard **Settings** page shows and changes the session timing: the registration
lifetime (default 900 seconds), the renewal interval of `koinon mcp` (default 300 seconds, at
most half the lifetime) and the succession guard (default 30 seconds, at least 30). A change
applies at once, without a restart, and the audit log records it.

A holder change retires and persistently fences the former holder. Registering again gives
it its native peer name only; only **Make holder** lifts its fence. Address messages now
belong to the participant inbox, and a holder's default memory/work consumer is
`participant:<address>`. The next holder continues its acknowledgements, memory cursor and
live claim generations without restarting a lease. Calls with that consumer, including
custom-consumer arguments and keyed retries, require the current holder or return
`stale_holder`. Read the current work and checkpoint before continuing; ownership grants no
new permissions. Non-holders keep their previous session defaults.

`inbox` returns session and participant messages, each tagged `inbox`. Track `after` and
`participant_after` separately; the top-level sequence state describes the session and the
`participant` object describes the participant. Page both until their `more` flags are false.
Acknowledge only handled messages with `through` and `participant_through` respectively.
CLI examples: `koinon inbox --as FAMILY:ID --after 0 --participant-after 0` and
`koinon ack --as FAMILY:ID --participant 7 3` (participant through 7, session through 3).
A holder sends with its address as `sender_name`, so replies follow the participant. A
send to an exact peer name remains in that native session's inbox.

Schema 12 keeps all pre-upgrade inbox messages, acknowledgements, peer-name memory cursors
and native-session work claims with their existing owners; it does not migrate their
ownership to participants. Existing aliases keep their addresses and holders.
Checkout status resolves participant writers to their current active holder's exact peer.
Requests are notifications only; handback followed by explicit `work_start` still creates
a new writer generation.

The launchers use configured native CLIs, exact repositories and private launch records.
Codex runs with `--no-daemon`, so its MCP servers belong to the launched CLI process. The
launcher forwards `KOINON_LAUNCH_ID`, `KOINON_STATE_DIR`, `KOINON_DAEMON_ADDRESS`, `TMUX`,
`TMUX_PANE` and `CODEX_HOME` through the configured Koinon MCP server's `env_vars` override.
Codex sub-agent threads have their own peer names, appear with `subagent: true` and never
hold the repository alias.

A launcher starts in a directory that holds other repositories and reports them before the
agent starts: each nested checkout, with its path relative to the start directory and its kind
(`submodule` when its enclosing repository records it as a gitlink, whatever its `.git` layout;
`worktree` for a linked worktree of another repository; `repository` for any other, such as a
separate clone). A linked worktree of the start repository is part of it and is not listed. The
scan goes into submodules and the start repository's worktrees, but not into another
repository, never follows directory symlinks, and stops after 3 seconds (Git queries
included) or 64 entries. A stop, an unreadable directory or a path that the launch record cannot
hold (longer than 256 bytes, or with a control character such as a newline) marks the list
incomplete and never refuses the start. The
session's Koinon repository, memory and work store stay the start directory's repository. The
launch record keeps the list, and the dashboard shows it with each session that registers with
its launch record (Claude Code, Codex, Antigravity, OpenCode and Grok).

To start a Claude background job, use `koinon claude --bg [args]`; put launcher options
before native CLI arguments. This runs outside tmux and requires Claude Code's `--bg` support.
Koinon supplies a private settings file with that job's launch environment and records the
job ID returned by Claude. Do not supply native `--bg`, `--background` or `--settings`
arguments yourself. A tool call that arrives before the job ID is recorded returns
`launch_pending`; the next tool call retries registration. If starting the job fails or
returns no usable job ID, Koinon retires the pending launch and removes its settings file.

When you view that job with `claude attach SHORT_JOB_ID` in tmux, naming looks for its client
in the user's tmux socket directory (`$TMUX_TMPDIR/tmux-<uid>`, else `/tmp/tmux-<uid>`).
Exactly one matching pane gets the usual session-name or pane-title behavior. No client
reports `attach_pane_not_found` and retries at the next renewal; multiple clients report
`attach_pane_ambiguous` and rename nothing until the published name changes. Tmux servers
outside that directory are not searched. The result can be reported without a terminal observation.

After an upgrade, an existing native session without a launch association renews as a plain
peer and releases any former participant address. Its own inbox, acknowledgements, memory
cursor and work claims remain retained. A retained native claim still needs the usual release
or lease expiry before another session can acquire it. Use the launcher for participant
features in a new session.

The dashboard link is single-use and expires; keep it out of logs, Git and messages.
Administrative actions require CSRF protection and are
audited. Health reports unknown observations explicitly; a failure is not permission to
replace state, kill an unrelated process, or change agent settings.

## Launcher paths in launchers.json

Authorized `koinon setup claude|codex|agy|opencode|grok` records its resolved absolute CLI path in
`launchers.json` under the Go state root, so subsequent launches need no `--cli`:

```sh
koinon setup codex --cli /opt/agents/bin/codex
koinon codex --directory /path/to/repository
```

The default file is `~/.local/state/koinon/go/launchers.json`, or
`$XDG_STATE_HOME/koinon/go/launchers.json`; setup and launch can select the same custom
root with `--state-dir DIR`. Its format is a JSON object mapping family to absolute path,
such as `{"codex":"/opt/agents/bin/codex","agy":"/opt/agents/bin/agy"}`. The file must
be owned by the current user, a regular file with one link, mode 0600 and at most 16 KiB,
in a private user-owned directory. Unsafe files are refused, with permissions left intact.

Repeat setup with `--cli /new/absolute/path` to update one family while preserving the
others. A launch's `--cli` overrides the stored path without changing it. Uninstall retains
the file.

When the family has no entry or an empty entry and no `--cli` is given, the launcher finds
the family's CLI on `PATH`, records its absolute path, prints
`koinon: recorded the FAMILY CLI PATH in launchers.json` and starts it (PDR 003, Decision 5).
It reads the file again under the lock first; an entry that another launch or setup recorded
meanwhile is used, not replaced. Later launches use the recorded path and do not search
`PATH`. When the CLI is not on `PATH`, the launch refuses with
`no FAMILY CLI is configured in launchers.json` or `the FAMILY CLI entry in launchers.json is
empty` and names the two remedies: run `koinon setup FAMILY` (with `--state-dir DIR` for a
custom root) to record the CLI, or start with `--cli ABS_PATH`. An invalid file refuses with
`invalid launcher configuration`, and a relative entry with `configured CLI path must be
absolute`; neither is changed.
