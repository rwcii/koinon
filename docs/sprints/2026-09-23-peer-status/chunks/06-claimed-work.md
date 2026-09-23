# Sprint 2026-09-23 · Chunk 06 — Claimed work

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API,
> `docs/WORK-ITEMS-POLICY.md`. Not restated here.

## Scope

`peers` and the notifier `status` report each peer's active work claims.

## Approach

- Session key: the work guidance lets a participant choose a stable consumer key. The
  association is explicit: `session.py work-key --key K` run in the participant's shell
  records `K` for that session in its status record; without it, the default is the native
  session identity (`CODEX_THREAD_ID`, `DSH_SESSION_ID`, or `CLAUDE_CODE_SESSION_ID`, which
  equals the `sessionId` of the Claude registry record: verified on 2026-09-23 on Linux; the
  build confirms it on macOS). Update `koinon/work_guidance.py` to name the Claude variable
  and the `work-key` command.
- Store association: each status record names the peer's repository and the state root of
  the installation that wrote it (the notifier's installation for a Koinon participant; the
  installation that set up the wrapper for a Claude peer). The listing queries that store,
  scoped to that peer, never silently the listing caller's own installation. The repository
  is resolved to its Git common directory with `memory.py`'s `repo_identity`. A record with
  no repository or state root, or no memory selection for it, gives
  `work_association_missing`; an unreachable service gives `memory_unavailable`.
- Query: `work list --owner <key>` for the active claims; `checkpoint` is not in its output, so
  add it to the list item (bounded, as the list output already is) rather than calling `work
  get` for each item. Show `work_id`, `title` and `checkpoint` of items whose lease is valid.
- The query runs in the process that answers `peers` or `status`; it is read-only and holds no
  claim.

## Documents

`docs/WORK-ITEMS-POLICY.md` (the Claude session key), `docs/DELIVERY.md`, `README.md`.

## Done-criteria (this chunk's slice)

- Criterion 7 holds, and criterion 9 for the work fields.
- The claimed-work tests of the definition of done pass.
