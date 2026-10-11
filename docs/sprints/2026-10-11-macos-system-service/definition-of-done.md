# Sprint 2026-10-11 — macOS system service — Definition of done

## Unit and integration tests (synthetic, Linux and macOS CI)

- Daemon log lines: start (version, state directory, listeners), stop with its reason
  (signal name, fatal error), and lock contention, written to standard error. Lines hold no
  secret, message body or private ID.
- Lock contention: a daemon started with `--wait-for-lock` while another holds the lock logs
  the contention once, keeps waiting, and becomes the daemon when the lock frees; without the
  flag `serve` still exits with its error at once.
- Plist and unit rendering: the user agent gains `StandardOutPath`/`StandardErrorPath`
  (macOS) and the service arguments gain `--wait-for-lock` (both platforms); the system plist
  has every key of acceptance criterion 1 and the log paths, and `UserName`/`GroupName`/`HOME`
  come from the installing user.
- Install mode: `--system` runs exactly the expected privileged commands through the fake
  runner (`sudo` install of the plist as root:wheel 0644, `sudo launchctl bootstrap system`,
  `sudo launchctl kickstart`), then boots out and disables `gui/<uid>/<label>`; later runs
  detect the mode from the system plist.
  A repeated plain `koinon install` on a system-mode host keeps system mode and never touches
  the user agent. `--system` on Linux returns the fixed error. A failed `sudo` step leaves the
  user agent as it was.
- Hand-written LaunchDaemon (criterion 7): an unmarked plist with the same label and state
  directory is detected as system mode, never rewritten, and restarted after a binary
  replacement; one with another state directory is refused with a fixed error.
- Uninstall in system mode removes an owned LaunchDaemon through `sudo` and
  leaves state.
- `recover` with the daemon unreachable prints the service mode and the exact restart
  command for each mode, and runs no `sudo`.
- Client re-registration (criterion 5): a synthetic daemon stops, the session's registration
  expires, the daemon starts again; the session's next tool call, and its next renewal
  without a tool call, register it again with the same identity and take back its address
  when it is unheld, and not when another session holds it.

## Real service managers

- `.github/workflows/native-go-lifecycle.yml`, macOS runner: `sudo`-backed
  `koinon install --system` with a scratch prefix and state; the LaunchDaemon runs as the
  runner user; the user agent is disabled; `koinon status` passes from the runner's
  `Background` launchd session; a repeated install leaves one daemon; the log file shows a
  start and a stop; `koinon uninstall` removes it. The Linux job keeps its current proof plus
  the log and `--wait-for-lock` arguments.

## Live checks (maintainer, on a Mac, at the final head)

1. `koinon install --system`, then desktop logout: the daemon keeps running and an agent in
   tmux stays registered (`koinon status` and `peers` over SSH).
2. A fresh SSH login: `koinon status` works.
3. A reboot with nobody logged in, FileVault unlocked: the daemon is back.
4. `koinon install` again (upgrade): one daemon, the user agent stays disabled.
5. The log shows the start and stop of each run.

## Gate

The `check` skill (vet, `go test -race -timeout 30m ./...` when #296 has merged, otherwise
`go test -race ./...`, whitespace, the four CGO-free builds, private-content scan), the Go
workflow on Linux and macOS, the native lifecycle workflow on both runners, and the observer's
sign-off bound to each chunk's head. The live checks above run before the last chunk's
sign-off.
