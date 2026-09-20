# Staged memory-service configuration

This is the configuration foundation for DQ-11 and issue #42. It supplies validation, pure admission helpers, and staged artifact publication,
not a supported service installation or supervision command. No CLI creates
these records yet. Do not edit live configuration to activate the unfinished feature.
The [implementation design](MEMORY-INSTALLATION-DESIGN.md) describes the remaining slices.

`memory_service_config.selection(repository, state_root)` resolves the same absolute
Git common directory as memory and returns its existing 16-character key plus the
full SHA-256 identity digest, selected state root, and exact derived service directory.
It creates no directories or stores. Linked worktrees, repository subdirectories,
and the common directory itself share the identity. Bare repositories are supported.
The full digest is additional installer evidence, not a new store layout or a change
to the memory protocol. Unmanaged stores retain their existing short-key identity.

## Version 1 shape

The optional `install.json` field `memory_services` has exactly `version` (integer 1)
and `repositories` (object keyed by the existing repository key). It permits at most
64 records. Every record has these fields:

| Field | Meaning |
| --- | --- |
| `common_directory` | Absolute canonical Git common directory selected through Git |
| `identity_digest` | Complete SHA-256 digest of that common-directory string |
| `state_root` | Explicit saved memory state root |
| `service_directory` | Exactly `state_root/memory/<key>` |
| `backend` | `systemd`, `launchd`, or `manual`; a selection, not capability evidence |
| `artifact` | Absolute managed artifact path, or null for manual operation |
| `artifact_digest` | SHA-256 digest of managed artifact bytes, or null for manual operation |
| `state` | Desired publication state: `pending`, `installed`, or `removing` |

Systemd artifact basenames are `koinon-memory-<key>.service`; launchd basenames are
`io.github.rwcii.koinon.memory.<key>.plist`. Recognizing a basename is not proof of ownership.
The label uses the project's GitHub namespace. Canonical rendering and disk ownership checks are staged below; backend operations
and live readiness remain later slices. Validation is structural
and does not follow paths or query services. The separate `verify_selection(record)`
helper resolves Git identity again and refuses internally consistent aliases or moved
repositories. Future publication and runtime selection must call this check; it does
not establish artifact ownership or create a store.

Managed `pending` records also require `before_digest` (null for no previous artifact,
otherwise a SHA-256 digest) and `after_digest` equal to `artifact_digest`. Managed
`removing` records require `before_digest` equal to `artifact_digest` and null
`after_digest`. Publication uses these as recovery evidence. `artifact_digest` describes intended
bytes; only a completed `installed` record plus verified disk bytes establishes
completed publication. A manual selection has no artifact and only the `installed` state;
that state never means a foreground process is running.

Unknown fields or versions within this object, malformed paths/digests, wrong keys,
wrong derived directories, malformed recovery evidence, and over-limit retained
inventories are refused. Unrelated top-level installation configuration is preserved.
A malformed retained object produces the existing `invalid_install_configuration`
boundary refusal without resetting or repairing it.

## Admission and preservation

`admit(current, record)` returns a deep-copied new inventory. It does not mutate either
argument, write configuration, render artifacts, or start services. Identical repeat
admission succeeds, including at capacity. A changed existing record refuses: state
transitions and migrations need their separate, verified publication operation.
A matching short key with different full identity must never adopt the other record.

The 65th distinct selection raises installer-only `NameConflict` with declared code
`memory_service_limit`. The installer classifies it as configuration exit 78. It is
not added to the memory wire error vocabulary and is never a memory command response.
The cap bounds installation lifecycle inventory, independently of store capacity.

The existing permanent installation lock and atomic merge validate this object before
publication and preserve it across unrelated configuration updates. The validation
module is included in installed runtime files so an installed configuration reader
does not depend on the source checkout. Activation, observation, migration, and removal remain required follow-up work.


## Staged artifact publication

`memory_service_artifacts.publish(prefix, python, desired)` publishes a new managed
selection or retries the same selection. It requires an existing installation and
artifact directory, revalidates Git identity before and under the installation lock,
and preserves unrelated configuration. It never creates memory state or contacts a
service manager. There is no public CLI for it yet; do not invoke it against live
manager directories before the lifecycle runner and integration are complete.

Pure `platform_support.memory_service_artifact` renders version-1 systemd or launchd
bytes for the proposed memory runner. Exact canonical bytes bind the interpreter,
installed runner, prefix, repository, state root, backend, and restart policy. Unknown
or additional directives, duplicate arguments, and alternative expansion syntax refuse
rather than being parsed permissively. The templates include a version marker for later
explicit migration. Rendering does not establish platform capability or readiness.

Publication rejects symlinked ancestors, unsafe directories, symlinked or hard-linked
artifacts, non-regular files, foreign owners, and non-private artifact files. An existing
file without an owning registration is refused even when its bytes match. Installed
artifacts that disappear or change are not silently repaired. `verify_owned` checks a
completed registration and disk evidence, optionally comparing an observed manager
artifact path literally; it never queries that manager or claims running health.

The permanent lock order is installation, sorted artifact namespace locks, then sorted
guidance locks. Artifact locks have path-hashed names in the artifact directory's parent,
outside the service scan directory. They exclude two prefixes trying to publish the same
repository-derived artifact name; a prefix-local installation lock alone cannot do that.
All participating publishers must follow this order. Locks are never unlinked.

The pending record is durable before an artifact mutation. A private temporary file is
fsynced, renamed atomically in the artifact directory, and that directory is fsynced
before the completed record is saved. The artifact and installation directories can be
on separate filesystems. Retry writes a missing initial artifact, or completes a matching
postimage without rewriting it (repeating directory fsync). Any unexpected bytes refuse,
retaining recovery evidence. Ordinary exceptions clean only the current temporary file;
crash-orphaned temporary files remain inert and are never adopted or broadly swept.

This primitive deliberately refuses removal, retargeting, and replacement of a distinct
older template. Those require the later lifecycle/upgrade operation. Filesystem and
selection refusals, including memory's `repo_unresolved`, translate to installer
`invalid_install_configuration`, classified as exit 78 by the installer. Lock contention
retains `configuration_busy`; admission can return `memory_service_limit` before writes.
There is no new protocol error or public command envelope in this staged slice.
