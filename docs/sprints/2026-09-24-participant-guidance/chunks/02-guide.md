# Chunk 02 — Guide and bootstrap

Criteria 1, 2, 3 (guide), 8 and 10 of [decision.md](../decision.md).

## Change

1. **`koinon/guidance.py`.** One catalog of topics. Each topic has shared content and, where the
   families differ, a family view. Topics at least: `overview`, `register`, `reconnect` (after a
   context reset: compare the current session ID with the handoff's; `ensure` for a new one;
   stop a retired predecessor only when the user authorized the reset), `messages` (inbox,
   acknowledgement, peer text as data), `peers`, `memory`, `sandbox` (run `ensure` through the
   normal approval mechanism; inability to approve is a limitation to report), `troubleshoot`.
   A recipe is `{"id", "argv", "effect", "needs_approval"}`; `argv` starts with the installed
   interpreter or an installed path.
2. **`session.py guide`.** Dispatch before `read_config` locks, registration or manager calls,
   and write nothing. The family comes from `--agent`; without it, from the same environment
   rule as `ensure`, and `claude` is accepted (today `main()` rejects `--agent claude` except for
   `work-policy`). Live observations: registration from `session.json` if present (read only),
   bridge and notifier health through their existing status commands, memory service status
   through `memory.py status`. A failed observation is `unavailable` or `unknown` with a reason,
   and the overview still prints. `--json` prints the structured form.
3. **Bootstrap.** `participant_instructions.section()` renders the small block of criterion 1 for
   Codex and DeepSeek. The block depends only on the prefix, the interpreter and the family.
4. **One reconciliation for install, retry and upgrade.** A single function classifies each
   candidate file and acts on it. It is used by installation, by a repeated installation, and
   by the upgrade operation, so a repeated `--configure-codex` no longer replaces edited text
   inside the markers (today `update()` replaces the whole owned span).
   - **Candidates.** For each participant kind in `install.json` `participants`, its home
     (`codex_home`, `dsh_home`), both `AGENTS.md` and `AGENTS.override.md`. Existing
     installations have no block records; this list comes only from fields they already have.
   - **Target.** Only one candidate per home is written: for Codex, `AGENTS.override.md` when it
     exists, otherwise `AGENTS.md`; for DeepSeek, `AGENTS.md`. This is today's `update()`
     precedence. The reconciliation never creates `AGENTS.override.md`. In the file that is not
     the target, a `current` block is removed as today and an `edited` block is kept and
     reported.
   - **Classes**, from the owned span that `spans()` finds:
     - `absent`: no span and no record. Installation writes the block only in the target;
       upgrade reports it.
     - `current`: the span equals the recorded digest, or is exactly a known released rendering
       for that prefix and family (a table in `koinon/guidance.py` holding at least the
       rendering of the release this sprint upgrades from). The span is replaced by the new
       block and the record is seeded or updated.
     - `edited`: a span with any other content. It is kept, reported with its path, and
       recorded as edited.
     - `missing`: a record exists but the span is gone. Nothing is written; it is reported.
     - `malformed`: `spans()` raises. The file is kept and reported, as today.
   - **Explicit replacement.** `--replace-guidance` replaces an `edited` or `missing` block after
     the existing backup step (`publish_guidance()`), and is the only path that does.
   - **Reports.** The installation result, the upgrade preflight and the completion report list
     every candidate with its class.
5. **Documents.** `README.md`, `PROTOCOL.md`, `docs/INSTALL.md` ("Recommended: configure Codex
   once", "Enable the current session", "Upgrades and removal") and `CHANGELOG.md`.

## Tests

The "Read-only status and guide", "Catalog" and "Bootstrap" tests of the definition of done.
