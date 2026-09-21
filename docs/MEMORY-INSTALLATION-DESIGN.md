# Managed memory installation design

Status: implementation candidate for
[DQ-11](DELIVERY-QUEUE.md#dq-11--install-and-manage-every-runtime-component) and [issue
#42](https://github.com/rwcii/koinon/issues/42). This document does not claim installed
support. DQ-12 supplies coordinated runtime replacement afterward. The driver implements
each slice; the peer reviews its committed hash independently.

## Problem and boundary

The installer copies memory code but has no repository memory-service registration,
managed startup, restart observation, or removal path. A running memory process is not
evidence that the installer owns its supervision. Work-item commands already work; the
missing part is their supported operational lifecycle.

Deliver one complete installation invocation for an explicitly selected repository.
Retain the global participant-guidance setup mode without selecting repositories
implicitly. Installing memory does not enable work guidance, bind a participant, create
a consumer, grant work claims, or send peer traffic. Preserve the existing memory
protocol, store layout, repository identity, and literal socket addressing.

No repository move, state-root migration, automatic schema rollback, system service,
sudo, or destructive store removal belongs to this slice. Runtime replacement of active
services remains governed by the current upgrade runbook until DQ-12 lands.

## Existing seams and consequences

- `memory.repo_common_directory` resolves the absolute Git common directory;
  `repo_identity` hashes it to 16 hexadecimal characters. Worktrees share that key.
  Store placement is `memory.state_dir(state_root, key)`.
- `install_state.locked` serializes preserving configuration publication and fsyncs
  both the replacement and its directory. Extend its merge path; do not reconstruct
  `install.json` from recognized keys.
- `install.unit_targets_prefix` currently selects an executable from session,
  notify, or bridge filenames. Memory ownership needs an explicit new service kind;
  accepting an arbitrary filename containing the installed prefix is insufficient.
- `memory.verify_running` verifies the same-user socket peer, repository, protocol,
  owner generation, PID/start marker, and health. Reuse this readiness check.
- `memory.serve` returns successfully when a verified service already exists.
  A managed child cannot use that result to claim that it owns the existing service.
  The managed runner must distinguish reuse from starting its own child.
- `memory.stop_service` accepts an optional `expected_generation` so a supervisor
  can bind shutdown to its captured child instead of whichever owner is current.
  A changed or unreadable owner refuses before a request. Omitting the parameter
  preserves existing CLI behavior. A closed listener is not proof of exit; reuse
  the helper and independently reap the supervisor-owned child for exit proof.
- `platform_support.SERVICE_MANAGER` currently describes systemd or no manager.
  All new backend selection and platform-specific process/service primitives belong
  there. Existing session service calls need an explicit integration audit; do not
  assume session and memory manager capabilities are interchangeable.

## Selection and durable identity

Proposed CLI: `install.py --configure-codex --repo REPOSITORY` configures memory for
that repository as well as participant guidance. The DeepSeek form behaves the same. Add
`--configure-memory --repo REPOSITORY` for memory-only or an additional repository; it
must not validate or require an unused participant executable. A fresh explicit-thread installation with an explicit `--repo` configures memory for
that repository. Existing installations retain their component scope until the operator
adds `--configure-memory --repo REPOSITORY`; an ordinary rerun must not silently start
an additional service. Legacy thread-only invocations without `--repo` retain their
previous scope. Guidance-only invocations
without an explicit repository preserve their current scope.

Resolve the supplied repository before publication. Retain both its canonical common
directory and the complete SHA-256 digest as collision evidence, with the existing
16-character key used by the store. A matching short key with different full identity is
a refusal. This adds an installer admission check; it does not change the existing
short-key store layout or protect stores created outside the installer. Execute the
memory runner with the common directory as repository input so deleting the originally
selected linked worktree does not strand its service. Test that Git can resolve the main
and bare common-directory forms used here.

Register an additive `memory_services` object in `install.json`:

- `version: 1` and at most 64 repository records keyed by the existing repository key.
- Each record contains `common_directory`, full identity digest, `state_root`, exact
  derived `service_directory`, chosen backend, and exact managed service artifact path.
- Desired installation state is `pending`, `installed`, or `removing`; it is not live
  health. Include before/after artifact digests during publication/removal recovery.
- The backend-specific service name derives from the repository key, never a thread.
  Proposed Linux name: `koinon-memory-<key>.service`.

The 64-record limit is a conservative bound on per-installation lifecycle inventory, not
a memory-store capacity limit. Adding a 65th distinct repository refuses with
`memory_service_limit` (exit 78) before publication; repeated installation of an
existing selection remains allowed at the limit. This is an installer-only admission
refusal, declared in `runtime_names.CONFIGURATION_CODES` before use through
`NameConflict`; it is not emitted by the memory runner or memory protocol. The runner
validates saved selections and reports malformed retained configuration through its own
declared configuration boundary rather than performing registration admission. Removing
an explicitly selected registration preserves its store. A retained configuration
exceeding the supported limit is invalid configuration, never truncated or reset
automatically.

Validate the whole known object before any write while preserving unrelated top-level
configuration. Recheck after acquiring the permanent installation lock. On repeat
installation preserve a saved repository's state root, backend, and identity unless an
explicit supported migration requests otherwise. A new global state-root option must not
silently move an existing store. Worktrees converge on one record.

The unit-directory namespace excludes two installed prefixes claiming the same managed
unit name. Cross-prefix ownership conflicts refuse. An operator-selected different state
root does not silently acquire authority over another installation's service.

## Managed lifecycle and platform completion

Use a repository-specific lifecycle runner, proposed as `memory_service.py`, shared by
manager-backed and persistent-process operation. Its entry points are `ensure`, `run`,
`status`, and `stop`, each requiring a saved repository selection. It verifies the
selection against the installed configuration before starting anything.

The runner holds a permanent repository supervisor lock and records its own PID/start
marker and generation separately from memory's existing `owner.json`. Its record binds
installation prefix, repository, state directory, backend, and child generation. It may
supervise only the child it started. A foreign or operator-owned healthy memory service
is reported as externally managed; it is not adopted.

`run` starts one memory child with explicit repository/state arguments, verifies its
identity and schema/capabilities, then reports readiness. It waits and reaps that child.
A successful child exit after `already_running` is an ownership refusal, not readiness.
On shutdown it stops only the captured child generation and waits for confirmed exit. An
unreadable owner, changed generation, or drain timeout refuses completion and leaves
recovery evidence. No arbitrary PID kill, socket unlink, or store deletion is allowed.

Permanent failures 70 and 78 end supervision. In manager-backed operation the runner
propagates retryable failures and the manager owns throttled restart. New memory units
use a 10-second restart delay; foreground memory supervision uses 10-second initial
backoff, doubling to a 60-second cap and resetting after 60 seconds healthy. Existing
Linux session restart timing stays unchanged during its abstraction slice. A normal
requested shutdown does not restart. Do not nest two independent retry loops. A durable
refusal record binds the observed failure to configuration digest and runner/child
generation; ordinary `ensure` reports it rather than erasing it. An explicit retry after
correction validates the selected identity, clears only its refusal record, and attempts
readiness again.

On Linux with a user systemd manager, render the owned unit using argument arrays and
the existing systemd escaping rules, `UMask=0077`, `Restart=on-failure`, and permanent
exit exclusions from `platform_support.PERMANENT_EXIT_STATUSES`. Unit activation alone
is insufficient: confirm the runner and memory handshake before returning `running`.

On macOS, add a user LaunchAgent backend selected through `platform_support.py`. Use a
deterministic repository label and `~/Library/LaunchAgents/<label>.plist`, serialized
with `plistlib`, an exact `ProgramArguments` array, `Umask` integer 63, explicit
`RunAtLoad: true`, `ThrottleInterval: 10`, and `KeepAlive` with `SuccessfulExit: false`.
No system-domain jobs, user switching, shell evaluation, or detached double-fork. Both
backends deliberately operate within a user-service lifetime; neither adds an always-on
system-service guarantee across logout or reboot. The launchd agent uses the selected
login/service domain; the systemd backend preserves existing user-manager policy without
enabling lingering. This is the supported boundary on both platforms.

Apple documents per-user agents loaded from the user's Library and the foreground
process lifecycle in its [launchd
guide](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html).
Its [published plist
manual](https://github.com/apple-oss-distributions/launchd/blob/main/man/launchd.plist.5)
documents successful-exit restart selection and integer umasks. These archived sources
are background. The current evidence is the user-run probe and on-device manual
inspection reported by the reviewer below; the complete Koinon wrapper and backend are
still unimplemented and untested on macOS.

Since that restart predicate cannot express the selected permanent exit exclusions, the
launchd boundary wrapper records permanent refusal durably and returns zero to its
manager while retaining the original 70/78 and error in status. The CLI must still
report failure, never `running`, when the refusal marker exists. A later login/reload
checks the marker before starting a child and remains stopped until explicit retry. If
recording the marker fails, the launchd boundary must still return zero: another nonzero
exit would restart it under the measured predicate. Stop without repeated child starts,
emit the original failure and recording failure to a private diagnostic sink, and report
observation as unavailable rather than claiming a durable recorded refusal. Require a
test with an unwritable marker location that asserts zero wrapper exit, no repeated
child starts, and unavailable observation. On systemd preserve the actual permanent exit
and its existing restart exclusions.

Route manager-specific render, availability, activate, observe, and deactivate through
platform support. The domain selected for launchd operations must belong to the current
user and be recorded/validated; never silently bootstrap into a different domain.
Validate exact command behavior against the target macOS `launchctl` manual and real
isolated CI jobs before freezing the implementation. Ownership must agree across
registered artifact, loaded job, executable arguments, runner record, and memory peer.
No successful CLI call or plist presence alone establishes readiness or ownership.

Port session-supervisor manager calls through this backend interface as a separate
reviewed slice before enabling launchd. Complete installation includes session
supervision as well as memory; adding a memory LaunchAgent while leaving session
supervisors dependent on an unattended terminal does not close #42. Preserve session
identity, child readiness, and permanent-error handling through the same launchd
boundary mechanism. This refactor must not change shipped Linux behavior.

Keep portable foreground operation for a host where the selected user manager is
unavailable. `ensure` reports `manual_required` with a quoted `start_command` and
`running: false`. A persistent execution host can run it and separately verify
readiness, but this remains a declared fallback, not unattended installation success. No
platform may silently launch an untracked background process. `--no-start` stages
artifacts without loading a job or querying a manager on either platform.

The platform completion gate is real isolated Linux and macOS evidence for activation,
verified readiness, crash restart, permanent-refusal non-restart, retry, stop, and owned
removal. Use uniquely named fixture jobs and temporary repositories/state. Missing
manager support in a runner is an unmet acceptance condition, not a passing skip. No
production participants or stores are test fixtures.

### Reported macOS restart-predicate measurements

The user ran the reviewer's isolated probe on macOS 26.5 (build 25F71), Darwin 25.5.0,
arm64. The reviewer supplied these results on 2026-09-20. The driver has not
independently run the probe on that host. Each synthetic job used `KeepAlive:
{SuccessfulExit: false}`, the default throttle, and a 25-second observation window;
counts came from the job's own start log.

| Exit case | Explicit RunAtLoad | Starts in 25 seconds |
| --- | --- | --- |
| 0 | true | 1 |
| 70 | true | 3 |
| 75 | true | 3 |
| 78 | true | 3 |
| 70 | absent | 3 |
| SIGKILL | true | 3 |

The reviewer also inspected that host's `man 5 launchd.plist`, which describes
`SuccessfulExit` as a binary zero/nonzero restart predicate without individual exit-code
exclusions. The observed restart spacing was about ten seconds. The absent-RunAtLoad
case started too: retain explicit `RunAtLoad: true` for clarity, not as a claim that the
key alone supplies load-time startup.

These observations support the planned translation of permanent 70/78 failures to a zero
manager exit while preserving failure in status. They do not test the Koinon wrapper,
explicit throttle overrides, artifact ownership, restart recovery, or install/uninstall
integration. Those remain required backend acceptance tests. A finite observation window
on one macOS version is not a guarantee across versions.

## Publication, observation, and removal

Preflight validates repository identity, all selected artifacts and parents, saved
configuration, and manager availability without creating a service or changing live
state. `--no-start` stages runtime/configuration/artifacts but neither queries nor
starts/stops a manager and never reports a running service.

Publish through the installation lock with private temporary files, fsync, and atomic
replacement. Write a pending record before the artifact mutation, recording its expected
old and new digest; complete the record only after the artifact is durable. On retry,
accept only one of the recorded states. Unexpected bytes refuse rather than overwrite.
An activation failure leaves an installed-but-not-running result that can be retried; it
does not remove a store or unpublish valid ownership evidence.

Use an explicit service-kind inventory for render, validation, restart observation, and
uninstall. Validate the marker, file owner/type, exact executable, repository key,
selected state root, and observed manager artifact path. Reject symlinks, ambiguous
arguments, environment/specifier expansion, duplicate target options, and changed
artifacts. A marker alone is not ownership. Preserve the old unit families unchanged.

Restart reporting includes registered memory services and distinguishes manager health,
verified memory readiness, unavailable observation, and a possible old runtime image.
Replacing runtime files never proves that running processes have loaded them.

Uninstall first inventories and validates all selected owned components before changing
guidance or deleting runtime files. It stops affected session supervisors and managed
memory runners, verifies exit, then disables/removes only their owned artifacts. A
failure leaves runtime/configuration and data sufficient for retry. Persist removing
phases before irreversible artifact operations. Preserve databases, sidecars, notes,
work history, claims, consumer cursors, inboxes, and retained diagnostics. Remove no
operator-created service merely because its command happens to reference the prefix.

## Operator-created service migration

Treat an existing operator-created unit as external ownership. Even if its memory
handshake is healthy, installation must report that supported supervision has not been
established. Never add a managed marker to that unit or overwrite it in place.

Provide an explicit migration preview naming the selected old artifact, its owner and
content digest, installation prefix, repository identity, exact state directory, live
owner generation, and proposed new owned artifact. Apply must match that preview and
refuse changed evidence. Stop and disable the specifically selected external service
only under the caller's explicit migration request; verify process exit before starting
the new runner. Preserve a private copy of the old artifact and do not delete its data.
Ambiguous or unsupported unit syntax requires operator correction, not guessed parsing.

A conflict at the proposed managed filename refuses with an exact explanation. Do not
scan and adopt every user unit. Detection of arbitrary external managers is incomplete:
report that limit and rely on the store's exclusive ownership check to prevent two
writers. Do not claim a new manager owns an already-running external process.

This migration operation has its own private resumable phases, ordered disable/stop,
verified exit, managed publication, and verified readiness. It does not install a new
runtime over an active old one; coordinated version replacement belongs to DQ-12.

## Reviewable implementation slices

The session-manager abstraction routes existing availability, fragment lookup,
observation, reload, start/stop, and enable/disable invocations through
`platform_support.user_service_manager`. Its first implementation retains the existing
systemd argument order and each caller's I/O, timeout, and failure policy. It adds no
launchd backend or new restart behavior. Unsupported managers refuse without invoking
an unrelated host program; existing availability probes retain the manual fallback.


1. **Identity and configuration foundation:** selection model, preserved configuration,
   record validation, collisions, common-directory identity, exact artifact ownership,
   and publication recovery primitives. No automatic activation yet.
2. **Session manager abstraction:** route existing session and legacy service-manager
   operations through platform support with unchanged Linux semantics and explicit
   unavailable-manager results. Review separately from new launchd behavior.
3. **Portable lifecycle and persistent backends:** staged [portable runner](MEMORY-SUPERVISOR.md),
   runner ownership/readiness/stop,
   refusal persistence and restart classifications, Linux units, and macOS user
   LaunchAgents for both session and memory supervision. Synthetic and real isolated
   manager tests in both OS CI are required before public activation.
4. **Installer integration and operational coverage:** one-invocation selection,
   repeat/no-start behavior, memory-only installation, observation and uninstall,
   explicit external-unit migration, complete user docs and end-to-end fixtures.

Each slice is a signed, independently reviewed commit with the full suite and diff check
before publication. Keep #42 open until the final acceptance evidence exists; do not
mark it complete from staged primitives or Linux-only behavior.

Fixtures cover two worktrees sharing a store, distinct repositories and installations,
malformed/unknown configuration, custom paths and quoting, repeated installation,
manager absence, no-start side effects, wrong owner, PID reuse, changed generation,
existing unmanaged service, permanently refused startup, temporary retry, partial
publication, interrupted migration/removal, and unrelated-service preservation.

Runtime rollout and promotion are separate from source delivery. Record the supported
backend and tested startup/readiness/stop/removal stages for each platform, with gaps
stated explicitly. DQ-12 then builds inventory, coordinated backup/replacement, and
post-upgrade verification on these lifecycle primitives.