# Managed context handoff — framing review

## Frozen framing 7bc2442df1716188bee0143650a930c3975dbdef

Claude checked the decision against #196 and the direct per-session enable answer.
One scope ambiguity was accepted: whether Enable ends after a managed reset or follows
its verified successor in the same host and pane. The maintainer was asked that single
question before dependent specifications. The maintainer approved Gate A and the recommended
carry-over on 2026-10-12 (#196 comment 6116403721; PDR 006, Decision 2). That question is settled.

Reset attribution also needs care. Existing participant live check F3 shows Claude's
transcript/session ID changing while its retained MCP environment ID, and thus the identity
seen by Koinon, stays continuous. Evidence inventory distinguishes those identities;
no new-session assertion is used as a universal reset proof.

This is a framing review, not approval of technical chunks or product implementation.

## Maintainer correction — 2026-10-12

The maintainer directly confirmed family mode/threshold defaults with a separate Enable for
each session. The maintainer also corrected the agent-authored “Nothing types into a
terminal” statement: it was not a maintainer requirement and caused refusals contrary to
intended system functions. Removed its exception framing. Reset/pickup terminal commands
and handling their prompts are intended work, with exact-pane verification and read-back.

The [reconciled scope](https://github.com/rwcii/koinon/issues/196#issuecomment-6123787212)
supersedes the earlier bare-only paired scope. Native commands remain bare, without appended
task text; prompt handling follows the actual pane read and existing authorization. The
active issue body was corrected as well. No general terminal-input prohibition is added.
The evidence inventory now records the shipped #303 observation fix separately from the
remaining native cycle proof. The full technical specification and Gate B remain pending.
