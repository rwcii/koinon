# Chunk 01 — Daemon log and lock wait

Criteria 2 (no tight restart loop) and 3 of `decision.md`.

## Current state

- `koinon serve` (`cmd/koinon/main.go`) prints one JSON line `{"ok":true,"daemon":"running",...}`
  to standard output and, on a startup error, one JSON error report to standard error. Nothing
  records a stop or its reason.
- `platform.Lock` (`internal/platform/platform.go`) takes a non-blocking `flock`; `core.Start`
  returns `daemon already running or lock unavailable` at once.
- The launchd plist (`platform.RenderDaemon`, `internal/platform/service.go`) has no
  `StandardOutPath`/`StandardErrorPath`. With `KeepAlive {SuccessfulExit: false}` and
  `ThrottleInterval 10`, a daemon that loses the lock exits with an error and launchd starts
  it again every 10 seconds. The systemd unit uses `Restart=on-failure`, with the same effect.

## Change

1. **Lifecycle log.** `serve` writes one JSON line to standard error for each event:
   `{"event":"start","version":...,"state_dir":...,"listeners":[...]}`,
   `{"event":"stop","reason":"signal","signal":"terminated"}` (or `"reason":"error"` with the
   fixed error code), and `{"event":"lock_wait"}`/`{"event":"lock_acquired"}`. Each line has
   `at` (RFC 3339 UTC). No secret, message body, peer text or session ID is logged. The
   existing standard-output line stays for compatibility.
2. **Lock wait.** `serve --wait-for-lock` retries the lock every 2 seconds instead of exiting,
   logs `lock_wait` once per wait, and starts normally when it gets the lock. A signal during
   the wait stops it with a `stop` line. Without the flag, `serve` behaves as today.
3. **Service definitions.** The launchd plist and the systemd unit add `--wait-for-lock` to
   `ProgramArguments`/`ExecStart`. The plist adds `StandardOutPath` and `StandardErrorPath`
   at `~/Library/Logs/koinon/daemon.log`; install creates the directory with mode 0700.
   systemd keeps standard error in the user journal (`journalctl --user -u koinon`).
   A repeated `koinon install` rewrites the owned service file with the new keys.
4. **Docs.** `docs/INSTALL.md` (state, recovery and compatibility): where the log is, what it
   records, and that a second daemon waits for the lock. CHANGELOG under Unreleased.

## Done

The chunk's part of `definition-of-done.md`: log lines, lock contention with and without the
flag, plist and unit rendering, and the native workflow's Linux and macOS proof that the
service starts with the new arguments and that the macOS log file holds a start line.
