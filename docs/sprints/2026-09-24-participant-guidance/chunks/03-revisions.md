# Chunk 03 — Revisions and notices

Criteria 5, 6 and 10 of [decision.md](../decision.md).

## Change

1. **Revisions.** `runtime_revision` is recorded in `install.json` by installation and by the
   upgrade release step. `guidance_revision` is the SHA-256 of the catalog's canonical JSON
   (topics, views and recipe templates; no status values). Both are also constants the running
   code can report.
2. **Reporting.** `guide`, `ensure`, `status` and `peers` include `guide_revision` and
   `guide_stale` (the session's acknowledged revision differs or is absent). The session service
   reports the `runtime_revision` of its own code; `guide` and `status` compare it with
   `install.json` and report `mismatch` or `unknown` (an older service that reports none). An
   upgrade journal that is not complete is reported as `upgrade_incomplete`.
3. **`session.py guide-ack REVISION`.** Refuses a revision that is not the installed one; writes
   the acknowledgement file named in [sprint.md](../sprint.md) atomically, owner-only.
4. **Notices.** The Codex and DeepSeek notifier compares the installed guidance revision with the
   session's acknowledgement and the last revision it noticed (kept in notifier state). It queues
   one content-free notice, `guidance revision <rev>: run <guide argv>, then guide-ack <rev>`, for
   each new revision, through the existing notice path (`koinon/notification_notices.py`).
5. **Documents.** `docs/DELIVERY.md`, `docs/NOTIFIER.md`, `PROTOCOL.md`, `CHANGELOG.md`.

## Tests

The "Revisions" and "Acknowledgement and notices" tests of the definition of done.
