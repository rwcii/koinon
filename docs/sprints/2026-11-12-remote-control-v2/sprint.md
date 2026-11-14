# Remote control v2 delivery

See [decision](decision.md) and [definition of done](definition-of-done.md).
The following dependency increments are local review checkpoints on the assigned
branch. They may become ordered PRs only on further maintainer instruction; one
open PR policy applies. No publication or merge is part of this execution.

1. [Durable sessions, credential lifecycle and devices](chunks/01-durable-sessions.md).
2. [Reconnect, fresh CSRF and optional drafts](chunks/02-reconnect-drafts.md).
3. [Atomic receipts and explicit recovery](chunks/03-action-receipts.md).
4. [Attention, browser read cursors and diagnostics](chunks/04-attention-diagnostics.md).

The unchanged baseline race suite was started before implementation. Record its
outcomes and each final check in verification.md. Scope follows the supplied spec;
routine storage/UI decisions are engineering choices, not new maintainer mandates.
