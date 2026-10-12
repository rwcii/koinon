# Managed context handoff — decision

## Problem

Koinon already observes context use for some agent families, but it cannot coordinate a
bounded handoff and reset when a session approaches its context limit. The maintainer must
watch each terminal, wait for a safe stopping point and run the reset and pickup manually.
A reset performed before the handoff, against the wrong pane or without native pickup
context can lose the session's place or target a different session.

This deliverable implements [#196](https://github.com/rwcii/koinon/issues/196).
Codex drives the plan; Claude observes. The [paired scope](https://github.com/rwcii/koinon/issues/196#issuecomment-6115978138)
records the boundary. On 2026-10-12 the maintainer directly selected “Family defaults; enable
each session”: mode and threshold come from family defaults; each native session needs its
own Enable switch. This supersedes the earlier family-wide enable interpretation.

## Acceptance criteria

1. The dashboard provides family defaults for context mode and threshold. Codex defaults
   to its native compaction; selecting Koinon's handoff cycle does not disable native
   settings by itself. Each session is disabled until the maintainer enables that session.
   Configuration changes require existing dashboard login and CSRF checks and are audited.
2. Only verified, fresh context observations from the actual session can trigger a cycle
   at the configured threshold. Unknown, stale or unsupported context produces a visible
   reason and no terminal input. A family without verified cycle interfaces cannot be enabled.
3. A threshold crossing requests that exact session's own handoff through Koinon. The agent
   can finish or pause its current step, writes its handoff and reports readiness. A peer
   cannot report readiness for another session, and a delayed reply cannot authorize a
   different cycle or a changed session.
4. Missing installed handoff or pickup skills can use the embedded library for this cycle
   only, without installation or pasted prose. The dashboard shows that fallback. The
   handoff artifact is verified before reset; inability to read the catalog or prove the
   native pickup bootstrap makes that cycle unavailable or stops it before reset.
5. After valid readiness, fresh idle evidence and current exact-pane proof, Koinon types
   only the family's bare commands into the session's own pane: /clear then /pickup;
   Codex uses /clear then $pickup. It reads the pane after each step and stops on anything
   unexpected. Peer text, user commands and skill bodies never become terminal input.
6. The target proof verifies the host in the exact native pane, including process ancestry
   with no intervening agent. A reused pane, ambiguous target, missing proof, busy session,
   changed host, timeout or cancelled enable stops the cycle with an explicit reason.
7. Native reset, current-session registration and receiving-model pickup are verified
   separately. Queue or transport acceptance, a low context value or an unchanged process
   name is insufficient. A family whose clear preserves its native ID needs verified reset
   epoch evidence, rather than an invented new identity.
8. Every transition, wait, failure and final outcome is visible in the dashboard and kept
   in the audit log. Daemon or agent restarts cannot silently repeat terminal commands or
   treat an uncertain reset as complete. A subsequent cycle requires a fresh handoff.
9. The cycle works on Linux and macOS for each family declared supported by the reviewed
   plan. Native compaction, ordinary wake delivery and existing dashboard actions continue
   to work. Family defaults do not enable present or future sessions automatically.

## Constraints

- The terminal-input exception is limited to this issue's ready/reset/pickup cycle; it
  does not authorize general input, commands from peers, or changes to agent permissions.
- Existing native identity, same-user boundary, unresolved tmux socket paths, loopback
  listeners, private state and content-free wakes remain mandatory.
- Existing handoff/pickup procedures remain the source of their behavior. A fallback reads
  the canonical embedded skills, rather than introducing a second prose implementation.
- #145's catalog/read interface is a dependency for the missing-skill fallback. #275's
  missing Codex clear-to-first-prompt proof and #199's missing DeepSeek live wake proof are
  not waived. A family can remain visibly unavailable until its required proof exists.
- Native startup hooks are evidence candidates, not verified reset or pickup behavior.
  The plan must prove the missing-skill bootstrap within the bare-command restriction
  before declaring any family with that fallback supported.
- Tests use synthetic sessions, temporary state, controlled native fixtures and private
  tmux servers. No live maintainer session is reset or configured during planning/testing.
- No actual native compaction configuration, skill installation, service change, release,
  arbitrary terminal control or automatic participant claim takeover is included.
- Bounded state and audit admission must preserve existing control capacity. A promised
  handoff cycle cannot depend on an unfunded future state transition.
- Reviewed-plan approval precedes implementation. Each independently complete chunk uses
  the shared checks, native evidence where needed, commit-bound peer review and squash
  auto-merge to develop. Only one PR per target branch may be open.

## Framing status

The per-session enable question is settled. Source freshness, ready correlation, native
reset/pickup proof, fallback bootstrap and crash reconciliation need a checked specification.
No family is declared cycle-capable by this framing document, and no actual reset is authorized
by an agent's message or by a saved work record.
