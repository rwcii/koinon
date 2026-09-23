# Sprint 2026-09-23 · Chunk 06 — Claimed work

> References: `decision.md`, `definition-of-done.md`, `sprint.md`, chunk 01's record API,
> `docs/WORK-ITEMS-POLICY.md`. Not restated here.

## Scope

`peers` and the notifier `status` report each peer's active work claims.

## Approach

- Session key: the claim's `consumer` is the participant's native session identity, as the
  work guidance already says: `CODEX_THREAD_ID` for Codex, `DSH_SESSION_ID` for DeepSeek, and
  `CLAUDE_CODE_SESSION_ID` for Claude. `CLAUDE_CODE_SESSION_ID` equals the `sessionId` of the
  session's Claude registry record (verified on 2026-09-23 on this installation; the build
  confirms it on macOS). Update `koinon/work_guidance.py` to name the Claude variable.
- Store association: the repository is the peer's `cwd` (Claude registry) or `repo`
  (`session.json` for Koinon participants), resolved to its Git common directory with
  `memory.py`'s `repo_identity`; the state root is the running installation's `state_root`
  from `install.json`. No repository, no installation or no memory selection for it gives
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
