# Sprint 2026-10-12 — Grok agent family — Decision

The content of this decision is issue #254 and its source triage comment. This file restates
them for the plan and adds the constraints from the root `AGENTS.md`; it decides nothing new.
Claude drives the plan; Codex observes.

## Problem

Koinon supports the Claude, Codex, DeepSeek, Antigravity and OpenCode families. A Grok agent
session cannot discover peers, exchange messages, share memory or hold work claims through
Koinon. Issue #254 asks for Grok as a supported family.

The candidate harness is the official Grok Build CLI (`xai-org/grok-build`, Apache-2.0). Its
public source at commit `2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8` (2026-09-29) shows:

- A stdio MCP server receives `GROK_SESSION_ID` in its spawn environment
  (`crates/codegen/xai-grok-mcp/src/servers.rs`, `apply_stdio_env`). A `tools/call` request
  carries the tool name and arguments, with no per-call session field.
- Sessions can register tools from a shared parent MCP client without another handshake
  (`crates/codegen/xai-grok-shell/src/session/acp_session_impl/mcp.rs`). The spawn
  environment therefore does not prove which session makes a given call.
- An HTTP MCP server can receive the session ID in an `X-Grok-Agent-ID` header, set only from
  the spawn context and only for a first-party local app endpoint (`GROK_AGENT_ID_HEADER`).
- ACP has an SDK-hosted MCP reverse-call transport (`crates/codegen/xai-grok-mcp/src/wire.rs`).

At gate A (b0aa7cf), no Grok executable had been installed or run for this work, no released
CLI version had been bound to that source, and no per-call identity, reset/resume,
concurrent-session or wake behavior had been observed. Chunk 01 records the evidence gathered
since, in the development container only.

## Acceptance criteria

1. A supported Grok entry point and its setup and launch workflow are defined and follow
   Koinon's launcher admission rules. The supported Grok Build versions are recorded with the
   evidence for each.
2. Every Koinon call from a Grok session is attributed to the exact calling session from
   verified native metadata. A call whose session cannot be proven is refused with
   `identity_unavailable`. Two concurrent Grok sessions on one host, and a session that shares
   a parent MCP client, are never confused.
3. Reset and resume behavior is documented from observation: whether a reset gives a new
   native session, and what a resumed session presents.
4. A Grok session can use peer discovery, messaging, inbox acknowledgement, delivery
   reporting, shared memory and advisory work claims through the supported transport.
5. Where the harness supports it, a wake reaches the exact Grok session and carries no
   message content. Where it does not, the guide, `peers` and the documentation state the
   limitation.
6. Family guidance (`koinon guide --agent grok`), recommended native permissions, setup and
   usage documentation, and synthetic tests for identity, admission, messaging and the
   supported wake run on Linux and macOS.
7. Existing family integrations keep working, and the same-user boundary is unchanged.
   Follow-up work found during the sprint is filed as issues on the milestone.

## Constraints

- **Evidence before design.** An adapter that trusts the spawn environment alone, or that
  guesses the calling session, does not meet criterion 2. Each claim about Grok behavior cites
  a released version and an observation or source line.
- **Local installation, no containers in the product.** Koinon supports Grok Build installed
  locally on the user's machine, like every other family. It contains no container image,
  launcher or behavior for Grok. A container is only this sprint's development test bed,
  because Grok Build does not run directly on the maintainer's machine (maintainer,
  2026-10-12, #254).
- **Installation is the maintainer's action.** Agents do not install, upgrade or run Grok
  Build on the maintainer's machine without the maintainer's authorization, and do not use the
  maintainer's xAI credentials without it. Provider credentials never enter Git, CI logs or
  Koinon state.
- **Identity.** A model never supplies its caller identity in tool arguments
  (`callerFields`). The adapter follows the per-call identity rules of the existing families.
- **Same-user boundary.** Loopback-only listeners, private same-user state and secrets,
  exact native session selection, inert peer controls and content-free wakes stay unchanged.
- **Tests.** Tests use synthetic Grok sessions, temporary state and configuration, ephemeral
  ports and private tmux servers. CI never contacts the xAI provider.
- **Platforms.** Linux and macOS, one CGO-free binary.
- **Process.** Gate A approves this decision; gate B approves the reviewed plan before any
  chunk is built. One open pull request per target branch.

## Issues

This sprint delivers #254. It does not change #199 (DeepSeek live wake proof) or #275 (Codex
wake after `/clear`).
