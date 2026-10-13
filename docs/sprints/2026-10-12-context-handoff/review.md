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

## Technical plan — frozen c99ef83

Claude reviewed S1–S6 at `c99ef83e2be90dccae1f307d9a05ba5ffbe524c1` against `52ab15a`.
Refusal review had no findings. New engineering values and compaction choices remain
Gate B proposals; settled per-session Enable and terminal input remain unchanged.

- F1, medium, accepted: serialize pane-typing wakes with clear/pickup, retain pending
  delivery and map session-changing dashboard actions to fenced terminal outcomes with
  Enable ended. D6/D11 now test these races. Source clarification: dashboard inbox clear
  acknowledges messages, not native context; it preserves the cycle/query and cannot
  report ready. The plan does not add a cancellation rule to that acknowledgement.
- F2, low, accepted: name the receiving model's reporting instruction. The installed
  guide's Managed cycle reporting section is reached by canonical pickup reconnect and
  the proven missing-skill bootstrap. D9 must show the model queried/reported its cycle.

Bounded review passed at `b874f57157a78b6113184b76401dda6caef0867d`: F1 and F2 closed,
peer and refusal verdicts passed. The [commit-bound evidence](https://github.com/rwcii/koinon/issues/196#issuecomment-6135869078)
records the source clarification for inbox clear and the native reporting proof.
Unchanged reviewed content carries forward. The later Gate B decision is recorded below.

## Maintainer sender decision and Gate B — 2026-10-13

F3 was reopened by the maintainer's sender question. The request had no specified sender.
Accepted: name reserved system identity overwatch, its own sender family/key, system origin,
cycle/Enable metadata, native-name reservation and guide meaning, with D3/D4 tests.
Sources are the maintainer's sender/reservation decisions (6136143253/6136161569).
Gate B approves the reviewed plan plus that addition
([record](https://github.com/rwcii/koinon/issues/196#issuecomment-6136155493)).
Future agentic capabilities are identity context, outside this deliverable. F3 awaits
bounded review; b874's general/fix review and d788's integration carry forward.
