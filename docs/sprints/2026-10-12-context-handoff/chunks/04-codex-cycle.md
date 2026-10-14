# Chunk 04 — Codex cycle adapter

Built after chunk 03 and #275's code (PR #359), from [the decision](../decision.md),
[DoD D6–D13 and the native matrix](../definition-of-done.md) and chunk 03's adapter contract.
The criteria below were agreed with the reviewer before the first code edit (C1–C13),
extended by two live findings (C14, C15) and corrected by the review of the first frozen head
(C2, C5, C15, C16). The results are in
[native-evidence.md](../native-evidence.md).

## Native facts (Linux, Codex CLI 0.162.0)

Each probe used a private daemon, a private tmux server and a temporary repository. Captures
stay private; the screens in `internal/core/testdata/codex-screens/cycle-*.ansi` have their
account data, paths and thread IDs replaced.

- P1 A new repository shows Codex's trust dialog, which saves the choice to the user's
  configuration; `-c 'projects={"<repo>"={trust_level="trusted"}}'` trusts it in memory only.
  A linked worktree of a real project is no test repository: its pickup reads the main
  checkout's real `_handoff/`.
- P2 A thread ID exists from launch, but no rollout, thread record or SessionStart hook
  appears before its first turn. `/status`, a local command without a model turn, shows
  `Session: <thread ID>` at any time.
- P3 `/clear` redraws the banner, clears the scrollback and starts a new `koinon mcp` child of
  the host; `/status` after it names the new thread before any turn. Typing `/clear` or
  `/status` opens the command popup with that command first, and one Enter runs it.
- P4 A skill runs by a `$pickup` mention; the menu row is `pickup  [Skill] <description>`,
  above a blank row and the hint `enter insert · esc close`. Escape closes the menu and keeps
  the text; Enter then submits. `/pickup` is no command, and Codex has no loader directory
  like Claude's `--add-dir`.
- P5 An MCP tool approval ("Allow the koinon MCP server to run tool …?", footer
  `enter to submit | esc to cancel`) and a command approval ("Would you like to run the
  following command?", footer `Press enter to confirm or esc to cancel`) mark the selected
  option with `›`, the composer's mark. One digit chooses an option without Enter.
- P6 Same-host succession refuses `holder_active` for the guard (30 s) after the holder's last
  tool call; in a cycle that call is `context_cycle_ready`, seconds before the pickup.
- P7 Codex lists skills from `.agents/skills` and `.codex/skills` of each directory from the
  start directory up to the Git root, nothing above it; skills with the same name are
  separate rows.
- P8 One file reached through two symlinked roots is one row.
- P9 No native view shows a skill's path before a turn. Codex turns a skill on or off only
  through `skills.config` entries, selected by name or by path.

## Criteria

- C1 Probe: `platform_unverified` off Linux (a `livecheck` build admits macOS);
  `native_registry_unavailable` without the session thread's own rollout `session_meta`
  (originator `codex-tui`); `native_version_unverified` unless its `cli_version` is
  evidenced (0.162.0); `pane_unproven` unless `codexForeground` proves the pane; then the
  pickup entry: every `pickup` skill under the P7 roots, `$CODEX_HOME/skills` and
  `$HOME/.agents/skills` must be byte-equal to the catalog item (`pickup_entry_differs`), at
  least one must exist (`pickup_entry_unverified`), and no configuration entry may turn one
  off (C5). The entry is `project`, or `personal` when any is personal. There is no loader
  and no interactive bootstrap: a missing entry is unavailable before the clear.
- C2 Inspect: a recognized dialog gives `native_dialog`. Otherwise it is idle only with
  `codexIdle` on the screen and the current thread's direct proof: before the clear, the
  requester's rollout tail shows its last turn ended; after it, the pane's `/status` card
  names the cycle's proven successor, which has no rollout and no submitted prompt after the
  card, or its own rollout shows its last turn ended. The old thread's idle never admits
  input after the clear.
- C3 Clear: `/clear`, read back, one Enter, read back the cleared screen (the receipt); in the
  same step `/status`, read back, one Enter. A failure in the `/status` part leaves the clear
  received and the reset unproven. Every key goes through the fence and the pane proof; typed
  text that is not submitted is erased and read back.
- C4 NewEpoch: no keys. It reads the pane with its history and returns the `Session:` thread of
  the `/status` card below the latest banner, with nothing of a turn before the card; a
  thread equal to the old epoch, or none, is no proof.
- C5 Pickup: the bare `$pickup`. The open menu must be complete and list exactly one `pickup`
  row per distinct verified file (P7, P8), so any other entry of that name is refused before
  Enter. The count binds only while every verified file is active, so Probe parses every
  configuration as TOML and takes the effective `skills.config` in Codex's order, lowest
  first: `/etc/codex/config.toml`, `$CODEX_HOME/config.toml`, the `.codex/config.toml` of each
  directory from the checkout root down, the host's own `-c`/`--config` arguments and
  `/etc/codex/managed_config.toml` (tables merge; a higher layer's array replaces a lower
  one), at the top level and in the active profile (all profiles when it is unknown), and the
  entries of `/etc/codex/requirements.toml`. An entry whose `enabled` is not `true` and that
  names `pickup`, a verified file or its directory, or a path that does not resolve, or
  another value in place of a table on the path, gives `pickup_entry_unverified`; a layer that
  does not parse gives `pickup_entry_unreadable`. Entries that turn a skill on, name other
  skills, or are replaced by a higher layer pass.
- C6 Succession: inside the succession transaction, after the fenced, subagent, no_host and
  other_participant checks and before the holder guard, a candidate succeeds with
  `cycle_reset` only for an unfinished cycle of that address and holder, pinned to the
  candidate's host, start, socket and pane, whose proven successor epoch is the candidate's
  thread, in reset_verified, pickup_intent or waiting_pickup (or a dialog wait from one of
  them), under its admitting Enable and before its deadlines. Every other candidate keeps
  the ordinary rules.
- C7 Dialogs: no Codex dialog is answered by the adapter. The Sessions page shows the dialog
  and its options, persistent ones included; Choose sends the digit only while the same
  dialog shows.
- C8 Docs and evidence: PROTOCOL, the Codex guide section, CHANGELOG, the done matrix,
  native-evidence.md and the macOS live check. macOS stays unverified until its own run.
- C9 Tests from captured screens and a synthetic terminal; Probe, configuration, NewEpoch
  without keys, the fence before each key and erase on failure.
- C10 Linux live matrix in standalone temporary repositories with the P1 override: cycles in
  sequence, a restart in waiting_ready, a dialog before the clear and a first post-clear MCP
  approval in waiting_pickup (each answered on the dashboard and in the pane), crash cases
  E1–E4.
- C11 (generic) In reset_verified, right before the pickup, NewEpoch must still return the
  proven successor epoch; otherwise the cycle fails `epoch_changed`.
- C12 (generic) A dialog in waiting_pickup moves the cycle to waiting_prompt from
  waiting_pickup and returns there as any other dialog; the report is accepted during that
  wait.
- C13 Succession tests: the grant to the proven thread, and the ordinary result for every
  other thread, host, pane, address, requester, stage, deadline and Enable, a fenced or
  sub-agent candidate, and after a daemon start inside the guard.
- C14 (live finding) Probe refuses `participant_unheld` when the session holds no
  participant address: the thread after the clear reports only as the address holder, so
  such a cycle would clear the session and could not complete.
- C15 (live finding, corrected in review) The pickup report names the revision of a
  waiting_pickup stage that the successor read; a dialog wait from it, such as the approval of
  the report itself, and its return move the cycle's revision, and the approved call can
  arrive after either. A dashboard answer whose approved call completed the cycle first
  counts as received. The pickup wait only reads the dialog, without a key.
- C16 (review) Each input step reads the pane again right before its first key and types
  only when that read shows the phase's readiness (C2; for `/status`, the empty cleared screen):
  a draft that someone started after the inspection gets no key and stays.
