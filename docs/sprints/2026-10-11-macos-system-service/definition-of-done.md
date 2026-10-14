# Sprint 2026-10-11 — macOS system service — Definition of done

## Unit and integration tests (synthetic, Linux and macOS CI)

- Daemon log lines: start (version, state directory, listeners), stop with its reason
  (signal name, fatal error), and lock contention, written to standard error. Lines hold no
  secret, message body or private ID.
- Lock contention: a daemon started with `--lock-wait` while another holds the lock logs the
  wait once and becomes the daemon when the lock frees within the wait; when it does not, it
  logs `lock_held` and exits with status 0. Without the flag `serve` still exits with its error
  at once. `koinon status` reports the daemon's `pid`.
- Plist and unit rendering: the user agent gains `StandardOutPath`/`StandardErrorPath`
  (macOS) and the service arguments gain `--lock-wait 60s` (both platforms); the system plist
  has every key of acceptance criterion 1 and the log paths, and `UserName`/`GroupName`/`HOME`
  come from the installing user.
- Transition: `--system` runs exactly the chunk 02 steps through the fake runner, in order and
  all through `sudo`. Plain `launchctl` calls on `gui/<uid>` fail with error 125 in the fake,
  as over SSH, and the transition still succeeds. A failure injected at each step (disable,
  bootout, print-disabled, print, install, bootstrap, kickstart, status timeout, and a status
  PID that differs from the system service's PID, i.e. the old agent answering) ends in the
  stated state: no system plist, the user agent restored to its recorded state (loaded and
  enabled, or disabled, or not loaded, as it was before; never created), and a report naming
  the step; a failing rollback reports the manual commands. An install interrupted after the
  system plist was installed but before `bootstrap` completes on the next run: reconciliation
  finds no loaded system service, bootstraps it, kickstarts it and verifies the PID. With the
  service already loaded, reconciliation does not bootstrap it again. Running as root
  is refused.
- Reconciliation: on a system-mode host where the fake shows the user agent re-enabled and
  loaded, a plain `koinon install` disables and boots it out again, verifies with
  `print-disabled`, never writes or bootstraps the agent, and kickstarts the system service.
  `--system` on Linux returns the fixed error.
- Hand-written LaunchDaemon (criterion 7): an unmarked plist with the same label, the binary
  link of this prefix, the same state directory and user is system mode, never rewritten, and
  restarted after a binary replacement. One that names another executable, another state
  directory or another user is refused with a fixed error naming the field, and nothing
  changes.
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
  the `--lock-wait` arguments; on both runners a second `serve --lock-wait` against the running
  service exits 0 with `lock_held` while the service keeps answering, and on macOS the agent's
  log file holds a start line.

## Live checks (maintainer, on a Mac, on a develop build after chunk 03 merges)

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
sign-off bound to each chunk's head. The live checks above run on a develop build after the
last chunk merges, and #283 closes only after they pass; a failure is fixed in a new pull
request before a release. The maintainer moved them there on 2026-10-12 ("move the live checks
to after the merge"), so that the last chunk merges on review and CI.
