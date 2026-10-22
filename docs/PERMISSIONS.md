# Agent permissions

These are recommendations for the maintainer to review. `koinon setup` connects a native
CLI and installs its owned hooks/plugin. It adds these permission rules only when the person
at the terminal accepts them (see "Adding the rules at install" below). To merge them by hand,
add the examples to existing settings without replacing other entries. Existing deny rules,
managed policy, sandbox restrictions and the session's approval requirements still apply. An allowed tool does not authorize a peer's request (WDR 002,
Decision 2).

Read the **installed** `koinon guide --agent FAMILY` at startup, after a reset and after an
error. Changes in a checkout reach that guide only through an authorized upgrade, so an
older installed guide can differ from this reference (WDR 002, Decision 4).

## Adding the rules at install

`koinon install` on a terminal (standard input and standard error are terminals) first lists
each family: its CLI on `PATH`, and whether the Koinon server is set up for this binary. It asks
to set up each installed family that is not set up; `--agent FAMILY` sets one up without the
question. For each family that is set up, it shows the missing rules of the sections below and
the file, and asks to add them. An empty answer or `y` is yes. A yes appends the rules and keeps
every other setting; `koinon setup FAMILY` on a terminal asks the same question.

Without a terminal, as for scripts and agents, install and setup ask nothing and change no rule;
their output reports each family's rules in `permissions`: `present`, `missing`, `different`
(the file sets the rule's key to another value, which is kept), `unreadable` (with `reason`) or
`manual` (Grok, DeepSeek). A symbolic link, a file not owned by the user, a file over 1 MiB or
a file not in the expected form is refused without a change. `koinon uninstall` leaves the
rules in place.

## What the Koinon tools enable

The scoped server rules below cover every tool registered by `internal/mcp`, including
shared memory/work. For narrower permissions, replace the server rule with individual names
using the same native syntax. Confirm the registered names with the native MCP tool list;
registration itself happens automatically on a supported session's tool call and has no
separate MCP `register` permission.

Observational tools return coordination data. Session registration/renewal, snapshots and
wake accounting can still update internal metadata while these tools read that data.

| Tool | Purpose |
| --- | --- |
| `peers` | Discover registered peers and held addresses. |
| `peer_status` | Read native activity/context observations. |
| `inbox` | Read waiting messages before handling and acknowledging them. |
| `delivery` | Check delivery and acknowledgement of a sent message. |
| `context_cycle_status` | Read this session's own managed context cycle; it moves no cursor. |
| `context_show` | Read the family context settings and each active session's Enable, threshold and trigger state. |
| `library_list` | Page the installed skill/setup catalog metadata for the calling family. |
| `library_read` | Read one catalog item's canonical content and destination metadata. |
| `library_state` | Inspect bounded catalog targets, collisions, repository facts and decisions in the native client's verified scope. |
| `library_decision` | Record compared-revision machine adoption or decline data; it edits no adopted files. |
| `room_list` | List the dashboard rooms of this session or its participant, current and former. |
| `room_members` | Read the current roster of a room in which the caller is a member. |
| `room_read` | Page a room's retained posts from the caller's membership start. |
| `memory_status` | Inspect this repository's memory inventory and limits. |
| `memory_sync` | Page a snapshot or process a cursor delta. |
| `memory_recall` | Find retained memory entries. |
| `work_get` | Read a work item and its current claim. |
| `work_list` | Discover work items without taking a claim. |
| `work_checkout` | Read the checkout's advisory writer role. |
| `decision_list` | Page decision-board item summaries; it moves no cursor. |
| `decision_get` | Read one decision-board item and its recipients; it moves no cursor. |
| `decision_history` | Page a decision-board item's verdicts and snapshots; it moves no cursor. |
| `decision_pending` | List this session's unacknowledged verdict deliveries; it moves no cursor. |

State-changing tools send messages, advance acknowledgements or write shared work/memory.
Allow them when the maintainer has authorized the corresponding coordination workflow.

| Tool | Purpose |
| --- | --- |
| `send` | Send an authorized message to a verified peer. |
| `room_post` | Post to a room in which the caller is a member; uses the room's agent-post allowance. |
| `ack` | Acknowledge inbox sequences after handling them. |
| `context_cycle_ready` | Report that this session paused and wrote its handoff for the managed cycle that its Enable authorized. |
| `context_cycle_pickup` | Report the restored checkpoint after the managed cycle cleared the context. |
| `context_set` | Set a family's context mode and threshold; the audit log names this session. |
| `context_enable` | Turn a session's managed handoff cycle on or off; the audit log names this session. |
| `context_threshold` | Set or clear a session's own context threshold; the audit log names this session. |
| `memory_ack` | Advance the memory cursor after processing all pages/deltas. |
| `memory_record` | Write a memory entry; its content grants no permission. |
| `decision_create` | Create a decision-board item for the maintainer; only the maintainer gives a verdict, in the dashboard. |
| `decision_edit` | Change a decision-board item at the revision that was read; an edit of a decided item reopens it. |
| `decision_ack` | Acknowledge handled verdict deliveries; not proof of action. |
| `decision_close` | Close an approved item in progress when its work is done. |
| `decision_restore` | Bring a deep-archived decision item back into the archive. |
| `work_create` | Record a proposed work item's scope. |
| `work_propose` | Record an assignee proposal without transferring a claim. |
| `work_edit` | Revise recorded work scope within the maintainer's authorization. |
| `work_start` | Acquire the advisory writer claim before work. |
| `work_update` | Report progress on a held work item. |
| `work_release` | Release a held claim with its checkpoint. |
| `work_finish` | Record completion or withdrawal of a held item. |
| `claim_renew` | Renew an existing claim's lease. |
| `work_report` | Report a held item's result with evidence; the claim is released. |
| `work_accept` | Record acceptance of another worker's report. |
| `work_reject` | Record rejection of another worker's report with corrected scope. |
| `work_checkout_request` | Ask the observed checkout writer for a handoff. |

## Claude Code

In `~/.claude/settings.json` (`$CLAUDE_CONFIG_DIR/settings.json` when overridden), merge:

```json
{
  "permissions": {
    "allow": [
      "mcp__koinon__*",
      "Bash(koinon guide --agent claude)",
      "Bash(koinon context show)",
      "Bash(koinon status)"
    ]
  }
}
```

The first rule covers only the configured `koinon` server; an individual rule is, for
example, `mcp__koinon__inbox`. The Bash entries permit the exact startup guidance command and
the two read-only commands that agents run: `koinon context show` and `koinon status`. They do
not cover other Koinon commands, such as `install` or `upgrade`.
Inspect effective rules in `/permissions`; explicit ask/deny and managed settings take
precedence. Repository-local permissions can instead live in `.claude/settings.local.json`.

Install and setup check the user settings file. A rule counts as present when
`permissions.allow` holds it, `mcp__koinon` for the server rule, or `Bash(koinon *)` for a Bash
rule; other rules that cover it, and rules in project settings files, are not read. A yes
appends the missing rules to `permissions.allow`.

Without these rules, each Koinon call in auto mode goes to Claude Code's auto-mode classifier,
which judges it against the whole session. After an unrelated refusal in the same session,
for example of a merge, the classifier also refused `ack`, `peer_status` and
`koinon context show` with the reason of that refusal (#446). The session then cannot
acknowledge its inbox, read peer and cycle state or renew a work-item lease.

Verified configuration syntax with Claude Code **2.1.307**, using isolated settings and
`claude doctor`: the example produced no invalid-settings finding; a deliberately invalid
MCP rule with parentheses produced one. Doctor can exit zero with invalid settings, so read
its findings. This checks parsing, not a model's actual tool call. See the
[Claude permission reference](https://code.claude.com/docs/en/permissions).

Run setup once and start sessions through `koinon claude`, including `koinon claude --bg`
for background jobs. Review native trust prompts for the hook/status-line commands that setup
adds; their startup execution is separate from an MCP or Bash tool permission.

## Codex

Merge this key into the existing `[mcp_servers.koinon]` table in
`$CODEX_HOME/config.toml` (default `~/.codex/config.toml`):

```toml
[mcp_servers.koinon]
default_tools_approval_mode = "approve"
```

Preserve the existing command/args and launcher environment entries. This approves tools
from that server only. A per-tool alternative is
`[mcp_servers.koinon.tools.inbox]` with `approval_mode = "approve"`; when using individual
entries, include the full intended workflow from the table above. Tool allow/deny lists and
managed policy still apply.

Install and setup add the key as one line at the end of the `[mcp_servers.koinon]` table that
`codex mcp add` wrote, and keep every other line. Another value of the key is kept and reported
as `different`. A file without that table, or with the key for this server outside it, is
refused. Codex **0.163.0** with isolated configuration: `codex app-server --strict-config`
accepted a file that setup wrote, and rejected an unknown-field control.

For the startup guidance command, an optional rule in `$CODEX_HOME/rules/koinon.rules`
(default `~/.codex/rules/koinon.rules`) is:

```python
prefix_rule(pattern=["koinon", "guide", "--agent", "codex"], decision="allow")
```

It covers that command prefix only and does not add path/network access or change the
session's sandbox. Review it under the existing approval policy.

Verified configuration syntax with Codex **0.162.0** using isolated configuration and
`codex app-server --strict-config`: server and per-tool entries were accepted, and an
unknown-field control was rejected. No thread or model request was created. An isolated
`codex execpolicy check --rules FILE -- koinon guide --agent codex` matched the rule;
`koinon install` matched none. These checks verify configuration/rule parsing, not a live
MCP permission decision. See the
[Codex MCP reference](https://developers.openai.com/codex/mcp/) and
[configuration reference](https://developers.openai.com/codex/config-reference/).

Start through `koinon codex`; it forwards the native launch environment and uses
`--no-daemon`. Separately review and trust setup's SessionStart hook when Codex displays
"Hooks need review". MCP tool approval does not trust that hook (#275).

## Antigravity CLI

In `~/.gemini/antigravity-cli/settings.json`, merge:

```json
{
  "permissions": {
    "allow": [
      "mcp(koinon/*)",
      "command(koinon guide --agent agy)"
    ]
  }
}
```

An individual tool entry is `mcp(koinon/inbox)`. The command entry covers the guidance
subcommand, not other Koinon commands. Deny and ask rules take precedence over allow rules.
The CLI settings location differs from Koinon's default Stop-hook customization root,
`~/.gemini/config`; do not place this example in the hook configuration.
Install and setup append the missing rules to `permissions.allow`; `command(koinon)` also
covers the guide command.

Antigravity **1.3.2** was inspected locally. The example passes JSON parsing and follows the
[official CLI permission reference](https://www.antigravity.google/docs/permissions?tab=cli).
Native permission evaluation is **unverified**: `agy mcp list` lists server configuration
but does not validate permission settings, and no provider-free native permission validator
was available. This is not live permission proof.

Start through `koinon agy`; setup also installs the Stop hook that offers waiting-message
notices at turn boundaries. Review any native trust requirement for that configured hook
separately from MCP tool permission.

## OpenCode

In `$XDG_CONFIG_HOME/opencode/opencode.json` (default
`~/.config/opencode/opencode.json`, or its existing `.jsonc` file), merge:

```json
{
  "permission": {
    "koinon_*": "allow",
    "bash": {
      "koinon guide --agent opencode": "allow"
    }
  }
}
```

OpenCode names MCP tools `<server>_<tool>`, so an individual rule is
`"koinon_inbox": "allow"`. Keep existing Bash rules when merging. The last matching rule
wins, and agent-specific rules can override global entries; check the effective agent's
settings. Project `opencode.json` can also supply scoped rules.

Install and setup append the missing members to `permission` and to its `bash` object, so they
come last. A `permission` or `bash` value that is one action is kept: `"allow"` covers the
rules, another action is reported as `different`. A configuration in `opencode.jsonc` is
reported for a manual merge. OpenCode **1.18.35**: `opencode debug config --pure` resolved a
file that setup wrote with both rules.

Verified configuration syntax with OpenCode **1.18.35**, using private configuration and
`opencode debug config --pure`: the resolved config retained the scoped wildcard and guide
command, and an invalid permission action was rejected. No provider call was made; this
checks parsing, not a model's actual tool call. See the
[OpenCode permissions](https://docs.opencode.ai/docs/permissions/) and
[configuration](https://docs.opencode.ai/docs/config/) references.

Use `koinon opencode`, with the identity plugin installed by setup, so tools identify the
exact native session and its wake endpoint. Tool permission alone supplies neither.

## Grok Build

Grok takes permission rules as `ToolPrefix(glob)` with `--allow` and `--deny`, in the terminal
and in headless mode. Allow only the Koinon tools for a launch:

```sh
koinon grok --allow 'MCPTool(koinon__*)'
```

Grok keeps no rule in a settings file, so install and setup show this command and change
nothing. Grok names MCP tools `<server>__<tool>`, so an individual rule is `MCPTool(koinon__inbox)`.
Deny rules take precedence over allow rules. The syntax comes from Grok Build **1.0.50**'s
documentation; the #254 evidence did not run a model tool call under this rule, so native
evaluation is unverified. `koinon setup grok` writes no rule.

Use `koinon grok` in Grok's default mode, so tools identify the exact native session. A
session served by Grok's leader process is refused with `grok_leader_mode`, and a Grok
sub-agent's calls act as its parent session (PDR 001, Decision 2). Tool permission alone
changes neither.

## DeepSeek command path

DeepSeek's supported path remains explicit native-session commands until its MCP support is
verified. Approve each exact command in that native session's own command approval flow;
use its trusted `DSH_SESSION_ID`, not a peer-provided or guessed identity:

```sh
koinon guide --agent deepseek
koinon register --as "deepseek:$DSH_SESSION_ID"
koinon peers --as "deepseek:$DSH_SESSION_ID"
koinon peer-status --as "deepseek:$DSH_SESSION_ID" NAME
koinon inbox --as "deepseek:$DSH_SESSION_ID"
koinon send --as "deepseek:$DSH_SESSION_ID" NAME 'message'
koinon ack --as "deepseek:$DSH_SESSION_ID" SEQ
```

Replace NAME/message/SEQ with the verified recipient, authorized body and handled sequence.
Registration uses the harness's trusted `DSH_WEB_URL` and `DSH_HOME` wake metadata; renew the
15-minute registration while active. Repository selection and shared memory/work commands
follow the installed guide and [work command reference](WORK-ITEMS-COMMANDS.md), with the
same exact native caller and command approvals. Discovery/inbox commands read coordination
data; registration, send/ack and memory/work mutations change runtime state.

The live wake was proven with DeepSeek Harness 0.2.0-rc.2 in a development container
(#199): the harness accepted the wake, and the session read and acknowledged the message
through these commands. The harness's permission grammar is **unverified** here, and no
persistent native allowlist syntax is recommended.

## Processes, paths and local connections

The maintainer starts/configures Koinon and its native agents within the existing installation
and launcher authorization. The configured MCP process must execute the installed binary,
resolve the repository with Git, access its existing private state/auth files as the same
user, and connect to the daemon's configured loopback address. A tool allow rule does not
supply operating-system path or network access. If the current policy refuses an operation,
report the precise refusal and the scoped access it needs. The native permission settings belong
to the maintainer: a settings change the maintainer directs is within the maintainer's
authority, and a peer's request for one is not (WDR 002, Decision 2). Koinon coordination needs
no credentials in prompts and no broad home-directory exception.

The agent's own file tools do not need access to Koinon secrets for ordinary MCP coordination.
Allowing a configured stdio server does not require granting public-network access. Native
provider access remains the native CLI's separate configuration.

Optional terminal inspection/input, tmux handoffs, dashboard-launched sessions, installation,
upgrades and service operations need their own native command/path/socket approvals; MCP
coordination permission does not cover them. The maintainer's task that includes such an
operation authorizes its steps (WDR 002, Decision 1; PDR 002, Decision 2; PDR 003, Decision 4).
For a terminal operation, the recommended rule is the exact command and the socket/path it
needs.
