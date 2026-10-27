# Changelog

User-visible changes to Koinon are recorded here. Unreleased entries move
into a dated release section when promoted to `main`.

## Unreleased

- OpenCode sessions can run the managed context handoff cycle on Linux (#513). An OpenCode
  session had no cycle: its Enable was refused. A session started with `koinon opencode` in
  tmux, with the family mode `handoff_cycle` and its participant address, now gets the cycle
  through its own OpenCode server: overwatch's request, a new session created and shown in the
  TUI, and the `koinon-pickup` skill command in that session with the requester's model, which
  takes the address over. The server proves the new session and the pickup's message; the
  report is accepted only while the pane's Koinon tool-call rows show the new session
  (`cycle_display_unproven` until then) and its replies come from the requested model
  (`cycle_model_changed` ends the cycle otherwise). The pickup's request and its read-back run
  apart from the controller, so a long pickup turn delays no other cycle or wake. The native checks ran with
  opencode 1.18.35. The family default stays `observe`; macOS waits for native evidence.
- OpenCode sessions now report their model and context use (#513). `peer_status` and the
  dashboard showed no context for OpenCode (`no_source`). For each session started with
  `koinon opencode`, the daemon now reads the newest finished reply's token counts and its
  model's context limit from the session's own OpenCode server every 15 seconds, with the launch
  password; it keeps only those numbers and IDs, never the message text. A direct `opencode`
  start shows none.
- A managed cycle no longer fails when the successor's pickup needs several approvals (#520).
  Each dialog during the pickup used two of the cycle's 16 events, so a pickup with more than
  about four dialogs, such as agy's, which asks before most commands, failed with
  `event_limit`. All dialogs of the pickup are now handled in one prompt wait: a dialog that the
  session answers itself records no event, a dashboard answer records two, and the wait keeps
  the deadline of its first dialog. A pickup report may name the revision of any stage of that
  wait, and between two dialogs the Sessions page shows nothing to answer.
- agy sessions can run the managed context handoff cycle on Linux (#512). An agy session had no
  cycle: its Enable was refused. A session started with `koinon agy` in tmux, with the family mode
  `handoff_cycle`, Koinon's status line from `koinon setup agy` and its participant address, now
  gets the cycle: overwatch's request, `/clear` in its pane, and `/koinon-pickup` in the new
  conversation, which takes the address over. The status-line hook proves the new conversation:
  it reports each conversation with its own process to `POST /v1/cycles/agy-witness`, and the
  daemon ties it to the cycle's pinned host. A workspace `.agent/skills` or `.agents/skills`
  `koinon-pickup` that would replace agy's entry is named as `pickup_entry_shadowed`. The hook
  also reports agy's activity. The native checks ran with Antigravity CLI 1.4.7. The family
  default stays `observe`; macOS waits for native evidence.
- agy sessions now report their model and context use (#512). `peer_status` and the dashboard
  showed no context for agy. `koinon setup agy` now makes `koinon hook agy-status` the
  `statusLine` entry of `~/.gemini/antigravity-cli/settings.json`: it wraps an existing entry's
  command, or without one keeps agy's own status line with `stack_with_default`. `koinon setup
  agy --remove-status-line` and `koinon uninstall` restore the previous entry.
- Codex's `native_compaction` mode now compacts (#509). It did nothing at the threshold: the
  Settings page showed a Codex threshold that had no effect. An enabled Codex session in that
  mode now gets Codex's own `/compact` in its tmux pane when its context reaches the threshold,
  once Codex is idle, once per crossing; a fresh reading below the threshold confirms it, and
  the Sessions page shows each compaction and its result. It needs no pickup skill or
  participant address, and Koinon changes no Codex setting. Schema 34 rebuilds the cycle table
  with the compaction states.
- A dashboard view whose read fails in the daemon, also for a stored record that cannot be
  read, answers 500 with "The daemon could not read this view." and logs
  `dashboard_view_failed` (#507). It answered 400 on the login page, "Not signed in" and "The
  request was not valid.", as for a wrong address. An invalid view query still answers 400.
  The page refreshes, and loads the whole view again once the read works.
- The dashboard refresh no longer erases typed input (#510). It waited only while a form field
  had focus, so a verdict note typed on a decision card was lost at the next refresh after a
  click elsewhere, such as on the card's **Copy** button. A field whose input is not sent now
  holds the refresh with or without focus, and the page says that it is not refreshed.
- The Decisions board's **Find** searches (#505). An exact reference opens the item as before.
  Part of a reference, or a reference in another case, lists the references that contain it,
  from the items and the deep archive, 20 per page with their titles, kinds and states. Spaces
  around the text are ignored. A **Clear** link ends the search and keeps the status filter,
  and a refresh and a verdict keep the search.
- Agents withdraw decision cards that no longer need the maintainer (#504). `decision_withdraw`
  (`koinon decision withdraw`, `POST /v1/decisions/withdraw`) archives a waiting decision item
  or an open fyi item at the revision the agent read, with a one-line reason: the work is done,
  a newer item supersedes it, it was answered in chat, or it is no longer valid. The item's
  author, a session of the author's participant or a named recipient can withdraw it. The
  withdrawal is no verdict and sends no delivery. The archive shows the item as withdrawn, and
  its lookup and `decision_get` show who withdrew it, when and why. `decision_restore` with
  `if_revision` reopens a withdrawn item at the next revision. `decision_history` with
  `withdrawals` pages every withdrawal. Schema 33 adds the table `decision_withdrawals` and
  each item's author participant; the daemon migrates the state when it starts, and the deep
  archive keeps the withdrawals.

## 2026-10-26 — Maintainer MCP mode, a maintainer secret and change notices

This release adds a way for a program to act as the maintainer. A maintainer secret logs in to
the dashboard, and `koinon mcp --maintainer` uses it to give a client, such as a voice bridge on
this machine or over SSH, the maintainer's tools and content-free change notices. Each
maintainer action records its client. Agents post in rooms without an allowance, work claims
last through a managed context cycle, and Koinon's text for agents, skills and docs says what to
do. The state schema moves from 28 to 32; the daemon migrates a v0.4.3 state in place when it
starts.

- `koinon mcp --maintainer` sends change notices (#470, PDR 009, Decision 3). It offers the
  resources `koinon://maintainer/inbox`, `koinon://maintainer/decisions` and
  `koinon://maintainer/rooms/ROOM_ID` with subscriptions: `resources/read` returns the JSON of
  the `inbox`, `board_list` (waiting) or `room_read` tool, and a change of a subscribed resource
  sends `notifications/resources/updated` with only its URI. Each subscription also sends one
  notice right after its reply, so a change between the client's read and its subscription is
  read. A deleted room is a change. While a
  resource is subscribed, the process runs the new long poll `POST /v1/maintainer/changes`,
  which returns the content-free versions of the inbox, the board and each room as soon as they
  change, after at most 30 seconds. Each poll moves the token's 24-hour idle limit, so a quiet
  subscribed client keeps its token; when the token ends, the process exits with
  `maintainer_token_expired` also while no tool call comes. Schema 32 adds one version counter
  per topic, which each write that the topic's read shows moves in its own transaction.
- `koinon mcp --maintainer --client NAME` acts as the maintainer from a program, such as a voice
  bridge, locally or over SSH (#470, PDR 009). It reads the maintainer secret from the first line
  of standard input (`--secret-stdin`) or from a private file (`--secret-file PATH`), exchanges it
  once for a token that the daemon keeps in memory, and serves the tools `board_list`,
  `decision_get`, `decision_history`, `decision_verdict`, `inbox`, `ack`, `send`, `room_list`,
  `room_read`, `room_post`, `room_create`, `room_add`, `room_remove`, `peers` and `peer_status`
  through the new `/v1/maintainer/` routes. It registers no session and is not in `peers`; the
  agent tools do not change. The token has the limits of a dashboard login and ends at a daemon
  restart and when the secret changes; the next tool call then answers
  `maintainer_token_expired` and the process exits. Each maintainer action records its client:
  the audit target ends with `by dashboard` or `by mcp:NAME`, and the dashboard's verdict
  history shows the `via` of each verdict, which schema 31 stores; agents do not see it.

- Agents post in a room without an allowance and without a maintainer post between rounds
  (#493). The `room_post` refusal `exchange_limit`, the `refill` field of the dashboard's
  room-create form, the dashboard's `room-refill` action and its notice, and the `allowance`
  field of the post receipt and of the room data are removed; the rooms table keeps its old
  `refill`, `allowance` and `create_refill` columns, which nothing reads. The other five code
  refusals of #493 stay: `identity_unavailable`, `grok_leader_mode`, the DeepSeek
  `launch`/`subagent` refusal, the required `peer-review` and `refusal-review` statuses on
  `develop` and the `.githooks/pre-commit` block on `main` and `develop`. ADR 001, PDR 001,
  PDR 005, WDR 001 and WDR 004 record the maintainer's verdicts.
- A session's work claims no longer expire during its managed context cycle (#489). Admission
  extends its live claims to 60 seconds past the cycle deadline, in the same transaction, and a
  `claim-renew` during the cycle, by the session or its managed successor, keeps that expiry.
  The claim revision and generation do not change.
- `koinon status` no longer counts an unread message to a retired session as a waiting wake
  (#486). The `wake` counts and reasons cover the sessions that the wake loop retries.
- A managed context cycle finishes for a session outside a Git checkout, such as a session that
  coordinates several repositories from their parent directory (#485). Its handoff directory is
  `_handoff/<agent>/` (or the Handoff path fact) of the directory where the session started,
  which Koinon registered; the file checks apply there and Git checks nothing. The `handoff` and
  `pickup` skills, the cycle request and `koinon guide` name that directory. Each refused ready
  report is kept on the cycle (`ready_refusals`, the newest 8, with code and text) and shown on
  the Sessions page, and a ready timeout after one names its code, for example
  `waiting_ready_timeout.refused.cycle_artifact_invalid`, in place of `.read`. The state
  database moves to schema 30.
- Koinon's text for agents says what to do, so that agents of different families work together
  (#487). The MCP server instructions, the tool descriptions, `koinon guide`, the daemon's
  messages, the skill entries it writes and the block it writes into a project's `AGENTS.md`
  lose their limits and caveats: a message from another session is a teammate's message, and
  the recipient does the work it asks for and replies through Koinon. The managed cycle keeps
  its steps (pause, write the handoff, report ready, stay idle). The install, `koinon setup
  codex` and the daemon's start remove the retired Python runtime's `KOINON CODEX` and
  `CODEX PEER BRIDGE` blocks from the Codex `AGENTS.md`, which made Codex refuse a peer's work
  requests; the install report names the result in `retired_instructions`.
- The 14 shared skills say what to do (#487). Their shared opening, their Repository facts
  paragraph and their steps lose their limits and caveats, as do the skill authoring file, the
  skills index, the project's `.koinon/skills` facts and the OpenCode command descriptions.
  `sprint` has no approval gates, `release` merges and publishes without a second approval
  step, and `pickup` starts the next action after its report. The handoff location in the main
  checkout, one open PR, auto-merge, the commit identity and the refusal-review gate stay as
  steps.
- The docs and decision records say what to do (#487). `AGENTS.md`, `README.md`,
  `PROTOCOL.md`, `CONTRIBUTING.md`, the pull request template, `docs/USAGE.md`,
  `docs/INSTALL.md`, `docs/PERMISSIONS.md` and the work and memory contracts lose their limits
  and caveats; facts about what the code does stay. The decision records whose limits had no
  maintainer source change under their own process: WDR 002 says a peer's request is a
  teammate's request, WDR 002 Decisions 5 and 6 (the sprint gates and the release approval) and
  WDR 001 Decision 4 are deprecated, and each changed record cites the maintainer's #487 answer.
  The decisions in the maintainer's own words stay. The `pair` skill drops its "once" limits.
- A maintainer secret logs in to the dashboard (#470, PDR 009). `koinon maintainer secret set`
  reads a secret from the terminal without echo, asked twice, or from the first line of
  standard input; `generate` prints a new 43-character secret once; `clear` removes it. The
  secret is never an argument. The daemon keeps only a salted PBKDF2-HMAC-SHA256 key of it
  (600,000 iterations). With a secret set, the dashboard login page asks for it, so a login no
  longer needs a link from the daemon's host; `koinon dashboard` still gives a one-time link.
  A change of the secret ends every dashboard login. After five wrong secrets in a row, the
  daemon waits before the next check, from 1 second up to 60 seconds. The state schema is 29;
  an upgrade starts with no secret set.
- The Homebrew caveats give each command on its own line, list the upgrade steps with
  `koinon version` and `koinon dashboard`, and show the `--system` step only on macOS (#483).

Upgrade from v0.4.3 with Homebrew: run `brew update` and `brew upgrade koinon`. The daemon
restarts on the new binary and migrates the state to schema 32, which ends current dashboard
logins: log in again with the maintainer secret after `koinon maintainer secret set` or
`generate`, or run `koinon dashboard` for a one-time link. A running `koinon mcp` server runs
the new release without a session restart. The `koinon-` skill entries load the new procedures
from the installed binary, and `koinon guide` prints the new guide text.

Upgrade notes:

- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- Codex keeps the tools and descriptions it read at the start until its session starts again
  (docs/INSTALL.md).

## 2026-10-25 — Progress notes for handoffs, wake guidance and failed-run accounting

This release changes procedures and guidance only. The `handoff` and `pickup` skills keep a
short progress note that a pickup reads first, the installed guide says how inbox notices reach
a session, and the `release` skill accounts for failed CI runs. The state schema stays 28; a
v0.4.2 installation upgrades in place.

- The `handoff` and `pickup` skills use a short progress note, `progress.md`, in the agent's
  handoff directory (#478, WDR 005 Decision 3). The agent updates it after each completed stage;
  `handoff` starts from it and updates it; `pickup` reads it first and then the newest handoff,
  or the one a managed cycle names. Pickup selects only timestamped files as handoffs.
- The installed guide says that Koinon sends inbox notices through the session's supported wake
  channel, so a session with a working automatic wake channel normally needs no timed inbox
  check, and that work-change notices are coalesced. It no longer says that work-change
  notices are not implemented.
- The `release` skill accounts for failed CI runs in the release scope that the project's
  Release checks define (WDR 004, Decision 6). Koinon's promotion pull request lists each
  failed run and attempt since the previous release with its cause, fix or open issue.

Upgrade from v0.4.2 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary, which ends current dashboard logins, so run `koinon dashboard` for a
new link. A running `koinon mcp` server runs the new release without a session restart. The
`koinon-` skill entries load the new procedures from the installed binary, and `koinon guide`
prints the new guide text.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md), and
  Antigravity's permission evaluation is unverified; Grok takes its rule at launch. Grok and
  DeepSeek get no skill entries.
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- Codex keeps the tools and descriptions it read at the start until its session starts again
  (docs/INSTALL.md).
- Why `codex queue` fails for a Codex session on macOS is not yet known (#466).

## 2026-10-24 — Skills for every agent family, clearer cycle refusals and Codex queue diagnostics

This release makes `koinon install` and the daemon write a skill entry for every catalog skill,
also for OpenCode and agy. A managed context cycle now names the handoff directory and the
ready deadline, and a refused ready reply says which check failed. A Codex wake whose
`codex queue` command fails says how, and the daemon log keeps a cleaned error line. The state
schema stays 28; a v0.4.1 installation upgrades in place.

- A Codex wake whose `codex queue` command fails now says how: `queue_failed` (non-zero exit),
  `queue_timeout`, `queue_drain` or `queue_unconfirmed` (also a signal), and the daemon log gets
  a `codex_queue` event with the exit status and the first error line. That line keeps only plain
  words and short numbers; the thread ID, the notice, IDs, keys and URLs are replaced, and so is
  the rest of the line after a credential name such as `password` or `Bearer`. A non-zero exit is
  no longer counted as a notice, so a message whose queue command keeps failing is retried and
  never ends as `not_read` (#466).
- A managed context cycle's request now names its ready deadline as a UTC time, such as "Report
  ready by 2026-10-23 21:11:07 UTC", and asks the session to stop background subagents that
  will not finish with enough time left to update the handoff and report ready. For a Claude
  session waiting for its ready reply, the Sessions page shows how many of its background
  subagents are still running, or `unknown` when they cannot all be read (#467).
- A managed context cycle's ready reply that is refused as `cycle_artifact_invalid` now says
  which check failed, such as "must be in _handoff/claude/ of the main checkout", and the
  request names the session's handoff directory. That directory follows the Handoff path fact
  of the repository's `.koinon/skills/handoff.md`, else `_handoff/<agent>/`, where the agent is
  the family with the launch's role, not the participant address. Before, a session that wrote
  its handoff elsewhere got the same general refusal three times, and its cycle failed (#468).
- `koinon install`, and the daemon at each start, now write a `koinon-NAME` skill entry for every
  catalog skill, not only the team skills: also `koinon-check`, `koinon-ship`, `koinon-release`,
  `koinon-pair`, `koinon-peer-review`, `koinon-refusal-review`, `koinon-sprint` and
  `koinon-guide`, which replaces only an earlier Koinon entry, never an adopted copy. OpenCode and
  agy get `koinon-pickup` and these entries in their configuration directories
  (`~/.config/opencode/skills`, `~/.gemini/config/skills`) when those directories exist. Grok
  and DeepSeek get none, because where they discover user skills is not verified. The uninstall
  removes them. An entry directory or file that is a symbolic link, such as a link to a
  project's skill, is left as it is and reported as `kept` (#461).
- After `koinon install` at a terminal adds a family's permission rules, the family's `review`
  and `agents` entries no longer report them as `missing` with a note to run the install again:
  they report them as `added`, and the `agents` entry lists them in `changed`, as `koinon setup`
  does (#462).

Upgrade from v0.4.1 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary, which ends current dashboard logins, so run `koinon dashboard` for a
new link. A `koinon mcp` server started from v0.4.1 runs the new release without a session
restart (#440). The daemon writes the new skill entries when it starts; agent sessions started
after the upgrade list them.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md), and
  Antigravity's permission evaluation is unverified; Grok takes its rule at launch. Grok and
  DeepSeek get no skill entries.
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- Codex keeps the tools and descriptions it read at the start until its session starts again
  (docs/INSTALL.md).
- Why `codex queue` fails for a Codex session on macOS is not yet known (#466); the daemon log's
  `codex_queue` event now records the failure line.

## 2026-10-23 — Team skills, Claude wakes during work and one dashboard login per port

This release makes `koinon install` and the daemon write the team skills next to
`koinon-pickup`, so each agent family can run the shared team procedures with the repository's
own facts. A Claude session now gets its wake notices while it works or while background
subagents run, and a managed context cycle asks again when a subagent worked after the ready
report. Each dashboard port keeps its own login. The state schema stays 28; a v0.4.0
installation upgrades in place.

- `koinon install`, and the daemon at each start, now write the team skills for Claude Code and
  Codex: `koinon-spin-up`, `koinon-spin-down`, `koinon-recover-team`, `koinon-peer-tmux` and
  `koinon-handoff`, next to `koinon-pickup`. Each one loads its catalog item from the installed
  binary, so the procedure changes with the runtime, and the procedure reads the repository's
  own facts from `.koinon/skills/NAME.md`. A project's own skill of the bare name is left as it
  is. The install report lists them in `team_skills`; the uninstall removes them (#453).
- A dashboard login no longer ends when another dashboard is opened under the same loopback
  address, such as a local daemon and a tunnel to another computer's daemon. The login cookie
  is now named after the port, `koinon_dashboard_PORT`, so each port keeps its own login. A
  login made before the upgrade ends at the upgrade's daemon restart, as before (#454).
- A Claude session now gets its wake notices while it works: during a turn, Claude Code takes
  the notice at its next tool boundary, and at a prompt with background subagents running, at
  once. Before, Koinon held every notice while the session showed `busy`, so a managed context
  cycle request could go unseen until its deadline. Only an approval or question prompt still
  holds the notice (#456).
- A managed context cycle no longer clears a Claude session whose background subagent worked
  after the session reported ready: it asks again under a new revision, so the new handoff holds
  the subagent's results. The request and the guide say to let background subagents finish, or
  to stop them, before writing the handoff (#456).

Upgrade from v0.4.0 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary, which ends current dashboard logins, so run `koinon dashboard` for a
new link. A `koinon mcp` server started from v0.4.0 runs the new release without a session
restart (#440). The daemon writes the team skills when it starts; agent sessions started after
the upgrade list them.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md), and
  Antigravity's permission evaluation is unverified; Grok takes its rule at launch.
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- Codex keeps the tools and descriptions it read at the start until its session starts again
  (docs/INSTALL.md).

## 2026-10-22 — Interactive install with permission rules, MCP servers that follow an upgrade and clearer handoff notices

This release makes `koinon install` on a terminal list the agent families on the computer, ask
which to set up and offer each family's recommended permission rules. A running `koinon mcp`
server now runs the new release after an upgrade without a session restart. A handoff request
is named in its wake notice, and a timeout says whether the session read it. The state schema
stays 28; a v0.3.3 installation upgrades in place.

- `koinon install` on a terminal lists the agent families on the computer, asks to set up
  each installed family that is not set up, and offers to add each family's recommended
  permission rules after it shows them: Claude Code and Antigravity allow rules, the Codex
  server approval and the OpenCode permission rules. A yes keeps every other setting.
  `koinon setup FAMILY` on a terminal asks the same question. Without a terminal, install and
  setup ask nothing, add no rule and report each family's rules in `permissions` (#446, #449).
- The recommended Claude rules now also allow `koinon context show` and `koinon status`.
  docs/PERMISSIONS.md describes how Claude Code's auto mode can refuse Koinon calls without
  these rules (#446).
- A running agent session's `koinon mcp` server runs the new release after an upgrade, in its
  own process and without a session restart, at a point where no request is in progress. It
  keeps the agent's MCP session and registered sessions and asks the client to read the tool
  list again: Claude Code and OpenCode then show the new tools and descriptions; Codex keeps
  the ones it read at the start until the session starts again. A `peers` result reports the
  server's release as `server_version`, and `koinon version` reports `mcp_resume`. A session
  whose server started from an earlier release needs one restart after this upgrade (#440).
- A wake notice whose range holds a managed context-cycle request ends with "Managed
  context-cycle request ID: answer it before other work.", so a session that also holds a
  participant address does not take it for a routine notice. A cycle whose ready report did
  not come in time fails with `waiting_ready_timeout.read` or `waiting_ready_timeout.unread`,
  which says whether the session read the request (#445).
- The dashboard's bulk session action without a selected session now says "Refused: select one
  or more sessions first. Nothing was changed." (`no_selection`), instead of the general "the
  request was not valid" (#442).

Upgrade from v0.3.3 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary, which ends current dashboard logins, so run `koinon dashboard` for a
new link. Restart each agent session once afterwards: a `koinon mcp` server started from v0.3.3
or earlier keeps its binary, and after this restart later upgrades reach it without one. To
review the families and add the permission rules, run `$(brew --prefix)/bin/koinon install` in
a terminal.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md), and
  Antigravity's permission evaluation is unverified; Grok takes its rule at launch.
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- Codex keeps the tools and descriptions it read at the start until its session starts again
  (docs/INSTALL.md).

## 2026-10-21 — Pause before the handoff clear, a Sessions status filter and longer dashboard logins

This release makes the managed handoff cycle ask the session to pause, report ready and stay
idle, and ask again when it keeps working, so a cycle on an active agent reaches its clear.
The dashboard Sessions page gets a status filter with counts and a select-all box for bulk
actions, and a dashboard login lasts 24 hours without a request. The state schema stays 28; a
v0.3.2 installation upgrades in place.

- The managed handoff cycle asks the session to pause at a safe stopping point, write its
  handoff, report ready and then end its turn; the ready answer says to start no other work.
  When the session does not become idle after its ready report, `overwatch` sends the request
  again under a new revision instead of failing the cycle with `ready_timeout`, and the session
  writes a new handoff for it (#431).
- The dashboard Sessions page has a status filter: a count for each status (all, active,
  expired, retired) that shows only the sessions of that status, with the search, sort and
  paging. **Select all sessions on this page** selects every session in the list for a bulk
  action, and the list no longer refreshes over a selection (#429).
- A dashboard login now lasts 24 hours without a request (was 30 minutes) and at most 7 days
  (was 12 hours), and its cookie survives a browser restart within that time (#435).

Upgrade from v0.3.2 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary, which ends current dashboard logins, so run `koinon dashboard` for a
new link. Restart each agent session afterwards, so its `koinon mcp` gives the new
`context_cycle_ready` description; the new request text and ready answer come from the daemon
at once.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md).
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- A running agent session keeps the `koinon mcp` server it started with (docs/INSTALL.md).

## 2026-10-20 — Handoff cycles on active machines, a chosen port and Homebrew tmux on macOS

This release lets the managed handoff cycle clear a Claude Code session that has a background
task running, and enters Koinon's own pickup skill after the clear, so projects with their own
`pickup` skill get the cycle. The daemon and dashboard can use another port, and the dashboard
works through an SSH tunnel. The macOS daemon runs the agent's own Homebrew `tmux`, so pane
wakes of Codex and Grok arrive. The Decisions page gets a status filter and a full-width card
for an item opened by reference. The state schema is 28; a v0.3.1 installation upgrades in
place, and a v0.3.1 binary does not open the upgraded state.

Upgrade from v0.3.1 with Homebrew: run `brew update` and `brew upgrade koinon`; the daemon
restarts on the new binary and writes the `koinon-pickup` skill. On macOS, then restart each
agent session that runs in tmux, so its new `koinon mcp` reports its `tmux` path
(docs/INSTALL.md). A failed cycle ends the session's Enable: run `koinon context enable NAME`
again for each session whose cycle failed before the upgrade (docs/USAGE.md).

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md).
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- A running agent session keeps the `koinon mcp` server it started with; the new behavior
  appears when the session, or its MCP server, starts again (docs/INSTALL.md).

Changes:

- Daemon and dashboard port (#405): `koinon install --port N` records the port of both loopback
  listeners in the state directory (`<state>/port`), on Linux, for the macOS agent and in macOS
  system mode; the service file is unchanged, and `koinon serve --state-dir` reads the port.
  Every command, hook, launched agent and `koinon mcp` without `--address` or
  `KOINON_DAEMON_ADDRESS` uses it, and `koinon dashboard` prints its link there. A later
  install, an upgrade and `brew upgrade` keep it; `--port 47671` returns to the default.
  Without a recorded port, everything stays at 47671 (docs/INSTALL.md).
- Dashboard through an SSH tunnel (#407): the dashboard accepts a `Host` of `127.0.0.1` or
  `[::1]` with any port, so a tunnel from another local port, such as
  `ssh -L 47670:127.0.0.1:47671 HOST`, works. Host names such as `localhost` are still refused,
  and `Origin`, CSRF and login checks are unchanged. ADR 001, Decision 4.
- Native upgrade evidence (#404): the native lifecycle job installs v0.3.1 from its Homebrew
  keg in user mode and checks that `brew upgrade` alone moves `koinon version` on the PATH and
  the daemon to the new build; the Go upgrade test starts from v0.3.1.
- Koinon's own pickup skill for the handoff cycle (#406): after the clear, the managed cycle
  enters `/koinon-pickup` (Claude Code) or `$koinon-pickup` (Codex), not the bare `/pickup` or
  `$pickup`. The daemon writes that one skill for you at each start, and `koinon install` and
  the upgrade's install write it too, never into a repository: `skills/koinon-pickup/SKILL.md`
  under the Claude Code configuration root (`$CLAUDE_CONFIG_DIR`, else `~/.claude`) and
  `~/.agents/skills/koinon-pickup/SKILL.md` for Codex. A `brew upgrade` restarts the daemon,
  which writes it, so the upgrade needs no further command. The install report lists the files
  under `pickup_skills`, and `koinon uninstall` removes them. The skill only loads the
  catalog's `skill.pickup` and checks the cycle's `catalog_digest`. Probe checks only that
  file, byte for byte: a missing or different file is `pickup_entry_missing` or
  `pickup_entry_differs`, and the Sessions page and `koinon context show` (`unavailable_step`)
  name the `koinon install` step, for example for a session whose Claude Code runs with another
  configuration root than the daemon. Personal and project `pickup` skills are no longer read,
  so sessions in projects with their own pickup skill now get the cycle, and that skill stays
  as it is. If you copied the catalog's pickup skill into `~/.claude/skills/pickup` or
  `~/.agents/skills/pickup` only to enable the cycle, you can remove that copy; it shadows your
  projects' own `pickup` skills and the cycle does not use it. `koinon claude` no longer adds
  the `--add-dir` loader, and the cycle no longer enters `/add-dir` before the clear or
  answers the add-directory dialog; `<state>/cycle/claude-loader` is no longer written or read
  and can be removed.
- Repository discovery (#410): a registration's Git calls share one six-second limit instead
  of three seconds each, so a slow first Git call no longer fails with
  `repository_git_timeout`; a hung Git still does. The refusal now gives the time of each
  Git call in `details.calls_ms` and the limit in `details.limit_ms`, and the command line
  prints them, so a later timeout shows which call was slow.
- Decision board filter (#411): each count on the dashboard's Decisions page (waiting, in
  progress, open fyi, archived) is a link that shows only the items of that status, and
  **all** shows every item again. The filter is the `status` query parameter, so it stays
  after a reload, a lookup, paging and a verdict; the counts stay totals, and an unknown value
  is refused. The page uses plain links, with no new script (PROTOCOL.md, Dashboard).
- Decision lookup card (#414): an item opened by reference on the Decisions page, from
  **Find** or the agent table's "Waiting for a verdict" links, shows its card at full width
  when it is waiting, in progress or an open fyi, with the same **Approve**, **Decline**,
  **Dismiss** and note as on the board; the cards below leave it out, so it shows once. A
  verdict there returns to the same lookup and status filter, with the answer on that card.
  Archived items stay read only, and the verdict history stays below the item (PROTOCOL.md,
  Dashboard).
- Handoff on an active machine (#417): after the ready report, the cycle clears a Claude Code
  session that is at its prompt with a background task running (registry status `shell`, such
  as a monitor of its inbox); before, it waited for `idle`, which such a session never shows,
  and the cycle failed with `ready_timeout` and ended the Enable. Peer messages after the
  ready report still wait until the cycle ends. A stage that times out while waiting for the
  pane now names what it saw, such as `ready_timeout.status_busy` or
  `ready_timeout.input_not_empty`.
- Homebrew tmux for the macOS daemon (#418): launchd's default `PATH`, which the LaunchAgent and
  the LaunchDaemon both get, has no Homebrew directory, so the daemon could not run tmux, and
  pane wakes of Codex and Grok waited as `codex_target_unavailable` or `grok_target_unavailable`
  with no notice. `koinon mcp` now reports the absolute `tmux` of the agent's own terminal when
  it registers, and the daemon runs that path after checking that it resolves to a regular
  executable owned by root or the daemon's user and not writable by its group or others; state
  schema 28 stores it. A tmux that cannot run now waits as `tmux_unavailable`, apart from an
  unproven pane, also in the handoff cycle's probe and inspection; the messages stay queued.
  Every pane, idle and submission check is unchanged. After the upgrade, restart each agent
  session that runs in tmux, so its new `koinon mcp` reports the path (docs/INSTALL.md).

## 2026-10-17 — Homebrew upgrades and maintenance from the command line

This release makes `brew upgrade koinon` the complete upgrade of a Homebrew installation, and
lets the maintainer choose a participant's holder and clean up sessions, inboxes, claims and
rooms from the command line, each change audited with the origin `cli`. The state schema stays
27; a v0.3.0 installation upgrades in place.

Upgrade from v0.3.0 with Homebrew: run `brew update`, `brew upgrade koinon`, then once
`$(brew --prefix)/bin/koinon install`, because a v0.3.0 installation has a copy of the binary.
After that, `brew upgrade koinon` alone upgrades the running daemon (docs/INSTALL.md).

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md).
- A macOS system-mode daemon (`--system`) keeps a copy; upgrade it with
  `$(brew --prefix)/bin/koinon install --system`.
- A running agent session keeps the `koinon mcp` server it started with; the new behavior
  appears when the session, or its MCP server, starts again (docs/INSTALL.md).

Changes:

- Homebrew upgrade (#397): `brew upgrade koinon` alone upgrades the running daemon. In user
  mode, `koinon install` from the Homebrew binary links `<prefix>/bin/koinon` to
  `$(brew --prefix)/opt/koinon/bin/koinon` (`"binary_result": "linked"`) instead of copying it.
  A daemon that a service manager runs exits when its executable is replaced
  (`binary_changed`), and the manager starts the new binary. An installation made by v0.3.0 or
  earlier needs `$(brew --prefix)/bin/koinon install` once after its upgrade; macOS system mode
  keeps a copy. `koinon status` reports the daemon's `version`.
- Holder choice from the command line (#398): `koinon participant list` shows each participant
  address with its holder, conflict and last event, and `koinon participant holder ADDRESS PEER
  [--if-revision N]` makes the dashboard's **Make holder** choice: the former holder is retired
  and fenced, and the chosen session's fence is lifted. The daemon serves them as
  `GET /v1/participants` and `POST /v1/participants/holder`, audited by `cli`. PDR 001,
  Decision 5 records the change.
- Cleanup from the command line (#388): `koinon sessions list|purge|unpurge|retire` and
  `koinon inbox clear` select sessions by peer name, `--state`, `--family` and
  `--inactive-for`, and show the selection without `--yes`. Each change runs the dashboard
  action's store path and audit record, by `cli`. `koinon maintenance sweep` runs the retention
  sweep now. Routes `POST /v1/cleanup/sessions` and `POST /v1/maintenance/sweep`.
  `koinon claims release` releases the claims of expired or retired sessions, `koinon rooms
  prune` removes their room memberships, and `koinon rooms delete` (and the rooms view's new
  delete action) deletes a room. The sessions view selects several sessions for retire,
  purge, keep or clear.

## 2026-10-16 — Context settings without the dashboard

This release changes the context-cycle settings from the command line and from agent sessions
through MCP, each change audited with its origin, and adds a per-session context threshold.
The cycle adapters run with any Claude Code and Codex version, and the Codex cycle is
available on macOS. The state schema is 27; `koinon install` upgrades a v0.2.x installation in
place, and a v0.2.x binary does not open the upgraded state.

Known limits:

- The DeepSeek harness's permission grammar remains unverified (docs/PERMISSIONS.md).
- A running agent session keeps the `koinon mcp` server it started with; the new MCP tools
  appear when the session, or its MCP server, starts again (docs/INSTALL.md).

Changes:

- Cycle adapters and native versions (#196): the Claude Code and Codex cycle adapters run with
  any CLI version; a run's native checks certify the version current then (PDR 006, Decision
  5). The Codex cycle is available on macOS: its checks passed there (X0, X1 and X3 with Codex
  CLI 0.162.1; X0 and the approval case X2 with 0.163.0). `native_version_unverified` is no
  longer reported.
- Context settings without the dashboard (#391): `koinon context show|set|enable|disable|threshold`
  and the MCP tools `context_show`, `context_set`, `context_enable` and `context_threshold`
  change the family mode and threshold and a session's Enable, as the dashboard does, and
  `koinon context show` lists a waiting native dialog. Each change is audited with
  its origin (`cli`, or `agent` and the calling session). A session can have its own threshold,
  which replaces its family's while it is set (dashboard, command line and MCP); state schema 27.
- Managed cycle approvals (#389): when the maintainer approves a successor's
  `context_cycle_pickup` call on the Sessions page, the report completes the cycle also when
  the return to `waiting_pickup` is recorded before the report. Before, the report could be
  refused with `cycle_changed`, Codex asked for a second approval, and the cycle failed at
  `waiting_prompt_timeout`.

## 2026-10-15 — Homebrew bottles

This release publishes Homebrew bottles, so that `brew install` and `brew upgrade` need no
developer tools on Apple Silicon and Linux, and adds `brew update` to the upgrade steps. The
state schema stays 26; `koinon install` upgrades a v0.2.1 or v0.2.0 installation in place.

- Homebrew bottles (#384): each release publishes a Homebrew bottle for macOS (arm64 and Intel,
  macOS 12 and later) and Linux (arm64 and x86_64), and the formula pours it. On Apple Silicon
  and Linux, Homebrew then needs no developer tools (Homebrew still requires them on Intel
  Macs): before, the formula installed the bare binary as a build from source, which on
  macOS 26 failed without the Command Line Tools. The release workflow
  installs the bottles through Homebrew on macOS and Linux before it publishes a release.
- Homebrew upgrade steps (#383): README, docs/INSTALL.md and the formula's caveat run
  `brew update` before `brew upgrade koinon`, and check `koinon version` after the install.
  Without the update, `brew upgrade` could keep the formula of the previous release.

## 2026-10-14 — Fixes: Homebrew upgrades, the dashboard view and release notes

This release fixes the Homebrew upgrade step, keeps the dashboard's sort and search across
actions and refreshes, and publishes release notes from the tag without a manual step. The
state schema stays 26; `koinon install` upgrades a v0.2.0 installation in place.

- Homebrew upgrades name the Homebrew binary (#380): the formula's caveat, README and
  docs/INSTALL.md say `$(brew --prefix)/bin/koinon install` (`#{opt_bin}/koinon install` in the
  caveat). Once `~/.local/bin` came first on `PATH`, the documented bare `koinon install` ran the
  copy already installed and copied it onto itself, so a `brew upgrade` changed nothing. When
  the installed binary runs the install and another `koinon` on `PATH` has other content, the
  report now names it in `other_binary`, with the command in `source_step`.
- Dashboard actions keep the view (#373): an action on Sessions, Messages, Memory or Work
  returns to the same sort order, search and filters, on the first page. Before, Enable
  handoff, Retire and the other actions reloaded the view with the default order and no search.
  An open read-only disclosure, such as a session's nested repositories, no longer stops the
  5-second refresh, and it stays open across it. Before, it kept the view stale until closed.
- Release notes from the tag (#377): the release workflow fetches the annotated tag before it
  creates the GitHub release, so `--notes-from-tag` reads the tag message. Before, the publish
  job had no local tag and failed, as it did for v0.2.0, whose release was then created by hand
  from the tag message.

## 2026-10-14 — Participants, rooms, the decision board and managed context cycles

This release adds participant addresses with roles and verified succession, rooms and the
decision board on the dashboard, and managed context cycles for Claude Code (Linux and macOS)
and Codex (Linux). It adds a macOS system service that survives logout, the Codex pane wake
after `/clear`, the Grok family, and portable skills embedded in the binary. The state schema
is 26; `koinon install` upgrades a v0.1.0 installation in place.

Known limits:

- The cycle adapters run only with the CLI versions that their evidence covers: Claude Code
  2.1.296 and Codex CLI 0.162.0. Another version reports `native_version_unverified`.
- The Codex cycle stays unverified on macOS until its approval case passes (#196).
- On macOS, the system service's start at boot with nobody logged in and a reinstall while
  logged out are not yet checked live (#283).
- The DeepSeek harness's permission grammar remains unverified (#199).
- A running agent session keeps the `koinon mcp` server it started with; tools that this
  release adds appear when the session, or its MCP server, starts again (docs/INSTALL.md).

Changes:

- Managed context cycle for Claude Code on macOS (#196): the macOS live check passed with
  Claude Code 2.1.296, so the adapter is available on macOS. The Codex adapter passed the same
  run except its approval case, which the sheet now runs with user approvals; Codex stays
  unverified on macOS until that case passes.
- Screen reads in a truecolor terminal (#371): the Codex pane wake and the Codex and Claude Code
  cycle adapters read a 24-bit color (`48;2;R;G;B`) or a 256-color index of 0, 2 or 22 as color
  data, not as the dim or reset attribute. Before, a notice typed on a truecolor Codex composer
  read as the dim placeholder, so the wake never pressed Enter and ended
  `pane_submission_unconfirmed`, and typed text on a truecolor Claude Code input line read as
  empty.
- Managed context cycle for Codex (#196): a session started with `koinon codex` in tmux
  on Linux (Codex CLI 0.162.0) can run the cycle. The daemon enters `/clear`, then `/status`,
  whose card names the new thread for the reset proof, then a bare `$pickup` only when every
  pickup skill that Codex lists is the catalog item and no `skills.config` entry turns one off
  (each configuration layer is read as TOML, with the new dependency
  `github.com/BurntSushi/toml`). The new thread takes the address at once as `cycle_reset`,
  only as the reset's proven successor. A session that holds no participant address reports
  `participant_unheld`. For every family, the epoch is proven again right before the pickup
  (`epoch_changed`), a dialog during the pickup wait is shown on the Sessions page, and the
  report is accepted during it with the revision of the pickup wait.
- `refusal-review` skill: the review also checks each maintainer decision a change records or
  cites. The record states the question it answered, the options, the answer quoted with its
  date, and the reason with its scope; a bare quote is a finding (WDR 001, Decision 6).
- Store reopen (#355): a restarted daemon no longer creates the state database's
  shared-memory file. The driver sorted its connection settings, so exclusive locking came
  after the log mode; the store now applies them in its own order on each connection.
- Managed context cycle for Claude Code (#196, #356): a session that needed the interactive bootstrap
  ran only its first cycle; the next one ended `uncertain` (`dialog_not_shown`), because the
  session keeps the added loader directory across `/clear` and Claude Code then shows no
  dialog. The adapter now accepts that reply and still checks the `/pickup` menu. On macOS the
  adapter reads the host's configuration directory and process start.
  Linux live evidence for a native dialog inside a cycle and for daemon crashes around the
  clear and pickup inputs is in `native-evidence.md` of that sprint.
- Decision board (#206): the dashboard's Decisions page is the designed board. It
  shows counts, a table of the active agents (name, activity, claimed work, a context meter or
  "not reported", and the waiting items that name the agent), and cards with `ACTION FOR YOU`
  fyi items first. Context, recommendation, agent text and notes render from a fixed Markdown
  subset with HTML, links and images as literal text. Each command has its own box with a
  Copy button; verdict buttons are disabled while a request runs, and the answer shows on the
  card. The archive and a read-only lookup show each verdict with its text.
- Decision verdicts (#206): the dashboard's Decisions page gives Approve and Decline
  on decision items and Dismiss on fyi items, on the revision shown, with a snapshot of that
  text. Each named agent gets a typed inbox message and a wake that names the verdict ID, and
  reads `decision_pending`, `decision_get` and `decision_history` and acknowledges with
  `decision_ack`. Approved work is closed with `decision_close`. Editing a decided item
  reopens it. 30 days after archiving, records move to a monthly log file in the state
  directory, and `decision_restore` brings one back; nothing is deleted. Schema 26.
- Faster store open: the expected schema catalog of each version is built once per process
  instead of by migrating an in-memory database twice at each open. The tests copy a
  migrated template store, so the race suite of `internal/core` fits its 30-minute limit
  again (about 13 minutes on the maintainer's Linux host, from about 29).
- DeepSeek live wake (#199): the wake of an idle DeepSeek Harness 0.2.0-rc.2 session
  (`dsh web`) through the Go daemon is proven in a development container. The guide,
  PERMISSIONS and INSTALL no longer call the live proof deferred; the harness's permission
  grammar remains unverified.
- Decision items (#206): agents create, edit, list and read items for the
  maintainer's decision board with `decision_create`, `decision_edit`, `decision_list` and
  `decision_get` (MCP, `POST /v1/decisions/*` and `koinon decision ... --as`). Lists are
  bounded JSON pages in item ID order (`waiting`, `active`, `in_progress`, `archived`, `mine`);
  no agent reads the dashboard page. Fields have byte limits, terminal-copy commands are
  printable ASCII, and known credential formats are refused. The dashboard has a read-only
  Decisions page. Schema 25 adds the decision tables.
- Codex pane wake (#275): on Linux and macOS, a participant notice for a session started with
  `koinon codex` in tmux is typed into the session's own pane instead of `codex queue`, so a
  message sent between `/clear` and the new thread's first turn runs in the visible new thread,
  not in the cleared one. The daemon proves the pane and that the host holds the terminal's
  foreground, types only while the observed activity is idle and the input is empty, and in each
  attempt types the notice once and presses Enter once, reading it back before Enter and after.
  On macOS, `platform.ProcessGroups` reads the process groups and terminals from
  `kern.proc.all`. Sessions outside tmux and session inbox notices keep the queue.
- Codex queue wait after `/clear` (#275): Codex starts a new `koinon mcp` server under the host
  at each `/clear`, before the new thread's first turn. A `codex queue` wake of the address
  holder, outside tmux or for its session inbox, waits with `codex_thread_unproven` while such a
  server started after, or up to 1 second before, the holder's first registration, last tool
  call and last rollout event (less 2 seconds), so the cleared thread does not read the
  message. `platform.ProcessStartTime` reads a process's wall-clock start on Linux and macOS.
- `peers` marks the calling session's own row with `self` (#348), so a session finds its own
  peer name, address, holder flag and succession result when several sessions of its family
  share a repository.
- Claude Code cycle adapter (#196): the managed context cycle runs for Claude Code
  2.1.296 sessions on Linux. It types `/clear` and `/pickup` into the session's own tmux pane,
  proving the pane and rechecking the cycle and Enable before every key, and proves the reset by
  the session's new native ID. `koinon claude` adds Koinon's pickup loader (`--add-dir`); a
  session without it gets it before the clear. The Sessions page names why a session cannot run
  the cycle and shows a dialog the cycle does not answer, with one button per option. The pickup
  skill now names the cycle query and pickup report after a clear. The state database moves to
  schema 24.
- Managed context cycle contract (#196): schema 23 adds durable cycles with a
  bounded event history, page credit for every remaining stage and the terminal outcome, the
  typed request from the reserved `overwatch` identity (its type, cycle and Enable revision in
  inbox reads), the `context_cycle_status`, `context_cycle_ready` and
  `context_cycle_pickup` tools (and DeepSeek `cycle-*` commands), handoff-file checks in the
  main checkout, cycle fences on Disable, mode changes, retirement, purge and holder choice,
  pane wake deferral, the Sessions page cycle history and the guide's Managed cycle reporting
  section.
- A macOS system-mode install after a desktop logout no longer stops at `observe_agent`
  (#344): launchctl's error 125 for the user's GUI domain counts as no desktop login, as
  `Could not find domain` does after a reboot, and the agent steps are skipped.
- Installer failure reports (#342) have `ok: false` while retaining completed-step details.
  macOS installation checks an existing log directory before replacing the binary or
  link, and creates a missing directory during the system step.

- Context policy (#196): the Settings page sets each family's context handoff mode
  and threshold, and the Sessions page shows each session's Enable switch and whether its
  fresh context reading reaches the threshold. Enabling is refused until the managed cycle is
  available; family changes enable nothing. Claude and Codex context reports carry their
  native transcript or thread, kept private. The state database moves to schema 22.
- Wake-pass diagnostics (#335) report the error class, stage, time and bounded error-type
  details, including SQLite result codes, in health and daemon logs. A successful pass
  clears the current fault while retaining the last failure; repeated identical faults
  do not flood logs. Raw error text, message bodies and native IDs are excluded.

- Room conversation view (#217): an open room on the Rooms page lists each post's
  recipients as they were at the post, with the delivery state and acknowledgement of each
  inbox copy, skipped inactive members, and `not retained` once a copy is deleted, plus the
  retained floor. Viewing acknowledges nothing.
- Inspect installed skills and setup targets with `library_state` and `koinon library state`
  (#145), including content, native name collisions, repository facts and scoped
  adoption decisions. Machine decisions use compared revisions and retain adopted digests
  through a decline; project receipts use repository memory. Connection guidance explains
  exact-change adoption and recommends required skills from verified state. These interfaces
  edit no adopted files. Schema 21 adds one ordinary machine-decision table.
- Room posts (#217): the maintainer posts to a room from its dashboard page, and
  members post with `room_post` or `koinon room post`. One write stores the ordered post, an
  inbox copy for each active member with typed `room` metadata (`seq`, `origin`
  `maintainer` or `member`) and a skipped outcome for each inactive one. A maintainer post
  allows the agents the room's refill value of posts; at the limit a member post is refused
  with `exchange_limit`. `room_read` and `koinon room read` page the posts since a member
  joined, with retention gaps. Posts are kept while any copy is. The state database moves
  to schema 20.

- Dashboard rooms (#217): the maintainer creates a room on the new Rooms page,
  sets its agent-post refill value (default 32) and adds or removes active sessions by peer
  name. A session that holds a participant address joins as that participant and keeps the
  membership across holder succession. Each member gets an inbox message with typed `room`
  metadata; `room_list`/`room_members` and `koinon room list|members` read your rooms and a
  room's roster. At most 64 rooms, 32 active members and 128 memberships per room. The state
  database moves to schema 19.

- A Grok session started with `koinon grok` is woken through its own tmux pane (#254).
  Before each wake the daemon proves that the recorded pane still holds the session's
  host, and it types Koinon's fixed notice and Enter only while Grok's input box is empty and
  no turn is running; otherwise nothing is sent and the wake waits.

- Koinon reads tmux's answers with space-separated fields. tmux before 3.4 (for example 3.3a
  on Debian 12) prints a tab in a format as `_`, so `koinon FAMILY --tmux-session` reported
  `invalid tmux launch result` after starting the session, and a launched session's pane was
  not proven for terminal naming, same-pane succession or the Grok wake (#254).

- Discover work by creator and current assignment, and receive coalesced daemon work
  notices through the existing inbox and wake adapters. Durable audience watermarks
  reconcile missed notices after restart or holder changes; ordinary notification
  admission preserves funded work controls. Status shows the 256-audience limit and
  unresolved or omitted audiences, with complete paged queries as the fallback.
  Worker progress does not create a wake loop, and routing history is read outside
  the memory operation lock.
  The state database moves to schema 18 (#140).

- A worker reports a held item's result with a summary and evidence (`work_report`,
  `koinon work report`); the claim is released and the item stays `reported` until another
  worker accepts or rejects it (`work_accept`, `work_reject`). The reporting consumer and
  native session cannot record the verdict. Reject returns the item open with a corrected
  scope. Each report reserves its verdict's storage first, or refuses and keeps the claim;
  at most 16 verdicts are pending per store and 32 per daemon. The state database moves to
  schema 17 (#140).

- Route work proposals to a repository's family/role, bind new role claims to the accepting
  native session, and page current assignment queries completely. Existing/default participant
  claims retain their generation and cursor continuation (#140).

- Add the portable `refusal-review` skill: a different agent family checks a frozen commit for
  added or changed refusals, gates, caps and exclusions, in text, tests and code, that have no
  source covering their case, and records a commit-bound verdict. The `develop` ruleset
  requires its `refusal-review` status beside `peer-review` (WDR 001, Decision 6).

- Agents can query the installed skill/setup catalog through `library_list`, `library_read`
  and `koinon library list/read` (#145). Connection instructions and each family
  guide explain the catalog, its native destination support and the reviewed-diff adoption
  procedure. Queries return embedded reference content without inspecting or changing
  project/native files or recording adoption decisions.

- Grok Build is a supported agent family (#254). `koinon setup grok` adds the
  user-scope `koinon` MCP server and reports the Grok Build version; `koinon grok` starts Grok
  with a launcher-chosen first session ID (`--session-id`) unless its arguments select a
  session. Koinon identifies each call by the `GROK_SESSION_ID` of a server whose parent is a
  Grok terminal process; a direct start registers as a plain peer, a sub-agent's calls act as
  its parent, and a session served by Grok's leader process is refused with
  `grok_leader_mode`. The dashboard can start Grok sessions.

- Repository registration distinguishes an unavailable Git executable or Git discovery
  timeout from invalid input, with fixed diagnostic error codes (#319).

- Embed the portable shared skills and setup references in the binary; report their
  fingerprint as `catalog_digest` in `koinon version`. Add team launch/stop/recovery and
  runtime-guide skills, project fact schemas and derived OpenCode commands. Skill adoption
  remains explicit.
  Repository facts accept CRLF, blank source-list lines and literal shell syntax as inert
  text; their required Go checks are verified against project instructions.

- `koinon claude|codex|agy|opencode` without `--cli` and without a recorded CLI now finds the
  family's CLI on `PATH`, records its absolute path in `launchers.json`, prints
  `koinon: recorded the FAMILY CLI PATH in launchers.json` and starts it. Later launches use the
  recorded path. A path recorded meanwhile by another launch is kept; when the CLI is not on
  `PATH`, the launch still names `koinon setup FAMILY` or `--cli` (PDR 003, Decision 5; #306).

- Direct Claude, Codex, Antigravity and OpenCode starts register as plain peers through their
  native MCP identity. They can send/read/ack their own inbox and use repository memory/work;
  participant addresses, roles, succession and terminal naming remain launcher features.
  Plain peers renew and recover after a daemon outage, and retained unlaunched registrations
  renew without losing their own inbox, cursors or work claims (#306).

- `koinon guide` and the MCP server instructions now say that a message with `sender_family`
  `maintainer`, sent by the maintainer from the dashboard, is the maintainer's instruction and
  approval (PDR 005). Messages from other sessions stay data and grant no permission. The
  guide and the installation and permission documents cite the decision records that state
  each behavior, and no longer say that agents never run `--system` or `sudo` (#306).

- Claude model, context and activity observations survive `/clear` while keeping the existing
  Koinon session and participant. Changed transcript IDs require the same pinned native host;
  status-line reports use verified hook ancestry, and foreign, reused or ambiguous hosts are
  refused (#303).

- When no daemon answers, `koinon status` and `koinon recover` name the service mode and the
  exact command that restarts the daemon (`sudo launchctl kickstart -k system/...` in macOS
  system mode); Koinon never runs it. An idle agent session whose registration expired during an
  outage registers again at its next renewal and takes back its address when it is unheld; a
  session that another session's holder change fenced meanwhile stays dropped (#283).

- On macOS, `koinon install --system` runs the daemon as a LaunchDaemon under your user, so it
  keeps running after a desktop logout, starts at boot and can be managed over SSH. It asks
  `sudo` for each step, stops and disables the user agent, verifies the new daemon and restores
  the agent when a step fails. Later installs keep system mode, and `koinon uninstall` removes
  it. A plain install now enables the user agent again after system mode was removed (#283).

- The daemon logs each start, each stop with its reason, and lock contention. On macOS the
  launchd agent writes the log to `~/Library/Logs/koinon/daemon.log`; on Linux it is in the user
  journal. A second daemon started by the service waits up to 60 seconds for the daemon lock
  and then exits cleanly, instead of being restarted every 10 seconds. `koinon status` reports
  the daemon's PID. Run `koinon install` again to update the service file (#283).

- Document scoped native permissions for every supported agent family in the installed guide
  and a shared reference, with named syntax checks, trust boundaries and verification limits.

- Generate OpenCode skill commands from the shared skills and check for stale wrappers in CI.

- Koinon now sees when a woken agent does not handle a message. Reading the inbox stops the
  notices for the messages read. A message that is neither read nor acknowledged gets a new
  notice after 2, 4, 8 and 16 minutes; after four notices it becomes `failed` and the
  maintainer gets a notice in the dashboard inbox. A failed Codex turn (for example a model at
  capacity) is recorded at once as `turn_failed:CODE`. `delivery` and the dashboard show the
  reason and the notice count, and each acknowledgement records the session and the operation
  that made it. Schema 15 stores these (#250).

- After a Codex `/clear`, the participant address moves to the new thread at its first turn
  instead of at its first Koinon tool call. `koinon setup codex` adds a SessionStart hook to
  `~/.codex/hooks.json` (or `$CODEX_HOME/hooks.json`); trust it when Codex asks to review it.
  The succession result is `host_cleared`. Until that turn the cleared thread stays the holder;
  the pane wake and the `codex_thread_unproven` wait keep a notice from running in it (#275).

- The dashboard has a **Settings** page for the session timing: the registration lifetime
  (default 900 seconds), the renewal interval of `koinon mcp` (default 300 seconds) and the
  succession guard (default 30 seconds). The values are checked together, audited, and applied
  without a restart; `koinon mcp` takes the interval from the daemon's replies. Schema 14
  stores them (#249).

- A running agent no longer loses its registration while it is idle. Every 30 seconds the
  daemon extends each launched session whose host process still runs, also when its
  `koinon mcp` was replaced and nothing renews it. `koinon mcp` tries a renewal that fails
  because the daemon is unreachable or its storage is blocked again at the next interval,
  instead of dropping the session until its next tool call. `peers` and the dashboard show the
  liveness evidence and when the host was last seen running, and `recipient_inactive` names
  its reason: `retired`, `ended`, `no_liveness_evidence` or `lapsed` (#249).

- docs/INSTALL.md gives the procedure to upgrade the Go runtime while agents run. From v0.1.0,
  a retained native session without a launch association renews as a plain peer, keeps its own
  inbox, claims and cursors, and releases any former participant address. DeepSeek keeps its
  command registration. The installed guide, the pickup skill and
  docs/USAGE.md say that `no_host` against a predecessor without a host record resolves when
  that predecessor expires, and that **Make holder** only makes it immediate (#274).

- `koinon upgrade --from-python` continues when a Python session that crashed or was killed
  left its control sockets. Under the sessions' writer locks it removes each such socket that
  refuses a connection and that the kernel's socket table shows without an owner, before the
  Python `uninstall.py` runs, and it lists them in `cleared_endpoints`. Before, `uninstall.py` stopped with `session stop is unconfirmed` (#278).

- MCP tool and command refusals now carry the rule that refused the call, such as
  `renew_for is 60 to 3600 seconds` or `missing argument work_id`, instead of a bare
  `invalid_request` or `invalid_arguments`. The work tool schemas state the lease bounds and
  the list limit (#264).

- An install or upgrade records the launcher CLI of each family that an earlier setup
  configured for the installed binary, though `--agent` did not name it, so `koinon agy` and
  `koinon opencode` still start. It only reads the agent's configuration (#273).

- A launcher for a family without a `launchers.json` entry reports that no CLI is configured
  for the family, and an empty entry reports that it is empty, instead of `configured CLI path
  must be absolute`. Both name `koinon setup FAMILY` and `--cli ABS_PATH` (#272).

- When no daemon has started with the state directory, launchers and command clients report
  that and name how to start one with that state directory and address (`koinon install` from
  a login session, or `koinon serve`), instead of `cannot read private daemon secret`. A
  launcher whose daemon has stopped reports `no daemon answers at ADDRESS` with the same
  remedy (#279).

- A write request that is canceled or times out while the daemon proves its write-ahead log
  empty no longer blocks every later write until `koinon recover`; only that request is
  refused, and the next write proves the log again. A failed proof still blocks (#276).

- Outside tmux, a launcher names its new tmux session after the participant address,
  `FAMILY-LABEL` with `-ROLE` for a role, instead of the directory name, and takes the first
  free `NAME-2` to `NAME-99` when that name is taken. A second agent in the same directory no
  longer fails with a taken name; when no name is free, the refusal names `--tmux-session`
  (#277).

- Launched agents retry a taken tmux name at later renewals. Claude background jobs name the
  single tmux pane with a matching `claude attach` client in the user's socket directory;
  missing clients retry and ambiguous clients rename nothing. `peers` and the dashboard
  report naming results with fixed reasons, also outside tmux, and suppress results for old
  published names or stale observations. The dashboard shows the last succession result.
  A Codex sub-agent, and the former thread after a Codex `/clear`, no longer rename the
  holder's tmux session after their own peer names.

- A holder change returns the participant's unacknowledged messages that were notified to
  the former holder to waiting, so the new holder gets a wake. After a Codex `/clear`, a
  notice accepted by the cleared thread left the successor without one.

- A Claude session gets wake notices again after `/clear`. The wake check accepted only the
  registry entry of the first transcript, so every later wake failed with
  `claude_identity_mismatch`; it now also accepts the session's recorded host process with
  its registration start time (#269).

- Launched successors continue their participant automatically on a native tool call when
  host evidence matches: the same process after the 30-second tool-call guard, or the same
  tmux server and pane after the former host ends. Missing or unreadable evidence refuses
  succession; fenced sessions still require the maintainer's dashboard choice. MCP retries
  `holder_active` at the next tool call after the daemon's wait, preserving that wait across
  renewals. Observation and renewal timers never count as activity or trigger succession.
  `peers` reports the last result and participant events retain the evidence. Schema 13 adds
  host records without changing existing owners. Pickup, handoff and peer-tmux guidance now
  follows the runtime's succession result and records participant address and role.

- Participant addresses now own their inboxes and a holder's default memory cursor and work
  claims. A new holder continues acknowledgement points, cursors and live claim generations
  without restarting leases. Holder changes retire and persistently fence the former holder;
  only the maintainer's dashboard choice lifts a fence. Former-holder participant calls,
  explicit consumer keys and keyed retries return `stale_holder`. Cursor and work-event
  provenance records the acting native session. Checkout status and role requests resolve
  participant writers to their current holder; handback still needs explicit acceptance.
  Inbox messages identify their independent session/participant sequences. Schema 12 keeps
  all pre-upgrade inbox messages, acknowledgements, memory cursors and claims with their
  existing owners; participant ownership applies to new address messages and default calls.

- Participants have one reserved address per family, repository and optional role. Launchers
  accept `--role`; linked worktrees share participants. An active holder keeps its address;
  without one, only a sole active qualifier takes it. Multiple qualifiers leave a conflict
  for the maintainer to resolve with **Make holder** in the dashboard. `peers` and the dashboard
  report role, address and holder state; the dashboard also shows conflicts and the last event.
  Schema 11 preserves existing aliases/holders and retained inbox, memory and work state.
  Launched Claude sessions now show their nested repository list in the dashboard too.

- Claude Code, Codex, Antigravity and OpenCode sessions started through their Koinon launchers
  register with a matching launch family, directory and host process or recorded background
  job; a launch ID that does not match returns `not_launched`. Codex launches with
  `--no-daemon` and forwards the launch environment to its MCP server. Codex sub-agent
  threads get peer names and `subagent: true`, and never hold the repository alias.
- `koinon claude --bg [args]` starts a background job with a private settings file and
  records its job ID. Registration before that ID is recorded returns `launch_pending`
  and retries at the next tool call. A failed start retires the pending launch and removes
  its settings file. The state schema is now 10, adding `sessions.subagent`.
  After upgrade, retained native sessions without a launch association renew as plain peers
  and keep their inboxes, acknowledgements, memory cursors and work claims. DeepSeek keeps
  command registration and renewal without a launch record.

- `koinon claude`, `koinon codex`, `koinon agy` and `koinon opencode` start in a directory
  that holds other repositories, such as a checkout with submodules, worktrees of other
  repositories or separate clones, and list them instead of refusing the start. The list
  (path and kind, at most 64 entries, marked incomplete after 3 seconds, at an unreadable
  directory or at a path with a control character, which is left out) is printed before the agent starts and kept with the launch. The dashboard shows
  it with each session that registers with its launch record (Codex, Antigravity and
  OpenCode). The session's repository is still the start directory's.
- Agents sharing a checkout can derive one advisory checkout resource, inspect its writer,
  generation token, expiry and checkpoint through `work_checkout` or `koinon work checkout
  status`, and request the role with an inbox notification. Release with `handoff_to` and
  `checkout_resource` saves a checkpoint and notifies an exact peer atomically; the requester
  explicitly picks up with a new claim generation. Different worktrees have separate roles.

- The dashboard's work view can show finished items and filter by lifecycle, and opens one
  item with its scope, progress, references and history. The maintainer can create an item,
  propose or clear an assignee, edit the scope of an item without a live claim and finish an
  item as completed or withdrawn. Each change is audited and recorded as `maintainer`, with the
  same revision checks and refusals as an agent's request.
- The dashboard's memory view lists the entries of a store: newest first, with paging, a text
  search, a type filter and, on request, replaced, revoked and expired entries with their
  links. The maintainer can record an entry of any type and scope, edit an entry (recorded as a
  new entry that replaces it) and revoke one with a reason. Each change is audited, is written
  as `maintainer` and has the same limits as an agent's write. An edit of an entry that someone
  else replaced meanwhile is kept and reported as a conflict.
- The dashboard's 5-second refresh now waits while a disclosure in the list is open or a form
  field in it has focus, so a purge confirmation or an edit form no longer closes by itself.
- `koinon claude` starts Claude Code like the other launchers: with the absolute CLI path
  from `--cli` or `launchers.json`, in the current terminal or a new tmux session, with a
  private launch record. The dashboard's start action offers Claude Code too.
  `koinon setup claude` now records the Claude CLI path. A started Claude keeps
  `CLAUDE_CONFIG_DIR` but not the `CLAUDE_` variables or `CLAUDECODE` of the session that
  started it.
- The daemon deletes old records in its maintenance sweep. Acknowledged messages go 30 to 60
  days after their acknowledgement, and expired or retired sessions, with their peer names,
  30 days after their activity ended, unless they still hold unacknowledged messages or a
  live claim. The dashboard can mark a session for purge, after a confirmation: the next sweep
  releases its claims and deletes it with its whole inbox, and the mark can be removed until
  then. `koinon status` and dashboard health report what each sweep deleted and kept, and
  `delivery` reports a deleted message as `deleted`. The state schema is now 9.
- The dashboard's sessions view has a search box. It searches every session on the server
  by peer name, alias, family, repository, directory or state, and the search stays in the
  URL, across pages and refreshes.
- The dashboard's send form suggests the names and aliases of active sessions, with their
  family and state, and of every session on request. The messages recipient filter also
  accepts a held alias, and it no longer resets while you type, because the 5-second refresh
  now leaves the filter forms alone.
- Every dashboard table sorts by its column headers. A click sorts the whole list on the
  server, a second click reverses it, and the order stays in the URL, so a sorted view can be
  reloaded, linked and refreshed. Paging keeps the order and the messages recipient filter.
- The dashboard's sessions view lists active sessions first by default and shows how many
  sessions are active, expired and retired. Before, it listed sessions by family and ID, so
  active sessions could be pages behind the expired ones.
- Setup for Codex, Antigravity and OpenCode records the resolved absolute CLI path in
  private `launchers.json`, preserving other families' entries. Launchers work without
  `--cli` after setup and keep explicit overrides; without a recorded CLI they find it on
  `PATH` (#306).

- A Claude session that has a background shell task running now receives Koinon wake
  notices. Claude Code reports such a session as `shell` while it waits at the prompt, and
  the wake treated that like a turn in progress, so no notice arrived until every background
  task ended. The dashboard also shows such a session as idle, not busy.
- Agents can read a peer's observed model, context and activity through `peer_status` and
  `koinon peer-status`, by name or held alias, with source and confirmation timestamps,
  freshness windows and explicit unknown reasons. Registration and acknowledgement remain
  separate signals, and private runtime/terminal targets are excluded.

- `koinon install` links `~/.local/bin/koinon` to the installed binary, so the documented
  `koinon ...` commands run it. The report names the `koinon` that `PATH` runs and, when that is
  not the installed copy, the step that makes it so; it never edits shell startup files.
  `koinon uninstall` removes the link only when the install created it and it still points at
  the installed binary. A symlink or an unrelated file at the link's marker is refused and
  never overwritten.
- Dashboard actions work from a browser again. Every page sent `Referrer-Policy: no-referrer`,
  which makes a browser send `Origin: null` with a form `POST`, so retire, release, acknowledge,
  clear, send, launch and logout were refused with "foreign origin". The pages now send
  `Referrer-Policy: same-origin`; the origin check is unchanged.

## 2026-10-08 — Go runtime: one daemon, MCP access, install and upgrade from Python

This release replaces the Python runtime with one Go binary, `koinon`, and one daemon for each
user. Linux and macOS builds use no cgo, and a fresh installation needs no Python.

- **Installation.** `koinon install` places the binary, writes and starts a systemd user unit
  (Linux) or a launchd agent (macOS), and sets up the agents you name. `koinon uninstall`
  removes them again and keeps the state. `koinon version` prints the build. Releases attach
  four CGO-free binaries (Linux and macOS, amd64 and arm64) with a `SHA256SUMS` file.
  Homebrew installs the release binary with `brew install rwcii/koinon/koinon` on macOS and
  Linux; run `koinon install` again after each `brew upgrade`.
- **Upgrade from the Python release.** `koinon upgrade --from-python` moves a `main`-release
  installation to the Go runtime. It takes the Python runtime's own upgrade marker, stops the
  Python services, and imports every inbox, memory store, work item and claim. It verifies the
  import before it removes the Python services. A failure before that point restores the
  Python runtime. A failure after it resumes when you run the command again. The Python state
  tree stays as a backup. `koinon import` imports a Python-era state tree on its own, and
  `--verify` compares the tree with the imported state.
- **Daemon core.** An authenticated IPv4 and IPv6 loopback API, durable session registration,
  renewal and retirement, and `koinon status`. Each session gets a permanent peer name, and
  each family and repository one alias. `koinon peers`, `send`, `inbox` and `ack` send, read and
  acknowledge messages with gapless sequence numbers, and a sender can read the delivery
  outcome of its own message. A built-in `maintainer` session receives messages from agents.
  Sessions can register from plain directories. The state schema is 8.
- **MCP access.** `koinon mcp` is a stdio MCP server with the messaging tools `peers`, `send`,
  `inbox`, `ack` and `delivery`, the `memory_*` tools, and the `work_*` and `claim_renew` tools.
  It takes the calling session from the agent on every call (Codex thread, Antigravity
  conversation, Claude Code session, OpenCode session through a plugin) and never from tool
  arguments. `koinon setup <family>` adds it to Claude, Codex, Antigravity or OpenCode through
  the agent's own command and changes nothing on a repeated run. `koinon guide --agent <family>`
  prints the guidance. DeepSeek uses the command path with `koinon register`.
- **Wake.** The daemon wakes Codex, Claude Code, DeepSeek and idle OpenCode sessions with
  content-free inbox notices, and offers Antigravity notices at its Stop boundary. Busy sessions
  are rechecked every three seconds; failed and unconfirmed attempts retry with bounded backoff
  across restarts. Acknowledged sequences are suppressed. Health reports delivery counts and
  adapter reasons.
- **Launchers.** `koinon codex`, `koinon agy` and `koinon opencode` start sessions with
  configured CLI paths, in the current terminal or a new tmux session, refuse a start folder
  that holds another repository, and clean the inherited Claude environment. OpenCode receives
  a loopback listener and a generated password. `koinon mcp` names a Claude session's or a
  launched session's own tmux session after its published name.
- **Memory.** One shared memory store per repository, with the record format 2, snapshot,
  delta, acknowledgement, idempotency, retention and capacity rules of the memory protocol,
  through the `memory_*` tools and `koinon memory`. The database has a fixed storage ceiling
  with a reserve for progress and withdrawal, proves its write-ahead log empty before every
  write, and refuses writes until `koinon recover` when it cannot.
- **Work items.** Records with criteria and non-goals, advisory writer leases with path and
  exact resource claims, progress and due-transition events in the memory stream, idempotent
  retries, list filters, 30-day retention of finished work and a bounded maintenance sweep,
  through the work tools, `koinon work` and `koinon claim renew`.
- **Dashboard.** `koinon dashboard` prints a one-time login link to a loopback web view of the
  sessions, every message with its body and delivery and acknowledgement state, the memory
  stores, the work items, the audit log and the daemon's health. Sessions show their model,
  context, activity and terminal, from Codex call metadata and rollouts, the Claude Code
  registry and the `koinon hook claude-status` status line, the Antigravity Stop hook and the
  OpenCode session status. Actions retire a session, release a claim on its owner's behalf,
  acknowledge or clear an inbox, send a message as `maintainer`, and start a Codex, `agy` or
  OpenCode session. Each action needs the login, the listener's literal address and origin and
  a CSRF token, and writes one audit record (90 days, at most 10,000 records).
- **Python runtime removed.** The Python runtime, its entrypoints, its tests and its CI jobs are
  removed. The design documents of the Python subsystems are marked historical. Committed
  fixtures and the pinned previous release keep the import and upgrade coverage.
- **Known limit.** The live proof that a DeepSeek session receives and processes a wake notice
  is deferred ([#199](https://github.com/rwcii/koinon/issues/199)); synthetic tests pass.

## 2026-09-23 — Bounded test runs

- Run the tests with `python3 tests/run.py -v`. The runner ends a run that stops making
  progress, with a nonzero exit status, and prints the running test and the stack of every
  thread. Test waits share one budget that `KOINON_TEST_TIMEOUT_SCALE` multiplies on slow
  machines, and a timeout names what the test waited for. The installed runtime is unchanged.

## 2026-09-22 — Package layout, native supervision, repository memory and runtime upgrade

- Correct four `README.md` statements the code contradicts. The current inbox schema is 4,
  which adds the delivery ledger; startup accepts a store at schema 2, 3 or 4, so describing
  the acknowledgement watermark as living in schema 3 named a superseded version. A state
  directory is refused when any group or other permission bit is set, rather than compared
  against 0700 exactly, so an owner-only mode other than 0700 also passes. `not_bootstrapped`
  fires only when no snapshot is open, which is the opposite of `snapshot_open`, so the two
  cannot share one recovery instruction: it means call `sync` to obtain a first snapshot.
  `idempotency_conflict` is also raised when a key is repeated with the same content and a
  different deadline, not only when the content differs.

- Report a missing recorded state root during upgrade preflight with
  `missing_state_root`, its path, and recovery guidance before shutdown or upgrade
  publication. Report a non-directory path component as `invalid_state_root`.
  Preserve state and configuration rather than creating an empty
  replacement. Installation now labels the state directory as configured, since
  staging without service initialization may leave it absent (#80).

- Describe the shipped schema-5 work commands and advisory claims in module
  documentation and CLI help, removing stale staging language. Document
  `schema_too_old`, its configuration exit status, and recovery without resetting
  stored state (#86).

- Memory client commands no longer create missing state directories while probing an
  absent service. Only `serve` initializes them; existing directory safety checks remain (#81).

- Stop an operator's own commands from blocking the next upgrade, and do not run a cache
  another account could have written. Under `umask 002`, one interactive command run from
  the installed prefix left a group-writable `__pycache__` that made every later upgrade
  refuse with `unsafe manifest ancestor`. Every command now sets `umask 077` before its
  first Koinon import, so the caches it creates are private. A command that finds an
  untrusted cache directory runs from source without reading any cache, and the install,
  uninstall and upgrade scripts never read one. The upgrade moves an untrusted cache
  directory into its operation directory instead of refusing, and refuses before shutdown
  only when that directory holds data other than the runtime's caches. The upgrade also
  located caches through `sys.pycache_prefix`, which would have checked the wrong
  directory once a private prefix is set; it now derives them from the runtime layout. The
  upgrade command no longer describes itself as experimental.

- Say what is wrong when a path is refused as unsafe. Installation and upgrade refuse a
  path that a second account can write, which an account whose `umask` is `002` produces
  for the directories it creates. The refusals named only the path, so an operator could
  see neither the condition nor the remedy. The three checks that validate paths an
  operator creates — the upgrade source checkout, the manager registration path and the
  parent of a selected guidance file — now name the condition that failed, and give the
  mode and the remedy when the mode is at fault, through one shared rule in
  `koinon/path_permissions.py`. A wrong owner and a file where a directory was expected
  are reported as themselves rather than as a mode to correct. The installation guide
  states the requirement before the first install. The rule itself is unchanged and still
  refuses a group-writable path: a group entry omits the accounts whose primary group it
  is, and account enumeration can be partial or unavailable, so a group that looks
  exclusive cannot be shown to be exclusive. Nothing repairs permissions or adopts a path.

- Document the coordinated runtime upgrade as the supported operation it is, rather
  than as work in progress with a manual runbook in its place. The installation guide
  now carries the three command forms, the manual-backend handoff, and the permission
  precondition that refuses a default `umask 002` checkout. The runbook remains for
  legacy and manual deployments the operation does not cover. Two command forms that
  the design document proposed do not parse and are corrected.

- Correct statements in `README.md` and `AGENTS.md` that the code contradicts. A session
  job's launchd artifact is published under its own state directory, not in
  `~/Library/LaunchAgents`, which holds only the memory artifact. `--name` is read on the
  legacy unit-pair path only and is ignored without warning on the component invocation,
  where the peer name derives from the repository directory name. Entry garbage collection
  is not limited to expired entries: finished work-item retention removes that item's stream
  rows and advances the retained-history floor regardless of expiry. Installed launchd jobs
  apply the same permanent-failure non-restart rule as systemd, by mapping those exits to
  zero. `koinon/codex_instructions.py` is a legacy shim; the implementation is
  `koinon/participant_instructions.py`.

- Support a declared runtime layout migration during upgrade. A release records
  where a shipped file moved to in `koinon/upgrade_layout.py`; replacement publishes the
  new path, confirms it, then retires the old one, and refuses an undeclared removal
  before shutting anything down. The frozen backup keeps every old path for recovery.

- Move the implementation modules into a `koinon` package. The executable
  entrypoints stay at the installation prefix — `bridge.py`, `notify.py`, `session.py`,
  `memory.py`, `memory_service.py`, `session_service.py` and `usage_report.py` — so
  installed service definitions and documented commands keep their paths. The shipped
  file manifest moves with the layout.

- Enable `fetch.prune` in `scripts/setup-repo.sh`, so a clone drops tracking refs
  for branches the remote has already deleted. A stale tracking ref otherwise reads
  as a live branch.

- Move the test suite into `tests/`, leaving the repository root for modules,
  documents and project metadata. Run the suite with
  `python3 -m unittest discover -v -s tests` from the repository root.

- Report and refuse unowned user-service definitions that directly reference an upgrading runtime, including loaded custom artifacts and definitions outside saved state roots.

- Upgrade saved manual memory through a verified persistent foreground handoff, retaining resumable phases and original stopped state. Legacy manual sessions still require explicit ownership migration.

- Restore inactive native components to their original stopped and registered/deactivated
  state after gated upgrade verification, before releasing active components. Exercise
  synthetic coordinator interruption and resume at every durable phase.

- Explain safe recovery from an unowned-memory upgrade refusal, including a structured
  recovery code and preservation guidance for existing legacy stores and services.

- Correct six stale claims in `AGENTS.md` and `README.md`: an authorized upgrade now points
  at `scripts/upgrade.py` instead of rerunning the installer, the installer is described as
  configuring and conditionally starting repository memory, macOS is launchd-supported rather
  than manual-only, memory is described as carrying subscriptions and content-free notices
  rather than being pull-only, and a fresh `--repo` installation is no longer described as
  the legacy unit pair or as starting nothing automatically.

- Add an experimental public native upgrade command with a retained recovery archive,
  explicit resume/status, preflight refusal of unowned memory state in selected roots,
  and private preservation reports through release and final readiness. Inactive/manual
  adapters and the full cross-platform acceptance matrix remain unfinished.

- Bind pre-release service admission to verified child generations, distinguish later
  restarts from preservation verification, and retain installation exclusion through
  component observation and marker publication.

- Retry deferred search initialization when the ordinary worker queue is full after
  upgrade release. Bind private memory inventories to both repository and generation.

- Connect the internal shutdown, backup and replacement phases to durable receipts;
  interrupted backup completion resumes without replacing unbacked runtime files.

- Connect internal native startup admission to the selected upgrade plan and complete
  new runtime bytes; ordinary ensure remains blocked during the operation.

- Stage resumable runtime file replacement against retained backups and exact memory
  migration verification. Defer search-index initialization until upgrade release.
  The public upgrade coordinator and native acceptance remain unfinished.

- Connect internal upgrade shutdown and component backup capture with retained
  startup locks, recorded endpoint reservations and frozen retry inventories.
  The public upgrade coordinator remains unfinished.

- Stage upgrade exclusion and service admission: selected verification can retain
  ownership checks while peer ingress, notification delivery and maintenance wait
  for durable release. Preserve upgrade-specific supervisor diagnostics. The public
  coordinated upgrade operation remains in development.

- Allow streaming upgrade backups up to 1 GiB per file through the shared binary
  file guard. Other callers retain their smaller explicit limits; JSON records
  keep the 4 KiB default and 1 MiB maximum.

- Stage plan-bound upgrade phase records and immutable private recovery documents;
  the public coordinator and native interruption acceptance remain in development.

- Flush guidance recovery copies before replacing user text, and reconfirm retained
  copies and guidance on retries. Apply the shared device flush to session registration,
  memory ownership, and self-contained uninstall recovery files as well.

- Flush installation configuration and service artifacts through the shared platform
  primitives, so a replacement is followed by the device-level synchronization macOS
  requires. Exact repeats reconfirm retained records instead of accepting a visible but
  possibly unflushed file.

- Stage read-only upgrade inventories and frozen source-file checks, including an explicit
  unsupported-SQLite refusal. The coordinated upgrade command remains in development.

- Correct legacy installation-guide statements to distinguish delivered native component
  supervision from operator-managed services and the pending coordinated upgrade command.

- Install selected repository memory and participant sessions through one public command,
  with native Linux/macOS supervision, memory-only and no-start modes, and exact repeats.
  Remove owned jobs with resumable cleanup while retaining inboxes, memory and recovery
  evidence; clean reinstall reuses retained data. Native paths require safe ancestors.

- Retry a state read when atomic publication replaced its opened predecessor,
  preserving strict validation of the current record.

- Pass parent-owned control sockets explicitly to session children, allowing native
  restart after a child exits before its first handshake while preserving bind intent.

- Activate explicitly selected native session pairs with verified manager ownership,
  guarded shutdown, and captured dead-child control-socket recovery after runtime crashes.

- Add saved native session selection, recoverable artifact publication, and an owned
  runner entrypoint with explicit recovery provenance; automatic activation remains pending.

- Stage durable session-runner ownership, refusal and generation-bound shutdown for
  bridge/notifier pairs; native session activation remains unwired.

- Run native manager and memory lifecycle evidence on integration PRs and branch
  updates, with manual dispatch available after feature branches are removed.

- Add optional generation-bound bridge and notifier stops for supervisor-owned
  shutdown, preserving explicit operator stops.

- Stage pure native session artifact rendering and its implementation sequence,
  preserving Linux behavior while ownership and refusal integration remain pending.

- Activate explicitly selected native memory supervisors with verified manager and
  child identity, explicit launchd domains, and opt-in literal systemd arguments.
  Refuse unsafe manager-reported registration paths and verify inert loader links
  before enabling or starting systemd children. Add isolated native lifecycle checks; complete installer
  integration remains pending.

- Stage exact native service-manager observations with unknown results for missing,
  malformed or changing evidence, plus isolated macOS interface fixtures. Activation
  and complete installation remain pending.

- Stage the repository memory supervisor with child readiness, generation-bound stop,
  bounded retry, retained failure diagnostics, and explicit refusal recovery. Native
  manager activation and installer integration remain pending.

- Allow managed callers to bind memory shutdown to their captured generation,
  refusing a replacement or unreadable owner before sending a stop request.

- Route existing session, installation, and removal service-manager operations
  through platform support while preserving Linux command and failure behavior.

- Stage exact memory-service artifact ownership and recoverable publication,
  preserving conflicting files and interrupted-install evidence without activation.

- Stage validated repository memory-service selections and bounded installer admission,
  preserving configuration and store identity without activating services.

- Add the managed memory installation design candidate, including repository
  ownership, Linux/macOS supervision, recovery, migration, and delivery slices.

- Record complete component installation and resumable upgrades in the delivery
  queue; clarify lifecycle responsibilities for operator-created memory services.

- Name missing required work-command fields, enforce required CLI options, and
  preserve machine-readable argument refusals and optional replay pairs.

- Report session supervisors that may need an explicit restart after runtime
  replacement; preserve no-start and manual-service uncertainty without restarting them.

- Reconcile delivery-queue milestones with released source and completed work-items
  integration; retain the error-code validation proposal as an explicit follow-up.

- Enable schema-5 memory startup with atomic schema-3/4 migration, complete catalog
  validation and explicit work/record-format capabilities. Preserve legacy data and
  snapshots; document coordinated runtime upgrades and rollback boundaries.

- Document finite work-item capacity, retained-history limits, funded claim reserves,
  and the distinction between reclaiming rows and recovering allocated page headroom.

- Add explicit repository/participant work-guidance opt-in and removal, private backups,
  interrupted-publication recovery, and rule-aware uninstall. Policy queries verify
  managed section digests and report edited or missing guidance as disabled.

- Preserve unknown installation fields and staged work rules across upgrades using
  a permanent configuration lock and atomic writes. Add a read-only `work-policy`
  query for explicit repository/participant selections; work activation remains deferred.

- Add staged bounded work maintenance, atomic finished-history reclamation, and
  timestamped status diagnostics. Idle subscribers receive committed change hints;
  shutdown drains accepted jobs. Public startup remains schema 4.

- Add staged work lifecycle commands, replay results and immutable stream/snapshot
  integration, with strict reader-format guards and note isolation. Public startup
  stays schema 4; work activation remains pending.

- Enforce remaining work reservations at the shared memory transaction boundary,
  including progress, cleanup and index rebuilds. Add synthetic capacity/rollback
  tests and package the accounting dependencies; public startup remains schema 4.

- Add staged schema-migration and advisory-lease primitives with synthetic rollback,
  restart, conflict, and reservation tests. Runtime activation remains pending;
  the memory service still uses schema 4 and exposes no work-item commands.

- Record the approved work-items v1 contract and its implementation design for
  advisory writer claims, progress reporting, interruption recovery, and 30-day
  retention of finished work and its history.
- Record the requirement for dead-session detection and supported single-session
  removal that preserves inbox state; design remains pending.

## 2026-09-19 — Usage reports and delivery evidence

- Add bounded local delivery records, acknowledgement-independent deduplication,
  explicit handled outcomes, and durable notification evidence export. Preserve
  uncertain outcomes without replaying native messages. Separate observed service
  health from fresh model activity and declare provider priority limits. Normalize
  `peers` status values (including shell to busy); distinguish observation time
  from the time a Claude status changed. Isolate incoming/outgoing ledger capacity
  and report definite missing/refused socket connections separately from uncertainty.

- Add explicit local Codex/Claude usage collection, source-lineage roles, before-work
  markers, retrospective reports, report combining, and eight-column rendering.
  Missing and inconsistent counters remain explicit. DeepSeek usage reporting is
  explicitly deferred for this release; messaging and installation are unchanged.

- Allow DeepSeek-only installations without a Codex executable. Validate Codex
  after provider selection, reuse a usable saved executable, and fall back to PATH
  when the saved executable is stale. Codex session startup refuses a missing or
  invalid executable before creating new session state.

- Record outstanding usage-reporting requirements, provider defects, and remaining
  programme work in a repository delivery queue, with proposals marked separately.

- Clarify memory maintenance terminology: expiry-based garbage collection, history
  pruning, and storage reclamation are distinct from semantic memory consolidation.
  The planned history-pruning work does not specify summarization. No runtime behavior
  or retention policy changes.

## 2026-09-19 — Durable delivery and shared transport

- Use Koinon defaults for new runtime paths, services and managed participant
  guidance. Reuse legacy paths and owned service names, preserve saved custom
  paths, and refuse ambiguous defaults. Upgrade and removal recognize both marker
  families. Registry, memory handshake and cross-version lock identifiers remain
  unchanged; no runtime state is moved or reset. Unit ownership recognizes
  directory aliases, including macOS paths, during upgrade and removal.

- Select private control endpoints from canonical state paths on both platforms. Preserve validated legacy endpoints for clients and refuse duplicate or ambiguous old/new listeners during upgrades. Peer messaging paths remain literal.

### Added

- Bounded notification journal core, crash-safe migration state, attempt accounting,
  and read-only source reconciliation. Worker integration tests cover acknowledgement
  races and process termination.

- Journal-backed asynchronous Codex and DeepSeek delivery, bounded automatic retry,
  content-free memory sync notices, and independent delivery health. Notifier controls
  expose status, explicit retry, health acknowledgement and accepted-loss rebuild.
  Session lifecycle checks preserve running processes during delivery faults and
  report an unconfirmed stop as unknown. Exact memory service paths are supported
  by `memory.py --service-dir`, with repository and generation checks.

- Explicit verified memory bindings and atomic content-free pointer refresh, with
  independent inbox quotas, durable observation state and replacement diagnostics.
  Inbox schema 2 upgrades to 3; memory schema 3 upgrades to 4 with a stable store UUID.
  Binding deadlines cover verification and cleanup; process identity probes stay
  bounded and off the service loop.

- Explicit bridge and memory change subscriptions with bounded connections, coalesced
  content-free hints after commit, and a shared reconnect/rescan helper. The notifier
  subscribes before each durable recheck and retains finite fallback checks.

- Inbox schema 2 with atomic migration, a bounded acknowledgement watermark and
  durable journal activation evidence. Status advertises only those implemented
  capabilities. Bridge software faults preserve exit 70 through the supervisor and
  stop automatic restarts.

- Bridge startup reserves both socket paths before opening the inbox and retains
  ownership until database shutdown completes. Endpoint refusal exits 78 without
  changing the store. Directory ownership refusals use the same status, and both
  direct and supervised services preserve it without automatic restart. Control timeouts and invalid or lost replies report structured
  errors without automatically repeating mutations.

- Private directory creation now gives missing parent directories mode 0700 even under
  a permissive process umask, while preserving existing parent permissions.

- Dedicated bridge and memory database workers with bounded queues, separate control
  admission, and shutdown that settles accepted writes even after caller cancellation.
  Status reports observed storage and programming faults separately from input refusals.
  Memory startup distinguishes a busy, unresponsive or invalid service from an absent
  listener and refuses replacement before constructing another database owner. The CLI
  reports expected lifecycle errors as structured responses, with exit 75 for temporary
  conditions, exit 70 for internal software errors, and exit 78 for refusals that need
  operator correction.

- Separate control endpoint validation and bounded exchanges for bridge and memory
  clients. Control sockets are excluded from generic messaging and discovery; memory
  reuse verifies the connected process, its start marker and current generation.
- Account-local notifier ownership across state directories. Duplicate provider/session
  targets fail before registration; conflict diagnostics and session status expose a
  matching digest. Persistent Koinon lock paths do not depend on agent home overrides,
  and delivery subprocesses cannot retain the lock after notifier exit. Ownership
  refusals exit with status 78; legacy and supervised services wait for an explicit
  restart after correction instead of repeatedly restarting the refused instance.

## 2026-09-18 — Koinon

### Changed

- Rename the project documentation to Koinon, with the descriptor "Shared coordination
  and memory for independent agents." Clarify the intended scope across agent families
  and repositories, the current per-repository memory service, and agent handoff rules.
  Runtime paths, service names, registry identifiers, and managed markers are unchanged.

## 2026-09-18 — Repository memory and session readiness

### Added

- `memory.py`, a shared per-repository memory service, in its pull-only form. Agents working
  in one repository append typed entries and read them back through a private control socket,
  so a session that started earlier can still learn what a later session recorded. Repository
  identity is the absolute Git common directory, so every worktree of one repository shares one
  store. Liveness is recorded on the entry it affects rather than derived, so reclaiming a
  replacement cannot resurrect what it replaced. A snapshot is frozen as immutable copies against
  a fixed head, so a revocation or a reclamation cannot change what a reader is still paging
  through. The server records page issuance and completion, so a cursor advances only on an
  acknowledgement it actually issued, and a retained acknowledgement replays after a lost
  response. Responses are bounded by encoded bytes with continuation. Storage enforces a logical
  budget and a durable page ceiling, with slots and pages reserved so a withdrawal stays
  recordable, and every retained record has a lifetime whose expiry returns a defined recovery
  result. The store is opened by a single exclusive owner; a second owner is told the store is
  busy rather than that the file is unreadable. When storage cannot be written safely the
  service records a blocked state and refuses writes until recovery is requested explicitly,
  while status, search and stop stay available. Expiry removes entries in batches, and falls
  back to invalidating the index rather than requiring room to maintain it, so a full store can
  always be reclaimed. A search index that cannot be rebuilt leaves the store serving complete
  scans instead of failing to open, and an index the store cannot maintain is marked invalid
  rather than left silently short. Initialisation writes the schema and the identity that
  describes it in one transaction, so an interrupted first start leaves nothing half-made, and a
  store left in that state by an earlier version completes rather than being reported as another
  repository's. Recovery is available as a `recover` subcommand. Search answers from the index only while the
  index is known to cover every live entry, and otherwise from a complete scan, and the reply
  says which answered. Start is
  serialized, and a socket left by an unclean exit is recovered only after its recorded owner is
  proved dead. Entries are reported data and grant no authority. There is no bus integration and
  no broader history pruning or semantic memory consolidation in this form.
- `docs/STORAGE-BOUND-DERIVATION.md`, the derivation of the storage bound the memory service
  enforces, with its terms traced to the SQLite sources at a pinned tag. It records why the log
  a single transaction can produce is finite, why the shared-memory and sub-journal files do not
  contribute to the declared total, what the choice costs in memory instead, and which figures
  are an example workload rather than a bound. No runtime behaviour changes with this entry.
- `docs/PARITY-MEMORY-DESIGN.md`, the agreed design and acceptance contract for peer
  capability parity and a shared per-repository memory service. It records the contracts
  for identity, delivery, presence, and memory, the capabilities that remain unverified
  until they are measured, and the acceptance criteria that judge completion. No runtime
  behaviour changes with this entry.

### Fixed

- Session registration releases its registration lock before starting the systemd
  supervisor. This prevents a false readiness timeout. Concurrent lifecycle commands
  for the same session remain serialized.

### Changed

- Git ignores local `_handoff/` directories to keep handoff content out of commits.

## 2026-09-11 — macOS, DeepSeek, and peer guidance support

### Added

- Generic peer-origin and permission-laundering guidance on inbox records, queued
  notifications, and managed session instructions, separate from peer message content.
- Updated inbox CLI adds guidance when reading from an older running bridge, allowing
  current sessions to receive it without a server restart.
- macOS support. Peer credentials, the process start marker, the peer domain, the socket
  allowlist, and the AF_UNIX address-length fallback are resolved in one platform module.
- A DeepSeek (DSH) participant alongside Codex. It reads and acknowledges the same inbox
  and sends through the same control socket, and the watcher delivers notices to a selected
  harness session over the harness's local RPC, the analogue of `codex queue`.
- `--agent` and `--model` on `session.py`. A DeepSeek peer advertises the harness's
  configured default model in its peer name, such as `deepseek-v4-pro-<repo>-a3`.
- `install.py --configure-deepseek`, which writes a managed DeepSeek section into the
  harness home's `AGENTS.md` so a harness session registers itself and reads its inbox,
  the same way the Codex section already does. Each participant gets its own delimited
  section with its own markers, so either can be added or removed without disturbing the
  other or the user's own guidance. Uninstallation removes exactly the sections the
  installation recorded, so a DeepSeek section is not left behind pointing at a removed
  runtime.

### Fixed

- Repository setup requires the six OS/Python matrix checks, replacing obsolete
  Python-only names that left pull requests waiting for nonexistent jobs.

- Control socket paths use filesystem byte lengths, so Unicode state paths also
  select the short fallback before exceeding the kernel limit.

- Repeated participant configuration preserves registered participants and home paths,
  so uninstall removes all managed guidance, including after a Codex-only upgrade.

- Harness notice delivery refuses redirects and ignores environment proxies to keep
  authentication cookies on the validated loopback destination.

- `peers()` returned no peers on macOS. A missing `/proc/<pid>/stat` raised inside a broad
  handler, so discovery reported an empty list even with live peers present; the same
  omission made a notifier fail at startup and left `session.py status` permanently
  reporting `repair_required`.
- The bridge could not start when a state directory was too deep for `sockaddr_un`, which
  macOS's long temporary paths reach easily. The control socket now falls back to a short
  path in the peer socket directory, which also fixes over-long Linux state paths.
- A peer connection that raised `AttributeError` was dropped without a trace; the handler
  now reports it.

### Changed

- Notice delivery is dispatched per participant. The Codex path, including generated
  systemd units and the manual start command, is unchanged.
- CI runs on Linux and macOS.

## 2026-09-11 — Codex-wide registration

### Added

- Codex-wide installation with a managed global instruction section that preserves
  existing guidance and supports `AGENTS.override.md` precedence.
- Per-thread registration with isolated inboxes, checkpoints, and service instances.
- A supervisor for bridge/notifier lifecycle and a persistent-process fallback when
  no systemd user manager is available.
- Live peer discovery through `bridge.py peers`, exposing only selected registry metadata.
- Explicit session status, stop, and rename commands.
- Tests for concurrent sessions, repeated registration, global instruction preservation,
  partial-service shutdown, and safe name allocation.

### Changed

- Peer names follow the fleet form `codex-<repo>-<two-hex>`. Full thread identity stays
  internal; names remain stable unless explicitly renamed.
- Registration verifies both the bridge and notifier before reporting a healthy session.
- Uninstallation stops the installed sessions and removes owned services and managed
  guidance while retaining inbox data.

### Fixed

- Failed or interrupted renames cannot truncate the previous session registration.
- Short-name collisions within an installation select another suffix rather than
  silently creating an ambiguous name.
- Manual sessions can stop even when an inactive unit file exists or systemd is unavailable.
- Notifier readiness is independent of the invoking shell's Claude configuration directory.

## 2026-09-11 — Initial release

### Added

- Linux Unix-domain socket bridge for Claude Code peer messages, with a persistent
  SQLite inbox and a private local control interface.
- Codex queue notifications, named Claude peer registration, and same-user socket checks.
- User-level installation/removal scripts, systemd service setup, and protocol documentation.
- MIT license, personal repository contribution conventions, agent setup guidance,
  signed contributions, and protected `develop`/`main` pull-request flow.
- Python 3.11–3.13 continuous integration.

### Fixed

- Python 3.13 test cleanup tolerates sockets already removed by asyncio.
- Installer upgrades abort on service-stop errors and refuse unrelated service files.
