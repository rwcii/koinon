# Sprint 2026-09-24 — stable alias — Definition of done

The sprint is done when every acceptance criterion in `decision.md` holds on `develop`, the tests
below pass on Linux and macOS, the live checks are recorded on #141, and #141 is closed with
that evidence.

## Unit and integration tests

All tests use synthetic peers, synthetic Codex host processes, temporary `CLAUDE_CONFIG_DIR`,
`CODEX_HOME` and state roots, and a private tmux server (`tmux -L <temporary socket>`, or
`-S` with a temporary path). No test reads or writes the user's registry, sessions, terminals
or services.

- **Host process (criterion 4).** `ensure` run from a synthetic process tree records the nearest
  ancestor that `platform_support.codex_process()` accepts, as pid and kernel start time. No
  matching ancestor records `unknown` with a reason; `ensure` still succeeds. A recorded host
  whose pid was reused (other start time) reports as not live.
- **Alias allocation (criteria 1, 2).** The alias is the per-thread name without its two-hex
  suffix (`codex-<label>`). A Codex `ensure` with no live holder takes the alias. A second
  Codex thread of the same repository, with a live holder, gets only its per-thread name and a
  report that names the holder. Two concurrent `ensure` runs (two processes, one barrier) leave
  exactly one holder. A per-thread name never equals any alias, and an alias never equals a
  live per-thread name: a repository label that ends in `-<two hex>` is a test case. DeepSeek and
  Claude registrations get no alias.
- **Registry publication (criteria 1, 2).** The holder's notifier publishes the alias as the
  registry `name` and its per-thread name in a separate field; a non-holder publishes its
  per-thread name. `bridge.py peers` and `bridge.py send` to the alias reach the holder. At no
  sampled point during a move (poll the registry while the move runs) do two live records carry
  the alias. A dead holder's Koinon record (dead pid or other start time, Koinon entrypoint,
  same owner) is removed before the alias moves; a record of another entrypoint, another owner
  or a live process is never changed.
- **No holder (criterion 2).** With the holder stopped and no successor, `bridge.py send` to
  the alias fails with a typed error that names the alias and says no live registration holds
  it.
- **Rebind (criteria 3, 4, 5).** Each case with synthetic registrations and recorded terminals:
  - same tmux server and pane, other session ID: the successor takes the alias, the
    predecessor stops, the unread count of its inbox is reported;
  - the same after an in-process resume (the older thread rebinds from the newer one);
  - another pane, another tmux server, no recorded terminal on either side, or a matching host
    process alone: refused, reported, nothing stopped, the alias unchanged;
  - explicit user authorization that names the predecessor: rebind without the pane match;
  - another family or another repository as predecessor: refused.
- **No inheritance (criterion 5).** After a rebind, a recursive digest of the predecessor's state
  directory (inbox, checkpoint, claims, `session.json`) equals the digest before it, except the
  files that its own stop writes; the successor's inbox and checkpoint are its own. A later
  `ensure` for the predecessor's thread registers it again with its old inbox.
- **tmux naming (criterion 6).** On a private tmux server:
  - one agent pane in the session: the session is renamed to the alias by session ID, and the
    name reads back;
  - two agent panes in the session: the session name is unchanged and only the own pane title
    is set;
  - another session already has the target name: nothing is renamed, the conflict is
    reported;
  - no `TMUX` or `TMUX_PANE`, or no `tmux` executable: no-op, reported as `not_in_tmux` or
    `tmux_unavailable`;
  - `TMUX_PANE` names a pane that does not contain the recorded host process: nothing is
    renamed, reported.
- **Reporting (criterion 7).** `ensure`, `status`, `peers` and `guide` report the alias, whether
  this registration holds it, the recorded host process and the tmux result, each marked
  observed, unavailable or unknown.
- **Upgrade.** An installation from the current `main` with Codex registrations that have no
  recorded host, terminal or alias state upgrades with `scripts/upgrade.py`; after the upgrade
  each registration reports `unknown` for the missing records, the first holder takes the
  alias on its next `ensure`, and the upgrade preserves the new state files of chunks 01–03.
- **Documentation and skills (criterion 8).** `tests/test_skills.py` and
  `tests/test_participant_guidance.py` fail when `pickup` or `peer-tmux` hold a `session.py`
  recipe other than a pointer to `guide`, or when the `reconnect` topic lacks the `rebind`
  recipe for Codex.

## Integration points exercised for real

- The `native-session-lifecycle` and `native-upgrade` workflows on systemd (Linux) and launchd
  (macOS) run `ensure`, the alias take, and a rebind with a synthetic Codex host.
- `tests.yml` installs tmux on both runners, so the tmux tests run on Linux and macOS and are
  never skipped in CI. Locally, a missing tmux skips them with a named reason.

## Live checks, authorized by the user

Recorded on #141 without thread IDs:

1. After chunk 02 is installed: a Claude session lists the Codex participant under its alias
   and a one-line message sent to the alias arrives in that Codex thread.
2. After chunk 03 is installed: a Codex `/clear`, then pickup; the successor holds the alias,
   the predecessor is stopped, and a Claude session's send to the alias reaches the successor.
   Then `/resume` back and pickup; the alias returns.
3. After chunk 04 is installed: the tmux session name equals the alias after each step of
   check 2.

## Edge cases that must have tests

- A holder whose notifier crashed and left its registry record.
- A registry directory that is missing or holds a non-Koinon record with the alias as name:
  the alias take refuses and reports; it never overwrites that record.
- Two tmux sessions, one of them already named the alias.
- The predecessor's service is already stopped at rebind: the alias moves, the stop reports
  `already_stopped`.
- A rebind interrupted between the stop and the alias move: the next `ensure` or rebind
  completes it, and the alias has at most one holder at every point.

## Gate

For each chunk: the `check` skill (`python3 tests/run.py -v`, `git diff --check`, the changelog
rule, the private-content scan), and green `tests.yml`, `native-session-lifecycle.yml` and
`native-upgrade.yml` on the head commit.
