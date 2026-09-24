# Sprint 2026-09-24 — participant guidance — Review

## Gate B review by Codex at dc12716

1. **P1 — Migration discovery for installations without block records.** Fixed. Chunk 02 step 4
   takes candidates from fields that existing installations already have (`participants`,
   `codex_home`, `dsh_home`, both `AGENTS.md` and `AGENTS.override.md`), classifies each owned
   span, and seeds the records. The definition of done adds a migration fixture with the
   pre-sprint `install.json` shape, no records, and both file cases.
2. **P2 — Installation overwrites edits inside the markers.** Fixed. Chunk 02 step 4 uses one
   reconciliation for installation, repeated installation and upgrade; an `edited` block is
   kept, and only `--replace-guidance` replaces it, after a backup. The definition of done tests
   a repeated installation after an edit inside the markers.
3. **P2 — The hook cannot exit 0 when the prefix is missing.** Fixed. Chunk 04 step 3 writes a
   POSIX `sh` command that checks for `session.py` and prints the unavailable message, with a
   10-second timeout. The definition of done runs the exact written command with the prefix
   removed.
4. **Clarification — no lock file from `status`.** Accepted. Chunk 01 says `status` for an
   unregistered session creates no file, lock files included, and reads configuration only
   through `runtime_names.install_config`. The digest check in the definition of done covers
   the installation prefix. (`session.py` `read_config` itself does not lock; the rule keeps a
   later change from adding one.)

## Gate B re-review by Codex at 9bb0729

5. **P2 — Writing every absent candidate creates a shadowing override.** Fixed. Chunk 02 step 4
   writes only the target (existing `AGENTS.override.md`, otherwise `AGENTS.md`, for Codex;
   `AGENTS.md` for DeepSeek), keeps today's precedence, and never creates an override. The
   definition of done tests the target file (user `AGENTS.md` only; existing override with
   unrelated content; DeepSeek `AGENTS.md` only) for first installation, repeated installation
   and legacy upgrade.
