# Sprint 2026-10-06 — Go daemon — Definition of done

The sprint is done when every acceptance criterion in `decision.md` holds on `develop`, the tests
below pass on Linux and macOS, the live checks are recorded on their chunk's pull request, and
the release that carries criterion 11 installs and upgrades from the current `main` through its
documented path.

## Unit and integration tests

All tests use synthetic peers, temporary state roots, temporary `CLAUDE_CONFIG_DIR`,
`CODEX_HOME` and `agy` configuration directories, ephemeral loopback ports and a private tmux
server. No test reads or writes the user's registry, sessions, terminals, services or agent
configuration. A test that needs a service manager patches every manager call, except in the
native workflows below.

- **Binary (criterion 1).** `CGO_ENABLED=0 go build ./cmd/koinon` succeeds for linux/amd64,
  linux/arm64, darwin/amd64 and darwin/arm64. `go vet ./...` and `go test -race ./...` pass.
- **Loopback and secret (criterion 3).** The daemon binds 127.0.0.1 and ::1 only. A
  configuration that names a non-loopback address, `0.0.0.0` or `::` is refused at start. A
  request without the secret, with a wrong secret or with a secret for another session is
  refused. The secret file is created with mode 0600, refused when its owner, mode or type is
  wrong, and never logged.
- **Session records (criterion 2).** Register, renew, expire and retire a session of each family.
  An expired session stops receiving deliveries, keeps its inbox, and registers again with the
  same inbox when it returns. Two registrations of the same session key never coexist, also under
  concurrent registration.
- **Messages (criterion 4).** For each pair of families: send, store, deliver, read, acknowledge.
  Each message has exactly one delivery state and one acknowledgement state at every point. A
  message to an unknown or retired session fails with a typed error. A crash between store and
  delivery delivers after restart; a crash after delivery does not deliver twice. Sequence
  numbers per inbox are gapless and ordered. A sender can read the delivery outcome of its own
  message (#82).
- **Wake (criterion 6).** Each wake adapter, against a synthetic receiver: the notice carries no
  peer text; a busy or unreachable receiver leaves the message waiting and reports it; a notice
  for a handled sequence is not sent again (#90). The Claude adapter follows `PROTOCOL.md`
  (newline-delimited JSON, unresolved socket path, peer credentials on Linux and macOS).
- **MCP (criterion 5).** The `koinon mcp` stdio server lists its tools, takes the session
  identity only from the environment of the agent that started it, and refuses a call that
  names another session as the caller.
- **Memory (criterion 7).** The memory tests of the Python runtime, ported: snapshot pages,
  deltas, acknowledgement only after processing, record format 2, one store per Git common
  directory including worktrees, and the error codes of `PROTOCOL.md`.
- **Work items (criterion 8).** The work item tests of the Python runtime, ported: schema 5
  records, advisory claims with leases, immutable events, maintenance bounds and the command
  interface of `docs/WORK-ITEMS-COMMANDS.md`.
- **Dashboard (criterion 9).** Every view renders from synthetic state. Every action changes
  the state it names and writes one audit record. A request without a login session, with a
  wrong `Host`, with a foreign `Origin`, or a state-changing request without a valid CSRF token
  is refused. A login link works once and expires.
- **Launcher (criterion 10).** Inside tmux, outside tmux, and without tmux, for Codex and `agy`.
  A start folder that holds another repository is refused. The configured CLI path is used, not
  the first match on `PATH`.
- **Import (criterion 11).** A Python-era state tree built by the Python runtime's own
  test fixtures (inboxes with handled and unhandled messages, checkpoints, memory stores with
  snapshots and deltas, work items with live and expired claims) imports with every record
  present and every cursor and acknowledgement unchanged. A second import is a no-op. An import
  interrupted at any step resumes or rolls back with no loss.

## Integration points exercised for real

- **Service managers.** The native workflows under `.github/workflows/` run the Go daemon under a
  real systemd user manager (Ubuntu) and a real launchd agent (macOS): install, start, restart
  after a kill, upgrade, uninstall with state preserved.
- **Live checks on the user's host, each with the user's authorization:**
  1. MCP from inside each family's default sandbox: Claude Code, Codex `workspace-write`,
     DeepSeek harness, `agy` (chunk 01, then again with the real server in chunk 05).
  2. Wake of an idle session of each family, and Antigravity delivery at its turn boundary
     (chunk 04).
  3. A message from one Claude session to another through Koinon (chunk 05).
  4. Upgrade of this host from the `main` release to the Go release, with the import verified by
     counts and by reading a known message, memory entry and claim (chunk 11).

## Edge cases that must have tests

- Two daemons for the same user (second start refuses; a stale lock from a dead daemon is taken
  over).
- A daemon restart while agents hold MCP connections (the MCP server reports the daemon as
  unavailable and recovers; no message is lost).
- Clock jumps for leases and session expiry.
- A socket path at the AF_UNIX length limit on Linux (108) and macOS (104).
- macOS `/tmp` that resolves to `/private/tmp`, with the registry path kept unresolved.
- A session of an agent family whose CLI is not installed.
- A disk-full or read-only state directory (refused with a clear error; no partial record).

## Gate

Each chunk passes the `check` skill, which from chunk 02 runs `go vet ./...` and
`go test -race ./...` beside the Python suite, and the CI workflows that cover it:
`tests.yml` (extended with a Go job on Ubuntu and macOS from chunk 02) and the native workflows
for chunks that touch a service manager. The Python suite runs until chunk 12 removes it.
