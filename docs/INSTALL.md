# Install and enable Koinon

## Work-item capacity planning

Memory startup now uses schema 5. Follow the [upgrade procedure](WORK-ITEMS-UPGRADE.md)
before replacing a running service. Explicit work guidance is a separate opt-in.
Before choosing this workflow, check that the repository's retained workload fits these finite
budgets. V1 supports bounded work, not indefinite sustained progress reporting.

| Limit | Capacity implication |
| --- | --- |
| 128 retained work items | Finished and withdrawn items still count during retention. |
| 2,048 retained work events across all items | Control reservations reduce ordinary admission; an open item's history does not expire. |
| 12 MiB work logical usage within 32 MiB shared logical usage | Records, scope history and events compete for this budget. |
| 16 retained writer bundles | Inactive bundles awaiting cleanup still count; storage admission can bind sooner. |
| 64 scope revisions per item, 1,024 total | Long-running items have finite revision history. |
| 20,000 shared replay records | Other memory operations and work control reservations also consume this budget. |

Strings, payloads, optional resource claims, live leases, query results and maintenance
batches also have explicit bounds. The complete [work budgets](WORK-ITEMS-IMPLEMENTATION-DESIGN.md#budgets-and-progress-reserves)
and [storage accounting](WORK-ITEMS-STORAGE.md) give the reviewed limits. These are
independent ceilings, not a promise that all maxima fit simultaneously.

The 128 MiB combined database/WAL ceiling is **not ordinary write capacity**. With all
16 claim bundles funded, their reserved debt is 10,240 pages and 1.5 MiB of logical
space. The remaining ordinary database band is 4,038 pages, about **15.8 MiB**, before
the append allowance and other limits apply. With no claim credits at schema 4, the
reviewed ceilings are 14,278 ordinary pages, 16,326 control pages, 4,936 ordinary
entries, 20,000 replay rows, and ordinary logical usage of 32 MiB minus the shared
reserved bytes. These values describe admission accounting, not usable file-space
estimates or instructions to change a store's schema.

Sixteen writers reporting hourly reach the 2,048-event ceiling in roughly **5.3 days**,
sooner after other mutations or reservations. Even one long-running item's history can
exhaust capacity before any finished history is eligible for removal. Renewing a lease
alone emits no work event, but still consumes shared replay-record capacity; meaningful
progress checkpoints consume event capacity as well.
There is no early history trimming. The design's example of 60 work blocks averaging
20 events and 4 KiB per event is 1,200 events and about 4.7 MiB of event payload: a
sizing example, not a throughput guarantee.

At capacity, new ordinary work mutations are refused while live items, checkpoints,
claims and recovery information are preserved. The refusal identifies the exhausted
dimension, retained usage, outstanding reservations, and earliest eligible finished-item
expiry, or explicitly none. Funded release, finish and expiry bookkeeping retain their
reserved capacity; an exhausted ordinary event budget does not prevent ending an
accepted active claim. This is a storage-admission promise, not immunity from disk
failure or corruption.

Finish only genuinely completed work or withdraw work that is actually abandoned.
Finished and withdrawn items and their history become eligible for cleanup after
30 days; active items remain preserved. Eligible cleanup frees rows and makes pages
reusable inside the database, but **does not vacuum or reduce allocated page count**.
Ordinary writes may therefore still refuse after rows have been reclaimed. Returning
pages uses the existing note-admission/reclaim path or explicit store recovery; work
admission does not itself trigger vacuum. At or above the applicable control ceiling,
cleanup has no general progress guarantee. Check the timestamped maintenance status
and faults instead of assuming every interval reclaimed capacity; see
[maintenance and recovery limits](WORK-ITEMS-MAINTENANCE.md).

Do not mark incomplete work finished to free history, rotate to an untracked store,
delete active records, or shorten retention as recovery. Higher sustained workloads
require a separately reviewed capacity/retention design. General event-history pruning
is outside v1.

## Name and path compatibility

Koinon was previously named Codex Peer Bridge. Fresh installations use
`~/.local/share/koinon`, state under `$XDG_STATE_HOME/koinon` (or
`~/.local/state/koinon`), and `koinon-*` service names. The examples below use those
fresh-install names. Existing installations retain their selected paths and names.

The installer first selects the prefix: an explicit `--prefix` wins; otherwise,
a lone legacy `~/.local/share/codex-peer-bridge` is reused. If both prefixes exist,
specify the intended prefix. It then reads saved installation configuration.
Explicit state and unit paths win over saved paths; saved paths win over defaults.
Without saved state configuration, a lone legacy state root is reused. Both state
names present cause a refusal that names both paths. Default selection reports its
path and reason on stderr. No state, cursor, memory store, registration or lock is
moved or reset. Invalid configuration refuses without falling back to empty state.
Historical explicit-thread installations without `install.json` must repeat any
custom state and unit paths on upgrade. Installer updates now preserve unrelated
configuration fields under a permanent `.install.lock` with atomic publication.
The [work-policy and guidance commands](WORK-ITEMS-POLICY.md) add explicit
repository/participant opt-in after a normal runtime installation. They preserve
unrelated guidance and support interrupted-publication recovery. Starting memory with
this runtime creates or migrates schema 5; follow the [upgrade procedure](WORK-ITEMS-UPGRADE.md).

Installation configuration must be a regular file owned by the current user and
not writable by group or others. An older `install.json` with mode 0664 is refused
with `invalid_install_configuration`; its contents are preserved. Verify the selected
prefix and expected owner, then explicitly restore owner-only write access (for example,
mode 0600 on that verified file) before retrying. The installer does not silently repair
ownership, permissions, symlinks, or malformed configuration. Ordinary 0600/0644 files
remain readable. Concurrent installers wait up to 30 seconds for `.install.lock`;
`configuration_busy` with exit 75 means check the other installer and retry, never
remove the permanent lock to force progress.

Existing owned `codex-peer-*` unit names remain in use. Both old and new ownership
markers and participant guidance sections are recognized for upgrade and removal.
The implementation is `koinon/participant_instructions.py`; `koinon/codex_instructions.py` remains
an import shim. The guidance lock filenames remain unchanged so old and new
updaters cannot write at the same time. Do not replace markers or delete locks by hand.

The Claude registry entrypoint remains `codex-peer-bridge`; the memory handshake
remains `codex-peer-memory`. Older clients depend on these values. The account-local
lock namespace remains `koinon-locks`. These are compatibility identifiers, not
unfinished branding. See the [migration contract](IDENTIFIER-MIGRATION.md).

A source update does not install or restart a runtime. Existing checkouts and
worktrees can keep their directory names. Moving them is a separate operation:
repository identity, peer registration and memory can depend on their paths.

## Requirements

Linux or macOS, Python 3.11+, Claude Code with local peer messaging, and — for a Codex
participant — a Codex CLI that supports `codex queue --thread ... --message ...`. Agents
must run under the same OS user. Verify `codex queue --help` and `python3 --version`.

A DeepSeek (DSH) participant needs no Codex CLI. It needs the running harness, which
exports `DSH_HOME`, `DSH_SESSION_ID` and `DSH_WEB_URL` to a session's shell.
`--configure-deepseek` alone does not require Codex. Installations with a saved or
selected Codex participant, and explicit `--thread` mode, still require it.
The executable selection order is explicit `--codex`, a saved installation value
that is still an executable file, then Codex on `PATH`; an invalid selected executable fails before files are written.

On macOS the system `python3` is often 3.9, which is below the floor; use a 3.11+
interpreter explicitly, for example `python3.12`.

The bridge targets an existing conversation. A standalone API key or unrelated
Codex daemon does not provide access to that conversation. Hosted clients without
local shell/queue access are not automatically supported.

### Directory permissions

Install and upgrade refuse a path that a second account can write. An ancestor must be a
directory owned by this user or by root and must not be group- or other-writable; the
selected path itself must be owned by this user and must not be group- or other-writable.
A root-owned sticky directory such as `/tmp` is the one place a writable mode is allowed,
and that exception never applies to the selected path itself.

Three checks apply this rule to paths an operator creates, and each reports the condition
that failed: the upgrade source checkout and every ancestor of it, the manager registration
path and its existing ancestors, and the parent of a selected guidance file. When the write
bits are the cause, the report adds the mode and the remedy; a wrong owner, or a file where a
directory was expected, is reported as itself instead. The installation prefix is checked
too, but it reports the fixed code `invalid_install_configuration` without that detail, so
read this section when you meet it.

An account whose `umask` is `002` creates directories with mode `0775` unless something
else sets the mode, which the rule refuses. Check the paths before the first install:

```sh
umask                 # 0002 makes a new directory group-writable unless its mode is set
ls -ld ~/.local ~/.local/share ~/.local/state ~/.config
chmod go-w ~/.local ~/.local/share ~/.local/state ~/.config
chmod -R go-w /path/to/checkout
```

A refusal caused by the mode names the path, that mode and the remedy, so a fault report
should quote it in full:

```
{"error": "unsafe manifest ancestor: /home/you/koinon (mode 0775): remove group and other write permission, for example chmod go-w", "ok": false}
```

A group-writable directory is refused even when its group appears to hold only the owner.
A group entry does not list the accounts whose primary group it is, and account enumeration
can be partial or unavailable, so an exclusive-looking group cannot be shown to be
exclusive. Koinon never repairs permissions and never adopts a path; the operator makes the
change.

## Repository components and native supervision

Select a repository to install participant guidance and its shared memory service in
one invocation. An explicit thread also stages and starts that existing session:

```sh
python3 scripts/install.py --configure-codex --repo /path/to/repository
python3 scripts/install.py --configure-codex --repo /path/to/repository --thread EXISTING_THREAD
# Memory alone needs Git and Python, but no Codex or DeepSeek executable:
python3 scripts/install.py --configure-memory --repo /path/to/repository
```

`--configure-deepseek --repo REPOSITORY` selects the same memory component alongside
harness guidance. `--configure-memory --thread EXISTING_THREAD --repo REPOSITORY`
selects memory and that Codex session without modifying global participant guidance.
A fresh `--thread EXISTING_THREAD --repo REPOSITORY` installation also selects both
components. Guidance-only commands without `--repo` and legacy thread-only commands
without `--repo` retain their previous scope. Repeating an existing installation does
not add an unselected repository service: use `--configure-memory --repo REPOSITORY`
to opt in explicitly. Existing unmanaged units and sessions are not adopted.

The default backend is systemd on Linux and launchd on macOS. Linux memory uses
persistent user registration and login enablement; session jobs use runtime registration
without login enablement. macOS memory uses the account's `Library/LaunchAgents`
directory and the selected GUI user domain. Session jobs are bootstrapped explicitly.
No system service, sudo, or lingering configuration is created. Without an available
user manager, startup reports `manual_required` with the exact command to keep running
in a persistent terminal or managed tool session. `--service-backend manual` selects
manual operation explicitly; changing a saved backend requires reconciliation.

Add `--no-start` to stage runtime, configuration, and selected artifacts without querying
or starting a manager. A later invocation without that flag activates the selection and
checks live readiness. Repeating the same selection preserves stores and registration.
Linked worktrees share the service selected by their Git common directory; a second
repository receives its own service. Inspect memory with:

```sh
python3 /path/to/prefix/memory_service.py status --prefix /path/to/prefix --repo /path/to/repository
```

Native installation requires owned, non-group-writable and non-other-writable ancestors
for the prefix, state, and registration paths. An existing group-writable `.local` or
`.config` directory is refused even when its group currently contains only the owner.
The refusal names the offending path. Review its ownership and sharing requirements,
then choose a suitable private location or explicitly correct that directory yourself.
The installer creates missing directories privately and never changes existing ancestor
permissions. This precondition also applies to the native CI fixtures.

Runtime files belonging to a selected native installation cannot be replaced with
different bytes by an ordinary repeat install. Coordinated runtime upgrades are a
separate operation; preserve the current installation when this refusal occurs.

## macOS

Use Python 3.11+ and a logged-in GUI user for launchd supervision:

```sh
python3.12 scripts/install.py --configure-codex --repo /path/to/repository
```

Guidance-only installations without a repository retain the manual supervisor path.
Their `session.py ensure` reports `manual_required`; run its exact `start_command` in a
persistent session. The historical systemd-only explicit-thread path remains unavailable
on macOS; use the repository component invocation above instead.

### Recovering from a killed instance

A start binds exclusively and never removes a socket it did not create, so a bridge killed
with `SIGKILL` leaves its socket behind and blocks the next start. The failure names the
path in a JSON diagnostic on stdout and exits with status 78. For example
(the operating-system error number can differ):

```json
{"ok": false, "code": "endpoint_unavailable", "error": "cannot bind /tmp/cc-socks/<hash>-control.sock: [Errno 48] Address already in use"}
```

On Linux, installed systemd bridge and session services prevent automatic restart
on exit 70 (internal software error) or 78 (configuration refusal). On macOS,
these errors end the manually started process; after
correcting the path, start it again with the command described in the
[macOS setup](#macos). The macOS leftover-socket note at the end of this recovery
section also applies. An unsafe startup directory also reports this refusal. Correct the named path
before starting the service again; do not bypass its ownership checks.

Proving the owner is gone cannot be done by connecting. A live listener whose accept queue
is full refuses a connection on macOS exactly as a dead owner does, so a refusal is not
evidence. Use `lsof`, which shows the owning process only when one exists:

```sh
lsof /tmp/cc-socks/<hash>-control.sock    # no output means nothing holds it
rm /tmp/cc-socks/<hash>-control.sock      # remove that one path
```

If `lsof` does print a process, the bridge is still running: stop it with `session.py stop`
or `bridge.py stop` rather than deleting the file. Then the same for the peer socket if its
error was reported too.

Remove only the specific path from the error. Never clear `/tmp/cc-socks` or the session
registry wholesale, and never remove a socket `lsof` reports as held.

The `<hash>` name appears when the state directory is too deep for a Unix socket path.
It is the first 16 hex characters of `sha256` of the **resolved** state directory, and it
can also be read directly:

```sh
python3 -c "import bridge,platform_support,pathlib;print(platform_support.control_socket_path(pathlib.Path('<state-dir>')))"
```

For a shorter state directory the control socket sits at `<state-dir>/control.sock` and the
same procedure applies. macOS has no automatic temporary-directory cleanup, so a leftover
socket stays in the way until it is removed by hand.

## Recommended: configure Codex once

```sh
git clone https://github.com/rwcii/koinon.git
cd koinon
python3 scripts/install.py --configure-codex
```

For a DeepSeek (DSH) participant, install the harness guidance instead of, or as well as,
the Codex guidance:

```sh
python3 scripts/install.py --configure-deepseek            # uses $DSH_HOME
python3 scripts/install.py --configure-deepseek --dsh-home /path/to/harness
```

This manages a clearly marked DeepSeek section in `$DSH_HOME/AGENTS.md`, leaving all other
content untouched. Each participant has its own delimited section and markers, so the two
can be installed and removed independently. Repeated configuration retains previously
registered participants and uses their recorded homes when home flags are omitted.
Uninstallation removes every managed section recorded by the installation.

This installs runtime files in `~/.local/share/koinon` and adds a clearly
marked section to `$CODEX_HOME/AGENTS.md` (normally `~/.codex/AGENTS.md`). If a global
`AGENTS.override.md` already exists, the installer manages that higher-priority file
instead. Existing content is preserved; a private backup is saved before the first
edit. Reinstallation replaces only the managed section. Removal strips the section
without restoring an old backup over subsequent user edits. Recovery copies are
atomically published and flushed before guidance replacement. A failed flush is an
error; retrying reconfirms the retained copy before proceeding. Guidance publication
and work-guidance retries also flush the retained target before reporting success.
Linux uses `fsync`; macOS additionally requests `F_FULLFSYNC` for files through the
shared platform primitives. These guarantees depend on filesystem and device support;
fault-injection tests do not establish physical power-loss durability.

The durability audit for gate #69 also covers these recovery records:

| Publication site | Decision and reason |
| --- | --- |
| `participant_instructions` and `work_guidance` | Needs durability: preserve the original text before overwriting guidance and retain it across interrupted retries. |
| `notify.save` | Needs durability: `session.save_registration` uses it for the saved session identity and assigned peer name. It is not merely a disposable notification cache. |
| `memory.write_owner` | Needs durability: the ownership record is needed to recover a socket left after an unclean exit. |
| `uninstall_finalize.publish` and directory synchronization | Needs durability: the recovery program and manifest must survive before runtime deletion begins. The generated helper embeds the shared flush primitives for the installation platform, so recovery still works after installed modules are removed. |

All these file publications use the shared file flush and flush the parent directory;
uninstall retries reconfirm the recovery files before continuing deletion.

The section instructs each Codex conversation to run `session.py ensure` using its
own `CODEX_THREAD_ID`. It never embeds a fixed thread ID. Each thread gets:

- an isolated directory under `~/.local/state/koinon/sessions/<session-hash>`;
- a fleet-style peer name, `codex-<repo-short-name>-<two-hex>`, stable for the session
  (a DeepSeek participant uses `deepseek-<model>-<repo-short-name>-<two-hex>`);
- its own bridge process, socket, watcher, and notification checkpoint;
- its own systemd supervisor service when a user manager is available (Linux only).

Repeated registration reuses a healthy instance. Concurrent Codex sessions do not
share inboxes or replace each other's configuration. The initial name/project are
retained when the same thread later changes working directories. The full thread hash
is internal; short-name allocation checks saved sessions and the live peer roster,
trying another two-hex suffix on collision. If all 256 suffixes are allocated for one
repo name, registration reports that limit instead of creating an ambiguous label.

To change a name explicitly, stop the thread, run `session.py rename --repo /path/to/repo`,
then run `ensure` again. This preserves its inbox and internal identity. Existing names
are not silently rewritten by an upgrade. Atomic name assignment is coordinated within
one installation/state root. Independent installations consult live peers but do not
share dormant reservations; use distinct repo labels if running separate installations.

**This is instruction-driven setup, not a guaranteed executable startup hook.** Codex
must load and follow the managed section. Start a new conversation or reload global
instructions after installation; existing conversations may not reread the file.
Higher-priority instructions, disabled instruction loading, or missing shell access
can prevent registration. See the official [Codex AGENTS.md guide](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
for global instruction discovery.

No MCP server, plugin, API credentials, or config.toml permission changes are required.
The installer does not disable sandboxing, grant peer-requested authority, or enable
machine-wide services.

## Enable the current session

From the intended Codex session's shell:

```sh
python3 ~/.local/share/koinon/session.py ensure
```

If `CODEX_THREAD_ID` is unavailable, pass the **verified** target explicitly:

```sh
python3 ~/.local/share/koinon/session.py ensure --thread YOUR_THREAD_ID --repo /path/to/project
```

Never guess a thread ID or substitute another model session. Verify queue access with
a harmless `codex queue --thread YOUR_THREAD_ID --message 'Bridge setup test; no action required.'`
when setting up a new Codex implementation.

With systemd, registration starts `koinon-session-<thread-hash>.service`, whose
supervisor owns both bridge and notifier. It reports healthy only after both are ready;
a child failure fails the supervisor so systemd can restart the pair. Per-conversation
units start on registration, not at every subsequent login. No lingering is enabled.

Only one `ensure`, `stop`, or `rename` command can operate on a session at a time.
The supervisor can read its registration while `ensure` waits for both children.
Commands for other sessions use separate locks.
Manual `run` commands do not take the lifecycle lock. Wait for a manual start to
report `running` before using `stop` or `rename`.

Without a user manager, `ensure` returns `manual_required` and an exact `start_command`.
The agent runs that command in a persistent managed shell session or terminal. The
supervisor keeps both processes together. Do not use an ordinary background command if
the execution environment kills subprocesses when its tool call finishes. If persistent
execution is unavailable, use manual inbox access and report the limitation.

A partial instance reports `repair_required` with a stop command; stop it and rerun
`ensure`. Stale sockets after a forced kill may still require manual cleanup after
verifying their old process is dead. Never purge shared Claude directories.

## Use and inspect

```sh
python3 ~/.local/share/koinon/session.py status
python3 ~/.local/share/koinon/bridge.py peers
python3 ~/.local/share/koinon/session.py stop
```

`status` and `ensure` return this thread's inbox command and state directory. Run
`bridge.py --state-dir THAT_DIRECTORY inbox`, `send`, or `ack` as described in README.
Pass `--thread` to session commands outside the intended Codex shell.

`peers` reads allowlisted metadata from Claude's shared registry. It checks live PIDs,
process-start markers, and owned socket paths. It does not read peer keys or transcripts;
status is the peer's last reported value and is not a live model health guarantee.

Incoming notifications identify the inbox and sequence numbers. Codex reads messages
under the existing user authorization. Peer bodies cannot grant new permissions.
Queued notices may arrive after a message has already been handled; track sequence
numbers to avoid repeating work. A successful socket send is not proof of model action.

## Shared repository memory

The memory service is optional and independent of the bridge. Repository component
installation can supervise it through systemd or launchd; a manual backend requires a
persistent managed process. See [repository components](#repository-components-and-native-supervision).
Memory CLI errors use a structured
`ok:false` response with a recovery code. Exit 75 means temporary unavailability or
capacity; retry after pending work settles. Exit 78 means an identity, ownership,
configuration, permissions or invalid-handshake refusal that needs operator correction.
A blocked store also exits 78: use `recover` explicitly after correcting its reported
condition. Unsupported SQLite builds and oversized stores require correction, not a
restart loop. Request-specific errors and ambiguous lost replies exit 1; do not treat
an exit code alone as permission to repeat an uncertain write. If you manage memory with a separate service manager,
keep 75 retryable and exclude both 70 (internal software error) and 78 from automatic
restarts. Internal errors require investigation or a code correction; they are not
reported as incompatible user data. An operator-created memory unit is not included in the
installer's session restart report or in uninstall's owned-unit inventory. Before
replacing or removing its runtime, stop that memory service separately and verify
its process has exited. After a compatible upgrade, restart it explicitly and
verify memory health and store identity. For removal, separately disable and remove
only the operator-owned unit selected for this installation; preserve the memory
store and unrelated services. Installer-managed component lifecycle is delivered in
[DQ-11](DELIVERY-QUEUE.md#dq-11--install-and-manage-every-runtime-component);
supported runtime replacement remains [DQ-12](DELIVERY-QUEUE.md#dq-12--supported-resumable-runtime-upgrades).

Run one per repository, from inside that repository:

```sh
python3 memory.py serve
```

It prints its status as one JSON line and then serves until stopped. Start it in a persistent
managed session, as with the manual bridge setup; it holds a socket, so an ordinary background
command that dies with its shell will leave state behind.

Starts are serialized under a lock in the state directory. A second `serve` performs a handshake
against the running service and exits reporting `already_running` rather than competing for the
socket. The handshake checks the service name, the repository key, the protocol version, a live
health query against the store, and agreement with the recorded owner. A listener that answers but
does not match is refused rather than reused.

### Paths

State lives under `<state-dir>/memory/<repository-hash>/`, beside the bridge's session
directories and using the same 0700 directory and 0600 file rules. The repository hash comes from
`git rev-parse --path-format=absolute --git-common-dir`, so every worktree of one repository maps
to one directory. That directory holds `memory.sqlite3`, `owner.json`, `start.lock`, and the
control socket, unless the path would exceed the kernel's `sun_path` limit, in which case the
socket falls back to the shared peer socket directory exactly as the bridge's does.

Use `--state-dir` and `--repo-path` to run an isolated instance for a preview or a test. Give a
distinct state root when you intend an independent installation.

### Stopping and recovery from a killed instance

Stop the service with `python3 memory.py stop`, or with SIGTERM or SIGINT. A clean stop drains
requests already in flight, closes the database, and removes only the socket and ownership record
it owns.

A killed service leaves its socket and its ownership record behind. The next `serve` recovers
automatically, but only after proving the previous owner is gone: it compares the recorded process
ID and process start marker against the live system. If that process is still running, or if its
state cannot be read, the socket is left in place and the start is refused with `socket_in_use`.
Nothing removes a socket because a connection was refused, since a live listener with a full
accept queue and a dead owner are indistinguishable by probing.

If you must clear state by hand, verify the recorded owner in `owner.json` is dead first, and
remove only files inside that repository's own directory. Never clear the shared socket directory.

### Upgrades

Stop the repository's memory service before replacing runtime code, then start it again. The
database carries its schema version and refuses a state directory belonging to another repository.
An existing store opened by a runtime that provides full-text search when the previous one did not
backfills its index on first open; no manual step is needed. Do not run memory commands from an
older runtime during an upgrade.

## Paths and options

`--prefix`, `--state-dir`, `--unit-dir`, `--codex-home`, and an absolute `--codex` path
support customized installations. An explicit invalid `--codex` is refused even in
DeepSeek-only mode. To add Codex later, rerun with `--configure-codex` and a valid
executable; `session.py ensure` and `run` refuse Codex startup without one.
In Codex-wide mode, `--state-dir` is the root for
all per-thread directories. Installed `install.json` records these private local paths;
do not commit it. `--no-start` with `--configure-codex` installs files/guidance without
registering a thread. It still edits the selected Codex instructions; use temporary
paths for a dry-run preview.

The pre-existing explicit single-thread mode remains available:

```sh
python3 scripts/install.py --thread YOUR_THREAD_ID --name codex-project --repo /path/to/project
```

For a fresh prefix, that repository-scoped invocation selects the session supervisor
and repository memory without adding global guidance. Existing legacy selections keep
their original scope unless memory is explicitly selected with `--configure-memory`.
Thread-only installation without a repository retains the legacy fixed pair. Existing
prototype or legacy inboxes are never adopted automatically; deliberate migration must
preserve their ownership and prevent duplicate registration for one conversation.

## Notifier ownership

The notifier takes its state-directory lock first, then a lock for the provider and
session identity across the OS account. Both acquisitions are nonblocking. A conflict
fails startup before registration or delivery, with `participant_in_use`, the provider,
the `account-local` scope, and the lock digest. Match that digest to `participant_lock`
in `session.py status` or the running notifier's private `notify-ready.json`. Under systemd, read the refusal JSON with
`journalctl --user -u koinon-notify -n 50 --no-pager` for a fresh fixed pair (use the retained name after upgrade), or
`journalctl --user -u koinon-session-INSTANCE -n 50 --no-pager` for a session.
Ownership refusals exit with status 78. Both service forms prevent automatic restart
on that status; the supervisor preserves it after stopping its bridge child. Correct
the reported condition before explicitly starting the instance again. Stop an
unwanted instance through its own session command; do not remove a lock file to bypass it.

Participant identity compares exact UTF-8 bytes; callers must supply the provider's
canonical session ID. Different spellings are not normalized into one identity. Real
and effective user IDs must match; set-user-ID execution is refused as `uid_mismatch`.

The shared namespace is derived from the effective user's operating-system account
entry, not `HOME`, `XDG_STATE_HOME`, `CODEX_HOME`, `DSH_HOME`, or the delivery URL:

- Linux: `<account-home>/.local/state/koinon-locks`.
- macOS: `<account-home>/Library/Application Support/koinon-locks`.

An unavailable account home produces `account_home_unavailable`; startup does not fall
back to a different namespace. A state directory equal to or below the resolved lock
namespace is refused, including symlink aliases. An ancestor state directory is allowed;
removal must preserve the shared namespace and must not recursively purge that state root.

Lock files persist after exit and uninstall. Each distinct historical provider/session
identity adds a zero-length file; inode and directory-entry use is not bounded by a
journal or memory-store budget. Never unlink these files while notifiers may use them.
Descriptors are close-on-exec, so a launched delivery command cannot retain the lock
once the notifier exits. Unrelated tools that do not take this lock are not coordinated.

When upgrading from a runtime without participant locks, stop and upgrade all Koinon
notifiers that could target the same participant before restarting them. An older
notifier in another state directory does not hold the new lock and cannot be excluded
by it. Preserve inboxes, checkpoints, registration targets and unrelated bridge instances;
do not treat installing files with `--no-start` as activating the new exclusion rule.

## Upgrades and removal

Ordinary reinstall refuses changed runtime bytes when component selections exist.
Replacing them is a supported operation, not a runbook. Run it from a complete checkout
of the new release against the installed prefix:

```sh
python3 /path/to/new-source/scripts/upgrade.py --prefix /absolute/installed/prefix --source /path/to/new-source
python3 /path/to/new-source/scripts/upgrade.py --status /absolute/installed/prefix
python3 /path/to/new-source/scripts/upgrade.py --resume /absolute/installed/prefix/.upgrade/OPERATION --plan PLAN_DIGEST
```

The three modes are mutually exclusive, and `--resume` requires the plan digest that
`--status` reports. The operation is resumable from its recorded phase, takes its own
consistent backup, and produces a before/after verification report naming what it
preserved and what it found. It reports any memory service the installation does not
own rather than adopting or passing over it. It also carries a release across a declared
layout change, so a file that moved between releases is retired only after its new path
is published and confirmed; see [WORK-ITEMS-UPGRADE.md](WORK-ITEMS-UPGRADE.md) for the
retirement rules and the manual-backend handoff.

When the memory component's selected backend is `manual`, the operation stops at that
component and returns `manual_handoff_required` with the exact start command. Run it in a
persistent managed session, keep it alive, then resume with the plan digest. The operation
never detaches a process, and a printed command is not readiness.

This is a property of the selected backend, not of the platform. macOS installs launchd
components like any other supported host, and they are upgraded without a handoff. An
unreachable user service manager is a refusal to be corrected, not an automatic fallback to
`manual`.

**Precondition.** The operation validates the source checkout it reads from, under the same
rule as installation; see [Directory permissions](#directory-permissions). Each ancestor must
be a directory owned by this user or by root, and must not be group- or other-writable —
except a root-owned sticky directory such as `/tmp`, which is allowed. The checkout itself
must be owned by this user and must not be group- or other-writable. An account using
`umask 002` creates `0775` directories, so the operation refuses with `unsafe manifest
ancestor`, naming the path, its mode and the remedy. The selected checkout is validated
inside the same ancestor walk, so it is reported the same way.

The [stopped-state runbook](WORK-ITEMS-UPGRADE.md) remains for legacy and manual
deployments that the operation does not cover, preserving the same prefix, state paths
and targets. Never silently reset a checkpoint.
Do not run session commands from an older runtime during an upgrade. Older commands
do not use the lifecycle lock that protects session startup. Already-installed code
that predates upgrade markers may report `invalid_install_configuration` instead of
`installation_upgrading` while the operation is active. This is a compatibility
limitation of that old reader, not evidence that the retained configuration needs
repair. Preserve the configuration and recovery directory; use the selected upgrade
operation's status/resume entrypoint. Do not repair, reinstall or delete its marker
to bypass this refusal.

For the peer-message guidance update, an operator may stage the compatible runtime
files and replace each file atomically, installing `koinon/peer_guidance.py` before its
importers, without stopping existing sessions. Preserve `install.json`, all state,
units, and unrelated global instructions. The updated inbox CLI adds guidance even
when connected to an older server. Running notifiers retain their loaded wording
until their sessions restart normally; existing conversations may also need to reload
managed instructions. This staged procedure is specific to this compatible update,
not a general guarantee for future runtime or schema changes.

```sh
scripts/uninstall.sh
# For a custom installed runtime:
python3 scripts/uninstall.py --prefix /path/to/installed/runtime
```

Removal verifies and stops this installation's registered sessions and memory runners,
removes only their owned manager registrations and artifacts, strips its managed guidance,
and deletes runtime files. Inbox state, memory databases, notification history, instruction
backups, removal provenance, and permanent locks remain. Stop/ownership errors abort removal instead of deleting files under a running
service. Unmarked units from pre-release experiments are refused; inspect and remove
only confirmed bridge units before migration.

If removal is interrupted, repeat the uninstall command. A durable removal marker
prevents installation or startup from reactivating a partially removed installation.
Before deleting runtime modules, uninstall prints a standalone recovery command using
`<prefix>/.uninstall-finalize.py`. If module deletion was interrupted, run that exact
command; it validates the retained manifest and deletes only the recorded runtime files.
It never traverses or removes the state directory. After successful removal, reinstalling
the same selection reuses retained session registration and memory data.

## Troubleshooting

- **No registration:** confirm the managed section is in the global file Codex actually
  loads, restart/reload the conversation, then run `session.py ensure` explicitly.
- **No notification:** check the exact target with a direct queue test and inspect both
  bridge and notifier health, not just socket existence.
- **Service failed:** `journalctl --user -u koinon-session-INSTANCE -n 50 --no-pager`.
  The instance suffix is the state-directory hash returned by `ensure`.
- **Name missing in Claude:** refresh its listing; registry `messagingSocketPath` is a
  bare filesystem path, while message addresses use `uds:`.
- **Custom Claude home:** set `CLAUDE_CONFIG_DIR` consistently for the supervisor. For
  systemd use a service override with `Environment=CLAUDE_CONFIG_DIR=/your/path`.
- **Inbox full:** read and acknowledge handled entries; the limit is 1,000 records.

## Inbox schema upgrade

Inbox schema 3 migrates legacy and schema-2 inboxes on bridge startup after exclusive socket reservation. Stop the old
bridge and notifier together, install the new runtime at the existing target and paths,
and restart both. Apply the same sequence to manual macOS processes. An installation
with `--no-start` does not upgrade running processes. Preserve the inbox and legacy
notification checkpoint; do not reset either to activate the schema.

Do not run an older bridge against an upgraded inbox. Schema-2 runtimes refuse
schema 3; earlier runtimes do not maintain the acknowledgement watermark. Restore a consistent pre-upgrade backup for a
rollback instead of mixing runtime and metadata versions. The schema change preserves
ordinary-reader compatibility but does not upgrade an old notifier. The new notifier
uses subscriptions and imports the legacy checkpoint into its separate journal.
Explicit bindings require memory schema 5; restart each optional memory service with
the new runtime before binding. Activation controls store evidence only. They are
not a substitute for [stopped-notifier recovery](NOTIFIER.md).

Migration and acknowledgements use SQLite transactions. Process-termination tests
verify rollback and retry; they do not establish power-loss durability on every
filesystem. Keep the existing state and checkpoint backups when upgrading.

Inbox startup storage failures, including a lock that outlasts SQLite's finite
busy timeout, exit 78. This deliberately requires inspection and an explicit restart;
it does not classify every SQLite operational error as retryable. Do not assume
that a storage failure means the migration or a prior mutation was lost.
The supervisor preserves bridge exit codes 70 and 78 through each startup phase
and its running loop. All installer-managed units exclude both permanent statuses
70 and 78 from automatic restart, while 75 remains retryable.


## Explicit memory bindings

Memory remains optional. Installation can start an explicitly selected repository
service, but does not bind participants. Use its exact state directory to bind it:

```sh
python3 bridge.py --state-dir /private/bridge-state bind-memory \
  --repo-path /path/to/repository --memory-state-dir /private/memory-state
python3 bridge.py --state-dir /private/bridge-state memory-bindings
python3 bridge.py --state-dir /private/bridge-state refresh-memory BINDING_KEY
python3 bridge.py --state-dir /private/bridge-state ack-binding-health BINDING_KEY
python3 bridge.py --state-dir /private/bridge-state unbind-memory BINDING_KEY
```

Binding verifies the live service but does not create a pointer until refresh.
The new notifier performs this refresh automatically and queues a content-free
sync command with the exact service root and a stable consumer identity. The legacy
notifier does not deliver memory pointers. Binding and notification do not acknowledge
or import memory; the consumer must run sync and acknowledge issued pages explicitly.

Memory schemas 3 and 4 upgrade to schema 5 in one transaction; schema 3 also receives
a durable store UUID. Existing records, replay results, snapshots and consumer cursors
are retained. Schema-4/5 stores with missing or invalid identity are refused, never
silently assigned a replacement identity. Older runtimes refuse schema 5. Read the
[coordinated runtime upgrade procedure](WORK-ITEMS-UPGRADE.md). Preserve
consistent backups before upgrade; rollback means restoring a compatible backup,
not changing a schema number. An inbox upgrade does not restart memory for you.
If `memory_upgrade_required` is returned, stop that memory service and start it
with the new runtime. Missing or unhealthy memory leaves existing bridge state intact.


### Upgrading a state directory reached through an alias

New private control sockets use the resolved state path when checking the Unix
socket path-length limit. Roots without aliases keep their endpoint. A short
alias to a long directory could have selected a direct socket in an older release;
a long alias to a short directory could have selected the hashed fallback instead.
Clients retain both legacy routes; new startup refuses a distinct retained legacy
endpoint before creating a second listener.

Stop the old service before restarting it with the new code. The new bridge
client can reach the old endpoint when given the original configured state path.
Memory clients can also recover its exact old path from the validated owner
record. An ownerless bridge cannot reconstruct an unknown alias from a long
canonical path: use its original configured `--state-dir`, or stop its verified
process through the service manager. Do not remove a socket while its owner is
alive. If both old and new control endpoints exist, clients refuse the ambiguity;
inspect their owners before proceeding. No inbox or memory state is reset.

## Usage collection

The installer copies the local usage-report CLI and adapters. It starts no collector
and registers no hooks. See [USAGE.md](USAGE.md) for explicit source selection,
private markers, reporting, and the explicit DeepSeek usage deferral for this release.

## Delivery evidence upgrade

Upgrade the bridge and notifier together. Inbox schema 3 migrates transactionally
to 4; the notification journal migrates from 1 to 2. An interrupted migration rolls
back. The persistent installation identity is generated once in the inbox database
and remains with that state through reinstall and restart. It is not a thread ID.

Before an authorized upgrade, stop the selected notifier and bridge and verify both
have exited. Copy that installation's entire private state directory to a private
backup, preserving permissions, including databases, SQLite sidecars, migration and
activation records, cursors, bindings and targets. Do not copy only a live main
SQLite file. Do not remove the original or reset its checkpoints. Install with the
same prefix, state paths and selected target, then restart and verify the pair.

Older binaries refuse the new schemas. To roll back, first stop and verify both new
processes have exited, then restore the complete stopped-state backup with the old
binaries and the same target. Never combine an older journal backup with a newer
inbox or switch targets during rollback. Restoring a backup loses subsequent local
records and requires an explicit decision about that loss. Keep the new state for
recovery rather than deleting it. Installation alone does not establish native
receipt support or close the discovery verification gate in [DELIVERY.md](DELIVERY.md).

### Session restart reporting after runtime replacement

Installing runtime files does not reload an existing session supervisor. The installer
reports owned session units in the selected unit directory that are active or changing
state as requiring an explicit restart. It checks both current and legacy unit names,
verifies the runtime prefix and service fragment, and leaves other installations alone.
This is a point-in-time observation, not proof of the code a process has loaded.

The report never stops or restarts session supervisors. Follow the coordinated upgrade
procedure and restart only the affected sessions. With `--no-start`, no service-manager
query is made; candidate units are reported as unverified. Missing, failed, timed-out or
ambiguous service-manager observations also remain unverified. An inventory exceeding
128 candidate units is reported incomplete rather than partially declaring success.
Manual supervisors require an explicit coordinated restart;
the installer does not inspect their processes. A successful file installation alone
is not evidence that a running supervisor uses those files.
