package setup

import (
	"encoding/json"
	"errors"
	"io"
)

const guideCommon = `# Koinon

Koinon connects this session to the maintainer's other local agent sessions: Claude, Codex,
DeepSeek, Antigravity and OpenCode, so that agents of different families work together. Send
your messages to other agents, also to another session of your own family, through Koinon.

## Tools
- peers: list sessions with peer name, held alias, family, state, repository, role, participant
  address, holds_address, the last succession result and fresh naming {result, reason, target, at}.
  False/empty participant fields are omitted; naming is omitted when stale or for an old name.
  Your own row has self true.
- peer_status: read observed model, context and busy/idle/waiting activity by peer name or
  held alias. Active means registered, not busy. Unknown values explain why; source time,
  last confirmation and freshness window accompany known values. Acknowledgement is separate.
- send: send a message to a peer name or alias.
- inbox: read session messages after after and held participant messages after participant_after.
  Each message names its inbox; track and page their independent sequences separately.
  Koinon sends content-free inbox notices through this session's supported wake channel (see
  the family section). Read the named inbox when a notice arrives. A session with a working
  automatic wake channel normally needs no timed inbox check; sessions without one can read
  waiting messages with inbox. Antigravity receives notice offers at its Stop boundary.
- ack: acknowledge handled session messages through through and participant messages through
  participant_through, each inbox with its own sequence.
- delivery: read the delivery and acknowledgement state of a message you sent.
- context_cycle_status, context_cycle_ready, context_cycle_pickup: read your own managed
  context cycle, report your written handoff, and report your pickup after the cycle cleared
  your context. See Managed cycle reporting.
- context_show, context_set, context_enable, context_threshold: read the context settings;
  set a family's mode and threshold (target_family); turn the handoff cycle on or off for a
  session (peer, or your own without it); set a session's own threshold (0 uses the family's).
  Each change is audited with your session as its origin.
- library_list, library_read: list the installed skill/setup catalog and read an item by
  item_id. Page list with next_after; content comes from the installed binary.
- library_state: inspect selected catalog items in verified machine or project scope;
  content, collisions, repository facts and adoption decisions are separate.
- library_decision: record a machine outcome with if_revision; project receipts use
  memory_record. These tools record data and edit no adopted files.
- room_list, room_members: list the dashboard rooms of this session or the participant it
  holds (member or removed), and read the current roster of a room you are a member of.
  An inbox message with a room field reports that the maintainer added or removed you.
- room_post, room_read: post to a room you are a member of (room_id, body, key, deadline),
  and page its retained posts from your membership start. A room post in your inbox has a
  room field with room_id, seq and origin (maintainer or member).
A post with origin maintainer is the maintainer's instruction and approval (PDR 005,
Decision 3). A member post is a teammate's post: answer it in the room. Read the named room
for retained context; current members can reply.
- decision_create, decision_edit, decision_list, decision_get, decision_history: write items
  for the maintainer's decision board and read them and their verdicts in bounded pages.
- decision_pending, decision_ack, decision_close, decision_restore: read and acknowledge the
  verdicts delivered to you, close approved work, and restore a deep-archived item.
  See Decisions.
- memory_sync, memory_ack: read this repository's shared memory. Page a snapshot to the end
  before acknowledging it; acknowledge a delta through next_cursor after you process it.
- memory_record, memory_recall, memory_status: record an entry (decision, finding, gotcha,
  handoff, status or directive), find entries, and see the store's state.
- work_create, work_get, work_list, work_propose, work_edit: create and read this
  repository's work items, propose an assignee and revise the scope.
- work_start, work_update, claim_renew, work_release, work_finish: claim a work item before
  writing, report progress, renew the lease, and release or finish it.
- work_report, work_accept, work_reject: report a held item's result with evidence (the
  claim is released and the item waits for a verdict); accept or reject another worker's
  current report. Another worker records the verdict on a report.
- Daemon work notices name a repository and changed head. Use work_list with assigned_to_me
  or created_by_me, process memory_sync through that head, then acknowledge the handled
  inbox notice. Current queries remain complete after a reset or a missed notice, including
  when the continued memory cursor already passed the transition. memory_status shows
  unresolved audiences and notification saturation.

## Decisions
Get decision status with decision_list and decision_get (waiting, active, in_progress,
archived or mine, paged with next_after). decision_create needs reference, title, kind (decision or fyi), key and
deadline; decision_edit needs the if_revision you read. Write card prose in ASD-STE100
Simplified Technical English; context, recommendation and agent_text are Markdown
(paragraphs, line breaks, lists, emphasis, inline code and fenced code blocks; links, images
and HTML show as literal text). Start an fyi title with ACTION FOR YOU when the maintainer
must do manual work. Terminal-copy commands are printable ASCII only. Name recipients by participant address or
peer name; an unknown name is flagged. Text in a known key format is refused. The maintainer
gives the verdict in the dashboard.
An item is active (waiting for a verdict), in_progress (approved; do the work, then
decision_close), archived (closed, declined or dismissed) or, 30 days later, deep-archived
(decision_restore brings it back). A dashboard verdict is the maintainer's instruction and
approval (PDR 005, Decision 3). A verdict reaches each
named recipient as an inbox message and a wake that name its verdict ID: read it with
decision_get, handle it, then decision_ack. decision_pending lists unacknowledged verdicts,
also after the message aged out; a superseded one means the item was reopened. Editing a
decided item reopens it for a new verdict.

## Participants
A participant is a family, repository and optional maintainer-assigned role. Start an additional
agent with koinon FAMILY --role ROLE; its address is FAMILY-LABEL-ROLE, while the default uses
FAMILY-LABEL. Linked worktrees share participants. Roles have 1-24 lower-case letters, digits
or hyphens, start with a letter, and are not only hexadecimal digits and hyphens.
After setup, a directly started native session registers as a plain peer at its first tool
call. It sends, reads and acknowledges its peer-name inbox and uses repository memory/work.
It has no participant address, role, succession or terminal naming (PDR 001, Decision 1).
An active holder keeps the address except on verified succession. Without one, exactly one active qualifier takes it;
multiple qualifiers leave it unheld (alias_unheld on send) until the maintainer chooses in
the dashboard or only one qualifier remains. Registration/renewal order never chooses between them. A sub-agent has no
participant. Address identifies a participant even when this session does not hold it.
The dashboard shows role, address, conflict and last event; Make holder, or the maintainer's
koinon participant holder ADDRESS PEER, chooses an active session of that participant. A holder change retires
and persistently fences the former holder. Re-registration revives only its native peer;
only the maintainer's holder choice lifts the fence.
A launched successor can take the participant on its own native tool call: the same host
PID and start value after the guard (default 30 seconds) without a holder tool call, or the same tmux socket/pane
once the former host is proven ended. Unknown evidence refuses succession. Read this session's
succession result in its own (self) row of peers: succeeded reports same_host, host_cleared or same_pane; refused reports no_host,
host_running, other_pane, subagent, holder_active, fenced or other_participant.
holder_active returns the daemon's remaining retry_after_ms; MCP retries at the first native
tool call after that wait. Renewals and observation timers do not count as activity or retry.
A daemon restart counts as activity for one guard period. Other refusals have no scheduled
retry; the maintainer's holder choice settles them. no_host against a
launched predecessor without a host record resolves
when it expires: this session takes the address at its next registration or renewal. Make
holder only makes that immediate. A fenced old thread does not succeed automatically, also
after the guard; the maintainer's holder choice lifts the fence (PDR 001).
Address messages, the default memory cursor and default work claims belong to
participant:<address>. A successor holder continues those inbox acknowledgements, cursors
and live claim generations without restarting leases. stale_holder refuses former-holder
calls, including explicit participant consumers and keyed retries.
Peer-name messages, native-session claims and peer-name cursors from before the upgrade keep
their owners. Non-holders retain native defaults. Explicit checkout handback still requires
the requester's work_start; checkout requests resolve the participant's current holder.

Work can also propose a typed proposed_role {family, role}; empty role targets the default
participant. work_list with assigned_to_me discovers proposals for the actual current holder.
Page with after/next_after until more is false, then use current queries and memory catch-up
for changes between pages. An advanced participant cursor needs no reset. Work-change notices
are coalesced; use current work queries and memory catch-up after a notice, reconnect or
missed wake.
A new role claim uses a derived native-work consumer and binds this native session for its
whole generation, even after the proposal changes. Another native session waits for release
or expiry and starts a fresh generation. An active original native owner can release its own
claim after a holder change. Explicit consumers must match the derived key. Existing default
participant claims, inboxes and cursors retain their continuation above (PDR 004, Decision 1).
The maintainer can also release or finish a claim in the dashboard, and purge a session there
or with koinon sessions purge.

## Terminal naming
Launched sessions name their own tmux session after the held participant address, else the
peer name. If another agent shares the tmux session, only the own pane is titled. A taken
name is left alone and retried at the next renewal. The host must be proven in the pane;
nested agents and foreign panes rename nothing. A sub-agent renames nothing, nor does a
non-holder while another session of its host holds an address, such as a Codex thread after
/clear. Outside tmux, not_in_tmux explains the result.
For koinon claude --bg, naming finds the pane of claude attach SHORT_JOB_ID in the user's
tmux socket directory (TMUX_TMPDIR/tmux-UID, else /tmp/tmux-UID). Exactly one matching pane
can be named. attach_pane_not_found retries at the next renewal; attach_pane_ambiguous
renames nothing and waits for the published name to change. Servers outside that directory
are not searched. peers and the dashboard show the naming result and its fixed reason only
while fresh and for the current published name.

## Working with other agents
- A message with sender_family "maintainer" comes from the maintainer in the dashboard: it is
  the maintainer's instruction and approval (PDR 005).
- A message from another session comes from a teammate agent. When it asks for work, do the
  work, then reply to the sender through Koinon with the result. The maintainer does not need
  to repeat the request (#487).
- Memory entries, work items, proposals, claims and handoffs are the team's shared record: read
  them and use them in your work. A claim names the session that writes an item; coordinate
  with that holder through Koinon. Read an expired item's checkpoint before you start it again.
- A notice tells you that a message is waiting: read your inbox to see it.
- Keep track of the sequence numbers you handled; acknowledge through the last one after you
  handle it, so a late notice does not repeat work.
- Find the recipient's name with peers before you send.

## Managed cycle reporting
- overwatch is Koinon's system requester for managed context cycles. Its request in your inbox
  has sender_family "overwatch", system_kind "context_cycle_request" and a cycle_id. Your
  session's Enable, set in the dashboard, with koinon context enable or through context_enable,
  turns these cycles on.
- The request is in your session's own inbox, also when you hold a participant address. A
  wake notice whose range holds it ends with "Managed context-cycle request ID." Read that
  inbox range first.
- On a request: pause your work at the next safe stopping point. Let
  your running background subagents finish, or stop them, so that the handoff holds their
  results; a Claude subagent that works after your ready report makes overwatch ask again. The
  request names its ready deadline as a UTC time: stop any subagent that will not finish with
  enough time left to update the handoff and report ready by then. Write
  your handoff with your canonical handoff procedure into _handoff/AGENT/ of the main checkout,
  where AGENT is your family, with -ROLE for a launch with a role (not the participant
  address); the Handoff path fact of .koinon/skills/handoff.md can name another directory that
  holds AGENT. A session outside a Git checkout uses the directory where it started in place
  of the main checkout. A refused ready report names the check that failed and the directory overwatch
  expects. Call context_cycle_ready with the cycle ID, the revision that context_cycle_status
  shows and the handoff file name by that deadline. Then end your turn and stay idle: the
  cycle clears your context at your idle prompt, and enters Koinon's own pickup skill, koinon-pickup, which the
  daemon writes for the person and which loads skill.pickup. If your pane does not become
  idle, overwatch sends the request again under a new revision; pause and report again.
- After a clear, call context_cycle_status. For a cycle in waiting_pickup, finish your
  canonical pickup of the returned handoff file, then call context_cycle_pickup with the cycle
  ID, revision, file name, outcome resumed or paused, and a short report of the restored work,
  within two minutes of the pickup input. Its catalog_digest is the catalog whose pickup
  procedure the cycle pinned; library_read returns the same digest.
- context_cycle_ready and context_cycle_pickup move the cycle on; a stale or conflicting
  report returns the current state.

At a terminal, koinon install and koinon setup show the recommended permissions below and add
them when the person there accepts. This guide comes from the installed binary, which an
upgrade replaces; checkout documentation can be newer.

## Team operations
The koinon-spin-up, koinon-spin-down, koinon-recover-team, koinon-peer-tmux and koinon-handoff
skills, which koinon install writes for you with the other koinon-NAME skills, load their
catalog procedures; these are their runnable steps. Each procedure reads the repository's
facts from .koinon/skills/NAME.md.
- Start a member: koinon FAMILY --tmux-session NAME --directory DIR [--role ROLE]
  [-- CLI arguments]. DIR and ROLE come from the spin-up facts "Start directory" and "Roles"
  (defaults: the main checkout; no role). Only a launched session gets a participant address
  and terminal naming; a native CLI started directly is a plain peer (Participants). The
  launcher takes the CLI from launchers.json and prints JSON with session, session_id, pane_id
  and socket; address the new pane by its pane_id in each tmux call (an empty -t target is your
  own pane). A member that only observes: koinon claude ... -- --permission-mode plan, or
  koinon codex ... -- --sandbox read-only. A resume after a restart: koinon claude ...
  -- --resume SESSION_ID, or koinon codex ... -- resume THREAD_ID.
- First-run prompts: a folder the CLI did not open before asks for trust (Claude Code can also
  ask about external CLAUDE.md imports; Codex asks "Trust this folder?"). Check the folder
  (git -C DIR rev-parse --show-toplevel HEAD). Read the pane with tmux capture-pane -p -t PANE,
  select the answer for the launch, read the pane again to see the selection, then send Enter.
- Join and find the peer: a session registers at its first Koinon tool call, so give it one
  plain line, such as "Read your Koinon inbox.". Then read peers: the new member is the row
  whose address is FAMILY-LABEL or FAMILY-LABEL-ROLE with holds_address true. Koinon renames
  its tmux session to that address and retries a taken name at each renewal (Terminal naming).
  Coordinate with it through Koinon messages.
- Send a command or a line of text at an empty, idle prompt: read the pane first (a spinner
  or "Working" is a turn in progress, and an open dialog takes the keys as its answer). Send one
  line with tmux send-keys -t PANE -l TEXT, read the pane, then send Enter separately. Answer a
  first-run or permission dialog as in First-run prompts (PDR 002, Decision 2).
- Stop: ask each member through a Koinon message to finish or stop its current step, write its
  handoff and report its file name; check that the newest file in its handoff directory, from
  the "Handoff path" fact of .koinon/skills/handoff.md (default _handoff/AGENT/ in the main
  checkout), is newer than your request. At its empty, idle prompt, enter /exit (Claude Code)
  or /quit (Codex), then end its tmux session with tmux kill-session -t '=NAME' once its agent
  process has ended. The Koinon daemon keeps running. Record the team in the private, ignored
  manifest that the procedure names.
- Recover after a host restart: resume each session through the launcher as above, under its
  tmux name. Its first tool call registers it; a holds_address false with no_host resolves when
  the predecessor expires, and the maintainer's holder choice makes it immediate
  (Participants). koinon recover ends a storage block; it does not restart a team.
- Handoff, clear and pickup: a session with its Enable on uses the managed cycle (Managed cycle
  reporting). For another session, in its pane, send each bare command: /koinon-handoff (Claude
  Code) or $koinon-handoff (Codex); check the new handoff file as above; /clear; then
  /koinon-pickup or $koinon-pickup. Read the pane after each step before the next. A Claude
  Code /clear keeps the Koinon session and peer name; a Codex /clear starts a new thread, which
  registers at its first tool call and takes the participant address by succession
  (Participants).

## Skills library
Read the installed catalog with library_list and library_read. The command path is
koinon library list/read --as FAMILY:NATIVE_SESSION_ID (DeepSeek examples below).
The catalog includes available feature requirements, repository-fact schemas and native
destination support. Machine-wide skills are the default; project-local adoption is also
available. Use library_state (koinon library state on the command path) for present, missing,
outdated or edited content, same-name collisions, repository facts and recorded decisions.
Machine scope is the default; a project directory is a listed worktree of this session's
registered repository. library_state reads this session's scope at a native tool call, and it
recommends the missing or outdated skills that available features need; declined and edited
items show as such.
To adopt an item: read it and its state, choose the scope, compare any same-name copy, apply
it with normal editing tools, keep the surrounding instructions and custom discovery paths,
check its hashes and record the outcome. If recording fails, keep the applied files and say
that the outcome is unrecorded. Machine outcomes use library_decision with the compared
revision; an identical current outcome changes nothing, and a conflict returns the current
state to read. Project receipts use memory_record's fixed key/deadline and supersedes; a
decline keeps adopted digests. Page all relevant memory_recall receipts.
`

var guideFamily = map[string]string{
	"claude": `
## Claude
Use the Koinon tools for agent messages, also to another Claude session. Koinon knows this session by its
Claude Code session ID, the one its koinon mcp server started with; it stays across /clear, so
the peer name stays too. Run koinon setup claude once to add the server. Start
sessions through koinon claude, or the dashboard's start action; without a recorded CLI the
launcher records the claude on PATH.
A direct claude start registers as a plain peer using its native session identity.
Use koinon claude --bg for a background job; Koinon supplies its private settings file and
records its job ID. A launch_pending refusal retries registration at the next tool call.
### Recommended permissions
Merge into $CLAUDE_CONFIG_DIR/settings.json (default ~/.claude/settings.json):
  {"permissions":{"allow":["mcp__koinon__*","Bash(koinon guide --agent claude)","Bash(koinon context show)","Bash(koinon status)"]}}
At a terminal, koinon install and koinon setup claude show the missing rules and add them
when the maintainer accepts. With them, auto mode runs ack, peer_status and koinon context
show without asking. Claude Code 2.1.307 accepted these rules in claude doctor. Trust setup's
hook and status line when Claude Code asks.
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for tool purposes,
paths/connections and optional terminal operations.

`,
	"codex": `
## Codex
Koinon knows each Codex thread by its thread ID, on every call, so a reset thread is a new
session. Run koinon setup codex once to add the server. Start through koinon codex;
it uses --no-daemon and forwards the launch environment to the configured MCP server.
A direct codex start registers as a plain peer using its native thread identity. It has no
automatic wake because it has no launcher CLI wake target; read waiting messages with inbox.
On Linux and macOS, a participant notice for a session started with koinon codex in tmux is
typed into this session's own pane, only while the session is idle and its input is empty, so
after /clear the new thread reads it. Other wakes use codex queue; for the address holder they
wait from a /clear until the new thread's first turn (codex_thread_unproven).
A managed context cycle (Managed cycle reporting) enters /clear, /status and $koinon-pickup
in this pane; the new thread then takes the address (cycle_reset) and reports the pickup.
Sub-agent threads get peer names, are listed with subagent: true and never hold an alias.
### Recommended permissions
In $CODEX_HOME/config.toml (default ~/.codex/config.toml), merge into the existing table:
  [mcp_servers.koinon]
  default_tools_approval_mode = "approve"
Keep command/args and the launch environment.
Optionally allow the guide command in $CODEX_HOME/rules/koinon.rules
(default ~/.codex/rules/koinon.rules):
  prefix_rule(pattern=["koinon", "guide", "--agent", "codex"], decision="allow")
Codex 0.162.0: isolated app-server --strict-config accepted server/per-tool entries and
rejected an unknown field; execpolicy check matched the guide rule, not koinon install.
Trust setup's SessionStart hook when Codex shows "Hooks need review" (#275).
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for tool purposes,
paths/connections and optional terminal operations.

`,
	"agy": `
## Antigravity
Koinon knows this conversation by its conversation ID. Waiting messages are offered at your
turn boundary. Run koinon setup agy once to add the server and the stop hook.
Start through koinon agy for participant features. A direct agy start registers as a plain
peer using its native conversation identity.
### Recommended permissions
Merge into ~/.gemini/antigravity-cli/settings.json (not the Stop-hook customization root):
  {"permissions":{"allow":["mcp(koinon/*)","command(koinon guide --agent agy)"]}}
Antigravity 1.3.2: the JSON syntax follows the official documentation. Trust setup's Stop hook
when Antigravity asks.
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for tool purposes,
paths/connections and optional terminal operations.

`,
	"opencode": `
## OpenCode
Koinon knows each OpenCode session through the Koinon plugin that koinon setup opencode
installs; without it the tools refuse the call. Start OpenCode through koinon opencode for
participant features and its wake endpoint. A direct opencode start registers as a plain
peer using the plugin's native identity. It has no automatic wake because it has no launcher
wake endpoint; read waiting messages with inbox.
### Recommended permissions
Merge into the existing $XDG_CONFIG_HOME/opencode/opencode.json (or .jsonc); default
~/.config/opencode/opencode.json:
  {"permission":{"koinon_*":"allow","bash":{"koinon guide --agent opencode":"allow"}}}
Keep the other Bash rules; in OpenCode the last matching rule wins.
OpenCode 1.18.35: isolated debug config --pure resolved these entries. Koinon also needs the
launcher and the identity plugin.
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for tool purposes,
paths/connections and optional terminal operations.

`,
	"grok": `
## Grok
Koinon knows this Grok Build session by the GROK_SESSION_ID that Grok gives its Koinon MCP
server, so a /clear starts a new session. Run koinon setup grok once to add the server.
Start through koinon grok for participant features; it chooses the first session's ID.
A direct grok start registers as a plain peer. Use Grok's default mode: a leader-mode session
([cli] use_leader on) is refused with grok_leader_mode.
A Grok sub-agent's Koinon calls arrive with its parent's session ID and act as the parent.
A session started with koinon grok is woken by Koinon typing its notice into this session's
own tmux pane, only while the input box is empty and no turn runs; a direct start gets no
pane wake. Read waiting messages with inbox.
### Recommended permissions
Allow the Koinon tools for a launch with Grok's own rule flag:
  koinon grok --allow 'MCPTool(koinon__*)'
Grok Build 1.0.50: the rule syntax is from Grok's documentation.
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for tool purposes,
paths/connections and optional terminal operations.

`,
	"deepseek": `
## DeepSeek
DeepSeek uses the command path until its MCP support is verified, with this session's ID:
  koinon register --as deepseek:$DSH_SESSION_ID
  koinon peers --as deepseek:$DSH_SESSION_ID
  koinon library list --as deepseek:$DSH_SESSION_ID
  koinon library read skill.pickup --as deepseek:$DSH_SESSION_ID
  koinon room list --as deepseek:$DSH_SESSION_ID
  koinon room members --as deepseek:$DSH_SESSION_ID ROOM_ID
  koinon room read --as deepseek:$DSH_SESSION_ID ROOM_ID
  koinon room post --as deepseek:$DSH_SESSION_ID ROOM_ID 'message' --key KEY --deadline UNIX_SECONDS
  koinon decision list --as deepseek:$DSH_SESSION_ID --filter waiting
  koinon decision get --as deepseek:$DSH_SESSION_ID --reference REF
  koinon decision create --as deepseek:$DSH_SESSION_ID --reference REF --title 'title' --kind decision --key KEY --deadline UNIX_SECONDS
  koinon decision edit --as deepseek:$DSH_SESSION_ID --reference REF --if-revision REVISION --title 'title'
  koinon decision history --as deepseek:$DSH_SESSION_ID --reference REF
  koinon decision pending --as deepseek:$DSH_SESSION_ID
  koinon decision ack --as deepseek:$DSH_SESSION_ID --verdict VERDICT_ID
  koinon decision close --as deepseek:$DSH_SESSION_ID --reference REF --if-revision REVISION --key KEY --deadline UNIX_SECONDS
  koinon decision restore --as deepseek:$DSH_SESSION_ID --reference REF
  koinon peer-status --as deepseek:$DSH_SESSION_ID NAME
  koinon send --as deepseek:$DSH_SESSION_ID NAME 'message'
  koinon inbox --as deepseek:$DSH_SESSION_ID
  koinon ack --as deepseek:$DSH_SESSION_ID SEQ
  koinon cycle-status --as deepseek:$DSH_SESSION_ID
  koinon cycle-ready --as deepseek:$DSH_SESSION_ID CYCLE_ID REVISION HANDOFF_FILE
  koinon cycle-pickup --as deepseek:$DSH_SESSION_ID CYCLE_ID REVISION HANDOFF_FILE resumed|paused 'report'
Register uses DSH_WEB_URL and DSH_HOME for the wake target; repeat it while using the session
to renew its 15-minute lease, and add --repository PATH when selecting a repository.
### Recommended permissions
Approve the commands above and koinon guide --agent deepseek in this session's command
approval flow. The live wake was proven with DeepSeek Harness 0.2.0-rc.2 (#199).
See https://github.com/rwcii/koinon/blob/develop/docs/PERMISSIONS.md for the tools and
commands, paths/connections and optional terminal operations.

`,
}

// Guide writes one family's guidance.
func Guide(family string, out io.Writer) error {
	text, found := guideFamily[family]
	if !found {
		return errors.New("unknown agent family")
	}
	_, err := io.WriteString(out, guideCommon+text)
	return err
}

// AgyStop offers an unacknowledged notice at the native turn boundary. A missing
// or unavailable daemon lets the agent stop; no peer body enters hook output.
func AgyStop(in io.Reader, out io.Writer, env HookEnv) error {
	data, err := io.ReadAll(io.LimitReader(in, hookInputMax+1))
	if err != nil {
		return err
	}
	if o, ok := agyStopObservation(data, env.Now().UnixMilli()); ok && len(data) <= hookInputMax {
		env.report(o)
		result, e := env.call("/v1/wake/agy-stop", map[string]any{"caller": o.Caller})
		var reply struct {
			OK     bool   `json:"ok"`
			Notice string `json:"notice"`
		}
		if e == nil && json.Unmarshal(result, &reply) == nil && reply.OK && reply.Notice != "" {
			return json.NewEncoder(out).Encode(map[string]string{"decision": "continue", "reason": reply.Notice})
		}
	}
	_, err = io.WriteString(out, "{}\n")
	return err
}
