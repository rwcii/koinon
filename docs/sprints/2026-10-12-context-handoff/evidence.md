# Managed context handoff — evidence inventory

This file records existing interfaces and gaps. It is not a specification, a support
claim or permission to reset a native session. The source inventory was rechecked against
published develop `52ab15a` on 2026-10-13. Installed guidance still comes from the installed
runtime; a checkout change does not upgrade it.
The post-clear source correction below was originally checked against develop 5170f1c. PDR 006
records the settled settings, Enable carry-over and terminal input; its remaining
verification mechanisms are plan proposals.

## Context and activity

| Evidence | Current behavior | Planning implication |
| --- | --- | --- |
| `internal/core/observations.go`, `PROTOCOL.md` observations | Context sources are `claude_statusline` and `codex_rollout`. | Other families have no admitted context source today; unknown must not become zero. |
| Same files | Context/model confirmation freshness is 30 minutes; activity/terminal freshness is 2 minutes. | Registration active does not prove idle. Context source time and confirmation time are distinct. A cycle needs a checked freshness policy; existing display freshness alone does not establish reset readiness. |
| `internal/mcp/observe.go` | Polling runs every 5 seconds and confirms unchanged readable observations every minute. | A repeated observation is not necessarily a new threshold crossing or a new context epoch. |
| `internal/mcp/observe.go` Claude registry reader | Reads bounded, owned registry metadata tied to native session and parent process; busy maps to busy; shell and idle map to idle, and waiting stays distinct. | Native session attribution and fresh status need rechecking at reset; MCP activity alone does not prove idle. |
| Codex rollout reader in the same file | Context and activity come from the session's own rollout. | Native compaction and thread changes must be distinguished from a managed cycle; retained usage alone cannot prove a new context. |

## Terminal and reset evidence

- `internal/mcp/terminal_name.go` already verifies a launched host against an exact tmux
  pane's process ancestry, refusing an intervening native agent. It preserves the provider's
  unresolved socket path. Naming proof does not itself authorize typing or prove readiness.
- `agents/skills/peer-tmux/SKILL.md` defines the family's native commands and prompt handling
  (PDR 002, Decisions 2 and 4), and validates
  receipt after Enter instead of blindly sending another Enter. Its handoff artifact and
  pickup checks should be the behavior reference, rather than another copied procedure.
- The manual skill and `docs/sprints/2026-10-08-participants/live-checks.md` F3 record
  that Claude gets a new transcript/session ID while its existing MCP server retains
  its first native environment ID. Koinon therefore sees one continuous session. A
  changed Koinon identity cannot be a universal reset criterion; the native transcript
  epoch and the identity presented by the retained MCP process must be distinguished.
  PR #304 fixed post-clear observations for this same-host case; see the source correction
  below. It did not implement a context cycle.
- Codex's process and pane survive `/clear`, while its thread changes. #275 still records
  the gap before the new visible thread's first native call: queueing against a previous
  thread is not proof of receiving-model delivery to the current one. The installed
  Codex version is 0.162.0; no new upstream behavior has been verified by this inventory.
- [Claude's SessionStart hook documentation](https://code.claude.com/docs/en/hooks#sessionstart)
  lists clear as a start source and supports additional startup context. This is a candidate
  bootstrap interface. It has not been tested here as a native clear-to-pickup proof and
  does not establish support for a missing native `/pickup` skill by itself.

## Embedded skills and bootstrap

The #145 canonical catalog and library queries are delivered by
[PR #320](https://github.com/rwcii/koinon/pull/320) and
[PR #323](https://github.com/rwcii/koinon/pull/323). Published source embeds the canonical
skills and exposes `library_list`/`library_read` through native MCP and command interfaces.
The state/decision chunk is independently reviewed at `2717575` and awaits publication;
the cycle fallback needs the delivered catalog/read interface, not adoption or installation.
An installed predecessor keeps its own interface until an authorized upgrade.

An agent can read a catalog skill before reset and still lose that context during reset.
An absent native pickup command cannot be assumed to load the catalog. Before declaring
this fallback supported, evidence must show how the successor receives its cycle-specific
pickup instruction and reads the canonical skill. The cycle includes native commands
and handling prompts that appear during reset or pickup. A startup hook, MCP initialization
text or parser behavior needs a real receiving-session proof, including reuse of an MCP
process. #303 now supports observations from a changed Claude registry session ID while
the MCP caller identity remains retained; its synthetic tests do not by themselves prove a
complete native reset/pickup cycle.

## Existing configuration and durable audit

- `internal/core/settings.go` implements dashboard-only durable timing settings with an
  atomic applied value. It currently contains no family context settings or session enable.
- `internal/core/dashboard_actions.go`, `dashboard_auth.go` and `audit.go` enforce existing
  login/Host/Origin/CSRF checks and accepted-action audit atomicity. An asynchronous launch
  writes a start record before its external side effect; unknown outcomes are surfaced.
  This is relevant precedent for uncertainty, not an existing cycle implementation.
- `internal/core/storage.go` accounts every write against ordinary capacity, reserved
  controls and durable work debt. A future cycle promise needs its own checked admission
  and crash behavior. The current audit cap and retention alone cannot fund future writes.

## Verification still required before a support claim

1. Native readiness correlation for the actual session and the particular requested cycle.
2. Bounded handoff artifact verification without importing its private body into the daemon,
   public docs or tests, and without following arbitrary model-supplied paths.
3. Fresh own-pane/host and idle proof immediately before each permitted input.
4. Native clear receipt and reset epoch, distinguishing native identity changes from an
   unchanged host or retained MCP process.
5. Pickup bootstrap when skills are absent, and receiving-model confirmation after reset.
6. Unknown external outcomes after a daemon crash, preventing blind command replay.
7. Compatibility with family defaults, explicit per-session enable and native compaction.
8. Linux/macOS native proofs for any family declared available. Missing #275/#199 proof
   stays explicit; synthetic transport tests cannot substitute for it.

No host service, skill installation, native configuration or terminal input was changed
while collecting this evidence.

## Installed interface metadata check

On 2026-10-12, local CLI version commands reported Codex 0.162.0 and Claude Code 2.1.296.
No upgrade was performed. Codex generated its experimental app-server JSON schema into
an isolated temporary CODEX_HOME without starting a provider turn or changing native settings.
The generated API exposes `hooks/list`, hook start/completion notices, hook trust/source
metadata, SessionStart as a hook event, and turn `additionalContext`. It exposes
`thread/started`; the clear-related method/notice found there is a thread goal clear,
which is not a transcript reset. This schema supplies no proof of a TUI-visible clear-to-first-
prompt event. Hook availability in the API does not establish that the running TUI emits it
at the required boundary or that a missing native skill command is accepted.

Claude's installed help lists `--plugin-dir` as session-only plugin loading and `--bare`
as a mode that skips hooks while still resolving slash skills. A transient native skill
wrapper is therefore an interface candidate for a missing installed skill, but it has not
been tested and is not a chosen design. It would need native discovery, command acceptance,
canonical library read and post-clear receiving-model proof; it must not be described as
verified or adopted merely because the option exists.

## Post-clear observation correction and remaining native proof

The current Claude MCP identity path reads the retained process environment's
`CLAUDE_CODE_SESSION_ID` (`internal/mcp/identity.go`). `claudeActivity` in
`internal/mcp/observe.go` originally required the owned parent registry's session ID to
equal that caller ID. Participant live check F3 establishes
that `/clear` can change the registry/transcript session ID while this MCP environment
ID stays unchanged.

PR #304 (fixing #303) added a same-host check to the activity reader: the parent host's
process start must match the start pinned at initialization before a changed registry
session ID is accepted. `internal/mcp/claude_clear_observe_test.go` covers the changed ID
and host mismatch with synthetic metadata. The daemon's Claude status-line observation
path also uses checked host attribution. These source fixes restore observations; they
do not prove native clear acceptance, reset epoch or receiving-model pickup.

The full cycle still needs native read-back and reset/bootstrap proof. The existing reader
labels a shell prompt with a background shell task as idle; that display value alone does
not establish reset readiness. The specification must distinguish current native transcript,
retained MCP caller identity and the verified cycle result.

## Checked technical-plan inputs — 2026-10-13

- `settings.go` stores integer settings atomically with the accepted dashboard audit and
  applies them after commit. Family mode/threshold rows can reuse that mechanism; session
  Enable and cycle state need new bounded schema. Current schema 20 is published by PR #333.
  Choose later migration numbers from the actual implementation base, not this plan.
- `observations.go` display freshness uses readable confirmation time. The plan proposes
  a separate trigger check of both source and confirmation ages. This is a specification
  choice for Gate B, not an existing cycle capability.
- `terminal_name.go`'s `hostPane` checks host-to-pane ancestry and an intervening agent.
  Registration pins host start and the unresolved socket/pane. The executor must recheck
  those facts before input; a previously successful rename is not current readiness.
- `storage.go`'s `debtPages` currently funds work-claim and work-verdict obligations.
  Adding cycle state or audit rows does not fund them automatically. Chunk 02 must derive
  its actual allocation bound and test shared saturation before admission promises exist.
- Codex's proposed native-compaction default and leaving native settings unchanged are
  identified as proposals in PDR 006. They are not separately settled maintainer decisions.
- No native reset, receiving-model pickup, absent-skill bootstrap or prompt-response test
  was run by this planning increment. The DoD native matrix records the evidence required
  before the implementation can declare a family/version/platform available.

## Native compaction check — 2026-10-26 (#509)

Linux, codex-cli 0.164.0, an interactive Codex in a private tmux socket after one short turn.
The adapter's own `CompactProbe`, `Inspect` and `Compact`, run against that pane with the real
tmux and process tools:

| Step | Result |
| --- | --- |
| `CompactProbe` | `Entry: /compact`, no reason: platform, the thread's rollout and the pane proof pass without a pickup skill or participant address |
| `Inspect` | `Idle: true` |
| `Compact` | `submitted=true received=true`: `/compact` typed, read back in the composer, one Enter, then the empty composer |
| Screen | "Compacting context (… esc to interrupt)", then "Context compacted · 7s" and the empty composer |
| Rollout | `task_started`, a `compacted` record, `token_count` with `last_token_usage.input_tokens` 0 and the same `model_context_window` (475000), `task_complete`, all in the same thread |

The confirmation rule reads that `token_count`: a fresh reading recorded after the receipt, for the
same epoch, below the threshold. macOS is checked by the maintainer before release.

## agy cycle adapter (#512)

Antigravity CLI 1.4.7 on Linux, 2026-10-27, in a private tmux server with a scratch Git
workspace; the redacted captures are `internal/core/testdata/agy-screens`. The maintainer's agy
settings were restored to their original bytes after each run.

- Screen. The composer is the `>` line between the last two full-width rules; submitted
  prompts in the transcript are framed by a rule too. Idle: an empty composer and the footer
  `? for shortcuts`. A draft shows `> TEXT` and no footer hint; a running turn shows a spinner
  line (`Generating...`, `Running command...`) above the rules and the footer `esc to cancel`.
  A typed `/` opens a fuzzy menu below the rules, its selected row marked `> `, with the hint
  `↑/↓ Navigate · enter Select · tab Complete` and `↓ N more` when it scrolls. `C-u` clears
  the composer. A key sent right after Escape is lost (read as Alt+key). A turn cancelled with
  Escape ends with `⎿ Interrupted · What should Antigravity CLI do instead?`.
- `/clear`. With the full command typed, the menu has the one selected row
  `/clear  Clear conversation and start a new one`; Enter runs it. The screen then shows the
  banner (`Antigravity CLI 1.4.7`) and an idle composer with no transcript line. The host
  process, its PID and start time stay; a new conversation starts at once, before any turn
  (the host's log says `Streaming conversation <new>`, and the host holds an exclusive flock on
  `~/.gemini/antigravity-cli/presence/<new>.lock`).
- New conversation proof. agy runs the `statusLine` command as `sh -c` under the host
  (`hook <- sh <- agy <- tmux server`), on change only. After the `/clear` Enter at t0 it
  reported the old conversation once more at t0+148 ms, then the new one at t0+461 ms (0 tokens,
  `idle`), with no turn and no tool call between. Its input carries `agent_state` (`authenticating`,
  `idle`, `working`, `tool_use`). The flock is Linux-only evidence and is not used.
- Pickup entry. `~/.gemini/config/skills/koinon-pickup/SKILL.md` is listed as `/koinon-pickup`
  with its description, cut with `...` at the pane width. A workspace
  `.agent/skills/koinon-pickup/SKILL.md` or `.agents/skills/koinon-pickup/SKILL.md` replaces it
  (`.agent` first) and the menu still shows one row, with the workspace description; a workspace
  `.gemini/skills` does not. Skills reload at each conversation switch.
- Pickup run, against a disposable daemon (`KOINON_STATE_DIR` and `KOINON_DAEMON_ADDRESS` in
  agy's environment, checked in its `koinon mcp` child). Enter on the `/koinon-pickup` row
  submits it: `> /koinon-pickup` becomes the newest prompt above an empty composer, and the
  skill calls `koinon/library_read`. Started with `koinon agy`, the first conversation registered
  at that call with the participant address; after `/clear`, the new conversation's first call
  registered it in the same host, retired the old one and moved the address to it.
- Dialogs. A command that the permission rules do not allow shows `Requesting permission for:`,
  the command, `Run this command?`, numbered options (`> ` marks the selected one; long options
  wrap onto unindented rows; the list scrolls) and the hint
  `↑/↓ Navigate · tab Amend · ctrl+g edit/expand command`. A digit moves the selection and Enter
  confirms it; a declined request ends with `⎿ User declined the tool call`. The status line
  shows `tool_use` meanwhile. The folder-trust question at the first start in a folder is not
  numbered. With `mcp(koinon/*)` allowed, no MCP tool approval appeared.
- Process. The launcher execs agy in the pane (`~/.local/bin/agy`, no arguments); agy leads the
  terminal's foreground group, and its `koinon mcp` child is in that group. A short-lived `agy`
  child appeared once at start. Terminal naming renamed the tmux session; the pane ID stayed.

- Managed cycle runs, with the branch binary as the daemon (a scratch state directory and
  `HOME`), as agy's MCP server and as its status-line command, family mode `handoff_cycle` at
  1%. Each run: `koinon agy` in the private tmux server, one `koinon/peers` call, Enable, a
  status-line change admits the cycle, and a short turn's Stop hook delivers the request.
  - Run 1: Enable, admission, the request and the handoff worked; `context_cycle_ready` was
    refused with `cycle_epoch_unverified`, because `koinon mcp` gave agy no epoch (fixed).
  - Run 2: ready, `/clear`, the new conversation's witness, `reset_verified`, `/koinon-pickup` and
    the successor's pickup worked. The successor's pickup raised command dialogs, and each one
    cost two events (`waiting_prompt`, then `waiting_pickup` `prompt_resolved`); at the fifth
    the cycle failed with `event_limit` (#520), and the successor's report was refused.
  - Run 3, with #520: `requested`, `ready`, `clear_intent`, `clear_received` (339 ms later),
    `reset_verified` (552 ms), `pickup_intent`, `waiting_pickup`, one `waiting_prompt` for the
    successor's four command dialogs, then `complete` `resumed` with the successor's report:
    9 events, 101 s from the pickup input to the report. The Enable moved to the successor,
    which was admitted to a new cycle at once at 1%; Disable cancelled it.
  - Each dialog was answered in the pane, after reading its whole command (`ctrl+g` shows a
    long one). After Escape from that view the next key was received.

Not yet verified: macOS; an MCP tool approval dialog.

## OpenCode context source (#513)

OpenCode 1.18.35 on Linux, 2026-10-27: a scratch `opencode serve --hostname 127.0.0.1 --port P
--mdns=false` with `OPENCODE_SERVER_USERNAME=opencode` and `OPENCODE_SERVER_PASSWORD`, as
`koinon opencode` starts it, in a scratch Git workspace, with two one-line prompts on
`opencode/big-pickle`. The probe sessions were deleted afterwards. The redacted page and
provider list are `internal/core/testdata/opencode`.

- Messages. `GET /session/{id}/message?limit=N` returns the newest N messages, each `info` with
  its `parts`; no parameter leaves the parts out. An assistant `info` has `role`, `providerID`,
  `modelID`, `time.created`, `time.completed`, `finish` (`stop`) and `tokens` `{total, input,
  output, reasoning, cache{read, write}}`. In the first reply, total 22314 = input 20520 +
  output 2 + reasoning 0 + cache.read 1792 + cache.write 0. A `step-finish` part carries its own
  token counts.
- A turn that did not finish (the configured default model stalled, then the turn was
  aborted) left an assistant message with `time.completed`, `finish` null, no `total` and all
  counts 0.
- Session counts. `GET /session/{id}` has `tokens` without `total`, summed over the turns: after
  two turns, input 82 = 35 + 47 and cache.read 44544 = 2 × 22272. They are not the context.
- Limits. `GET /config/providers` lists `providers[]` with `id` and `models`, a map from model ID
  to a model with `id` and `limit` (`big-pickle`: context 200000, input 160000, output 32000). A
  provider entry also has `key`, `options` and `env`.
- The v2 `GET /api/session/{id}/message` and `/api/session/{id}/context` returned no data for
  these sessions.

- Reading, with the branch binary as the daemon (a scratch state directory and `HOME`) and
  `koinon opencode --model opencode/big-pickle` running the TUI in a private tmux server. One
  `koinon_peers` call registered the session. After the next round, `peer-status` showed
  model `opencode/big-pickle` and context 17100 of 200000 (`opencode_server`, source time
  the reply's completion), the same count as OpenCode's sidebar (`17,100 tokens`, `9% used`).
  After one more turn both showed 17139. The test session was deleted afterwards.

Not yet verified: macOS; a session after OpenCode's own compaction.

## OpenCode cycle adapter (#513)

opencode 1.18.35 on Linux, 2026-10-27, launched with `koinon opencode` in a private tmux server
with a scratch Git workspace, against a disposable daemon (a scratch state directory and `HOME`),
on the person's configured model `openrouter/qwen/qwen3.6-35b-a3b`. The redacted captures are
`internal/core/testdata/opencode-screens`; the probe sessions were deleted afterwards.

- N1. `POST /session` creates a session and returns its `id` and `title`. `POST
  /tui/select-session {sessionID}` returns `true` and only publishes a `tui.session.select`
  event; nothing reads back the session that the TUI shows. A draft in the composer stays
  through a switch.
- N2. `GET /command` lists `koinon-pickup` with source `skill`. Its template is the skill body
  after the front matter, then `Base directory for this skill: DIR` and `Relative paths in this
  skill (e.g., scripts/, references/) are relative to this base directory.`, with `DIR`
  `~/.config/opencode/skills/koinon-pickup`. OpenCode also lists skills from `~/.claude/skills`;
  one `koinon-pickup` is listed when both exist.
- S1. `POST /session/{id}/command` answers only after the whole turn (27 s, busy throughout).
  The successor registered through the identity plugin with that session's ID, in the same
  launch and host.
- S3, S4. A typed `/new` goes to the home screen (`Ask anything…`, centered); no session exists
  until the first prompt. The TUI slash menu lists no skill: `/koinon-pickup` shows `No matching
  items`.
- S5. A session created by API uses the configured default model, not the TUI's.
- S6. The sidebar, with the session title, shows only at about 160 columns or wider.
- S7. The command body is `{command, arguments, messageID, model}`. The given `messageID`
  (pattern `^msg`) is kept exactly and is the only user message; with empty `arguments` the text
  is the template, trimmed. OpenCode's own message IDs are `msg_`, 12 hex digits of the creation
  time in milliseconds times 4096, then 14 base-62 characters.
- S8. When the client gives up after 5 s, the turn goes on (busy for more than 45 s).
- S9. `model` `openrouter/qwen/qwen3.6-35b-a3b` gave the user message `providerID` `openrouter`
  and `modelID` `qwen/qwen3.6-35b-a3b`, and the replies came from that model; the string is
  split at its first `/`.
- S10. The TUI renders each Koinon tool call as `⚙ koinon_TOOL [ARGUMENTS]` at a five-space
  indent, with the identity plugin's `koinon_session=ID` last; the glyph and the call are in the
  muted color (`38;2;128;128;128`, red `224;108;117` for a failed call), the space between them
  in another, and reply text in the text color (`38;2;238;238;238`). Long arguments wrap at
  80 columns onto rows at a seven-space indent in the muted color. User messages and tool output
  are framed by `┃`. A long final reply scrolls the tool rows out of the current view.
- Permissions. `GET /permission` lists pending requests with `id`, `sessionID`, `permission` and
  `patterns`; `POST /permission/{id}/reply {reply: once|always|reject}` returns `true`. A pending
  permission replaces the composer in the pane.

- Managed cycle runs, with the branch binary as the daemon and as OpenCode's MCP server (a
  scratch `OPENCODE_CONFIG` that names it), family mode `handoff_cycle`, session threshold 5%.
  - Run 1: Enable, admission, the request and the handoff worked; `context_cycle_ready` was
    refused with `cycle_epoch_unverified`, because `koinon mcp` gave OpenCode no epoch (fixed:
    an OpenCode call's epoch is its own session).
  - Run 2: `requested`, `ready`, `clear_intent`, `clear_received` (54 ms later),
    `clear_received` `select_sent`, `reset_verified` (1.0 s after the select),
    `pickup_intent`, `waiting_pickup` (0.9 s), `waiting_pickup` `pickup_displayed` (1.0 s),
    then `complete` `resumed` with the successor's report 17 s later: 10 events. The created
    session had the cycle's title, its first message had the derived ID and the requester's model
    pair, its first call was `koinon_library_read`, and its last `koinon_context_cycle_pickup`.
    The participant address and the Enable, with its 5% threshold, moved to it.
  - In runs 1 and 2 the requester model needed the steps typed in its pane before it wrote
    its handoff; the cycle does not depend on that model.
  - Run 3: the successor of run 2, at 5%, was admitted to a new cycle at once and wrote its
    handoff and ready report itself. Create, select, reset, pickup, receipt and display proof
    passed (`pickup_displayed` 1.0 s after `waiting_pickup`), but the new successor made no
    report within the 2-minute stage deadline, and the cycle ended `failed`
    `waiting_pickup_timeout`. Its session was deleted before its transcript was read, so why it
    did not report is not known.
  - Run 4, unattended after Enable: the requester answered the request itself, and its cycle and
    the chained cycle of its successor both completed `resumed`: 10 events each, the report 37 s
    and 23 s after `pickup_displayed`. A Disable cancelled the third chained cycle in
    `waiting_ready`.
  - Run 5, after the review fixes (a report needs a current view with the successor's row, the
    replies' model is checked, and the worker reads the receipt), unattended: both chained
    cycles completed `resumed`, each report accepted on its first call (23 s and 22 s after
    `pickup_displayed`). In the second, the requester kept working after its ready report twice,
    so overwatch asked again (`not_idle.turn_running`) before its clear. A Disable cancelled the
    third.

Not yet verified: macOS; a permission dialog during the pickup; a pane narrower than 80 columns.

## DeepSeek context source (#514)

Read 2026-10-27 from DeepSeek Harness `@deepseek-ai/dsh` 0.2.0-rc.2, in the development
container that #199 used (the harness does not run on the host). The package files were read
in the image; the recorded session's log and projection cache were read for event types, seqs,
times and token and model fields only.

- Reads. `session/projections` reads all projections of a session without starting an agent
  (`dsh-api-session-controller`). `session/page` returns a contiguous, ascending run of events
  that ends before `min(throughSeq + 1, beforeSeq)`, each `{type, seq, time, data}`.
  `session/list` rows have `updatedAt` = max(`createdAt`, the newest user prompt's time), so it
  is not the time of a reply.
- Pressure. The token meter (`dsh-token-meter`, in the base composition) keeps
  `contextPressure`: `pressureTokens` = the newest usage's `inputTokens + cacheReadTokens +
  cacheWriteTokens`, and `contextWindow` from the newest `request/context`. Its source says
  the two are independent last-wins values, not one request's observation. `projectedTokens`
  adds the change in its own surface estimate since that usage.
- Model. `modelSelection.lastUsed` is the `{provider, model}` that the latest request used.
  `request/context` is written only when the model or window changes (once in the recorded
  session, at seq 11).
- Recorded session (only its model, context and newest usage events are named here):
  `request/context` seq 11 `openrouter`, `deepseek/deepseek-v4-flash`, `contextWindow`
  1024000 (the OpenRouter catalog's value; the harness default is 1e6); the newest usage at
  `assistant/message` seq 58, time 1791916491862, `inputTokens` 728 and `cacheReadTokens` 8960;
  `turn/end` at seq 60. The projection at `asOfSeq` 60: `pressureTokens` 9688,
  `projectedTokens` 9808, `contextWindow` 1024000.
- Reading, with the branch binary (`24a96be`, linux/amd64) as a container-local daemon with a
  new state directory, and `dsh web` restarted on the existing home volume. The recorded
  session was registered with `koinon register` and its harness origin and credential file; no
  model request was made. The harness's own `session/projections` answered the values above.
  After the next round, `peer-status` showed model `openrouter/deepseek/deepseek-v4-flash` and
  context 9688 of 1024000 (`dsh_projections`), with source time 1791916491862, the usage
  event's time. The daemon and the harness were stopped and the container was stopped.

Not yet verified: macOS; a reading that changes during a live turn; a session after the
harness's own compaction.

## DeepSeek cycle adapter (#514)

Run 2026-10-27 with DeepSeek Harness `@deepseek-ai/dsh` 0.2.0-rc.2 (`dsh web`) in the
development container that #199 used, on Linux; the harness does not run on the host. The
branch binary ran as a container-local daemon with a new state directory, and the agents'
model requests went to `openrouter`/`deepseek/deepseek-v4-flash`, the harness default, through
the container's OpenRouter key. Only commands, IDs, event types and stage records are named
here; the agents' text is not.

- API facts. Each method's arguments are named by its descriptor, and any other field is
  refused with `gateway/arguments-invalid`: `session/list` takes `_request`,
  `session/modelCatalog` takes none (it returns `default` `{provider, model}`), and
  `session/projections`, `session/page`, `skills/list`, `session/create` and `session/prompt`
  take `request`. `session/list` rows carry `sessionId`, `updatedAt`, `running`, `blank` and
  `cwd`. `skills/list` listed `koinon-pickup` at `$DSH_HOME/skills/koinon-pickup/SKILL.md`,
  ahead of Codex's entry of the same name in `~/.agents/skills`. A queued prompt appears as
  `agent/inbox/spliced` `inserted[]` and then, after `turn/start`, as `user/message`, each with
  `source {kind: user, rpcId}`; `request/header` carries `header.config {provider, model}` and
  `assistant/message` `message.source {kind: model, provider, model}`.
- Create. `session/projections` for an unused chosen ID answered `null`. `session/create` with
  `{sessionId, cwd}` created it, the same request again returned the same session, also after
  `dsh web` was restarted, and the same ID with another `cwd` answered `session/conflict`.
- Prompt. `session/prompt` sent twice with one `requestId` was admitted twice (two turns), so
  the harness does not deduplicate request IDs; the adapter sends the pickup once, and a
  restart only reads its receipt.
- Cycle 1. The requester session registered with `--repository`; the family mode was
  `handoff_cycle` at 1 percent, because the session's context was about 1.5 percent of the
  1024000-token window. Enable passed the probe. After one turn the reading admitted the cycle;
  the requester's agent read its inbox, wrote its handoff and ran `koinon cycle-ready`. A queued
  wake turn ran after the ready report, and the quiet point waited for its end (`clear_intent`
  24 s after `ready`). The daemon created the session with the cycle's ID in the same `cwd`,
  recorded `select_sent` without input, proved the reset, sent `/koinon-pickup` and recorded the
  receipt 0.8 s after `pickup_intent`. The created session registered and took the address
  with `cycle_session`, and its `koinon cycle-pickup` completed the cycle `resumed` 106 s after
  the pickup. The Enable moved to it.
- Cycles 2 and 3. The moved Enable met the 1-percent threshold at once, so the successor ran
  the next cycle: cycle 2 completed `resumed` 115 s after its pickup; cycle 3's pickup took
  longer than the 2-minute `waiting_pickup` deadline and the cycle ended `failed`
  (`waiting_pickup_timeout`), with nothing sent again. The family mode was then set back to
  `observe`.
- Cycle 4, with a claim and a restart. With a new state directory, the requester started a
  work item through the command path (claim generation 1, consumer `participant:deepseek-repo`)
  and the family threshold was set back to 75 percent right after admission. The daemon was
  stopped and started again 1 s after `waiting_pickup`. The created session read the
  cycle's handoff file, registered with `--repository` (`cycle_session`) and reported; the
  cycle completed `resumed` 81 s after the pickup. The claim stayed active with generation 1,
  its expiry extended to the cycle deadline plus 60 s, and the created session held
  `deepseek-repo`.
- Settings. The harness's `cordis.patch.yml` files kept their 2026-10-13 times through both
  runs: the adapter changed no harness setting. The daemon, the harness and the container were
  stopped.

Not yet verified: macOS; a daemon restart inside `clear_intent` or `pickup_intent` with the
real harness (the synthetic tests cover them); a pickup within the 2-minute deadline when the
model is slower.
