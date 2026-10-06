# Chunk 02 — core

Read `../decision.md`, `../definition-of-done.md` and `../spike.md` first.

## Outcome

- `go.mod` at the repository root (module `github.com/rwcii/koinon`), the command in
  `cmd/koinon`, packages under `internal/`. Platform differences live in `internal/platform`
  only.
- `koinon serve` starts the daemon for the current user: one instance per user (a lock in the
  state directory; a stale lock of a dead daemon is taken over), loopback listeners on 127.0.0.1
  and ::1, the user secret file (mode 0600, created on first start), and a SQLite database
  through a pure-Go driver.
- Session records: register, renew, expire, retire, keyed by family and the family's session
  identifier. A session holds its family, repository (Git common directory), working directory,
  wake target and timestamps.
- `koinon status` reports the daemon, its listeners and the session count.
- The state directory defaults beside the Python runtime's (`~/.local/state/koinon/go/` on
  Linux, the matching path on macOS) and never writes Python-era files.
- `tests.yml` gains a Go job on Ubuntu and macOS. The `check` skill runs `go vet ./...` and
  `go test -race ./...`. The root `AGENTS.md` describes where Go code lives while both runtimes
  exist.

## Tests

The binary, loopback and secret, session record and two-daemon cases of the definition of done.

## Done

Gate passes; the daemon runs under `go run ./cmd/koinon serve` on both platforms.
