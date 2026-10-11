# Chunk 02 — System mode

Criteria 1, 2, 6, 7 and the CI part of 8 of `decision.md`. Needs chunk 01 (log paths and
`--wait-for-lock` in the service arguments).

## Current state

- `install.Install` (`internal/install/install.go`) places and links the binary, then
  `Services.WriteDaemon` and `StartDaemon`. `platform.Services` (`internal/platform/service.go`)
  uses the domain `gui/<uid>` everywhere and the artifact `~/Library/LaunchAgents/<label>.plist`.
  It changes only an artifact that carries the `KoinonManaged` marker; an unmarked one gives
  `service_artifact_unowned`.
- The service `Run` function is injectable; tests record the commands.
- Nothing records an install mode.

## Change

1. **Mode.** System mode is the presence of `/Library/LaunchDaemons/<label>.plist`. That file
   is the record: no separate mode file. `koinon install --system` (macOS) selects it; any
   later `install` or `uninstall` detects it from the file.
2. **System plist.** Same label, `KoinonManaged` marker and `ProgramArguments` as the agent
   (binary link, `serve`, `--state-dir`, the state directory, `--wait-for-lock`), plus
   `UserName` and `GroupName` of the installing user (from the running process, never root),
   `EnvironmentVariables` with `HOME`, `RunAtLoad true`, `KeepAlive true`,
   `ThrottleInterval 10`, `Umask 63` (077) and the chunk 01 log paths.
3. **Privileged steps.** `koinon install --system` runs as the user. It renders the plist to a
   private temporary file and runs, in this order and each through `sudo` so that the person
   approves it: `sudo install -o root -g wheel -m 0644 TMP /Library/LaunchDaemons/<label>.plist`,
   `sudo launchctl bootstrap system <path>` (after a `bootout` of `system/<label>` whose error
   is ignored) and `sudo launchctl kickstart -k system/<label>`. Only then does it run
   `launchctl bootout gui/<uid>/<label>` and `launchctl disable gui/<uid>/<label>` for the user
   agent and remove the owned agent plist. If any `sudo` step fails, it stops there and reports
   the step; the user agent is untouched. The brief overlap is safe because the system daemon
   waits for the lock (chunk 01). Running `koinon install --system` as root is refused with a
   fixed error, so `UserName` is never root.
4. **Later installs.** In system mode, a plain `koinon install` replaces the binary in place,
   rewrites an owned system plist only when its content differs (through `sudo`), runs
   `sudo launchctl kickstart -k system/<label>`, and never writes, enables or bootstraps the
   user agent. The report says `"service": "system"`.
5. **Hand-written LaunchDaemon (criterion 7).** An unmarked system plist with the same label
   whose `ProgramArguments` name `serve` and the same state directory is system mode: install
   never rewrites it, replaces the binary and kickstarts it. Another state directory, or a
   `UserName` that is not the installing user, is refused with a fixed error.
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
   scratch prefix and state, using the runner's passwordless `sudo`: the LaunchDaemon runs as
   the runner user; `launchctl print gui/<uid>/<label>` shows the agent disabled; `koinon
   status` passes; a second install leaves one daemon (`pgrep -f` count) and the user agent
   disabled; the log has a start line; uninstall removes the plist.

## Done

The chunk's part of `definition-of-done.md`: install-mode tests through the fake runner,
the hand-written cases, uninstall, Linux refusal, and the native macOS system-mode case.
