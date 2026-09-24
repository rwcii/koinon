# Sprint 2026-09-24 — stable alias — Review

## Round 1 — Codex, plan commit 957bc28 (PR #146)

1. **P1 — alias not unique across repositories.** One state root and one `names.lock` serve
   every repository, so `/a/koinon` and `/b/koinon` both derived `codex-koinon`. A saved
   per-thread name (`codex-foo-ab`) could also equal a later alias (repository `foo-ab`).
   **Fixed.** Chunk 02 reserves the alias per repository key (Git common directory). A taken
   candidate or one that equals a saved or live name gets the four-hex repository suffix, which
   no per-thread name can match. The definition of done tests both cases.
2. **P1 — rebind lock and lease protocol.** Holding `names.lock` through a restart deadlocks
   the notifier start; releasing it let a third thread take the alias during a move; a crash
   after `held` left the alias unpublished. **Fixed.** Chunk 02 states the lock boundary (never
   held across a start, stop or wait), the one-holder rule, the transient states `moving` and
   `publishing` with a recorded `operation`, and the take refusal while an operation is live.
   Chunk 03 writes each step as one locked lease transition and gives recovery for each. The
   definition of done adds kill points after every step and a third thread held at barriers.
3. **P2 — no publication for a running registration.** A take on `ensure` did not change the
   registry name of a service that was already running. **Fixed.** Chunk 02 adds publication on
   a running service: a holder whose live record lacks the alias restarts under `publishing`.
   The definition of done tests a running pre-upgrade participant through alias delivery.
4. **P2 — host walk matches a transient child.** `platform_support.codex_process()` accepts a
   process whose parent names the CLI, so the walk could record Codex's shell. **Fixed.**
   Chunk 01 adds `platform_support.codex_host()`, which matches only the CLI process itself,
   and tests a shell child under an absolute-path CLI launch.

These fixes change the chunks and the definition of done, so the changed plan needs a second
review.
