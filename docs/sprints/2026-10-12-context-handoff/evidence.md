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
