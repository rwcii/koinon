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

1. **Mode.** System mode is the presence of `/Library/LaunchDaemons/<label>.plist`, which
   the installer writes only as the last step of a transition that succeeded (item 3). There
   is no separate mode file. `koinon install --system` (macOS) selects it; any later `install`
   or `uninstall` detects it from the file and then reconciles the user agent (item 4).
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
   2. Stop the user agent: `sudo launchctl disable gui/<uid>/<label>`, then
      `sudo launchctl bootout gui/<uid>/<label>` ("not loaded" counts as success).
   3. Verify both: `sudo launchctl print-disabled gui/<uid>` lists the label as disabled, and
      `sudo launchctl print gui/<uid>/<label>` reports no service. Remove the owned agent plist.
   4. Install and start the daemon:
      `sudo install -o root -g wheel -m 0644 TMP /Library/LaunchDaemons/<label>.plist`,
      `sudo launchctl bootstrap system <path>`, `sudo launchctl kickstart -k system/<label>`.
   5. Verify the owner: `koinon status` answers within 90 seconds (the chunk 01 lock wait plus
      the throttle), and its `pid` equals the PID that `sudo launchctl print system/<label>`
      reports. A daemon that answers with another PID is the former agent and fails this step.
   On a failure in steps 2 or 3, nothing else has changed: re-enable the agent
   (`sudo launchctl enable gui/<uid>/<label>`), leave its plist, and report the step. On a
   failure in steps 4 or 5, roll back: `sudo launchctl bootout system/<label>`, remove the
   system plist, rewrite the owned agent plist, re-enable and bootstrap it through `sudo`, and
   report the step and the rollback result. System mode exists only after step 5 succeeds.
   A failed rollback reports the exact `sudo` commands that finish it by hand.
4. **Later installs (reconciliation).** In system mode, a plain `koinon install` first repeats
   steps 2 and 3 for the user agent, so an agent re-enabled or restarted since then is stopped
   and disabled again. Then it replaces the binary in place, rewrites an owned system plist
   only when its content differs (through `sudo`), runs
   `sudo launchctl kickstart -k system/<label>`, and repeats step 5. It never writes, enables
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
