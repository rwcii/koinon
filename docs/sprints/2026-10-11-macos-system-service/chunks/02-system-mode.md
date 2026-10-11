# Chunk 02 — System mode

Criteria 1, 2, 6, 7 and the CI part of 8 of `decision.md`. Needs chunk 01 (log paths,
`--lock-wait` and the status PID).

## Current state

- `install.Install` (`internal/install/install.go`) places and links the binary, then
  `Services.WriteDaemon` and `StartDaemon`. `platform.Services` (`internal/platform/service.go`)
  uses the domain `gui/<uid>` everywhere and the artifact `~/Library/LaunchAgents/<label>.plist`.
  It changes only an artifact that carries the `KoinonManaged` marker; an unmarked one gives
  `service_artifact_unowned`.
- The service `Run` function is injectable; tests record the commands.
- Nothing records an install mode.

## Change

1. **Mode.** The presence of `/Library/LaunchDaemons/<label>.plist` records the intent to run
   in system mode; it does not prove that system mode works. There is no separate mode file.
   `koinon install --system` (macOS) creates the intent; any later `install` or `uninstall`
   detects it from the file. Readiness is verified separately every time (step 5 of item 3):
   an install reports success only after that check passes. A transition that was interrupted
   after step 4 leaves the plist, so the next install takes the reconciliation path (item 4),
   which repeats every step and completes or reports it.
2. **System plist.** Same label, `KoinonManaged` marker and `ProgramArguments` as the agent
   (binary link, `serve`, `--state-dir`, the state directory, `--lock-wait 60s`), plus
   `UserName` and `GroupName` of the installing user (from the running process, never root),
   `EnvironmentVariables` with `HOME`, `RunAtLoad true`, `KeepAlive true`,
   `ThrottleInterval 10`, `Umask 63` (077) and the chunk 01 log paths.
3. **Transition.** `koinon install --system` runs as the user; running it as root is refused
   with a fixed error, so `UserName` is never root. Every launchctl step runs through `sudo`,
   so that the person approves it and so that it works from SSH and tmux, where the user's
   own `gui/<uid>` commands fail with error 125. Steps, in order:
   1. Render the plist to a private temporary file and validate it with `plutil -lint`.
      Record the agent's original state: loaded or not (`sudo launchctl print
      gui/<uid>/<label>`) and disabled or not (`sudo launchctl print-disabled gui/<uid>`).
   2. Stop the user agent: `sudo launchctl disable gui/<uid>/<label>`, then
      `sudo launchctl bootout gui/<uid>/<label>` ("not loaded" counts as success).
   3. Verify both: `sudo launchctl print-disabled gui/<uid>` lists the label as disabled, and
      `sudo launchctl print gui/<uid>/<label>` reports no service. The agent plist stays in
      place until step 6.
   4. Install and start the daemon:
      `sudo install -o root -g wheel -m 0644 TMP /Library/LaunchDaemons/<label>.plist`,
      `sudo launchctl bootstrap system <path>`, `sudo launchctl kickstart -k system/<label>`.
   5. Verify the owner: `koinon status` answers within 90 seconds (the chunk 01 lock wait plus
      the throttle), and its `pid` equals the PID that `sudo launchctl print system/<label>`
      reports. A daemon that answers with another PID is the former agent and fails this step.
   6. Remove the owned agent plist.
   **Rollback** on a failure in steps 2 to 5: if steps 4 or 5 ran, `sudo launchctl bootout
   system/<label>` and remove the system plist. Then restore the agent to its recorded state:
   `sudo launchctl enable gui/<uid>/<label>` only if it was enabled, and `sudo launchctl
   bootstrap gui/<uid> <agent plist>` only if it was loaded. An agent that was disabled or
   not loaded stays so; none is created. Report the failed step and the rollback result; a
   failed rollback reports the exact `sudo` commands that finish it by hand.
   **Proof obligation.** That root `launchctl` can disable, boot out, bootstrap and print
   `gui/<uid>` from a `Background` session (SSH, tmux, CI) is an assumption until the native
   CI case of item 9 passes. Build that case first; if it fails, stop the chunk and return the
   plan to review and gate B.
4. **Later installs (reconciliation).** In system mode, a plain `koinon install` first repeats
   steps 2 and 3 for the user agent, so an agent re-enabled or restarted since then is stopped
   and disabled again. Then it replaces the binary in place and rewrites an owned system plist
   only when its content differs (through `sudo`). If `sudo launchctl print system/<label>`
   shows no loaded service (an interruption after step 4's file install, or a booted-out
   service), it runs `sudo launchctl bootstrap system <path>`; then it runs
   `sudo launchctl kickstart -k system/<label>` and repeats step 5. It never writes, enables
   or bootstraps the user agent. The report says `"service": "system"`.
5. **Hand-written LaunchDaemon (criterion 7).** An unmarked system plist with the same label is
   system mode only when its `ProgramArguments` are the agent's exactly: the binary link path
   of this prefix, `serve`, `--state-dir` and the same state directory (any trailing
   `--lock-wait` is allowed), and `UserName` is the installing user. Install never rewrites it;
   it replaces the binary at that link path, kickstarts it, reconciles the user agent as in
   item 4, and verifies the owner as in step 5 of item 3. A plist
   that names another executable, another state directory or another user is refused with a
   fixed error that names the differing field and changes nothing.
6. **Uninstall.** In system mode, an owned system plist is booted out and removed through
   `sudo`; an unowned one is left with the existing `service_artifact_unowned` report and the
   exact commands to remove it. State is kept. The user agent stays disabled; a later plain
   install enables it again (`launchctl enable`) because no system plist exists then.
7. **Linux.** `--system` on Linux returns `system_mode_unsupported`.
8. **Docs.** `docs/INSTALL.md`: a system-mode section (what it installs, the `sudo` prompts,
   one daemon only, upgrade, uninstall, the hand-written case) and the FileVault note
   (criterion 6). Replace "Koinon never uses sudo, system services, ..." with: the default
   install never uses them; `--system` is an explicit choice whose privileged steps the
   person approves at the `sudo` prompt. Root `AGENTS.md`, installation section: agents never
   run `--system` or `sudo` on the maintainer's machine. README installation line. CHANGELOG.
9. **Native CI.** The macOS job of `native-go-lifecycle.yml` adds a system-mode case with a
   scratch prefix and state, using the runner's passwordless `sudo`. The runner's launchd
   session is `Background`, as over SSH, so it also proves the transition without the GUI
   domain. Checks: the LaunchDaemon runs as the runner user; `print-disabled` lists the agent
   as disabled and `print gui/<uid>/<label>` shows no service; `koinon status` reports the
   system service's PID; after `sudo launchctl enable` and a bootstrap of a stray agent, a
   second install disables it again and leaves one daemon (`pgrep -f` count); the log has a
   start line; uninstall removes the plist.

## Done

The chunk's part of `definition-of-done.md`: install-mode tests through the fake runner,
the hand-written cases, uninstall, Linux refusal, and the native macOS system-mode case.
