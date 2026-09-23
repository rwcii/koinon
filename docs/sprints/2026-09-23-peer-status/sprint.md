# Sprint 2026-09-23 — peer status — Chunks

Realizes [decision.md](decision.md) (gate A approved at ba2816e) and is measured by
[definition-of-done.md](definition-of-done.md). Milestone `2026-09-23-peer-status`.

| Chunk | Outcome | Criteria | Depends on |
| --- | --- | --- | --- |
| [01 Status records](chunks/01-status-records.md) | The account-local status directory, the record format and allowlist, and the new `model`, `context` and `work` fields in `bridge.py peers` and the notifier `status`, all `unknown` with typed reasons until a source exists. | 1, 6, 8, 9 (framework), 11 (its documents) | — |
| [02 Claude status line](chunks/02-claude-statusline.md) | The `statusline.py` wrapper that records Claude model and context and runs the user's own command unchanged; `peers` reports Claude context. | 2, 3 (wrapper), 9 | 01 |
| [03 Claude settings](chunks/03-claude-settings.md) | Installation sets up the wrapper by default, with decline and removal options, saved original, conflict detection and uninstall. | 3 (settings), 11 (its documents) | 02 |
| [04 Upgrade](chunks/04-upgrade.md) | The upgrade operation sets up the wrapper with preflight, evidence and reporting, and keeps a saved decline. | 3 (upgrade), 11 (its documents) | 03 |
| [05 Codex activity and context](chunks/05-codex.md) | The notifier finds the process that owns its thread, reads the Codex session log and publishes activity, model and context. | 4, 5, 6, 9 | 01 |
| [06 Claimed work](chunks/06-claimed-work.md) | `peers` and `status` report each peer's active work claims through an explicit store and session-key association. | 7, 9 | 01 |
| [07 Process](chunks/07-process.md) | The `ship` and `sprint` skills tell the building agent to start one work item per deliverable issue. | 10 | 06 |

Chunks 02, 05 and 06 depend only on 01 and may be built at the same time; they change
disjoint source readers. Chunks 03 and 04 change installation and upgrade and run in order.
Chunk 05 closes #84. The last chunk to merge records the live checks of the definition of done
and closes #119.

## Shared names

These names are fixed here so that later chunks can be written against chunk 01 without
reading its code first.

- `koinon/participant_status.py`: the record format, the allowlist, the writer and the reader.
- `platform_support.participant_status_dir()`: the account-local directory, a sibling of
  `participant_lock_dir()` derived the same way from the account home (Linux
  `<account-home>/.local/state/koinon-status`, macOS
  `<account-home>/Library/Application Support/koinon-status`). Owner-only.
- Record files in that directory:
  - `claude-<session id>.json`, written by the status-line wrapper, keyed by the Claude
    `session_id` that the Claude registry record also carries as `sessionId`.
  - `bridge-<bridge pid>.json`, written by a Koinon notifier for its participant, valid only
    while the bridge PID, its process-start marker and the notifier generation match the
    registry record's `pid`, `procStart` and `bridgeOwner`.
- Output fields next to `presence` in each `peers` entry and in the notifier `status`:
  - `model`: `{state, source, id, recorded_at_ms, observed_at_ms, freshness_ms, reason}`.
  - `context`: `{state, source, limit_tokens, used_tokens, fill, recorded_at_ms,
    observed_at_ms, freshness_ms, reason}`; `fill` is `used_tokens / limit_tokens`.
  - `work`: `{state, source, claims: [{work_id, title, checkpoint}], recorded_at_ms,
    observed_at_ms, freshness_ms, reason}`. Work is queried live, so its `recorded_at_ms` is
    the query's `observed_at` from the memory service.
  - `state` is `observed` or `unknown`; `reason` is a typed code, `null` when observed.
    `recorded_at_ms` is the source's time for that value; `observed_at_ms` is Koinon's read
    time; `freshness_ms` is 15000, as for presence. `presence.model_activity` keeps its
    existing shape and gains Codex values in chunk 05.
- `statusline.py`: a new root entrypoint, because Claude settings name its path.
- `install.json` key `claude_statusline`: `{state: enabled | declined, settings_file,
  original, wrapper}`, where `original` is the saved previous `statusLine` value or `null`.
- Reason codes used across chunks: `no_status_record`, `status_record_invalid`,
  `participant_not_live`, `participant_not_associated`, `statusline_missing`,
  `source_unrecognized`, `no_token_usage`, `work_association_missing`,
  `memory_unavailable`, `provider_unsupported`.
