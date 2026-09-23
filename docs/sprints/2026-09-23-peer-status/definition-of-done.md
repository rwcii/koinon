# Sprint 2026-09-23 — peer status — Definition of done

The sprint is done when every acceptance criterion in `decision.md` holds on `develop`, the
tests below pass on Linux and macOS, the live checks are recorded, and #84 and #119 are
closed with that evidence.

## Unit and integration tests

All tests use synthetic peers, synthetic source files, a temporary `CLAUDE_CONFIG_DIR` (which
also holds the status records), a temporary `CODEX_HOME` and a temporary account namespace. None reads or writes the user's
Claude settings, Codex home, registry or services.

- **Presence records.** Writing and reading a record in the status directory:
  owner-only modes, refusal of a record owned by another user, a symlink, an oversized file or
  a malformed record; a record that names a dead or recycled process is ignored.
- **Allowlist (criterion 9).** Every source reader receives input that carries text in every
  field that can hold text (Claude status-line fields such as `workspace`, `session_name`,
  `pr`, `worktree`; Codex events with `last_agent_message`, prompts and tool output). A test
  proves that no such text appears in any stored record or in `peers` or `status` output. The
  work title and checkpoint are the only free text that appears (criterion 7).
- **Claude context (criteria 2, 6).** Synthetic status-line input produces the model ID,
  limit, tokens used and fill level with source `claude_statusline` and both times; a missing
  or changed integration gives `unknown` with `statusline_missing` and the repair command; a
  record of an ended Claude process gives `unknown`.
- **Status-line wrapper (criterion 3).** With a user command that reads its whole input and
  prints one line: identical input and output and exit status with and without the wrapper;
  the user's command fails (nonzero exit, no output) and the wrapper returns the same result;
  Koinon's part fails (unwritable presence directory, malformed input) and the user's command
  still receives the input unchanged and its output is returned; the input is read once; an
  input larger than 1 MiB reaches the user's command unchanged and is not parsed. No user
  command configured: the wrapper prints nothing and exits 0.
- **Settings edit (criterion 3).** Set up on a settings file with and without an existing
  `statusLine`, with other keys and other `statusLine` fields present; repeat set-up does not
  nest the wrapper or replace the saved original; decline is recorded and survives a repeated
  installation and an upgrade; removal restores the saved original only while the entry is
  still the wrapper; a changed entry is kept and reported with its removal action; a file that
  changes between read and replacement is a reported conflict and keeps the user's content;
  malformed JSON is refused without a write.
- **Upgrade (criterion 3).** An upgrade of an installation without the integration sets it up
  and reports the edit in its preflight, evidence and completion report; an upgrade with a
  saved decline changes nothing and reports the decline; an interrupted upgrade resumed after
  the settings step does not repeat it; a conflict is reported and the upgrade still completes
  the runtime replacement.
- **Codex activity (criterion 4).** Synthetic Codex logs: a started turn with a live
  participant process is `busy`; a completed or aborted latest turn is `idle`; a started turn
  whose participant process has ended is `unknown`; no associated participant process is
  `unknown`; turn IDs that do not match are not paired.
- **Codex context (criterion 5).** `token_count` with `last_token_usage` and
  `model_context_window` gives the fill from the last request, not the cumulative total; a log
  with no `token_count`, an unknown record shape or an unreadable file is `unknown` with a
  typed reason.
- **Participant association.** The owner of a synthetic log is the one process that holds it
  open; no holder and two holders are `participant_not_associated`; a recycled PID with a
  different start marker is not live; an owner that closes the log while it stays alive loses
  the association at the next observation. Runs on Linux and macOS.
- **Claimed work (criterion 7).** Against a synthetic memory store: an active claim held by
  the peer's session key appears with work ID, title and checkpoint; a successful query with
  no claim reports no claimed work; an expired lease is not shown and the work item is
  unchanged; no association and an unavailable service are `unknown` with distinct reasons;
  a claim held under a custom key declared with `session.py work-key` appears, and a claim
  under the native key does not appear for a peer that declared a custom key; a peer whose
  record names another installation's state root is queried in that store.
- **Freshness (criterion 6).** A cached observation older than 15 seconds or across a
  disconnect is not reused; a fresh read of an old context value of a live participant keeps
  the value with its source time.
- **Both families read (criterion 8).** `bridge.py peers` run with a Codex participant's
  environment returns the same fields for a synthetic Claude peer and a synthetic Codex peer
  as it does with a Claude environment.

## Integration points exercised for real

- `platform_support` open-file-holder and process-start functions on Linux and macOS, in the
  `Tests` matrix.
- The native upgrade workflow's memory, session and combined cases on systemd and launchd run
  with the settings step against a temporary `CLAUDE_CONFIG_DIR`.
- **Live checks, each with the user's authorization and recorded in the pull request that
  closes the chunk:**
  - After chunk 04 merges and the user's installation is upgraded: this Claude session's
    model, limit and fill appear in `bridge.py peers`, and the user's own status line looks
    unchanged.
  - After chunk 05 merges and the Codex session is restarted on the new runtime: the Codex
    peer shows `busy` during a turn, and its context values match the session log. After the
    turn, the check records whether the idle, loaded thread keeps its log open and so shows
    `idle`, or shows `unknown`; either result is correct under criterion 4, and the record
    names which one holds.
  - After chunk 06 merges: a work item started by an agent appears under that agent's `work`.

## Edge cases that must have tests

- A Claude peer and a Codex peer in the same repository, and in different repositories.
- Two Claude sessions of one user at the same time; each maps to its own record by session ID.
- `CLAUDE_CONFIG_DIR` set to a non-default directory: the settings edit, the wrapper and the
  registry reader use the same directory.
- The status-line input arrives before the first API response (`current_usage` null,
  percentages null): context is `unknown` with a reason, not zero.
- A DeepSeek peer: every new field is `unknown` with a reason.

## Measurement

The pull request of chunk 02 records the time the wrapper adds per update: 50 runs each on
Linux and macOS, with a user command that prints one line. The median added time must be at
most 100 ms. This is a recorded measurement, not a timed test in CI.

## Gate

For each chunk: the `check` skill; `Tests` green on all six jobs of the pull request and again
on the push to `develop`; for chunks that change installation or upgrade (03, 04), the native
installation and upgrade workflows green on the pull request. Documentation that describes
`peers`, presence, installation and upgrade (`README.md`, `PROTOCOL.md`, `docs/DELIVERY.md`,
`docs/INSTALL.md`, `docs/NOTIFIER.md`, `docs/RUNTIME-UPGRADE-DESIGN.md`) changes in the chunk
that changes the behaviour.
