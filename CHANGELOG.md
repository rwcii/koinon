# Changelog

User-visible changes to Koinon are recorded here. Unreleased entries move
into a dated release section when promoted to `main`.

## Unreleased

- Say what is wrong when a path is refused for being writable by a second account.
  Installation and upgrade refuse the source checkout, the prefix, the state directory
  and the manager registration path when the mode carries group or other write
  permission, which an account whose `umask` is `002` produces for every directory it
  creates. The refusals named only the path, so an operator could not see the condition
  or the remedy; they now name the mode and the remedy as well, through one shared rule
  in `koinon/path_permissions.py`, and the installation guide states the requirement
  before the first install. The rule itself is unchanged and still refuses: a group
  entry omits the accounts whose primary group it is, and account enumeration can be
  partial or unavailable, so a group that looks exclusive cannot be shown to be
  exclusive. Nothing repairs permissions or adopts a path.

- Document the coordinated runtime upgrade as the supported operation it is, rather
  than as work in progress with a manual runbook in its place. The installation guide
  now carries the three command forms, the manual-backend handoff, and the permission
  precondition that refuses a default `umask 002` checkout. The runbook remains for
  legacy and manual deployments the operation does not cover. Two command forms that
  the design document proposed do not parse and are corrected.

- Correct statements in `README.md` and `AGENTS.md` that the code contradicts. A session
  job's launchd artifact is published under its own state directory, not in
  `~/Library/LaunchAgents`, which holds only the memory artifact. `--name` is read on the
  legacy unit-pair path only and is ignored without warning on the component invocation,
  where the peer name derives from the repository directory name. Entry garbage collection
  is not limited to expired entries: finished work-item retention removes that item's stream
  rows and advances the retained-history floor regardless of expiry. Installed launchd jobs
  apply the same permanent-failure non-restart rule as systemd, by mapping those exits to
  zero. `koinon/codex_instructions.py` is a legacy shim; the implementation is
  `koinon/participant_instructions.py`.

- Support a declared runtime layout migration during upgrade. A release records
  where a shipped file moved to in `koinon/upgrade_layout.py`; replacement publishes the
  new path, confirms it, then retires the old one, and refuses an undeclared removal
  before shutting anything down. The frozen backup keeps every old path for recovery.

- Move the implementation modules into a `koinon` package. The executable
  entrypoints stay at the installation prefix — `bridge.py`, `notify.py`, `session.py`,
  `memory.py`, `memory_service.py`, `session_service.py` and `usage_report.py` — so
  installed service definitions and documented commands keep their paths. The shipped
  file manifest moves with the layout.

- Enable `fetch.prune` in `scripts/setup-repo.sh`, so a clone drops tracking refs
  for branches the remote has already deleted. A stale tracking ref otherwise reads
  as a live branch.

- Move the test suite into `tests/`, leaving the repository root for modules,
  documents and project metadata. Run the suite with
  `python3 -m unittest discover -v -s tests` from the repository root.

- Report and refuse unowned user-service definitions that directly reference an upgrading runtime, including loaded custom artifacts and definitions outside saved state roots.

- Upgrade saved manual memory through a verified persistent foreground handoff, retaining resumable phases and original stopped state. Legacy manual sessions still require explicit ownership migration.

- Restore inactive native components to their original stopped and registered/deactivated
  state after gated upgrade verification, before releasing active components. Exercise
  synthetic coordinator interruption and resume at every durable phase.

- Explain safe recovery from an unowned-memory upgrade refusal, including a structured
  recovery code and preservation guidance for existing legacy stores and services.

- Correct six stale claims in `AGENTS.md` and `README.md`: an authorized upgrade now points
  at `scripts/upgrade.py` instead of rerunning the installer, the installer is described as
  configuring and conditionally starting repository memory, macOS is launchd-supported rather
  than manual-only, memory is described as carrying subscriptions and content-free notices
  rather than being pull-only, and a fresh `--repo` installation is no longer described as
  the legacy unit pair or as starting nothing automatically.

- Add an experimental public native upgrade command with a retained recovery archive,
  explicit resume/status, preflight refusal of unowned memory state in selected roots,
  and private preservation reports through release and final readiness. Inactive/manual
  adapters and the full cross-platform acceptance matrix remain unfinished.

- Bind pre-release service admission to verified child generations, distinguish later
  restarts from preservation verification, and retain installation exclusion through
  component observation and marker publication.

- Retry deferred search initialization when the ordinary worker queue is full after
  upgrade release. Bind private memory inventories to both repository and generation.

- Connect the internal shutdown, backup and replacement phases to durable receipts;
  interrupted backup completion resumes without replacing unbacked runtime files.

- Connect internal native startup admission to the selected upgrade plan and complete
  new runtime bytes; ordinary ensure remains blocked during the operation.

- Stage resumable runtime file replacement against retained backups and exact memory
  migration verification. Defer search-index initialization until upgrade release.
  The public upgrade coordinator and native acceptance remain unfinished.

- Connect internal upgrade shutdown and component backup capture with retained
  startup locks, recorded endpoint reservations and frozen retry inventories.
  The public upgrade coordinator remains unfinished.

- Stage upgrade exclusion and service admission: selected verification can retain
  ownership checks while peer ingress, notification delivery and maintenance wait
  for durable release. Preserve upgrade-specific supervisor diagnostics. The public
  coordinated upgrade operation remains in development.

- Allow streaming upgrade backups up to 1 GiB per file through the shared binary
  file guard. Other callers retain their smaller explicit limits; JSON records
  keep the 4 KiB default and 1 MiB maximum.

- Stage plan-bound upgrade phase records and immutable private recovery documents;
  the public coordinator and native interruption acceptance remain in development.

- Flush guidance recovery copies before replacing user text, and reconfirm retained
  copies and guidance on retries. Apply the shared device flush to session registration,
  memory ownership, and self-contained uninstall recovery files as well.

- Flush installation configuration and service artifacts through the shared platform
  primitives, so a replacement is followed by the device-level synchronization macOS
  requires. Exact repeats reconfirm retained records instead of accepting a visible but
  possibly unflushed file.

- Stage read-only upgrade inventories and frozen source-file checks, including an explicit
  unsupported-SQLite refusal. The coordinated upgrade command remains in development.

- Correct legacy installation-guide statements to distinguish delivered native component
  supervision from operator-managed services and the pending coordinated upgrade command.

- Install selected repository memory and participant sessions through one public command,
  with native Linux/macOS supervision, memory-only and no-start modes, and exact repeats.
  Remove owned jobs with resumable cleanup while retaining inboxes, memory and recovery
  evidence; clean reinstall reuses retained data. Native paths require safe ancestors.

- Retry a state read when atomic publication replaced its opened predecessor,
  preserving strict validation of the current record.

- Pass parent-owned control sockets explicitly to session children, allowing native
  restart after a child exits before its first handshake while preserving bind intent.

- Activate explicitly selected native session pairs with verified manager ownership,
  guarded shutdown, and captured dead-child control-socket recovery after runtime crashes.

- Add saved native session selection, recoverable artifact publication, and an owned
  runner entrypoint with explicit recovery provenance; automatic activation remains pending.

- Stage durable session-runner ownership, refusal and generation-bound shutdown for
  bridge/notifier pairs; native session activation remains unwired.

- Run native manager and memory lifecycle evidence on integration PRs and branch
  updates, with manual dispatch available after feature branches are removed.

- Add optional generation-bound bridge and notifier stops for supervisor-owned
  shutdown, preserving explicit operator stops.

- Stage pure native session artifact rendering and its implementation sequence,
  preserving Linux behavior while ownership and refusal integration remain pending.

- Activate explicitly selected native memory supervisors with verified manager and
  child identity, explicit launchd domains, and opt-in literal systemd arguments.
  Refuse unsafe manager-reported registration paths and verify inert loader links
  before enabling or starting systemd children. Add isolated native lifecycle checks; complete installer
  integration remains pending.

- Stage exact native service-manager observations with unknown results for missing,
  malformed or changing evidence, plus isolated macOS interface fixtures. Activation
  and complete installation remain pending.

- Stage the repository memory supervisor with child readiness, generation-bound stop,
  bounded retry, retained failure diagnostics, and explicit refusal recovery. Native
  manager activation and installer integration remain pending.

- Allow managed callers to bind memory shutdown to their captured generation,
  refusing a replacement or unreadable owner before sending a stop request.

- Route existing session, installation, and removal service-manager operations
  through platform support while preserving Linux command and failure behavior.

- Stage exact memory-service artifact ownership and recoverable publication,
  preserving conflicting files and interrupted-install evidence without activation.

- Stage validated repository memory-service selections and bounded installer admission,
  preserving configuration and store identity without activating services.

- Add the managed memory installation design candidate, including repository
  ownership, Linux/macOS supervision, recovery, migration, and delivery slices.

- Record complete component installation and resumable upgrades in the delivery
  queue; clarify lifecycle responsibilities for operator-created memory services.

- Name missing required work-command fields, enforce required CLI options, and
  preserve machine-readable argument refusals and optional replay pairs.

- Report session supervisors that may need an explicit restart after runtime
  replacement; preserve no-start and manual-service uncertainty without restarting them.

- Reconcile delivery-queue milestones with released source and completed work-items
  integration; retain the error-code validation proposal as an explicit follow-up.

- Enable schema-5 memory startup with atomic schema-3/4 migration, complete catalog
  validation and explicit work/record-format capabilities. Preserve legacy data and
  snapshots; document coordinated runtime upgrades and rollback boundaries.

- Document finite work-item capacity, retained-history limits, funded claim reserves,
  and the distinction between reclaiming rows and recovering allocated page headroom.

- Add explicit repository/participant work-guidance opt-in and removal, private backups,
  interrupted-publication recovery, and rule-aware uninstall. Policy queries verify
  managed section digests and report edited or missing guidance as disabled.

- Preserve unknown installation fields and staged work rules across upgrades using
  a permanent configuration lock and atomic writes. Add a read-only `work-policy`
  query for explicit repository/participant selections; work activation remains deferred.

- Add staged bounded work maintenance, atomic finished-history reclamation, and
  timestamped status diagnostics. Idle subscribers receive committed change hints;
  shutdown drains accepted jobs. Public startup remains schema 4.

- Add staged work lifecycle commands, replay results and immutable stream/snapshot
  integration, with strict reader-format guards and note isolation. Public startup
  stays schema 4; work activation remains pending.

- Enforce remaining work reservations at the shared memory transaction boundary,
  including progress, cleanup and index rebuilds. Add synthetic capacity/rollback
  tests and package the accounting dependencies; public startup remains schema 4.

- Add staged schema-migration and advisory-lease primitives with synthetic rollback,
  restart, conflict, and reservation tests. Runtime activation remains pending;
  the memory service still uses schema 4 and exposes no work-item commands.

- Record the approved work-items v1 contract and its implementation design for
  advisory writer claims, progress reporting, interruption recovery, and 30-day
  retention of finished work and its history.
- Record the requirement for dead-session detection and supported single-session
  removal that preserves inbox state; design remains pending.

## 2026-09-19 — Usage reports and delivery evidence

- Add bounded local delivery records, acknowledgement-independent deduplication,
  explicit handled outcomes, and durable notification evidence export. Preserve
  uncertain outcomes without replaying native messages. Separate observed service
  health from fresh model activity and declare provider priority limits. Normalize
  `peers` status values (including shell to busy); distinguish observation time
  from the time a Claude status changed. Isolate incoming/outgoing ledger capacity
  and report definite missing/refused socket connections separately from uncertainty.

- Add explicit local Codex/Claude usage collection, source-lineage roles, before-work
  markers, retrospective reports, report combining, and eight-column rendering.
  Missing and inconsistent counters remain explicit. DeepSeek usage reporting is
  explicitly deferred for this release; messaging and installation are unchanged.

- Allow DeepSeek-only installations without a Codex executable. Validate Codex
  after provider selection, reuse a usable saved executable, and fall back to PATH
  when the saved executable is stale. Codex session startup refuses a missing or
  invalid executable before creating new session state.

- Record outstanding usage-reporting requirements, provider defects, and remaining
  programme work in a repository delivery queue, with proposals marked separately.

- Clarify memory maintenance terminology: expiry-based garbage collection, history
  pruning, and storage reclamation are distinct from semantic memory consolidation.
  The planned history-pruning work does not specify summarization. No runtime behavior
  or retention policy changes.

## 2026-09-19 — Durable delivery and shared transport

- Use Koinon defaults for new runtime paths, services and managed participant
  guidance. Reuse legacy paths and owned service names, preserve saved custom
  paths, and refuse ambiguous defaults. Upgrade and removal recognize both marker
  families. Registry, memory handshake and cross-version lock identifiers remain
  unchanged; no runtime state is moved or reset. Unit ownership recognizes
  directory aliases, including macOS paths, during upgrade and removal.

- Select private control endpoints from canonical state paths on both platforms. Preserve validated legacy endpoints for clients and refuse duplicate or ambiguous old/new listeners during upgrades. Peer messaging paths remain literal.

### Added

- Bounded notification journal core, crash-safe migration state, attempt accounting,
  and read-only source reconciliation. Worker integration tests cover acknowledgement
  races and process termination.

- Journal-backed asynchronous Codex and DeepSeek delivery, bounded automatic retry,
  content-free memory sync notices, and independent delivery health. Notifier controls
  expose status, explicit retry, health acknowledgement and accepted-loss rebuild.
  Session lifecycle checks preserve running processes during delivery faults and
  report an unconfirmed stop as unknown. Exact memory service paths are supported
  by `memory.py --service-dir`, with repository and generation checks.

- Explicit verified memory bindings and atomic content-free pointer refresh, with
  independent inbox quotas, durable observation state and replacement diagnostics.
  Inbox schema 2 upgrades to 3; memory schema 3 upgrades to 4 with a stable store UUID.
  Binding deadlines cover verification and cleanup; process identity probes stay
  bounded and off the service loop.

- Explicit bridge and memory change subscriptions with bounded connections, coalesced
  content-free hints after commit, and a shared reconnect/rescan helper. The notifier
  subscribes before each durable recheck and retains finite fallback checks.

- Inbox schema 2 with atomic migration, a bounded acknowledgement watermark and
  durable journal activation evidence. Status advertises only those implemented
  capabilities. Bridge software faults preserve exit 70 through the supervisor and
  stop automatic restarts.

- Bridge startup reserves both socket paths before opening the inbox and retains
  ownership until database shutdown completes. Endpoint refusal exits 78 without
  changing the store. Directory ownership refusals use the same status, and both
  direct and supervised services preserve it without automatic restart. Control timeouts and invalid or lost replies report structured
  errors without automatically repeating mutations.

- Private directory creation now gives missing parent directories mode 0700 even under
  a permissive process umask, while preserving existing parent permissions.

- Dedicated bridge and memory database workers with bounded queues, separate control
  admission, and shutdown that settles accepted writes even after caller cancellation.
  Status reports observed storage and programming faults separately from input refusals.
  Memory startup distinguishes a busy, unresponsive or invalid service from an absent
  listener and refuses replacement before constructing another database owner. The CLI
  reports expected lifecycle errors as structured responses, with exit 75 for temporary
  conditions, exit 70 for internal software errors, and exit 78 for refusals that need
  operator correction.

- Separate control endpoint validation and bounded exchanges for bridge and memory
  clients. Control sockets are excluded from generic messaging and discovery; memory
  reuse verifies the connected process, its start marker and current generation.
- Account-local notifier ownership across state directories. Duplicate provider/session
  targets fail before registration; conflict diagnostics and session status expose a
  matching digest. Persistent Koinon lock paths do not depend on agent home overrides,
  and delivery subprocesses cannot retain the lock after notifier exit. Ownership
  refusals exit with status 78; legacy and supervised services wait for an explicit
  restart after correction instead of repeatedly restarting the refused instance.

## 2026-09-18 — Koinon

### Changed

- Rename the project documentation to Koinon, with the descriptor "Shared coordination
  and memory for independent agents." Clarify the intended scope across agent families
  and repositories, the current per-repository memory service, and agent handoff rules.
  Runtime paths, service names, registry identifiers, and managed markers are unchanged.

## 2026-09-18 — Repository memory and session readiness

### Added

- `memory.py`, a shared per-repository memory service, in its pull-only form. Agents working
  in one repository append typed entries and read them back through a private control socket,
  so a session that started earlier can still learn what a later session recorded. Repository
  identity is the absolute Git common directory, so every worktree of one repository shares one
  store. Liveness is recorded on the entry it affects rather than derived, so reclaiming a
  replacement cannot resurrect what it replaced. A snapshot is frozen as immutable copies against
  a fixed head, so a revocation or a reclamation cannot change what a reader is still paging
  through. The server records page issuance and completion, so a cursor advances only on an
  acknowledgement it actually issued, and a retained acknowledgement replays after a lost
  response. Responses are bounded by encoded bytes with continuation. Storage enforces a logical
  budget and a durable page ceiling, with slots and pages reserved so a withdrawal stays
  recordable, and every retained record has a lifetime whose expiry returns a defined recovery
  result. The store is opened by a single exclusive owner; a second owner is told the store is
  busy rather than that the file is unreadable. When storage cannot be written safely the
  service records a blocked state and refuses writes until recovery is requested explicitly,
  while status, search and stop stay available. Expiry removes entries in batches, and falls
  back to invalidating the index rather than requiring room to maintain it, so a full store can
  always be reclaimed. A search index that cannot be rebuilt leaves the store serving complete
  scans instead of failing to open, and an index the store cannot maintain is marked invalid
  rather than left silently short. Initialisation writes the schema and the identity that
  describes it in one transaction, so an interrupted first start leaves nothing half-made, and a
  store left in that state by an earlier version completes rather than being reported as another
  repository's. Recovery is available as a `recover` subcommand. Search answers from the index only while the
  index is known to cover every live entry, and otherwise from a complete scan, and the reply
  says which answered. Start is
  serialized, and a socket left by an unclean exit is recovered only after its recorded owner is
  proved dead. Entries are reported data and grant no authority. There is no bus integration and
  no broader history pruning or semantic memory consolidation in this form.
- `docs/STORAGE-BOUND-DERIVATION.md`, the derivation of the storage bound the memory service
  enforces, with its terms traced to the SQLite sources at a pinned tag. It records why the log
  a single transaction can produce is finite, why the shared-memory and sub-journal files do not
  contribute to the declared total, what the choice costs in memory instead, and which figures
  are an example workload rather than a bound. No runtime behaviour changes with this entry.
- `docs/PARITY-MEMORY-DESIGN.md`, the agreed design and acceptance contract for peer
  capability parity and a shared per-repository memory service. It records the contracts
  for identity, delivery, presence, and memory, the capabilities that remain unverified
  until they are measured, and the acceptance criteria that judge completion. No runtime
  behaviour changes with this entry.

### Fixed

- Session registration releases its registration lock before starting the systemd
  supervisor. This prevents a false readiness timeout. Concurrent lifecycle commands
  for the same session remain serialized.

### Changed

- Git ignores local `_handoff/` directories to keep handoff content out of commits.

## 2026-09-11 — macOS, DeepSeek, and peer guidance support

### Added

- Generic peer-origin and permission-laundering guidance on inbox records, queued
  notifications, and managed session instructions, separate from peer message content.
- Updated inbox CLI adds guidance when reading from an older running bridge, allowing
  current sessions to receive it without a server restart.
- macOS support. Peer credentials, the process start marker, the peer domain, the socket
  allowlist, and the AF_UNIX address-length fallback are resolved in one platform module.
- A DeepSeek (DSH) participant alongside Codex. It reads and acknowledges the same inbox
  and sends through the same control socket, and the watcher delivers notices to a selected
  harness session over the harness's local RPC, the analogue of `codex queue`.
- `--agent` and `--model` on `session.py`. A DeepSeek peer advertises the harness's
  configured default model in its peer name, such as `deepseek-v4-pro-<repo>-a3`.
- `install.py --configure-deepseek`, which writes a managed DeepSeek section into the
  harness home's `AGENTS.md` so a harness session registers itself and reads its inbox,
  the same way the Codex section already does. Each participant gets its own delimited
  section with its own markers, so either can be added or removed without disturbing the
  other or the user's own guidance. Uninstallation removes exactly the sections the
  installation recorded, so a DeepSeek section is not left behind pointing at a removed
  runtime.

### Fixed

- Repository setup requires the six OS/Python matrix checks, replacing obsolete
  Python-only names that left pull requests waiting for nonexistent jobs.

- Control socket paths use filesystem byte lengths, so Unicode state paths also
  select the short fallback before exceeding the kernel limit.

- Repeated participant configuration preserves registered participants and home paths,
  so uninstall removes all managed guidance, including after a Codex-only upgrade.

- Harness notice delivery refuses redirects and ignores environment proxies to keep
  authentication cookies on the validated loopback destination.

- `peers()` returned no peers on macOS. A missing `/proc/<pid>/stat` raised inside a broad
  handler, so discovery reported an empty list even with live peers present; the same
  omission made a notifier fail at startup and left `session.py status` permanently
  reporting `repair_required`.
- The bridge could not start when a state directory was too deep for `sockaddr_un`, which
  macOS's long temporary paths reach easily. The control socket now falls back to a short
  path in the peer socket directory, which also fixes over-long Linux state paths.
- A peer connection that raised `AttributeError` was dropped without a trace; the handler
  now reports it.

### Changed

- Notice delivery is dispatched per participant. The Codex path, including generated
  systemd units and the manual start command, is unchanged.
- CI runs on Linux and macOS.

## 2026-09-11 — Codex-wide registration

### Added

- Codex-wide installation with a managed global instruction section that preserves
  existing guidance and supports `AGENTS.override.md` precedence.
- Per-thread registration with isolated inboxes, checkpoints, and service instances.
- A supervisor for bridge/notifier lifecycle and a persistent-process fallback when
  no systemd user manager is available.
- Live peer discovery through `bridge.py peers`, exposing only selected registry metadata.
- Explicit session status, stop, and rename commands.
- Tests for concurrent sessions, repeated registration, global instruction preservation,
  partial-service shutdown, and safe name allocation.

### Changed

- Peer names follow the fleet form `codex-<repo>-<two-hex>`. Full thread identity stays
  internal; names remain stable unless explicitly renamed.
- Registration verifies both the bridge and notifier before reporting a healthy session.
- Uninstallation stops the installed sessions and removes owned services and managed
  guidance while retaining inbox data.

### Fixed

- Failed or interrupted renames cannot truncate the previous session registration.
- Short-name collisions within an installation select another suffix rather than
  silently creating an ambiguous name.
- Manual sessions can stop even when an inactive unit file exists or systemd is unavailable.
- Notifier readiness is independent of the invoking shell's Claude configuration directory.

## 2026-09-11 — Initial release

### Added

- Linux Unix-domain socket bridge for Claude Code peer messages, with a persistent
  SQLite inbox and a private local control interface.
- Codex queue notifications, named Claude peer registration, and same-user socket checks.
- User-level installation/removal scripts, systemd service setup, and protocol documentation.
- MIT license, personal repository contribution conventions, agent setup guidance,
  signed contributions, and protected `develop`/`main` pull-request flow.
- Python 3.11–3.13 continuous integration.

### Fixed

- Python 3.13 test cleanup tolerates sockets already removed by asyncio.
- Installer upgrades abort on service-stop errors and refuse unrelated service files.
