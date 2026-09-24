# Sprint 2026-09-24 — participant guidance — Definition of done

The sprint is done when every acceptance criterion in `decision.md` holds on `develop`, the tests
below pass on Linux and macOS, the live checks are recorded, and #133 and #131 are closed with
that evidence.

## Unit and integration tests

All tests use synthetic peers, synthetic sessions, temporary `CLAUDE_CONFIG_DIR`, `CODEX_HOME`,
harness homes and state roots, and a temporary account namespace.

- **Read-only status and guide (criteria 3, 4).** On native and legacy selections: `status` for
  an unregistered session writes no file under the state root and reports `unregistered`;
  `ensure` then registers normally; native `ensure` and `status` print `name`, `state_dir` and
  `inbox_command`. `guide` for every family, with no registration, no bridge, no notifier and no
  memory service, exits 0, prints the complete overview, and leaves the installation prefix,
  the state root, the agent homes and the Claude configuration byte-identical (a recursive
  digest of names, modes and contents). The same digest check applies to `status` for an
  unregistered session, so no lock file is created either.
- **Catalog (criterion 2).** Every topic renders for every family; an unknown topic or family is
  a typed error; every recipe is an argument array whose first element is the installed
  interpreter or an installed path; no recipe is run by `guide`; each live observation carries
  `observed`, `unavailable` or `unknown`.
- **Bootstrap (criteria 1, 8).** The rendered block for a prefix and family is identical across
  two synthetic releases with different catalogs. Install into a file with unrelated content
  preserves it. A repeated installation changes nothing for a `current` block and keeps an
  `edited` block (edit the heading inside the markers, then install again). A `missing` block is
  reported and not recreated by installation or upgrade. `--replace-guidance` replaces an
  edited block after a backup. **Migration fixture:** an installation with the pre-sprint
  `install.json` shape (`participants`, `codex_home`, `dsh_home`, no block records) and the
  pre-sprint rendered block, once in `AGENTS.md` and once in `AGENTS.override.md`: the upgrade
  replaces each exact rendering, seeds the records, and reports an edited copy as `edited`.
  **No new override:** with only a user `AGENTS.md` and no `AGENTS.override.md`, a fresh and a
  repeated installation write the block into `AGENTS.md`, keep the user's text, and create no
  `AGENTS.override.md`.
- **Revisions (criterion 5).** The guidance revision changes when catalog content changes and not
  when status values change; `guide`, `ensure`, `status` and `peers` report the revision and
  `guide_stale`; a service that reports no runtime revision is `unknown`; an interrupted upgrade
  and a runtime/service mismatch are labelled.
- **Acknowledgement and notices (criterion 6).** `guide-ack` records the revision for that
  session only; a notifier queues one notice for a never-acknowledged revision and one for a
  changed revision, none on repeated polls or a notifier restart, and none after `guide-ack` of
  the current revision; the notice text holds no catalog content.
- **Claude integration (criterion 7).** With and without existing `CLAUDE.md` content and
  existing `SessionStart` hooks: set-up adds one block and one hook; a repeated installation or
  upgrade adds no duplicate; decline is recorded and survives upgrade; removal restores only
  Koinon's entries and keeps changed entries with a report; other hooks and settings stay
  byte-identical. The exact command string written to the settings file, run through `sh` with
  the prefix removed, exits 0 within its timeout and prints the unavailable message. Settings
  edits reuse the conflict detection of `koinon/claude_statusline.py`.
- **Skills (criterion 9).** A test fails when `handoff`, `pickup` or `peer-tmux` contain a
  `session.py` or `bridge.py` recipe other than a pointer to `guide`.

## Integration points exercised for real

- The native installation and upgrade workflows on systemd and launchd, with the bootstrap
  migration and the Claude hook against temporary agent homes and a temporary
  `CLAUDE_CONFIG_DIR`.
- **Live checks, each with the user's authorization and recorded in the pull request that
  closes the chunk:**
  - After chunk 01: a Codex context reset, then `ensure` from the new conversation through the
    approval mechanism, prints the new peer name; `status` before it writes nothing.
  - After chunk 03: an upgrade of the user's installation produces one guidance notice in the
    Codex session; `guide-ack` stops further notices.
  - After chunk 04: `/clear` in a Claude session loads the brief guide through the hook, and the
    user's status line and other hooks are unchanged.

## Edge cases that must have tests

- `CLAUDE_CONFIG_DIR` set to a non-default directory: the block, the hook and the registry use
  the same directory.
- Codex with an existing `AGENTS.override.md`.
- Two sessions of one family: acknowledgement and notices stay per session.
- An installation removed while a session runs `guide`: `guide` reports the missing prefix and
  exits without writing.

## Gate

For each chunk: the `check` skill; `Tests` green on all six jobs of the pull request and again on
the push to `develop`; for chunks that change installation or upgrade (02, 03, 04), the native
installation and upgrade workflows green on the pull request.
