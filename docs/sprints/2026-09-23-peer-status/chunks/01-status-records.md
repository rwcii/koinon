# Sprint 2026-09-23 · Chunk 01 — Status records

> References: `decision.md`, `definition-of-done.md`, `sprint.md` (shared names). Not restated
> here.

## Scope

The account-local status directory, the record format with its allowlist, and the three new
fields in `bridge.py peers` and the notifier `status`. No source writes records yet, so every
new field is `unknown` with a typed reason after this chunk.

## Approach

- Add `participant_status_dir()` to `koinon/platform_support.py`, derived from
  `account_home()` like `participant_lock_dir()`. Create it owner-only (0700) on first write.
- `koinon/participant_status.py`:
  - `write(kind, key, fields)`: validates `fields` against the allowlist for `kind`
    (`claude`, `bridge`), adds `recorded_at_ms`, and publishes the file atomically, mode 0600,
    with `O_NOFOLLOW`. Unknown or text fields are dropped, never stored.
  - `read_claude(session_id)` and `read_bridge(pid, proc_start, generation)`: refuse a file
    that is not a regular file owned by the user, is a symlink, is larger than 16 KiB, or does
    not parse; return `unknown` with `no_status_record` or `status_record_invalid`.
  - `model_view`, `context_view`, `work_view`: build the output fields of `sprint.md`, with
    `observed_at_ms` set at read time and the 15-second `freshness_ms` of
    `participant_presence.FRESHNESS_MS`.
- `bridge.py peers` (`peers()` near bridge.py:65): for a Claude `cli` record, read
  `claude-<sessionId>.json`; for a Koinon record (`entrypoint` `codex-peer-bridge`), read
  `bridge-<pid>.json` and check `procStart` and `bridgeOwner`. A record whose process is not
  live gives `participant_not_live`. A DeepSeek participant gives `provider_unsupported`.
- Notifier `status` (koinon/notification_runtime.py near line 397): the same three fields for
  its own participant, from its own `bridge-<pid>.json`.
- Remove the notifier's `bridge-<pid>.json` at shutdown under the same `bridgeOwner` check that
  removes its registry record (notification_runtime.py near line 568).

## Documents

`PROTOCOL.md` "Delivery ledger and presence", `docs/DELIVERY.md` "Presence and priority",
`docs/NOTIFIER.md` (the `status` reply), `README.md` (the `peers` description): the new fields,
their two times, the freshness rule and the reason codes.

## Done-criteria (this chunk's slice)

- Criteria 1, 6, 8 and 9 hold for the framework: every new field exists, is `unknown` with a
  reason, and the allowlist test of the definition of done passes for the writer.
- The presence-record, freshness and both-families tests of the definition of done pass.
