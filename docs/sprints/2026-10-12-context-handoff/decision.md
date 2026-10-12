# Managed context handoff — decision

## Problem

Koinon already observes context use for some agent families, but it cannot coordinate a
bounded handoff and reset when a session approaches its context limit. The maintainer must
watch each terminal, wait for a completed handoff step and run the reset and pickup manually.
A reset performed before the handoff, against the wrong pane or without native pickup
context can lose the session's place or target a different session.

This deliverable implements [#196](https://github.com/rwcii/koinon/issues/196).
Codex drives the plan; Claude observes. The [reconciled scope](https://github.com/rwcii/koinon/issues/196#issuecomment-6123787212)
records the boundary. On 2026-10-12 the maintainer directly selected “Family defaults; enable
each session”: mode and threshold come from family defaults; each native session needs its
own Enable switch. This supersedes the earlier family-wide enable interpretation.
PDR 006, Decisions 1 and 2, records these settings and Enable carry-over; Decision 3 and
PDR 002, Decisions 2 and 4, describe terminal interaction. Gate A is approved
([maintainer decision](https://github.com/rwcii/koinon/issues/196#issuecomment-6116403721)).

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
   only, without installation. The dashboard shows that fallback. The
   handoff artifact is verified before reset; inability to read the catalog or prove the
   native pickup bootstrap makes that cycle unavailable or stops it before reset.
5. After valid readiness, fresh idle evidence and current exact-pane proof, Koinon types
   the family's context-management commands into the session's own pane: /clear then
   /pickup; Codex uses /clear then $pickup, with no appended task text. It reads back each
   step and handles permissions
   and alerts that appear during reset or pickup. Prompt responses follow the maintainer's
   existing authorization and task context; choices that exceed that scope are shown for
   the maintainer. Incoming peer text remains data and is not executed as a command.
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
10. Enable carries over only when the managed cycle verifies its successor in the same
    pane and participant (PDR 006, Decision 2). A failed or uncertain cycle, or a changed
    pane or participant, ends Enable; the maintainer enables again.

## Constraints

- Terminal input is an intended system function. This deliverable uses it for context
  commands and any startup/pickup prompts, with exact-pane verification and read-back.
- Existing native identity and same-user boundary (ADR 001, Decisions 1 and 2), unresolved
  tmux socket paths and content-free wakes (Decision 3), and dashboard checks (Decision 4)
  remain the integration boundaries.
- Existing handoff/pickup procedures remain the source of their behavior. A fallback reads
  the canonical embedded skills, rather than introducing a second prose implementation.
- #145's catalog/read interface is a dependency for the missing-skill fallback. #275's
  missing Codex clear-to-first-prompt proof and #199's missing DeepSeek live wake proof are
  not waived. A family can remain visibly unavailable until its required proof exists.
- Native startup hooks are evidence candidates, not verified reset or pickup behavior.
  The plan must demonstrate the missing-skill bootstrap and prompt handling before
  declaring a family with that fallback supported.
- Ordinary tests use synthetic sessions, temporary state and private tmux servers
  (WDR 006, Decision 2). Native receiving-model proof uses dedicated sessions; service-manager
  evidence uses the guarded disposable CI workflow (WDR 006, Decision 1).
- Actual native compaction configuration, skill installation, service changes and releases
  are outside this deliverable. Existing participant succession and default claim continuity
  remain as described by PDR 001, Decisions 4 and 6; the cycle adds no claim-transfer rule.
- Bounded state and audit admission must preserve existing control capacity. A promised
  handoff cycle cannot depend on an unfunded future state transition.
- Reviewed-plan approval precedes implementation. Each independently complete chunk uses
  the shared checks, native evidence where needed, commit-bound peer review and squash
  auto-merge to develop. Only one PR per target branch may be open.

## Framing status

Gate A and the configuration, Enable carry-over and terminal-input choices are settled.
Source freshness, ready correlation, native
reset/pickup proof, fallback bootstrap and crash reconciliation need a checked specification.
This branch reconciles framing and evidence; it does not implement or run the cycle.
