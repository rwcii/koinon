# Managed context handoff — framing review

## Frozen framing 7bc2442df1716188bee0143650a930c3975dbdef

Claude checked the decision against #196 and the direct per-session enable answer.
One scope ambiguity was accepted: whether Enable ends after a managed reset or follows
its verified successor in the same host and pane. The maintainer was asked that single
question before dependent specifications. No choice is inferred while it is pending.

Reset attribution also needs care. Existing participant live check F3 shows Claude's
transcript/session ID changing while its retained MCP environment ID, and thus the identity
seen by Koinon, stays continuous. Evidence inventory distinguishes those identities;
no new-session assertion is used as a universal reset proof.

This is a framing review, not approval of technical chunks or product implementation.
