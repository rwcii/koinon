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
4. **Install and upgrade.** Installation writes the block as today (`update()`, same files, same
   preservation and backup) and records in `install.json` the path and digest of what it wrote.
   The upgrade operation verifies each recorded block: a block equal to the recorded digest, or
   exactly the pre-sprint `section()` rendering for that prefix and family, is replaced by the new
   block; any other content is kept and reported in the preflight and the completion report as
   `edited`, `missing` or `conflict`. A missing block is not recreated.
5. **Documents.** `README.md`, `PROTOCOL.md`, `docs/INSTALL.md` ("Recommended: configure Codex
   once", "Enable the current session", "Upgrades and removal") and `CHANGELOG.md`.

## Tests

The "Read-only status and guide", "Catalog" and "Bootstrap" tests of the definition of done.
