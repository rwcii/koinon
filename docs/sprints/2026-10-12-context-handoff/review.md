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
each session. The terminal correction recorded in [PR #305](https://github.com/rwcii/koinon/pull/305)
quotes the maintainer: “this is not a fucking rule, you made this up.” and “items like this ...
serve to only cause agents to refuse legitimate work.” The
[#196 requirement](https://github.com/rwcii/koinon/issues/196#issuecomment-6117338153)
quotes: “it also needs to enter commands in the tmux session for context management handoffs.”
Intent: terminal commands and the cycle's prompts are intended functionality, rather than
an exception to the withdrawn agent-written statement. The plan retains exact-pane
verification and read-back.

The [reconciled scope](https://github.com/rwcii/koinon/issues/196#issuecomment-6123787212)
supersedes the earlier bare-only paired scope. Native commands remain bare, without appended
task text; prompt handling follows the actual pane read and existing authorization. The
active issue body was corrected as well. No general terminal-input prohibition is added.
The evidence inventory now records the shipped #303 observation fix separately from the
remaining native cycle proof. The full technical specification and Gate B remain pending.

Bounded reconciliation review at `5793020`: one accepted source-attribution finding in the
paragraph above. Fixed by linking the correction and requirement and quoting the actual
words with their intent. The rest of the reviewed reconciliation carries forward.
