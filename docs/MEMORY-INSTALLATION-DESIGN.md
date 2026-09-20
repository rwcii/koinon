# Managed memory installation design

Status: implementation candidate for [DQ-11](DELIVERY-QUEUE.md#dq-11--install-and-manage-every-runtime-component)
and [issue #42](https://github.com/rwcii/koinon/issues/42). This document does not
claim installed support. DQ-12 supplies coordinated runtime replacement afterward.
The driver implements each slice; the peer reviews its committed hash independently.

## Problem and boundary

The installer copies memory code but has no repository memory-service registration,
managed startup, restart observation, or removal path. A running memory process is
not evidence that the installer owns its supervision. Work-item commands already
work; the missing part is their supported operational lifecycle.

Deliver one complete installation invocation for an explicitly selected repository.
Retain the global participant-guidance setup mode without selecting repositories
implicitly. Installing memory does not enable work guidance, bind a participant,
create a consumer, grant work claims, or send peer traffic. Preserve the existing
memory protocol, store layout, repository identity, and literal socket addressing.

No repository move, state-root migration, automatic schema rollback, system service,
sudo, or destructive store removal belongs to this slice. Runtime replacement of
active services remains governed by the current upgrade runbook until DQ-12 lands.

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
- `memory.stop_service` binds shutdown to an observed generation. A closed listener
  is not proof of exit. Use its shutdown discipline plus managed-process exit checks.
- `platform_support.SERVICE_MANAGER` currently describes systemd or no manager.
  All new backend selection and platform-specific process/service primitives belong
  there. Existing session service calls need an explicit integration audit; do not
  assume session and memory manager capabilities are interchangeable.

## Selection and durable identity

Proposed CLI: `install.py --configure-codex --repo REPOSITORY` configures memory for
that repository as well as participant guidance. The DeepSeek form behaves the same.
Add `--configure-memory --repo REPOSITORY` for memory-only or an additional repository;
it must not validate or require an unused participant executable. Existing explicit
thread installation configures memory for its selected repository. Guidance-only
invocations without an explicit repository preserve their current scope.

Resolve the supplied repository before publication. Retain both its canonical common
directory and the complete SHA-256 digest as collision evidence, with the existing
16-character key used by the store. A matching short key with different full identity
is a refusal. Execute the memory runner with the common directory as repository input
so deleting the originally selected linked worktree does not strand its service.
Test that Git can resolve the main and bare common-directory forms used here.

Register an additive `memory_services` object in `install.json`:

- `version: 1` and at most 64 repository records keyed by the existing repository key.
- Each record contains `common_directory`, full identity digest, `state_root`, exact
  derived `service_directory`, chosen backend, and exact managed service artifact path.
- Desired installation state is `pending`, `installed`, or `removing`; it is not live
  health. Include before/after artifact digests during publication/removal recovery.
- The backend-specific service name derives from the repository key, never a thread.
  Proposed Linux name: `koinon-memory-<key>.service`.

Validate the whole known object before any write while preserving unrelated top-level
configuration. Recheck after acquiring the permanent installation lock. On repeat
installation preserve a saved repository's state root, backend, and identity unless
an explicit supported migration requests otherwise. A new global state-root option
must not silently move an existing store. Worktrees converge on one record.

The unit-directory namespace excludes two installed prefixes claiming the same managed
unit name. Cross-prefix ownership conflicts refuse. An operator-selected different
state root does not silently acquire authority over another installation's service.

## Managed lifecycle and platform completion

Use a repository-specific lifecycle runner, proposed as `memory_service.py`, shared by
manager-backed and persistent-process operation. Its entry points are `ensure`, `run`,
`status`, and `stop`, each requiring a saved repository selection. It verifies the
selection against the installed configuration before starting anything.

The runner holds a permanent repository supervisor lock and records its own
PID/start marker and generation separately from memory's existing `owner.json`.
Its record binds installation prefix, repository, state directory, backend, and child
generation. It may supervise only the child it started. A foreign or operator-owned
healthy memory service is reported as externally managed; it is not adopted.

`run` starts one memory child with explicit repository/state arguments, verifies its
identity and schema/capabilities, then reports readiness. It waits and reaps that child.
A successful child exit after `already_running` is an ownership refusal, not readiness.
On shutdown it stops only the captured child generation and waits for confirmed exit.
An unreadable owner, changed generation, or drain timeout refuses completion and leaves
recovery evidence. No arbitrary PID kill, socket unlink, or store deletion is allowed.

Permanent failures 70 and 78 end supervision; retryable child failures use bounded
backoff, with no rapid restart loop. A normal requested shutdown does not restart.
Manager and foreground paths must implement the same restart classification exactly
once rather than nesting two independent retry loops.

On Linux with a user systemd manager, render the owned unit using argument arrays and
the existing systemd escaping rules, `UMask=0077`, `Restart=on-failure`, and permanent
exit exclusions from `platform_support.PERMANENT_EXIT_STATUSES`. Unit activation alone
is insufficient: confirm the runner and memory handshake before returning `running`.

The portable foreground runner is part of the first lifecycle slice, not a later
port. When a manager is unavailable, `ensure` returns `manual_required` with a quoted
`start_command` and `running: false`. A caller with a persistent managed execution
facility starts that command, retains the handle, and performs a separate readiness
check. Loss of the host execution facility ends this supervision guarantee; a printed
command alone cannot satisfy unattended installation acceptance.

**Platform completion gate:** issue #42 also requires unattended installation on macOS.
The existing foreground fallback does not establish that property by itself. Resolve
and document the persistent macOS host/backend and its ownership, login lifetime,
restart exclusions, and removal semantics before activating the public installer path.
A generated user service backend or a verified persistent host handoff may satisfy it;
merely returning `manual_required` may not be reported as a complete installation.
No platform may silently launch an untracked background process.

## Publication, observation, and removal

Preflight validates repository identity, all selected artifacts and parents, saved
configuration, and manager availability without creating a service or changing live
state. `--no-start` stages runtime/configuration/artifacts but neither queries nor
starts/stops a manager and never reports a running service.

Publish through the installation lock with private temporary files, fsync, and atomic
replacement. Write a pending record before the artifact mutation, recording its expected
old and new digest; complete the record only after the artifact is durable. On retry,
accept only one of the recorded states. Unexpected bytes refuse rather than overwrite.
An activation failure leaves an installed-but-not-running result that can be retried;
it does not remove a store or unpublish valid ownership evidence.

Use an explicit service-kind inventory for render, validation, restart observation,
and uninstall. Validate the marker, file owner/type, exact executable, repository key,
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

1. **Identity and configuration foundation:** selection model, preserved configuration,
   record validation, collisions, common-directory identity, exact artifact ownership,
   and publication recovery primitives. No automatic activation yet.
2. **Portable lifecycle and persistent backends:** runner ownership/readiness/stop,
   restart classifications, Linux manager support and the resolved macOS completion
   path together. Synthetic manager and real isolated foreground tests in both OS CI.
3. **Installer integration and operational coverage:** one-invocation selection,
   repeat/no-start behavior, memory-only installation, observation and uninstall,
   explicit external-unit migration, complete user docs and end-to-end fixtures.

Each slice is a signed, independently reviewed commit with the full suite and diff
check before publication. Keep #42 open until the final acceptance evidence exists;
do not mark it complete from staged primitives or Linux-only behavior.

Fixtures cover two worktrees sharing a store, distinct repositories and installations,
malformed/unknown configuration, custom paths and quoting, repeated installation,
manager absence, no-start side effects, wrong owner, PID reuse, changed generation,
existing unmanaged service, permanently refused startup, temporary retry, partial
publication, interrupted migration/removal, and unrelated-service preservation.

Runtime rollout and promotion are separate from source delivery. Record the supported
backend and tested startup/readiness/stop/removal stages for each platform, with gaps
stated explicitly. DQ-12 then builds inventory, coordinated backup/replacement, and
post-upgrade verification on these lifecycle primitives.
