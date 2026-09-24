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

## Round 2 — Codex, plan commit 22260c6 (PR #146)

The four round-1 findings were confirmed resolved.

1. **P1 — a third thread could take a dead `moving` lease.** The rule tested the third key's
   own lifecycle, which is stopped before every `ensure`. **Fixed.** Chunk 02 keeps a dead
   transient lease reserved for its recorded successor; a third key takes it only when that
   successor is stopped and has no live record. Chunk 03's recovery and the definition of done
   (a stopped third registration at the kill point after step 2, with the successor running and
   then stopped) follow.
2. **P2 — a non-holder predecessor stopped without an alias move.** **Fixed** with an explicit
   outcome, no criterion change. The same-pane evidence still retires the predecessor
   (criterion 3). The alias moves only when free; a live holder in another terminal keeps it,
   because criterion 4 forbids that move, and the result reports `alias_held_by` and
   `predecessor_stopped`. The definition of done tests both cases for lifecycle and alias
   state.
3. **P2 — the four-hex fallback could collide.** **Fixed.** Chunk 02 probes suffix lengths 4,
   6, … 64 of the full SHA-256 digest of the Git common directory; distinct repositories end at
   distinct names. The definition of done forces a shared prefix with a patched digest.

Finding 1 changes the take rule, so the changed plan needs a third review.

## Round 3 — Codex, plan commit 16e0692 (PR #146)

Round-2 findings 1 and 3 were confirmed resolved.

1. **P2 — the non-holder branch did not meet criterion 3.** Criterion 3 and #141 item 3 said the
   successor takes the alias; the chunk left it with a live holder in another terminal. **Fixed
   by a user decision (2026-09-24).** Criterion 3 now says the alias moves when the predecessor
   holds it or when it is free; a live holder in another terminal keeps it, the predecessor
   still stops, and the result reports both. #141 records the decision. Chunk 03 already
   specifies this outcome.
