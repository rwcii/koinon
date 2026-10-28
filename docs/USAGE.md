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
decisions; MCP still registers and renews its calling session as usual. Adopt an item as
[Inspect and adopt an item](#inspect-and-adopt-an-item) describes (PDR 008).

### Koinon skills

`koinon install`, and the daemon at each start, write a skill entry for each catalog skill for
you, next to the managed cycle's `koinon-pickup`: `koinon-spin-up`, `koinon-spin-down`,
`koinon-recover-team`, `koinon-peer-tmux`, `koinon-handoff`, `koinon-check`, `koinon-ship`,
`koinon-release`, `koinon-pair`, `koinon-peer-review`, `koinon-refusal-review`, `koinon-sprint`
and `koinon-guide` ([INSTALL.md](INSTALL.md#install), step 7). Claude Code, Codex, and OpenCode
and agy when they are on the computer, get them; Grok and DeepSeek do not. Run them as
`/koinon-spin-up` in Claude Code or `$koinon-spin-up` in Codex. Each one loads its catalog
item, so the procedure is the installed release's, and the procedure reads this repository's
facts from `.koinon/skills/NAME.md`; give a project its own start checks, stop checks or
manifest path there. The runnable steps, such as the launch command, first-run prompts and
the stop and recovery steps, are in the Team operations section of
`koinon guide --agent FAMILY`. A project's own skill of the bare name stays as it is
(PDR 008, Decision 2).

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
apart from the adoption decision. `outdated` requires unchanged previously adopted
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
`CODEX_HOME/skills`. Claude machine discovery remains unverified metadata.
The default Codex root is verified when the managed launcher proves environment forwarding;
a direct MCP connection with no visible CODEX_HOME reports an unverified-root limitation
because the native client can filter a custom home out of its child environment.
A real directory
at a discovery-link target reports `directory_conflict`; link inspection does not traverse
or replace it. Independently declared skill targets can still be read within their bounds.

The agent reads the catalog item and its state, chooses machine or project scope and
compares any differing same-name copy. It applies the item with normal editing tools,
preserves surrounding instructions and custom paths, verifies hashes and records the result.
A sidecar or discovery pointer is a separate change. If recording fails, keep the applied
files and report an unrecorded outcome. Koinon's state/decision interfaces write no adopted
files. These steps are the #145 adoption procedure.

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
remain explicit, rather than resetting a decline. The history floor tracks removed library
receipts, not unrelated memory/work expiry. Recommendations cover only verified
missing/outdated skills required by currently available features; other skills are optional.
Declined and edited items remain visible. Read-only state changes no memory cursor or decision.

## Messages and participants

Add the [recommended native permissions](PERMISSIONS.md) for each family. The file gives
scoped examples, configuration locations and verified syntax checks. Setup does not apply
these permission rules, native hook trust or optional terminal operations. Read the installed
family guide for the running version.

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
Grok start gets no pane wake. Koinon supports Grok's default mode. A leader-mode session
(Grok's `[cli] use_leader` on) is refused with `grok_leader_mode`. `koinon grok` chooses the first session's ID with `--session-id` unless
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
koinon send --as "codex:$CODEX_THREAD_ID" VERIFIED_PEER 'short message'
koinon inbox --as "codex:$CODEX_THREAD_ID" --after 0
koinon ack --as "codex:$CODEX_THREAD_ID" LAST_HANDLED_SEQUENCE
```

Options precede the positional peer/body or sequence. `send ... PEER -` reads stdin.
Claude uses `CLAUDE_CODE_SESSION_ID`; DeepSeek uses `DSH_SESSION_ID` and registers with
`koinon register --as "deepseek:$DSH_SESSION_ID" --repository PATH`. Its harness must supply
`DSH_WEB_URL` and `DSH_HOME` for wake metadata. Use the session's own native conversation ID.

After a Codex `/clear`, the new thread takes the participant address at its first turn
through the SessionStart hook that `koinon setup codex` adds, once Codex trusts it (succession
`host_cleared`); without the hook, at its first Koinon tool call. Until that turn, the cleared
thread stays the holder. On Linux and macOS, a participant notice for a session started with
`koinon codex` in tmux is typed into its pane while it is idle and its input is empty, so the
new thread's first turn reads it. A wake for the holder that still goes through `codex queue`
(outside tmux, or a notice for the session's own inbox) waits with `codex_thread_unproven` from
the `/clear` until the new thread's first turn moves the address, so the cleared thread does
not read the message (#275).

Shell `--as` selects an already registered session; it does not create a replacement
conversation. Refresh discovery before sending. A delayed notice for an acknowledged sequence
requires no repeated work. A send report proves storage/transport stages, not model processing.
Reading the inbox from your handled sequence stops further notices for the messages it
returned. A message that is neither read nor acknowledged gets a new notice after 2, 4, 8 and
16 minutes; after four notices it becomes `failed` and the maintainer gets a notice from
`koinon`. A Codex wake whose `codex queue` command exits with a non-zero code (`wake_reason`
`queue_failed`) is not a notice: it is retried with backoff and never ends as `not_read`, and
the daemon log's `codex_queue` event gives its exit status and cleaned first error line (#466).
`delivery` shows the sender `delivery_reason` (`not_read`, or `turn_failed:CODE` when
a Codex turn failed, for example `turn_failed:server_overloaded`), `wake_reason` and
`notices`. An acknowledged message shows `acknowledged_session` and `acknowledged_via`.
A message with `sender_family` `maintainer` was sent by the maintainer, from the dashboard or
through `koinon mcp --maintainer` with the maintainer secret; no agent MCP tool or other
`koinon` command can send as that session. Koinon currently treats it as the
maintainer's instruction and approval (PDR 005, Decision 3). A message from every other session
comes from a teammate agent: do the work it asks for and reply through Koinon (#487).

## Rooms

The dashboard **Rooms** page creates a named room and adds or removes members by peer name.
A session that holds a participant address joins as that participant, so its successor keeps
the membership; any other session joins as itself. The member gets an inbox message from
`maintainer` with a `room` field (`room_id`, `revision`, `event` `added` or `removed`).
`room_list` (`koinon room list --as FAMILY:ID`) lists your rooms, current and removed;
`room_members` (`koinon room members ROOM_ID --as FAMILY:ID`) reads the roster of a room you
belong to.

Open a room on the Rooms page to post as maintainer and to read its posts. Each post lists
its recipients: delivered copies with their delivery state and whether the recipient
acknowledged them, skipped inactive members, and copies that retention has deleted, shown as
not retained. Viewing the room acknowledges nothing. A maintainer post
reaches every active member. Members post with `room_post` (`koinon room post ROOM_ID
'message' --key KEY --deadline UNIX_SECONDS --as FAMILY:ID`) without an allowance and without
a maintainer post between rounds (#493); the post reaches the other active members. `room_read` (`koinon
room read ROOM_ID --as FAMILY:ID`) pages the posts since you joined, also the ones you missed
while inactive. A room post in your inbox has `room.origin`: `maintainer` for a dashboard post,
which is the maintainer's instruction and approval (PDR 005, Decision 3), and `member` for a
teammate agent's post: do the work it asks for and reply through Koinon (#487).

## Decisions

Agents write items for the maintainer's decision board with `decision_create` (`koinon
decision create --reference REF --title TITLE --kind decision|fyi --key KEY --deadline
UNIX_SECONDS --as FAMILY:ID`, with optional `--order`, `--where`, `--agent-text`, `--context`,
`--recommendation`, `--command` and `--recipient`, the last two repeated for each entry;
`--no-commands` and `--no-recipients` store an empty list) and
change them with `decision_edit` (`koinon decision edit --reference REF --if-revision N ...`).
Agents read decision status with `decision_list` (`--filter waiting|active|in_progress|archived|mine`,
paged with `--after`) and `decision_get` (`--reference REF`).
Write card prose in ASD-STE100; context, recommendation and agent text are Markdown, and
terminal-copy commands are printable ASCII. Known key formats are refused. Credentials,
personal data, private session IDs and peer message bodies stay in their own stores; a card
holds the decision's own text. Markdown renders as
paragraphs, line breaks, lists, emphasis, inline code and fenced code blocks; links, images
and HTML show as literal text. An fyi item whose title starts with `ACTION FOR YOU` asks the
maintainer for manual work.

The dashboard **Decisions** page is the board. It shows the counts (waiting, in progress, open
fyi, archived), then a table of the active agents: name, activity, claimed work, context use
or "not reported", and the waiting items that name the agent. The cards follow:
`ACTION FOR YOU` items first, then item order and reference. Each terminal-copy command is in
its own box with a **Copy** button. The archive lists closed items, and **Find** opens any item
by its exact reference, as do the references in the agent table, with its verdict history and
the text of each verdict. For part of a reference, or another case, **Find** lists the
references that contain the text, also deep-archived ones, with a link to each; spaces around
the text are ignored. **Clear** ends the search and keeps the status filter. A waiting, in-progress or open fyi item opens as its card, at full width, with
the same buttons as on the board, and the cards below leave it out while it is open. A verdict
there returns to it with the answer on the card; an archived item opens read only. Choose a count
to show only the items of that status (`status=waiting`, `in_progress`, `fyi` or `archived` in
the address); **all** shows every item again. The chosen count is marked, the counts stay
totals, and the filter stays after a reload, a **Find** and a verdict. An unknown status in
the address is refused.

Give a verdict on the card: **Approve** or **Decline** a decision item, or **Dismiss** an fyi
item, with an optional Markdown note. The buttons are disabled while the request runs, and
the answer shows on the card. A card with a hidden command (one that fails the printable-ASCII
rule) cannot be approved. The verdict applies to the revision
the page showed, keeps a snapshot of that text, and is your instruction and approval to the
named agents. Approve moves the item to in progress; Decline and Dismiss archive it. Each
named agent gets an inbox message and a wake that name the verdict ID. An agent reads it with
`decision_get` (`koinon decision get`), handles it and acknowledges it with `decision_ack`
(`koinon decision ack --verdict ID`); `decision_pending` (`koinon decision pending`) lists
the verdicts it has not acknowledged. When the approved work is done, the agent closes the
item with `decision_close` (`koinon decision close --reference REF --if-revision N --key KEY
--deadline UNIX_SECONDS`), which archives it. An agent edit of a decided item reopens it for a
new verdict; `decision_history` pages every verdict with its snapshot.

The agent that handles a verdict records it in the repository: `decision_record` (`koinon
decision record --reference REF`) appends the verdict's entry, with the card text that you saw
and your note, to `DECISIONS.md` at the root of its checkout. The agent corrects the spelling
and typing errors in your words, commits the file with the work, and cites the decision as
`DECISIONS.md#REFERENCE`, never by verdict ID. An item is recorded only in the repository it
was created in. When a repository's `DECISIONS.md` is not a Koinon record, the agent asks you
on the board and changes nothing. A recorded entry is public, so agents write every card as
public text.

When a waiting item or an open fyi item no longer needs you (the work is done, a newer item
supersedes it, you answered it in chat, or it is no longer valid), an agent withdraws it with
`decision_withdraw` (`koinon decision withdraw --reference REF --if-revision N --reason TEXT
--key KEY --deadline UNIX_SECONDS`). The item's author, a session of the author's participant or
a named recipient can withdraw it; the reason is one line. The item moves to the archive
without a verdict: the archive shows it as **withdrawn**, and its lookup shows who withdrew it,
when and why. `decision_restore` with `--if-revision N` reopens a withdrawn item on the board
at the next revision, and the item keeps the withdrawal in its history. An item read shows the
newest 8 withdrawals; `decision_history` with `withdrawals` (`koinon decision history
--withdrawals`) and the lookup's **All withdrawals** link page all of them.

Nothing is deleted. 30 days after an item is archived, and once every delivery is
acknowledged, Koinon moves the whole record to `decisions/YYYY-MM.jsonl` in its state
directory. `decision_get` then answers `deep_archived`, and `decision_restore` (`koinon
decision restore --reference REF`) brings the item back into the archive, with its
withdrawals.

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
finding, gotcha, handoff, status and directive. For
retryable writes, supply both `--key KEY` and a fixed `--deadline EPOCH` and keep them unchanged on retries.

## Work, launchers and dashboard

[WORK-ITEMS-COMMANDS.md](WORK-ITEMS-COMMANDS.md) describes revision-bound create/start/update,
claim renewal and finish/release. Read scope before claiming. A claim records who works on
an item; the lease and checkpoint record its progress.

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
sessions view, use **Make holder** on the intended active session, or, without the dashboard,
`koinon participant holder ADDRESS PEER` (`koinon participant list` shows each address and its
holder). The choice requires the current session revision (the command reads it, or takes
`--if-revision`), is audited (by `cli` for the command), and shows as the participant's last
event. A plain peer,
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
**Make holder** choice, or `koinon participant holder`, when a manual choice is needed. A `no_host` against a launched predecessor
without a host record resolves when that predecessor expires: the successor takes the address at its next
registration or renewal. **Make holder** only makes that immediate. After a daemon restart, the guard
waits at least one guard period because earlier tool-call activity is unknown.

The dashboard **Settings** page shows and changes the session timing: the registration
lifetime (default 900 seconds), the renewal interval of `koinon mcp` (default 300 seconds, at
most half the lifetime) and the succession guard (default 30 seconds, at least 30). A change
applies at once, without a restart, and the audit log records it.

The Settings page also sets each family's context handoff mode and threshold: Claude
`handoff_cycle`, Codex `native_compaction` and the others `observe`, at 75 percent by default.
These are defaults only: a session runs no handoff cycle or compaction until it is enabled on
the Sessions page, and changing a family enables nothing. The Sessions page shows
each session's mode, threshold, Enable, whether its family has a verified cycle, and why it is
or is not at its threshold, with the age of its context reading. Enabling is refused
(`context_cycle_unavailable`) and its button is inactive while the cycle cannot run for that
session; the page names the reason (for example a session that is not in its launched tmux pane,
or a missing `koinon-pickup` skill, with the `koinon install` step that installs it). Disabling always works. A stored mode or threshold that is not valid
keeps the family default, and the Settings page names the error.

agy sessions report their context and activity through the status line that `koinon setup agy`
installs, so the Sessions page shows their readings. The agy family default stays `observe`.
OpenCode sessions started with `koinon opencode` show their model and context too: the daemon
reads them every 15 seconds from the session's own OpenCode server, from the newest finished
reply and its model's context limit. A direct `opencode` start shows none. The OpenCode family
default stays `observe`.
DeepSeek sessions registered with `koinon register` show their model and context too: the
daemon reads them every 15 seconds from the session's own harness, with the wake's credential,
from the token meter's newest usage and the model's context window. Its family default stays
`observe`.
Grok Build sessions report their model and context through the status line that `koinon setup
grok` installs in `~/.grok/config.toml` (start Grok again after setup): the context window's
current tokens and its size. An input without both counts leaves the last reading as it was.
Its family default stays `observe`.

Claude Code sessions launched with `koinon claude` on Linux and macOS can run the cycle, and
Codex sessions launched with `koinon codex` in tmux on Linux and macOS can run it too (family
mode `handoff_cycle`) while they hold their participant address. agy sessions launched with
`koinon agy` in tmux on Linux can run it the same way, with Koinon's agy status line set up by
`koinon setup agy`; macOS waits for native evidence. OpenCode sessions launched with
`koinon opencode` in tmux on Linux can run it too: instead of a clear, the daemon creates a new
session through the session's own OpenCode server, shows it in the pane and runs the
`koinon-pickup` skill command there with the requester's model; the report is accepted once the
pane shows the new session's Koinon tool calls. macOS waits for native evidence. DeepSeek
sessions registered with their harness URL and credential file on Linux can run it too, while
their model is the harness's default model: once the session is idle after `koinon cycle-ready`,
the daemon creates a new harness session in the same directory and enters `/koinon-pickup`
there; the new session registers with the same `--repository`, takes the address once the
pickup's receipt is recorded and reports with `koinon cycle-pickup`. The daemon never changes a
harness setting, so a session on another model shows `model_not_default`. macOS waits for
native evidence. Grok Build sessions launched with `koinon grok` in tmux on Linux can run it
too, with Koinon's Grok status line set up by `koinon setup grok`: the cycle enters `/new`,
which starts a new Grok session in the same pane, and then `/koinon-pickup`; the new session
takes the address at its first tool call. macOS waits for native evidence. After the clear the cycle enters
`/koinon-pickup` (Claude Code, agy) or `$koinon-pickup` (Codex): Koinon's own pickup skill,
which the daemon at each start and `koinon install` write for you under `~/.claude/skills`
(or `$CLAUDE_CONFIG_DIR/skills`), `~/.agents/skills` and `~/.gemini/config/skills`, and
DeepSeek's command-path entry under an existing `$DSH_HOME/skills` (default `~/.dsh/skills`),
never into a repository. A workspace `.agent/skills/koinon-pickup` or `.agents/skills/koinon-pickup` would
replace agy's entry, so the Sessions page names it as `pickup_entry_shadowed` unless it is the
same file. It loads the catalog's canonical pickup
procedure, so a project's own `pickup` skill neither blocks the cycle nor is changed by it
([INSTALL.md](INSTALL.md#install), step 7). For Codex, agy and OpenCode, the new thread, conversation or session after the
reset takes the address and reports the pickup. An enabled session
whose context reaches its threshold gets a request from `overwatch`, Koinon's reserved system
requester, in its inbox. The request asks the session to pause at a safe stopping point, write
its handoff and report it with `context_cycle_ready`, then end its turn and stay idle. The
daemon clears the session's context in its tmux pane only at its idle prompt, enters its
pickup, and the new context reports with `context_cycle_pickup`. When the session does not
become idle within two minutes of its ready report, `overwatch` sends the request again under a
new revision, and the session pauses, writes its handoff and reports ready again; the cycle's
30-minute lifetime and its event limit bound the repeats. The Sessions page shows
each session's current or newest cycle: its stage, the stage deadline and each recorded step.
Disabling, retiring or purging the session, a family mode change, or a holder
choice for its address ends the cycle; a cycle that fails or cannot be proven ends the
session's Enable.

In `native_compaction`, an enabled Codex session launched with `koinon codex` in tmux gets
Codex's own `/compact` when its context reaches the threshold: the daemon types it in the
session's pane once Codex is idle at an empty prompt, once per crossing, and waits for a fresh
reading below the threshold to confirm it. It needs no pickup skill and no participant address,
and Koinon changes no Codex setting. A busy pane only delays the compaction; if Codex is not
idle within 15 minutes, the compaction ends and the next reading at the threshold starts
another. A compaction that is not confirmed within 10 minutes, or whose submission cannot be
proven, ends the session's Enable, so `/compact` is never typed twice for one crossing. The
Sessions page shows the newest compaction with its steps and result. A session must report its pickup within five minutes of the pickup input. When
the session shows a dialog that the cycle does not answer itself, the Sessions page shows it
with one button per option; your choice applies only to that dialog.

Without the dashboard, a shell on the same computer (for example over SSH) or an agent session
changes the same settings (#391). Each change is in the audit log with its origin:

```sh
koinon context show                                   # families, sessions, waiting dialogs
koinon context set codex --mode handoff_cycle --threshold 80
koinon context enable NAME                            # or: koinon context disable NAME
koinon context threshold NAME 60                      # NAME's own threshold
koinon context threshold NAME family                  # back to the family threshold
```

Agents use the MCP tools `context_show`, `context_set`, `context_enable` and
`context_threshold`; without `peer`, `context_enable` and `context_threshold` change the
calling session. `koinon context show` also shows a waiting dialog. Without the dashboard, answer
it with its option key in the session's own tmux pane, as the maintainer or an agent that
manages the session.
While a retained name record is called `overwatch`, no cycle starts and the Sessions page
names the conflict. See PROTOCOL.md, Managed context cycles.

A holder change retires and persistently fences the former holder. Registering again gives
it its native peer name only; only the maintainer's holder choice (**Make holder** or
`koinon participant holder`) lifts its fence. Address messages now
belong to the participant inbox, and a holder's default memory/work consumer is
`participant:<address>`. The next holder continues its acknowledgements, memory cursor and
live claim generations without restarting a lease. Calls with that consumer, including
custom-consumer arguments and keyed retries, require the current holder or return
`stale_holder`. Read the current work and checkpoint before continuing. Non-holders keep their previous session defaults.

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
job ID returned by Claude. Koinon supplies these native arguments and refuses a native
`--bg`, `--background` or `--settings` argument in `[args]`. A tool call that arrives before the job ID is recorded returns
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
To reach another computer's dashboard, forward a local port to its daemon, for example
`ssh -L 47670:127.0.0.1:47671 HOST`, run `koinon dashboard --no-open` there and open the printed
link with your local port, `http://127.0.0.1:47670/...`. Use `127.0.0.1` or `[::1]`, not
`localhost`. Each port keeps its own login, so a local daemon and a tunnel can both be open
under `127.0.0.1`.

### Log in with a maintainer secret

With a maintainer secret, the dashboard login page asks for it, so a login needs no link from
the daemon's host (PDR 009). Set one on that host:

```sh
koinon maintainer secret set        # asks twice, without echo; or reads the first line of standard input
koinon maintainer secret generate   # prints a new 43-character secret once
koinon maintainer secret clear      # removes it
```

The secret is never an argument. It is valid UTF-8 of at least 12 characters and at most 1024
bytes; one LF or CRLF at the end of the line is removed, and nothing else is changed. A macOS
terminal holds at most 1,024 bytes of a line, so give a longer secret through standard input. The
daemon keeps only a salted PBKDF2 key of it. Setting, generating or clearing the secret ends
every dashboard login. Open `http://127.0.0.1:PORT/dashboard/login`, also through a tunnel,
and enter the secret. After five wrong secrets in a row the daemon waits before it checks the
next one, from 1 second up to 60 seconds; a correct secret ends the wait. `koinon dashboard`
still gives a one-time link, also during the wait and before a secret is set. A login still
ends when the daemon restarts. Any process of your OS user can set a new secret, so the
secret keeps agents that use Koinon's tools from acting as you; it is no protection against
a same-user program that sets out to get around it.

### Act as the maintainer from another program

`koinon mcp --maintainer` is a stdio MCP server that acts as the maintainer, for a client that
is not a browser, such as a voice bridge (PDR 009). It uses the maintainer secret from above:

```sh
koinon mcp --maintainer --client koinon-voice --secret-stdin        # the first input line is the secret
koinon mcp --maintainer --client koinon-voice --secret-file ~/.config/koinon-voice/secret
ssh HOST /opt/homebrew/bin/koinon mcp --maintainer --client koinon-voice --secret-stdin
```

With `--secret-stdin` the client writes the secret as the first line, then the MCP stream; over
SSH the secret is not on the remote command line. Give the binary's full path, such as
`/opt/homebrew/bin/koinon` or `~/.local/bin/koinon`: macOS runs a non-interactive SSH command
without `path_helper`, so its `PATH` may hold neither `~/.local/bin` nor `/opt/homebrew/bin`. A
secret file is a regular file of your user with mode 0600 or 0400, not a symbolic link.
`--client NAME` (a-z, 0-9 and `-`, at most 32 characters) names the client: each action records
`mcp:NAME` as its via in the audit log and in the verdict history, as the dashboard's actions
record `dashboard`. Agents see `sender_family` `maintainer` and never the via of a verdict.

The tools are `board_list`, `decision_get`, `decision_history`, `decision_verdict` (at the
revision that `decision_get` showed), `inbox`, `ack`, `send`, `room_list`, `room_read`,
`room_post`, `room_create`, `room_add`, `room_remove`, `peers` and `peer_status`. The process
registers no agent session and is not in `peers`. It exchanges the secret for a token with the
limits of a dashboard login; each request to the daemon moves the 24-hour idle limit. When the
token ends, at a daemon restart, after 24 hours without a request, 7 days after it started or
when the secret is set, generated or cleared, the next tool call answers
`maintainer_token_expired` and the process exits; start it again. A refused start writes its
code to standard error before any MCP output: `maintainer_secret_invalid`,
`maintainer_secret_unset`, or during the wait after wrong secrets `maintainer_secret_backoff`
with `retry_after_seconds`.

A client learns of changes without reading on a timer. It subscribes with `resources/subscribe`
to `koinon://maintainer/inbox`, `koinon://maintainer/decisions` (the waiting items and the
board's counts) or `koinon://maintainer/rooms/ROOM_ID`; `resources/list` names them with the
current rooms. Right after the subscribe reply the process sends one notice for the URI, so a
change between the client's last read and its subscription is not lost. On each change the
process sends `notifications/resources/updated` with only the
URI, and the client reads it again with `resources/read`, which returns the same JSON as the
`inbox`, `board_list` or `room_read` tool. A deleted room gets one last notice, and reading it
then gives `room_not_found`. While a subscription stands, the process keeps a long poll open to
the daemon, and each poll moves the idle limit, so a client that stays connected and quiet
keeps its token until a daemon restart, the 7-day limit or a secret change. Then the process
writes `maintainer_token_expired` to standard error and exits with status 1, also when no tool
call comes. `resources/unsubscribe` ends a subscription; after the last one the process polls no
more.

Administrative actions require CSRF protection and are
audited. Health reports unknown observations explicitly.

### Clean up sessions without the dashboard

A shell on the same computer cleans up sessions and inboxes in bulk (#388). Select sessions by
peer name, `--state active|expired|retired|purge-marked`, `--family FAMILY` and
`--inactive-for DURATION` (such as `72h` or `30d`; since the retirement, otherwise the
expiry). A change needs a selection; the maintainer session is never selected. Without
`--yes`, a change prints the sessions it would change, with their unacknowledged counts and
claims, and changes nothing:

```sh
koinon sessions list --state expired                  # add --json for the daemon's reply
koinon sessions purge --inactive-for 7d --yes         # mark for purge; retires an active one
koinon sessions unpurge NAME                          # remove a mark before the sweep
koinon sessions retire --family codex --yes           # retire active sessions
koinon inbox clear --state retired --yes              # acknowledge every message
koinon maintenance sweep                              # run the retention sweep now
koinon claims release --held-by NAME --yes            # release an inactive session's claims
koinon rooms prune --yes                              # remove inactive members from every room
koinon rooms delete ROOM_ID --yes                     # delete a room with its posts
```

`koinon claims release` lists, and with `--yes` releases, the claims held by expired or
retired sessions; a participant's claims stay with the participant. `koinon rooms prune`
removes members whose session is expired or retired; a participant member stays. In the
dashboard, the sessions view selects sessions with the box before each name, or every session
on the page with **Select all sessions on this page**, and applies retire, purge, keep or clear
to each; the rooms view deletes a room. While a session is selected, the list does not refresh,
so the selection stays. The counts above the list (all, active, expired, retired) show only the
sessions of one status when chosen (`status=active`, `expired` or `retired` in the address);
**all** shows every session again. The chosen count is marked, the counts stay totals, and the
search, the sort and the next pages keep the status.

Each changed session gets the dashboard action's audit record (`purge`, `unpurge`, `retire`,
`clear`) with the origin `by cli`; a session that changed since the selection is refused with
`revision_changed`, and the others continue. The sweep deletes purge-marked sessions with
their whole inbox, under the same claim rule as the scheduled sweep, and prints its counts.

## Launcher paths in launchers.json

`koinon setup claude|codex|agy|opencode|grok` records its resolved absolute CLI path in
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
