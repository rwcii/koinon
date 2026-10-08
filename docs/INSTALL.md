# Install and enable Koinon

## The Go runtime

The Go runtime is one `koinon` binary and one daemon per user. It needs no Python. The checkout no longer carries the Python runtime. The pinned previous release
remains a supported input to the import and upgrade commands below. Every command prints a JSON report; a refusal prints `ok: false` with a fixed `code`.

### Homebrew

On macOS or Linux, the tap `rwcii/koinon` installs the release binary for your platform:

```sh
brew install rwcii/koinon/koinon
koinon install --agent claude --agent codex
```

Homebrew checks the binary against the release's `SHA256SUMS` entry. The formula does not use
`brew services`: `koinon install` copies the binary into its own prefix and manages the
service, as the next sections describe. A `brew upgrade koinon` therefore changes only the
Homebrew copy. Run `koinon install` again after each upgrade to replace the installed binary and
restart the daemon. A Python-era installation upgrades with `koinon upgrade --from-python`, as
below, with the Homebrew binary.

### Download and check

Each release attaches four binaries, `koinon-linux-amd64`, `koinon-linux-arm64`,
`koinon-darwin-amd64` and `koinon-darwin-arm64`, and a `SHA256SUMS` file. Check the binary
before you run it:

```sh
sha256sum --check --ignore-missing SHA256SUMS      # Linux
# macOS: check the selected file against its SHA256SUMS entry with shasum -a 256.
chmod +x koinon-linux-amd64 && ./koinon-linux-amd64 version
```

### Install

```sh
./koinon-linux-amd64 install --agent claude --agent codex
```

`koinon install [--prefix DIR] [--state-dir DIR] [--agent FAMILY]... [--no-start]` does this:

1. It copies the running binary to `<prefix>/bin/koinon`. The default prefix is
   `$XDG_DATA_HOME/koinon/go`, which is `~/.local/share/koinon/go`. The copy is atomic, and it is
   checked against the source's SHA-256.
2. It links `~/.local/bin/koinon` to that copy, on Linux and macOS, and records the link in
   `<prefix>/link`. An existing link to the copy is kept; any other file or link at that path
   is never replaced (`"link_result": "occupied"`). The report names the `koinon` that `PATH`
   runs (`on_path`). When that is not the installed copy, `path_step` names the one step that
   makes it so, for example adding `~/.local/bin` to `PATH` in the shell's startup file; on
   macOS that directory is not on `PATH` by default. When another `koinon`, such as the
   Homebrew copy, comes first on `PATH`, the report names it. The install never edits shell
   startup files.
3. It writes one service that runs `<prefix>/bin/koinon serve --state-dir <state>`, then enables
   and (re)starts it:
   - on Linux, the systemd user unit `~/.config/systemd/user/koinon.service`, enabled for login;
   - on macOS, the launchd agent
     `~/Library/LaunchAgents/io.github.rwcii.koinon.daemon.plist`, bootstrapped in `gui/<uid>`.

   The file carries the marker `koinon-go-daemon-v1`. An existing file at that path without the
   marker is refused (`service_artifact_unowned`), never replaced.
4. It waits until the daemon answers `status`.
5. It runs `koinon setup` for each `--agent`, with the installed path.

When no user service manager answers, the report says `"service": "manual_required"` and gives
the `start_command` to run in a managed session. Koinon never uses sudo, system services,
lingering or permission changes. `--no-start` writes the binary and the service file and starts
nothing. A repeated install replaces the binary in place and restarts the service. The Go state
defaults to `$XDG_STATE_HOME/koinon/go`, which is `~/.local/state/koinon/go`.

When a Python-era installation is present (`~/.local/share/koinon/install.json`), the install is
refused with `python_install_present`. Use the upgrade instead.

### Upgrade from the Python runtime

```sh
./koinon-linux-amd64 upgrade --from-python --agent claude --agent codex
```

`koinon upgrade --from-python [--python-prefix DIR] [--prefix DIR] [--state-dir DIR]
[--agent FAMILY]... [--repository KEY=PATH]... [--python PATH]` is the supported path from the
`main` release. It records one attempt in `<state>/upgrade/journal.json` and runs these steps:

1. **Preflight.** The Python `install.json` is readable and in the `installed` state. The
   installed runtime honours the upgrade marker and has `scripts/uninstall.py`. The Go state
   database is absent or empty, and no Go daemon runs on it.
2. **Exclude and stop.** The upgrade writes the Python runtime's own upgrade marker
   (`installation_state: upgrading`) under its `.install.lock`. While the marker is present,
   the Python install, `ensure`, uninstall, upgrade and memory-service start all refuse. The
   marker is written only while `install.json` is still the configuration that the preflight
   inspected. Under the marker, the state root and the list of Python services must still
   equal the preflight's; otherwise the result is `source_changed`. Then the upgrade stops each
   Python service: the session supervisors, the memory services and the legacy pair. It does
   not remove them. It records each service as its own to restart before its stop begins, and
   it waits until the manager reports the service stopped.
3. **Import.** This is the import that `koinon import` describes below, into a staging database.
   Only a fully verified set replaces the Go state database.
4. **Remove Python.** The upgrade changes the marker to `installation_state: removing`. In that
   state the Python install, `ensure` and memory-service start still refuse. Then it runs the
   installed `uninstall.py`. That script removes the Python services, the Python-managed Codex
   and DeepSeek guidance sections, and the Claude guidance and status line. It keeps the Python
   state tree, which stays as a backup.
5. **Start Go.** The upgrade installs and starts the Go service, as `koinon install` does, and
   runs `koinon setup` for each `--agent`.

The upgrade recovers as follows:

- **A failure in steps 1–3.** The attempt ends. The staging database is deleted,
  `install.json` gets its original bytes back, and every service that the attempt stopped
  starts again. The Python runtime runs as before, and the report names the phase and the
  `code`. Run the command again for a fresh attempt. It takes a fresh copy, so messages and
  memory that Python received in the meantime are included.
- **A failure after the rename.** This covers steps 4–5, and also a failure just after the
  verified import replaced the state database. The import stays, and the Python runtime stays
  excluded. Fix the cause named in the report, then run the same command again to resume.
- **A crash.** A crashed run is resumed by running the same command again. A resume first
  observes each recorded action again. It refuses with `source_changed` when `install.json`
  (apart from the marker) or the list of Python services changed after the attempt began.
  A state database counts as this attempt's import only when two things hold: its import
  records equal the source digests that the attempt recorded before the rename, and a fresh
  capture of the excluded sources verifies against it. Records from any other writer end the
  attempt with `target_not_empty`, and the Python runtime is restored. When that check cannot
  be completed, the Python runtime stays excluded until the next run.

`koinon upgrade --status [--state-dir DIR]` prints the journal. The upgrade does not roll back
a completed attempt. The Python state tree is never deleted.

After the upgrade, an instruction file that you wrote yourself may still name the Python
`session.py`. Change it to `koinon guide --agent FAMILY`.

### Import

`koinon import [--from DIR] [--python-prefix DIR] [--state-dir DIR] [--repository KEY=PATH]...
[--verify]` runs the import on its own. It stops nothing. While a Python installation exists, it
holds the upgrade marker for the duration of the import, then gives `install.json` its original
bytes back. It refuses with `python_running` while any Python service runs. It refuses with
`daemon_running` while a Go daemon runs on the state directory. It records each attempt in
`<state>/import/journal.json` before it writes the marker. After an interruption, the next
`koinon import` first restores `install.json` from that record and removes the attempt's
staging files.

- **Sources.** The import reads every Codex and DeepSeek session inbox, a legacy single-thread
  inbox, and every memory store with its work items and claims. Each source's writer locks are
  held, and each database is captured with `VACUUM INTO` inside a held write lock. The capture
  includes the write-ahead log. The sources are never written.
- **Staging.** The captures are mapped and staged in `state.sqlite3.import-<attempt>`, one
  transaction per source. Each source is verified by count and by canonical digest. The staging
  database then replaces the state database by an atomic rename.
- **Repeats.** The import writes only into an absent or empty state database. A second run with
  the same sources reports `"result": "unchanged"`. Any other non-empty target is refused with
  `target_not_empty`.
- **`--verify`.** This option compares the sources with the state database and changes nothing.
- **Repositories.** A memory store's repository is taken, in this order, from the saved memory
  selection, then an inbox memory binding, then a session's repository through Git. The
  recorded path must hash to the store's key. When no source names it, the import is refused
  with `repository_unresolved` and the key. Supply the path with `--repository KEY=PATH`.

[PROTOCOL.md](../PROTOCOL.md#import-of-python-era-state) defines how each record maps.

### Uninstall

```sh
~/.local/share/koinon/go/bin/koinon uninstall
```

`koinon uninstall [--prefix DIR] [--state-dir DIR] [--agent FAMILY]...` removes these:

- What `koinon setup` added for each agent (all families by default):
  - the MCP entry, through the agent's own CLI, only while it still runs this binary,
    whether it is enabled or disabled;
  - for OpenCode, which has no remove command, the `mcp.koinon` entry in `opencode.json` or
    `opencode.jsonc`;
  - the `agy` Stop hook;
  - the OpenCode identity plugin;
  - the Claude status line, whose saved original is restored.
- The service file that carries the marker. It is removed only after the manager reports the
  service stopped. When the manager refuses the stop, or still reports the service running,
  uninstall refuses with `service_stop_failed` and keeps the service file and the binary.
- The binary.
- The `~/.local/bin/koinon` link, only when the install created it (`<prefix>/link`) and it still
  points at the installed binary. Any other link or file there is kept (`"link_result": "kept"`).

It keeps the state directory and reports its path. A repeated uninstall changes nothing.

## Requirements and manual operation

Fresh installation needs Linux or macOS and the binary for that operating system and CPU.
No Python interpreter is needed. Building from source needs the Go version in `go.mod`.
Git resolves repository identity for shared memory/work and repository launchers. Agent setup
requires the selected agent CLI. Codex must support `queue`; OpenCode must support its server.
DeepSeek requires a running harness, but its live wake proof remains deferred under #199.

A legacy upgrade additionally needs Python 3.11+ to run the **installed previous release's**
uninstaller. The Go importer reads SQLite itself; it does not import a Python package from
this checkout. Do not delete the old installation or state before the verified upgrade.

Without a reachable user manager, run the returned `start_command` in a persistent managed
session. The equivalent manual command is `koinon serve --state-dir STATE`; terminate that
owned process gracefully when finished. There is one daemon, not a bridge/notifier pair.
Do not use sudo, system services, lingering or permission changes to work around a refusal.

## Configuration and agent access

`koinon setup FAMILY` installs the MCP entry through the selected agent's CLI. Families are
`claude`, `codex`, `agy`, `opencode` and `deepseek`; DeepSeek uses the explicit command path
until its MCP support is verified. `--cli ABS_PATH` selects the native CLI and `--binary
ABS_PATH` selects the installed Koinon binary. Setup does not create an agent conversation.
Codex and Antigravity identify the calling conversation on every MCP call; Claude uses its
native session environment and verified executable; OpenCode needs the managed identity
plugin. Launch OpenCode through `koinon opencode` for a reachable wake server.

Read `koinon guide --agent FAMILY` at startup and after a reset. Discover the exact recipient
with `peers`, send only within the maintainer's authorization, read notices through `inbox`,
and acknowledge only messages already handled. Use MCP when available; command examples
and memory/work operations are in [USAGE.md](USAGE.md). Setup and runtime replacement require
the maintainer's authorization, separate from repository development.

## State, recovery and compatibility

The Go default state root is `~/.local/state/koinon/go` (or `$XDG_STATE_HOME/koinon/go`). It
holds `state.sqlite3`, the private secret and daemon lock. Directories are owned by the
current UID and private; credentials never belong in Git or logs. The daemon binds loopback
only. Custom state and address must match between the daemon, command clients and MCP server.
The dashboard uses one-time login links, a login cookie and CSRF-protected actions.

The daemon applies supported Go schema migrations transactionally and refuses an unknown
future schema. `koinon status` reports health, storage and delivery counts. Correct the
reported condition before using `koinon recover` for blocked storage. Capacity refusals
preserve existing records; a failed or timed-out request alone does not prove a write failed.
Use the same idempotency key and deadline to resolve uncertain writes within their horizon.
An unavailable daemon is not permission to replace its state or change a secret.

Memory and work share the daemon database, with one logical store per resolved Git common
directory, including worktrees. Snapshot pages and deltas still require explicit
acknowledgement. Work claims are advisory leases, not permission to perform actions. See
[PROTOCOL.md](../PROTOCOL.md), [the work contract](WORK-ITEMS-V1.md) and
[Go storage limits](WORK-ITEMS-GO-STORAGE.md). Python-era implementation evidence under
historically marked documents is retained for migration provenance, not as an active recipe.
