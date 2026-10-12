# Chunk 02 — adapter

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

A Grok Build session started with `koinon grok` in Grok's default (non-leader) mode registers
as family `grok`, is identified exactly on every Koinon tool call, and uses messaging, inbox,
delivery, memory and work claims like the other families. Setup, guide, permissions and
documentation cover it. Leader mode is refused with a stated reason.

## Specification

1. **Family.** Add `grok` to `launcher.Families`, setup's family set and the guide. The
   participant address follows the existing rule (`grok-LABEL[-ROLE]`).
2. **Launch.** `koinon grok [args]` creates the launch record as for the other families and
   starts `grok [args]`. When `args` hold none of `--session-id`, `--resume`, `-r`,
   `--continue` or `-c`, it adds `--session-id <UUID>` with a fresh UUID that it records in
   the launch (spike fact 3). It sets `KOINON_LAUNCH_ID` in Grok's environment; the stdio MCP server
   inherits the host's environment (spike fact 1: `TMUX`, `TMUX_PANE` and `PWD` reached the
   probe). `koinon setup grok` adds the user-scope `[mcp_servers.koinon]` entry for
   `<prefix>/bin/koinon mcp`, with no per-launch values. Nothing reaches the server through
   model-visible arguments.
3. **Client and identity.** `initialize` with `clientInfo.name` `grok-shell-koinon` marks the
   client as Grok (spike fact 1). The caller key is `{grok, GROK_SESSION_ID}` from the server
   process environment (spike fact 2). It is trusted only when the server's parent is a `grok`
   process that the launch's ancestors and host record prove. A missing or empty
   `GROK_SESSION_ID`, or a parent that is not the launched host, is `identity_unavailable`.
   `callerFields` in arguments stay refused.
4. **Leader mode.** When the server's parent is a `grok agent leader` process (spike fact 1),
   registration is refused with `grok_leader_mode`: its ancestry, directory and pane do not
   identify the session. The guide and `docs/INSTALL.md` say that Koinon supports Grok's
   default mode and that `[cli] use_leader` must stay off for Koinon sessions.
5. **First session.** When the launch recorded a session ID, its first registration must
   present that ID; another ID under the same launch before that is `identity_unavailable`.
6. **Reset.** `/clear` starts a new session with a new `GROK_SESSION_ID` under the same host
   and pane and leaves the old MCP server running (spike fact 4). The new session registers
   through the same launch and host; the existing same-host succession rules move the
   participant (`same_host` after the guard). The former holder is retired and fenced, so the
   old server's renewals are refused and stay dropped (#283 chunk 03). No `clear` flag or
   Grok hook is added.
7. **Resume.** `koinon grok --resume <id>` passes the flag through; the resumed session keeps
   its original ID under a new host (spike fact 4) and registers through the new launch.
8. **Subagents.** A subagent's call arrives at the server with the parent's ID (spike fact 5),
   so the server cannot attribute it. `koinon setup grok` adds one user-scope
   `[[hooks.PreToolUse]]` entry, matcher `koinon__.*`, running `<prefix>/bin/koinon hook
   grok-pre-tool-use`. The hook reads the event and returns `deny` ("a Grok subagent cannot
   call Koinon") when `subagentType` is present; otherwise it prints nothing and exits 0. It
   writes nothing and is time-bounded. A subagent therefore never acts as its parent on
   Koinon. **Residual:** Grok hooks fail open, so if the hook crashes or times out, that one
   subagent call reaches Koinon as the parent's call. The guide and PROTOCOL state this
   limit. Setup keeps all other hook content and is idempotent; removal takes out only its
   entry.
9. **Permissions.** `koinon setup grok` documents, and does not write, the allow rule
   `MCPTool(koinon__*)` (spike fact 7). `docs/PERMISSIONS.md` adds the Grok row.
10. **Version.** Setup records the Grok Build version it saw; a version other than 1.0.50 is
    reported, not refused.
11. **Docs.** PROTOCOL (identity and admission), USAGE, INSTALL, PERMISSIONS, guide text and
    CHANGELOG.

## Tests

Synthetic only (`definition-of-done.md`): a fake `grok` host process and a probe client with
`clientInfo.name = "grok-shell-koinon"`. Cover items 3–8: two concurrent sessions; missing or
foreign `GROK_SESSION_ID`; a leader parent; a first session with another ID; `/clear` with the
old server still renewing; resume; caller fields in arguments; `not_launched` without a
launch; the `grok-pre-tool-use` hook denying an event with `subagentType` and staying silent
without it; setup adding, keeping and removing only its hook entry. The messaging end-to-end test runs one synthetic Grok session against a test daemon.

## Done

The tests pass on Linux and macOS CI, and `check` passes.
