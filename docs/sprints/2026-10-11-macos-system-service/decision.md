# Sprint 2026-10-11 — macOS system service — Decision

> Historical sprint plan. Its non-goals and constraints limited this sprint only. Current decisions are in the [decision records](../../engineering/wdr/README.md); see WDR 001.

The content of this decision is the maintainer's issue #283. This file restates it for
the plan and adds the constraints from the root `AGENTS.md`; it decides nothing new.

## Problem

On macOS, `koinon install` sets up a per-user LaunchAgent
(`~/Library/LaunchAgents/io.github.rwcii.koinon.daemon.plist`) in the desktop login's
`gui/<uid>` launchd domain. Services in that domain stop when the desktop login ends. A
reported outage left running clients unable to connect and let session registrations
expire. Desktop logout is a suspected trigger, but the cause is unconfirmed because the
daemon writes no lifecycle log.

Over SSH or tmux, `launchctl managername` reports `Background`, and operations on the GUI
domain fail with error 125, so the agent cannot be managed or restarted remotely. The agent
plist sets `KeepAlive` to `SuccessfulExit = false`, so a clean exit leaves the daemon
stopped. A daemon that loses `daemon.lock` exits with an error, so launchd starts it again
every 10 seconds while another daemon holds the lock.

## Acceptance criteria

1. `koinon install --system` on macOS installs the daemon as a LaunchDaemon in
   `/Library/LaunchDaemons` that runs as the installing user (`UserName`, `GroupName`,
   `HOME`), starts at boot (`RunAtLoad`), restarts on any exit (`KeepAlive = true`) with a
   throttle, and keeps `Umask` 077 and the existing state directory, so existing state and
   clients keep working. Uninstall removes it.
2. One daemon only. A system-mode install stops and disables the per-user agent
   (`launchctl bootout` and `launchctl disable gui/<uid>/<label>`). Later install runs detect
   system mode and never re-enable or rewrite the user agent. When two daemons start anyway,
   the one that does not get `daemon.lock` does not make launchd restart it in a tight loop.
3. The daemon's output goes to log files (`StandardOutPath`, `StandardErrorPath` under
   `~/Library/Logs/koinon/`). The log shows each start, each stop with its reason or signal,
   and lock contention.
4. `koinon status` and `koinon recover` work over SSH without the GUI domain. The
   installation guide documents what `recover` does, including its relation to the
   blocked-storage recovery. When the daemon is not reachable, `recover` names the service
   mode and gives the exact command that restarts it; in system mode that is
   `sudo launchctl kickstart -k system/<label>`.
5. A running session whose registration expired while the daemon was down registers again
   on its next tool call or when `koinon mcp` reconnects, and takes back its participant
   address when no other session holds it. Restarting the agent CLI is not needed.
6. The installation guide says that with FileVault on, a LaunchDaemon starts only after the
   disk is unlocked at boot.
7. A hand-written LaunchDaemon with the same label, `ProgramArguments` and state directory,
   `UserName` set to the installing user, `KeepAlive = true` and per-user log paths is
   detected as system mode and kept: later install runs replace the binary and restart it,
   and never re-enable the user agent.
8. The issue's checks hold: after a system-mode install the daemon keeps running and peers
   stay registered through a desktop logout and a fresh SSH login; the daemon comes back
   after a reboot with nobody logged in, once FileVault is unlocked; after an upgrade there is
   still one daemon and the user agent stays disabled; `koinon status` works over SSH; the
   logs show the start and stop of each daemon run.

## Constraints

- **Explicit opt-in.** System mode exists only behind `--system`. The default install stays the
  per-user LaunchAgent or systemd user unit. It changes only where criteria 2 and 3 require it
  for every service: the log paths, and the lock loser's bounded wait and clean exit.
- **Sudo boundary.** The root `AGENTS.md` forbids sudo and system services as a workaround.
  `--system` is the maintainer's explicit choice, not a workaround: the person who runs
  `koinon install --system` approves each privileged step at the `sudo` prompt. Agents never
  run `--system` or `sudo` on the maintainer's machine; `recover` prints the restart command
  and never runs `sudo` itself. This sprint updates the root `AGENTS.md` and
  `docs/INSTALL.md` wording to say so.
- **Tests.** Unit tests use the existing fake service runner. A real LaunchDaemon runs only
  on the disposable macOS CI runner of the native workflow (passwordless `sudo` there).
  Desktop logout, a fresh SSH login and a reboot cannot run in CI: the maintainer checks them
  on a Mac at the final head (`definition-of-done.md`, live checks).
- **Linux.** Linux keeps the systemd user unit. `--system` on Linux is refused with a fixed
  error; a Linux system unit is out of scope (the issue names only macOS).
- The user agent's own `KeepAlive` stays as it is; only system mode uses `KeepAlive = true`.
- Loopback-only listeners, private same-user state and secrets, and the content-free wake
  rules stay unchanged.

## Issues

This sprint delivers #283. It relates to #249 (running-session expiration, merged) and #274
(upgrades with running agents, merged); it reopens neither.
