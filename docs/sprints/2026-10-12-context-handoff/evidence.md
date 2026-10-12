# Managed context handoff — evidence inventory

This file records existing interfaces and gaps. It is not a specification, a support
claim or permission to reset a native session. Evidence is from develop 496ec52 and
installed guidance read on 2026-10-12.

## Context and activity

| Evidence | Current behavior | Planning implication |
| --- | --- | --- |
| `internal/core/observations.go`, `PROTOCOL.md` observations | Context sources are `claude_statusline` and `codex_rollout`. | Other families have no admitted context source today; unknown must not become zero. |
| Same files | Context/model confirmation freshness is 30 minutes; activity/terminal freshness is 2 minutes. | Registration active does not prove idle. Context source time and confirmation time are distinct. A cycle needs a checked freshness policy; existing display freshness alone does not establish reset readiness. |
| `internal/mcp/observe.go` | Polling runs every 5 seconds and confirms unchanged readable observations every minute. | A repeated observation is not necessarily a new threshold crossing or a new context epoch. |
| `internal/mcp/observe.go` Claude registry reader | Reads bounded, owned registry metadata tied to native session and parent process; busy/shell map to busy, idle/waiting remain distinct. | Native session attribution and fresh status need rechecking at reset; MCP activity alone does not prove idle. |
| Codex rollout reader in the same file | Context and activity come from the session's own rollout. | Native compaction and thread changes must be distinguished from a managed cycle; retained usage alone cannot prove a new context. |

## Terminal and reset evidence

- `internal/mcp/terminal_name.go` already verifies a launched host against an exact tmux
  pane's process ancestry, refusing an intervening native agent. It preserves the provider's
  unresolved socket path. Naming proof does not itself authorize typing or prove readiness.
- `agents/skills/peer-tmux/SKILL.md` defines the manual bare-command cycle and validates
  receipt after Enter instead of blindly sending another Enter. Its handoff artifact and
  pickup checks should be the behavior reference, rather than another copied procedure.
- The manual skill and `docs/sprints/2026-10-08-participants/live-checks.md` F3 record
  that Claude gets a new transcript/session ID while its existing MCP server retains
  its first native environment ID. Koinon therefore sees one continuous session. A
  changed Koinon identity cannot be a universal reset criterion; the native transcript
  epoch and the identity presented by the retained MCP process must be distinguished.
- Codex's process and pane survive `/clear`, while its thread changes. #275 still records
  the gap before the new visible thread's first native call: queueing against a previous
  thread is not proof of receiving-model delivery to the current one. The installed
  Codex version is 0.162.0; no new upstream behavior has been verified by this inventory.
- [Claude's SessionStart hook documentation](https://code.claude.com/docs/en/hooks#sessionstart)
  lists clear as a start source and supports additional startup context. This is a candidate
  bootstrap interface. It has not been tested here as a native clear-to-pickup proof and
  does not establish support for a missing native `/pickup` skill by itself.

## Embedded skills and bootstrap

The reviewed #145 proposal supplies canonical library reads in chunk 02, but that chunk
has not been approved or implemented. Existing catalog plan files live in
`docs/sprints/2026-10-11-skills-library/` on their own branch; they are not runtime behavior.

An agent can read a catalog skill before reset and still lose that context during reset.
An absent native pickup command cannot be assumed to load the catalog. Before declaring
this fallback supported, evidence must show how the successor receives its cycle-specific
pickup instruction and reads the canonical skill while terminal input remains only the bare
family commands. A startup hook, MCP initialization text or parser behavior needs a real
receiving-session proof, including reuse of an MCP process and same-ID Claude clears.

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
4. Native clear receipt and reset epoch, including Claude's retained native identity.
5. Pickup bootstrap when skills are absent, and receiving-model confirmation after reset.
6. Unknown external outcomes after a daemon crash, preventing blind command replay.
7. Compatibility with family defaults, explicit per-session enable and native compaction.
8. Linux/macOS native proofs for any family declared available. Missing #275/#199 proof
   stays explicit; synthetic transport tests cannot substitute for it.

No host service, skill installation, native configuration or terminal input was changed
while collecting this evidence.
