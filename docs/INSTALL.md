# Install and enable Koinon

## The Go runtime

The Go runtime is one `koinon` binary and one daemon per user. It needs no Python. The checkout no longer carries the Python runtime. The pinned previous release
remains a supported input to the import and upgrade commands below. Every command prints a JSON report; a refusal prints `ok: false` with a fixed `code`.

Install and uninstall keep completed-step details in their report when a later step fails;
the report then has `ok: false`, and the command exits unsuccessfully.
On macOS, both agent and system-mode installs check an existing log directory before
replacing the binary or its link.

### Homebrew

On macOS or Linux, the tap `rwcii/koinon` installs the release binary for your platform:

```sh
brew install rwcii/koinon/koinon
$(brew --prefix)/bin/koinon install --agent claude --agent codex
```

Homebrew pours the release's bottle for your platform (`koinon-VERSION.TAG.bottle.tar.gz`, which
holds the same binary) and checks it against the release's `SHA256SUMS` entry. A bottle needs no
build, so on Apple Silicon and Linux Homebrew needs no developer tools; Homebrew still requires
them for any install on an Intel Mac (its Installation page, footnote 3). The release workflow
installs each release's bottles through Homebrew on macOS and Linux before it publishes them.
Releases up to v0.2.1 have no bottles: Homebrew treats their install as a build from source,
which on macOS needs the Command Line Tools (`xcode-select --install`); without them, use the
download below.

The formula does not use `brew services`: `koinon install` manages the service, as the next
sections describe. Run from the Homebrew binary in user mode, the install links
`<prefix>/bin/koinon` to the formula's stable path, `$(brew --prefix)/opt/koinon/bin/koinon`,
instead of copying it. Each `brew upgrade` moves that path to the new version, the daemon finds
that its executable changed and exits, and the service manager starts the new binary, which
migrates the state. To upgrade:

```sh
brew update
brew upgrade koinon
koinon version
```

`brew update` first: `brew upgrade` refreshes a third-party tap only when its auto-update interval
has passed, and otherwise keeps the formula of the previous release. Within about 15 seconds,
`koinon status` reports the new `version` and its `schema`.

An installation made by v0.3.0 or earlier has a copy, and its daemon does not check its
executable. Upgrade it once with `$(brew --prefix)/bin/koinon install` after `brew upgrade`.
Each later `brew upgrade` is complete: the restarted daemon also writes the managed cycle's
`koinon-pickup` skill (Install, step 7). A macOS system-mode daemon (`--system`) keeps a copy,
because brew can replace the linked file; upgrade it with
`$(brew --prefix)/bin/koinon install --system`. A Python-era installation upgrades with
`$(brew --prefix)/bin/koinon upgrade --from-python`, as below.

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

`koinon install [--prefix DIR] [--state-dir DIR] [--port N] [--agent FAMILY]... [--no-start] [--system]`
does this:

1. It copies the running binary to `<prefix>/bin/koinon`. The default prefix is
   `$XDG_DATA_HOME/koinon/go`, which is `~/.local/share/koinon/go`. The copy is atomic, and it is
   checked against the source's SHA-256. A Homebrew binary in user mode is linked instead
   (`"binary_result": "linked"`): the binary is in a keg,
   `<brew>/Cellar/koinon/<version>/bin/koinon` with the keg's `INSTALL_RECEIPT.json`, and
   `<prefix>/bin/koinon` becomes a link to `<brew>/opt/koinon/bin/koinon`. An install from any
   other binary, and every system-mode install, replaces that link with a copy.
2. It links `~/.local/bin/koinon` to that copy, on Linux and macOS, and records the link in
   `<prefix>/link`. An existing link to the copy is kept; any other file or link at that path
   is never replaced (`"link_result": "occupied"`). A symlink or an unrelated file at
   `<prefix>/link` is refused (`link_marker_invalid`) and left as it is. The report names the
   `koinon` that `PATH` runs (`on_path`), reading relative and empty `PATH` entries against the
   current directory, as a shell does. When that is not the installed copy, `path_step` names
   the one step that makes it so, for example adding `~/.local/bin` to `PATH` in the shell's
   startup file; on macOS that directory is not on `PATH` by default. When another `koinon`,
   such as the Homebrew binary, comes first on `PATH`, the report names it. When the installed
   binary itself ran the install, it copies nothing; if another `koinon` on `PATH` has other
   content, such as a newer download or Homebrew binary, the report names it in
   `other_binary` and `source_step` gives the command that installs it. The install never
   edits shell startup files.
3. It writes one service that runs
   `<prefix>/bin/koinon serve --state-dir <state> --lock-wait 60s`, then enables and
   (re)starts it:
   - on Linux, the systemd user unit `~/.config/systemd/user/koinon.service`, enabled for login;
   - on macOS, the launchd agent
     `~/Library/LaunchAgents/io.github.rwcii.koinon.daemon.plist`, bootstrapped in `gui/<uid>`.

   The file carries the marker `koinon-go-daemon-v1`. An existing file at that path without the
   marker is refused (`service_artifact_unowned`), never replaced.
4. It waits until the daemon answers `status`.
5. It runs `koinon setup` for each `--agent`, with the installed path.
6. For each other launcher family (`claude`, `codex`, `agy`, `opencode`, `grok`) without a usable
   `launchers.json` entry, it reads the family's MCP configuration through the CLI on `PATH`.
   When the enabled `koinon` server runs exactly the installed binary, as an earlier setup left
   it, the install records that CLI in `launchers.json` and lists the family under `agents`.
   It changes nothing in the agent's configuration. A family without such an entry or CLI is
   left out; set it up with `koinon setup FAMILY`.
7. It writes the managed context cycle's pickup skill, `koinon-pickup` (PDR 006, Decision 4),
   for you, whichever agents it names: `skills/koinon-pickup/SKILL.md` under the Claude Code
   configuration root (`$CLAUDE_CONFIG_DIR`, else `~/.claude`) and
   `~/.agents/skills/koinon-pickup/SKILL.md`, which Codex lists for every session. It never
   writes into a repository. A missing directory is created private to you; a file with other
   content is replaced. `pickup_skills` in the report gives each path and its result
   (`created`, `replaced` or `unchanged`); a file that cannot be written fails the install with
   `pickup_skill_failed` after the service and setup steps, and a repeated install completes it.
   The daemon also writes both files at each start, from its own environment, when one is
   missing or holds other content, so an upgrade that only restarts it, such as
   `brew upgrade`, installs the skill too; a file it cannot write is logged as a
   `pickup_skill` event with `write_failed`, and the daemon keeps running. A session whose
   Claude Code runs with another configuration root than the daemon shows the step that
   installs the skill there (`koinon install` with that `CLAUDE_CONFIG_DIR`).
   The skill only reads the catalog's `skill.pickup` through Koinon, so it changes only when
   this entry changes. A personal or project `pickup` skill is left as it is.

When no user service manager answers, the report says `"service": "manual_required"` and gives
the `start_command` to run in a managed session. The default install never uses sudo, system
services, lingering or permission changes (PDR 003, Decision 1); `--system` on macOS (below) is an
explicit choice whose privileged steps run through `sudo` under the host's sudo policy.
`--no-start` writes the binary and the service file and starts nothing. A repeated install replaces the binary in place and restarts the service. The Go state
defaults to `$XDG_STATE_HOME/koinon/go`, which is `~/.local/state/koinon/go`.

When a Python-era installation is present (`~/.local/share/koinon/install.json`), the install is
refused with `python_install_present`. Use the upgrade instead.

### Port

The daemon and its dashboard listen on `127.0.0.1` and `[::1]` at one port, 47671 by default.
`koinon install --port N` (1 to 65535) records another port in the state directory, in the file
`<state>/port` (mode 0600), before it writes and restarts the service, and waits for the daemon at
`127.0.0.1:N`; the report's `port` names it. An SSH tunnel to a remote host's dashboard can use
any local port (docs/USAGE.md), so a remote port only needs to change when it conflicts on that
host. The service file stays the same
on Linux, for the macOS agent and in macOS system mode: `koinon serve --state-dir <state>` without
`--listen` or `--listen-v6` reads the recorded port, and each of those options, when given,
replaces only its own family's listener. A later `koinon install` without `--port`, an upgrade
and `brew upgrade` keep the recorded port, because they keep the state directory. To return to
the default, run `koinon install --port 47671`. A port file that is not a private file of this
user holding one port is refused with `port_invalid`, by the daemon and by every command, and
never read as the default.

Each command, hook, launched agent and `koinon mcp` finds the daemon at its `--address`, else at
`KOINON_DAEMON_ADDRESS`, which a launcher passes to its agent with `KOINON_STATE_DIR` and which
applies only to that state directory, else at `127.0.0.1` and the port
that its state directory records, else at `127.0.0.1:47671`. `koinon dashboard` prints its
login link at that address. Without a recorded port, everything stays at 47671. A session that
a launcher started before a port change keeps the former address in its environment and in its
`koinon mcp` server; start it again after the change.

### System mode on macOS

The launchd agent runs in the desktop login's `gui/<uid>` domain, so it stops when that login
ends, and it cannot be managed from SSH or tmux. `koinon install --system` instead runs the
daemon as a LaunchDaemon, `/Library/LaunchDaemons/io.github.rwcii.koinon.daemon.plist`. It runs
as you (`UserName`, `GroupName` and `HOME` of the installing user, never root), with the same
arguments, state directory and log file as the agent. It starts at boot (`RunAtLoad`), restarts
after any exit (`KeepAlive`, at most every 10 seconds) and keeps `Umask` 077. With FileVault on,
it starts only after the disk is unlocked at boot.

Run it as yourself, not with `sudo`: it refuses to run as root (`system_mode_root`) and runs
`sudo` with each privileged step's command. Whether `sudo` asks for a password depends on the
host's sudo policy and cached credentials (PDR 003, Decision 2).

Before replacing the binary or its link, the installer checks an existing log directory
against the same ownership and mode requirements used by the system step. A missing directory
is created by that step; an existing invalid directory is left unchanged.

The steps:

1. Render the plist to a private temporary file and check it with `plutil -lint`. Record whether
   the user agent is loaded and whether it is disabled.
2. `sudo launchctl disable gui/<uid>/<label>` and `sudo launchctl bootout gui/<uid>/<label>`, then
   verify with `print-disabled` and `print` that the agent is disabled and not loaded.
3. `sudo install -o root -g wheel -m 0644` the plist, `sudo launchctl bootstrap system` it and
   `sudo launchctl kickstart -k system/<label>`.
4. Wait up to 90 seconds until `koinon status` answers with the PID that `sudo launchctl print
   system/<label>` reports. A daemon that answers with another PID is the former agent.
5. Remove the agent plist that the install wrote.

A failure in steps 2 to 4 removes the system service and plist again and restores the agent as
it was recorded: loaded or not, enabled or disabled. The error names the step
(`system_step_failed: STEP`). When the restore fails too, the error lists the `sudo` commands that
finish it (`rollback_failed`).

Without a desktop login, after a reboot or a desktop logout, the user has no `gui/<uid>`
domain: no agent runs, and `launchctl` refuses every command on that domain (`Could not find
domain` after a reboot, error 125 after a logout). The install then skips
the agent steps and removes the agent plist that it wrote, so nothing starts at the next login.
It refuses an agent plist with the daemon's label that it did not write, because that one would
start then.

There is one daemon. The presence of the system plist records system mode: a later `koinon
install`, with or without `--system`, stops and disables the agent again if it was started,
replaces the binary, rewrites the plist only when its content changed, bootstraps the service
when it is not loaded, restarts it and verifies it as in step 4. It never writes, enables or
starts the user agent, and the report says `"service": "system"`. `--no-start` is refused in
system mode (`system_mode_no_start`); on Linux `--system` is refused
(`system_mode_unsupported`), and the systemd user unit stays.

The system plist is shared by every user of the Mac. An existing one is used only when its
`Label` is the daemon's and its `UserName` is you; a daemon of another user is never stopped,
rewritten or removed (`system_plist_mismatch`, naming the field). A LaunchDaemon that you wrote
yourself, without the Koinon marker, counts as system mode when its `ProgramArguments` are `<prefix>/bin/koinon serve --state-dir <state>` (a trailing `--lock-wait`
is allowed) and its `UserName` is you. The install never rewrites it; it replaces the binary and
restarts the service. Any other plist at that path is refused before anything changes, with
`system_plist_mismatch` and the differing field.

`koinon uninstall` boots out and removes an owned system plist through `sudo`, and keeps the
state. The user agent stays disabled; a later plain install, with no system plist present,
enables and starts it again.

The LaunchDaemon's `PATH` is launchd's default, `/usr/bin:/bin:/usr/sbin:/sbin`, which holds no
Homebrew directory, so the daemon does not find a Homebrew `tmux` there (#418). The user
LaunchAgent sets no `PATH` either and gets the same default. The daemon
runs the `tmux` of the session's own terminal instead: `koinon mcp`, which runs under the
agent's `PATH`, reports the absolute `tmux` path when it registers the session, at the session's
first Koinon tool call and again when its record had expired or was retired. The daemon runs
that path only after it resolves every link to a regular executable owned by root or you and not
writable by its group or others. Nothing is reinstalled and no `PATH` is changed. After the
upgrade that brings this, restart each agent session that runs in tmux, for example with
`koinon codex`: a `koinon mcp` server that started before the upgrade reports no path. Until
a new one registers, a pane wake that finds no `tmux` on the daemon's `PATH` leaves its messages
queued with the reason `tmux_unavailable`.

### Upgrade the Go runtime

With Homebrew, `brew update` and `brew upgrade koinon` are the upgrade (see Homebrew above).
Otherwise, run `koinon install` with the new binary, with the same `--prefix` and `--state-dir`:
name the new binary by its path, such as the downloaded file, because the install copies the
binary that runs it. Check `koinon version` after the install. It replaces the binary in place
and restarts the daemon, and the state stays, with the recorded port (see Port above). A daemon that a service manager runs also exits
when its executable is replaced, and the manager starts the new binary (`binary_changed` in
the daemon log). A launched session continues:
`koinon mcp` tries a renewal that fails during the restart again at the next interval, and
the daemon keeps a session whose host process runs (liveness, see PROTOCOL.md). A session
without a host record, such as a DeepSeek session or one registered before host records, is
kept only by its renewals and tool calls. A running session keeps the `koinon mcp` server that
it started with, from the old binary: the install replaces the file, not the running program.
Tools that the new version adds appear when the session, or its MCP server, starts again.

#### Upgrade from a build without launch admission

v0.1.0 has no Claude launcher. It records a launch only for a session that `koinon codex`,
`koinon agy` or `koinon opencode` started. With the new daemon, a retained native session
without a launch association renews as a plain peer. It keeps its own inbox, acknowledgements,
native-session memory cursors and work claims. It releases any former participant address;
the participant's inbox, cursor and claims stay with that participant. DeepSeek retains its
command registration and participant behavior.

Run `koinon install` with the new binary. Start new sessions through their launcher when they
need a participant address, role, succession or terminal naming. Before replacing a native
session that has work, handle its inbox, finish or release its native claims and write a
handoff for the next session. A successor does not inherit those native claims or cursors.

A launched session retained without host evidence can still report `succession: refused`
with `no_host` when its successor calls. The maintainer's **Make holder** choice, or
`koinon participant holder ADDRESS PEER`, selects the successor immediately; otherwise the ordinary expiry and qualifier rules apply.
`koinon codex` uses `--no-daemon`; a directly started Codex session can still use the shared
app-server, and its registration remains active while its MCP server renews it.

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

   `uninstall.py` stops each Python session, and it takes a control socket that remains for
   the session as a running session. A session that crashed or was killed leaves its sockets:
   `control.sock` and `notifier/control.sock` in the session directory, or
   `/tmp/cc-socks/HASH-control.sock` when the direct path is too long. Before `uninstall.py`
   runs, the upgrade holds the writer locks of every session directory, which a running
   supervisor and notifier hold. Under those locks it removes each such file that is a socket
   of this user, refuses a connection and has no owner. A refused connection alone is no proof:
   a socket that is bound but not yet listening refuses, and so does a full listener on macOS.
   The owner check reads the kernel's socket table after the refusal: `/proc/PID/net/unix` for
   each network namespace of this user's processes on Linux, `netstat -anf unix` on macOS. The
   upgrade lists each removed file in `cleared_endpoints` of the report and the journal. A held
   lock, a socket that accepts a connection or a socket in the table gives `python_running`. A file that it cannot prove dead gives `python_endpoint_unverified` with
   the path: stop its owner, make sure that it is gone, remove the file and run the same
   command again.
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
  - the Codex SessionStart hook in `$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`),
    only while it runs this binary;
  - the OpenCode identity plugin;
  - the Claude status line, whose saved original is restored.
- The service file that carries the marker. It is removed only after the manager reports the
  service stopped. When the manager refuses the stop, or still reports the service running,
  uninstall refuses with `service_stop_failed` and keeps the service file and the binary.
- In system mode on macOS, the LaunchDaemon plist that carries the marker: `sudo launchctl
  bootout system/<label>`, then `sudo rm`. A plist without the marker is kept, and the error
  gives the commands that remove it (`service_artifact_unowned`).
- The binary.
- The `~/.local/bin/koinon` link, only when the install created it (`<prefix>/link`) and it still
  points at the installed binary. Any other link or file there is kept (`"link_result": "kept"`).
- Each `koinon-pickup` skill file (Install, step 7) that still holds this binary's skill, and its
  then empty directory; another content is kept (`pickup_skills`, `"result": "kept"`).

It keeps the state directory and reports its path. A repeated uninstall changes nothing.

## Requirements and manual operation

Fresh installation needs Linux or macOS and the binary for that operating system and CPU.
No Python interpreter is needed. Building from source needs the Go version in `go.mod`.
Git resolves repository identity for shared memory/work and repository launchers; a launcher
reports, and never refuses, other repositories nested in its start directory. Agent setup
requires the selected agent CLI. Codex must support `queue`, `--no-daemon` and the
`mcp_servers.koinon.env_vars` configuration override (verified with Codex CLI 0.161.0).
OpenCode must support its server. Claude background jobs require Claude Code's `--bg` and
`--settings` support. Grok Build must support `--session-id` and `grok mcp list --json`
(verified with Grok Build 1.0.50); setup reports another version and does not refuse it.
DeepSeek requires a running harness (live wake verified with DeepSeek Harness 0.2.0-rc.2, #199).

A legacy upgrade additionally needs Python 3.11+ to run the **installed previous release's**
uninstaller. The Go importer reads SQLite itself; it does not import a Python package from
this checkout. Keep the old installation and state until the upgrade has verified the import:
the upgrade reads them, and it removes the previous services only after that check (PDR 003,
Decision 3).

Without a reachable user manager, run the returned `start_command` in a persistent managed
session. The equivalent manual command is `koinon serve --state-dir STATE`; terminate that
owned process gracefully when finished. There is one daemon, not a bridge/notifier pair.
Until a daemon has started with the state directory, launchers and command clients refuse
with `no daemon has started with state directory STATE`. When the daemon has stopped, a
launcher refuses with `no daemon answers at ADDRESS`. The launcher does not start the agent.
Both refusals name how to start the daemon with that state directory, shell-quoted: at the
installed service's address, `koinon install --state-dir STATE` from a login session or `koinon serve
--state-dir STATE` in a persistent managed session. The installed service listens only at the
state directory's port (47671 unless `koinon install --port` recorded another), so at another
address the refusal names only `koinon serve --state-dir STATE --listen IPV4 --listen-v6 IPV6`,
with the address and the other loopback family on its port.
The default install uses no sudo, system service, lingering or permission change; with no user
service manager it reports the manual start command above. The system service that Koinon
supports is the explicit `--system` mode on macOS (PDR 003, Decisions 1 and 2). A host change
such as enabling systemd lingering is a separate operation that the person runs or assigns
under their own instructions and host policy (WDR 002, Decision 1; PDR 003, Decision 4).

## Configuration and agent access

Review [recommended native permissions](PERMISSIONS.md) for each family, including scoped
examples, configuration locations, verified syntax checks and their limits. Setup does not
apply these permission rules; native hook trust and optional terminal operations remain
separate. Read the installed family guide for the running version.

`koinon setup FAMILY` installs the MCP entry through the selected agent's CLI. Families are
`claude`, `codex`, `agy`, `opencode`, `grok` and `deepseek`; DeepSeek uses the explicit command path
until its MCP support is verified. `--cli ABS_PATH` selects the native CLI and `--binary
ABS_PATH` selects the installed Koinon binary. Setup does not create an agent conversation.
Codex and Antigravity identify the calling conversation on every MCP call; Claude uses its
native session environment and verified executable; OpenCode needs the managed identity
plugin; Grok uses the `GROK_SESSION_ID` that Grok Build gives its MCP server, with a verified
Grok terminal process as the server's parent. Start Claude Code, Codex, Antigravity, OpenCode
and Grok Build through `koinon claude`, `koinon codex`, `koinon agy`, `koinon opencode` and
`koinon grok` for participant features. After setup, a
native CLI started directly registers as a plain peer at its first tool call. It can use its
own inbox and repository memory/work, with no participant address, role, succession or
terminal naming (PDR 001, Decision 1). DeepSeek keeps command registration without a launch.

`koinon setup grok` adds the user-scope `koinon` server with `grok mcp add --scope user`. It
writes no permission rule; it reports the Grok Build version it saw and the recommended
`koinon grok --allow 'MCPTool(koinon__*)'`. Koinon supports Grok's default mode only: keep
`[cli] use_leader` off in Grok's configuration, because a session served by Grok's leader
process is refused with `grok_leader_mode` (#254).

`koinon setup codex` also adds a SessionStart hook, `'BINARY' hook codex-session-start`, to
`$CODEX_HOME/hooks.json` (default `~/.codex/hooks.json`). It keeps every other hook. A group
counts as Koinon's only in exactly the form setup writes; setup leaves one such group, for
this binary, in place of the first, and removal takes out every copy for this binary. Codex runs a new or changed hook only
after you trust it: at its next start it shows "Hooks need review"; choose to trust the
Koinon hook. Without it, a participant moves to the thread that `/clear` started only at that
thread's first Koinon tool call, not at its first turn. Run `koinon setup codex` again after
an upgrade that changes this hook.

`koinon codex` uses `--no-daemon` and forwards the launch environment to its configured MCP
server. `koinon claude --bg [args]` passes a job-specific private settings file and binds
registration to the returned job ID; see [USAGE.md](USAGE.md) for background launch usage.
When upgrading, existing launcher-family sessions without launch associations cannot renew
and expire at their existing deadlines. Their inboxes, acknowledgements, memory cursors and
claims are retained; start subsequent sessions through the launchers without replacing retained
state. [Upgrade from a build without launch admission](#upgrade-from-a-build-without-launch-admission)
gives the procedure.

For two agents of the same family in one repository, assign the additional agent a role
at launch, for example `koinon codex --role review`. The role
selects the participant address `<family>-<repository-label>-<role>`; without a role the
short address is retained. Roles use 1–24 lower-case letters, digits or hyphens, start with
a letter, and cannot consist only of hexadecimal digits and hyphens. Outside tmux, the
launcher names its new tmux session after that address, so each agent gets its own session.
Inside tmux, separate panes can use the same
repository. See [USAGE.md](USAGE.md) for holder conflicts and the maintainer's dashboard choice.
After registration, the held participant address (else the peer name) becomes the session's
tmux name once its host is verified in the pane. If another agent shares that tmux session,
only the own pane title changes. A taken name is retried at later renewals. Outside tmux,
`not_in_tmux` explains why naming did nothing. For `koinon claude --bg`, a single matching
`claude attach SHORT_JOB_ID` client in the user's tmux socket directory supplies the candidate
pane; no client retries and multiple clients rename nothing. A private server outside
`$TMUX_TMPDIR/tmux-<uid>` (else `/tmp/tmux-<uid>`) is not searched. `peers` and the dashboard
show fresh naming results with reasons for the current published name; see [USAGE.md](USAGE.md).
Schema 11 preserves existing aliases and holders as participants without a role; inboxes,
acknowledgements, memory cursors and work claims keep their existing owners.

Launched sessions record their host PID and process start value; MCP reports a tmux socket
and pane only after verifying that the pane holds the host, with the absolute `tmux` it runs.
Schema 28 adds that path to the session record; existing sessions start without one and run
`tmux` from the daemon's `PATH` until they register again (see System mode on macOS). A new native session in the same
host can succeed after the tool-call guard (30 seconds by default), and a new host in the same pane can
succeed once the former host is proven ended. `peers` reports `succession` with the evidence
or refusal. A `holder_active` refusal retries at the next tool call after the daemon's wait;
renewal and observation timers never trigger succession. A fenced old thread needs the
maintainer's dashboard choice. Schema 13 adds host records and succession results without
moving existing inboxes, cursors or claims; missing host records cannot prove succession.
Schema 14 adds the session timing settings with their defaults; a binary from before schema
14 refuses the upgraded database. Schema 15 adds read tracking, per-message notice counts and
the acknowledgement record; a binary from before schema 15 refuses it. After the upgrade, a
message that was notified and is still unacknowledged gets one new notice, because no read
of it was recorded before.

Read `koinon guide --agent FAMILY` at startup and after a reset. Discover the exact recipient
with `peers`, send only within the maintainer's authorization, read notices through `inbox`,
and acknowledge only messages already handled. Use MCP when available; command examples
and memory/work operations are in [USAGE.md](USAGE.md). Setup and runtime replacement change the
host; they run as part of a task the maintainer assigns, such as an install or upgrade, and that
task authorizes their steps (PDR 003, Decision 4; WDR 002).

## Installed skills and setup catalog

The binary carries the canonical skill/setup catalog; installation needs no checkout or
catalog directory beside it. Query it with `library_list`/`library_read`, or
`koinon library list/read --as FAMILY:NATIVE_SESSION_ID` for the existing command path.
See [USAGE.md](USAGE.md#skills-and-setup-catalog) for paging and examples. The version
report's `catalog_digest` identifies the catalog, including its canonical content and
metadata. Every MCP connection and family guide mention the query paths.

Machine-wide skills are the recommended default; project-local adoption is an explicit
alternative. The metadata includes supported destinations and native discovery evidence.
Unknown/unverified native roots support catalog reading; reading is not evidence of an
installed skill. List/read inspect no project or native-home files and record no adoption
decision. Installation and upgrade apply no catalog items; the one skill they write, the
managed cycle's `koinon-pickup` (Install, step 7), is Koinon's own entry and not a catalog item
(PDR 008, Decision 1). An adoption uses an exact
reviewed diff approved by the maintainer (PDR 008); locally edited files remain as they are.
`library_state` and `koinon library state` inspect declared targets in the calling native
client's verified environment. Codex's effective `CODEX_HOME` selects its machine target;
known same-name shared-user and project discovery copies are checked for collisions.
Unverified roots report an inspection limitation. Machine decisions are shared across
repositories for that family/root; project receipts share the repository's Git common
directory. Schema 21 preserves existing state and adds ordinary bounded machine decisions.
See [the adoption procedure](USAGE.md#inspect-and-adopt-an-item) before applying an item.

## Launcher paths in launchers.json

After successful `koinon setup claude`, `codex`, `agy`, `opencode` or `grok`, setup records the absolute
native CLI path it resolved in `launchers.json` under the selected Go state root:
`~/.local/state/koinon/go/launchers.json`, or `$XDG_STATE_HOME/koinon/go/launchers.json`.
`--state-dir DIR` selects a different root. The file is a JSON object, for example:

```json
{"claude": "/opt/agents/bin/claude", "codex": "/opt/agents/bin/codex", "agy": "/opt/agents/bin/agy", "opencode": "/opt/agents/bin/opencode"}
```

The root must be owned by the current user and private (normally mode 0700); the file
must be owned by that user, be a regular file with one link, have mode 0600 and fit within
16 KiB. Symbolic links and unsafe existing files are refused without changing permissions.
Setup preserves other families' entries, uses a private `launchers.lock` for concurrent
updates and replaces the JSON atomically. Repeat setup with the same path leaves it unchanged.

An install or upgrade runs setup for each `--agent` family. For another family it records the
CLI only when an earlier setup configured that family for the installed binary (step 6 of
the install). A launcher for a family without an entry records the family's CLI from `PATH`
and reports it; when the CLI is not on `PATH`, it refuses with `no FAMILY CLI is configured
in launchers.json` and names `koinon setup FAMILY` or `--cli ABS_PATH`; see
[USAGE.md](USAGE.md#launcher-paths-in-launchersjson).

To change a recorded path, rerun the authorized setup operation with
`koinon setup codex --cli /absolute/path/to/codex` (or the other family). A launcher searches
`PATH` only when the family has no entry; with an entry it uses the recorded absolute path.
`koinon codex --cli /absolute/path/to/codex` overrides it for that
launch without editing the file. Match `--state-dir` between setup and launches. Uninstall
preserves this state-root configuration along with other retained state.

## State, recovery and compatibility

The Go default state root is `~/.local/state/koinon/go` (or `$XDG_STATE_HOME/koinon/go`). It
holds `state.sqlite3`, the private secret and daemon lock, and the `port` file that `koinon
install --port` writes. Directories are owned by the current UID and private; credentials never
belong in Git or logs. The daemon binds loopback only. Custom state and address must match
between the daemon, command clients and MCP server; a recorded port matches by itself for every
client of that state directory.
The dashboard uses one-time login links, a login cookie and CSRF-protected actions. A login
ends after 24 hours without a request, 7 days after it was made, or when the daemon restarts;
then run `koinon dashboard` for a new link.

The daemon applies supported Go schema migrations transactionally and refuses an unknown
future schema. `koinon status` reports health, storage, delivery and retention counts. The
daemon deletes acknowledged messages 30 to 60 days after their acknowledgement, and expired or
retired sessions 30 days after their activity ended, when they hold no unacknowledged message
and no live claim. Unacknowledged messages, memory entries and work items are kept by these
rules; a purge mark, from the dashboard or `koinon sessions purge`, deletes a session with its
whole inbox at the next sweep, which `koinon maintenance sweep` runs at once (docs/USAGE.md). Correct the
reported condition before using `koinon recover` for blocked storage. Capacity refusals
preserve existing records; a failed or timed-out request alone does not prove a write failed.
Use the same idempotency key and deadline to resolve uncertain writes within their horizon.
An unavailable daemon is not permission to replace its state or change a secret.

`koinon recover` asks a running daemon to end a storage block, after you corrected its cause.
When no daemon answers, `koinon recover` and `koinon status` fail with `daemon_unavailable` and
name the installed service mode (`service`: `system`, `agent`, `systemd` or `manual`) and the
`restart_command`: `sudo launchctl kickstart -k system/io.github.rwcii.koinon.daemon` in macOS
system mode, `launchctl kickstart -k gui/<uid>/io.github.rwcii.koinon.daemon` for the launchd
agent (which needs your desktop login session), `systemctl --user restart koinon.service` on
Linux, or the `koinon serve` command when no service is installed. Koinon prints the command
and never runs `sudo` itself. Both commands use loopback HTTP, not launchd, so they work over
SSH; in system mode the daemon also runs without a desktop login.

After an outage longer than the session lifetime, a running agent session does not need a
restart. `koinon mcp` registers it again with the same identity at its next tool call, or at
its next renewal when it is idle, and it takes back its participant address when no other
session took the address meanwhile. When another session took it, that holder change retired
the old session, which then stays retired.

The daemon writes one JSON line to standard error for each lifecycle event: `start` (version,
state directory, listeners), `stop` with its `reason` (`signal` and its name, `canceled`,
`error` with a fixed code, or `lock_held`), `lock_wait` and `lock_acquired`. The lines hold no
secret, message body or session ID. On macOS the launchd agent writes them to
`~/Library/Logs/koinon/daemon.log` (directory mode 0700). Installation refuses an existing log
directory that is not private to this user (`service_log_unsafe`) and does not change it. On
Linux the lines are in the user journal (`journalctl --user -u koinon`). Read them to find when and why a daemon stopped. `koinon
status` reports the daemon's `pid`.

Only one daemon holds the daemon lock. The service starts the daemon with `--lock-wait 60s`: a
second daemon, for example a service started while a manual `koinon serve` runs, waits up to 60
seconds for the lock and then exits with status 0 and `lock_held` in the log. The launchd agent
and the systemd unit do not restart a clean exit, so it stays stopped instead of restarting
every few seconds. A manual `koinon serve` without the flag still fails at once.

Memory and work share the daemon database, with one logical store per resolved Git common
directory, including worktrees. Snapshot pages and deltas still require explicit
acknowledgement. Work claims are advisory leases, not permission to perform actions. See
[PROTOCOL.md](../PROTOCOL.md), [the work contract](WORK-ITEMS-V1.md) and
[Go storage limits](WORK-ITEMS-GO-STORAGE.md). Python-era implementation evidence under
historically marked documents is retained for migration provenance, not as an active recipe.
