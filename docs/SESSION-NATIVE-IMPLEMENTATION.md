# Native session supervision implementation

This extends the approved [installation design](MEMORY-INSTALLATION-DESIGN.md).
Memory activation is integrated through PR #56; native session activation now serves
explicitly published bridge/notifier selections. The installer remains
incomplete until both components and exact removal have operational coverage.

## Compatibility and selection

Keep current Linux unit names, argument order, restart timing and permanent-exit
exclusions. A session manager selection must explicitly bind the installation prefix,
participant identity digest, repository, state directory, backend, artifact digest and
current-user launchd domain. Saved selection changes require conflict/recovery handling;
manager absence must not silently rewrite a selection. Existing sessions without the
new selection retain their existing Linux/manual behavior.

Session jobs start for an explicitly selected conversation. Preserve the existing
policy that they are not enabled at every subsequent login. A temporary-domain job can
provide unattended supervision during that user-manager lifetime without promising
reboot persistence. Memory's repository lifetime is separate. Do not copy the memory
fixture's runtime override into a public persistent-memory installation path.

The pure `platform_support.session_launchd_artifact` renderer is staged with synthetic
tests. It is not called by registration or installation. The separate owned-session renderer targets the generation-bound runner and its
launchd refusal boundary; rendering alone is not evidence of loaded ownership.

## Implementation sequence

1. Add pure canonical session command and LaunchAgent rendering in platform support,
   with synthetic tests for exact argument boundaries, restart predicate, umask,
   deterministic identity and malformed selections. Keep public activation disabled.
2. Add bounded private runner ownership and refusal records. Bind configuration and
   runner generation, PID/start marker and both directly owned child identities.
   Join bridge handshake and notifier readiness to those identities. A healthy bridge
   alone is insufficient. Reuse durable publication and process observation primitives;
   do not reuse repository-memory selection records for participant sessions.
3. Add generation-bound stop and explicit retry. The staged `stop-generation`
   control operation requires capability evidence and refuses on old servers, including
   replacement between status and stop. The shared client checks captured generation
   and PID/start identity; it never falls back to legacy `stop`. Preserve both the triggering error
   and any unconfirmed shutdown. Never signal a PID merely because a record names it,
   or clear refusal while the old runner/children cannot be proven dead. Keep the
   original permanent failure in status even though launchd requires a zero wrapper
   exit. Preopen a bounded private diagnostic sink before starting children; recording
   failure must not produce an automatic permanent-failure restart loop.
4. Add explicit manager activation/status joining registered bytes, loaded program and
   argv, runner PID/start/generation, and both child handshakes. Rechecks detect changes;
   they do not make global manager state atomic. A manager timeout has unknown outcome
   and cannot establish readiness. Keep unrelated loaded jobs and operator artifacts.
5. Exercise real isolated Linux/macOS jobs using synthetic participants and repositories:
   startup, bridge/notifier crash, permanent startup/runtime failure, refusal-recording
   failure, retry, concurrent ensure/stop, replacement evidence, and exact removal.
   Assert all owned children exit before deregistration; retain evidence on ambiguity.
   Verify a second synthetic session and its inbox remain intact.

## Review boundaries

Render-only tests establish neither loaded ownership nor supervision. Do not enable
launchd publicly from those results. The final native fixture must use the actual
session supervisor and a synthetic queue executable; it must never target a real
conversation. Waits must cover the native restart cadence and expose failed subprocesses.
The unrelated notification timeout tracked in #48 remains unresolved and is not fixed
by lengthening native-manager waits.

Only after both native lifecycles pass should the installer integrate one-invocation
component selection, no-start behavior, inventory, exact removal and explicit external
service migration.
## Saved selection and entrypoint

`koinon/session_service_config.py` binds an explicit native selection to its private session
registration, participant digest, installed prefix, Python interpreter, notifier command,
backend and artifact bytes. Records live in the existing session directory as
`native-service.json`; they do not create an unbounded installation-wide inventory.
The raw thread is not included in a native unit or plist. DeepSeek selections require
an explicit harness URL and credential path, rather than inheriting a shell environment.
Legacy manual operation remains separate.

`session_service_artifacts.publish` requires existing private directories and a saved
`session.json`. It acquires installation, lifecycle, registration and supervisor locks
in that order, rechecks inputs, writes bounded publication intent, then publishes the
artifact and completion record. Exact repeats preserve the artifact inode. Interrupted
publication accepts only its saved empty preimage or exact intended bytes. Identical
unregistered artifacts, changed registrations, unsafe locks and foreign paths refuse;
evidence is retained. The hardened file and ancestor primitives are shared with memory
publication. Publication performs no manager operation.

`session_service.py` is the owned runner entrypoint and exposes status, stop, retry and
explicit spawn recovery for an already-published selection. Status joins both private
child handshakes with runner and child process identities; saved `running` state alone
cannot establish readiness. Recovery requires the exact generation and
`--assert-no-unrecorded-child`. The recovery `basis` field is mandatory, and every
consumer retains `operator_assertion` beside that evidence. An unresolved spawn remains
unresolved in status even after an assertion authorizes a separate explicit retry.

The installer does not create native session selections yet. Explicitly published
selections dispatch through the native activation path below. The renderer targets the
owned runner and has no login enablement section. Sessions without a saved native
selection retain their existing behavior.

## Upgrade preflight constraints

An old regular session unit in a higher-precedence systemd lookup directory can shadow
an otherwise valid runtime link with the same name. Activation must detect and identify
that path before manager mutation, preserve it, and require the supported migration
operation. That migration is deferred and not implemented. It must not silently rename the service, remove the old unit, or
adopt its bytes. Native fresh-install evidence cannot establish upgrade acceptance.

A legacy `supervisor.lock` with permissions such as `0664` is refused before starting
children. The command reports the exact path and preserves its inode and permissions.
Operator resolution means inspecting ownership and ensuring the old service and both
children are stopped before an explicit, authorized repair/migration; deleting lock or
supervisor evidence is not a supported recovery procedure. Automatic permission repair
is not part of this slice. Reconciling legacy sessions during upgrade is deferred and not implemented.

## Native activation and captured endpoint recovery

A completed explicit selection now dispatches `session.py ensure`, `status`, `run`
and `stop` to the owned native service. Sessions without that selection keep the
legacy path. Rename refuses a selected native service until explicit reconciliation.
Activation uses the existing twice-read systemd lookup layout and shared registration
preflight. Any earlier same-name artifact is reported before mutation. A runtime
loader link is verified while inert, then the selected job starts. launchd uses the
explicit current-user domain and the registered plist. No session job is login-enabled.

Readiness requires stable loaded executable/arguments/artifact, the recorded runner's
PID/start identity, both child generations and private handshakes, and notifier binding
to that bridge. Manager success or a saved running phase alone is insufficient.
Ensure, stop and deactivation serialize with the same session lifecycle lock.
Deactivation first confirms pair shutdown, then removes only its verified registration;
it retains the artifact, selection, inbox, notification history and supervisor evidence.

For a child that answered a generation- and kernel-PID-verified status exchange, the
runner records the control socket's literal path, device and inode, checking the same
inode before and after the exchange. Before publishing a successor, under supervisor
ownership, it may remove only that captured private inode after the previous runner
and every recorded child are proven dead. A replacement socket, changed owner record,
unknown process, unsafe path or missing capture remains a refusal. Connection failure
is never used as evidence for removal. The supervisor lock excludes cooperating
runners, not independent operator path removal/replacement. Final checks detect
changes, but unlink is not atomic with inode verification. Peer PID sockets are
outside this cleanup.

This differs deliberately from operator-installed endpoints: those still require
explicit manual resolution. Automatic cleanup applies only to control sockets captured
from this runner's verified children. Version 1 supervision can leave an uncaptured socket if a child dies before that
exchange. [Parent-owned descriptor handoff](SESSION-SOCKET-HANDOFF.md) now records
control socket ownership before spawning, closing that child startup window. Its
separate parent bind/publication crash window remains conservative refusal. Merely seeing a new path
under `supervisor.lock` is insufficient because direct bridge/notifier starts do not
acquire that lock.

`scripts/test-native-session.py` tests two isolated real native session pairs, runtime
notifier crash/restart, independence of the second session, permanent runtime refusal
and explicit retry, permanent startup refusal, and guarded shutdown with exact
deregistration and data retention. With descriptor handoff it also tests a child crash before first handshake. It does
not establish atomic parent bind/publication, complete installer integration, persistent
memory login behavior, or upgrades
from legacy units. The fixture records failed cleanup and retains evidence on ambiguity.

A systemd failed-unit tombstone may remain after the registration is removed. It is
classified as absent only when `LoadState=not-found`, the artifact and executable list
are empty, and `MainPID=0`, with matching adjacent typed reads. A loaded transient unit
is not absent, even with an empty fragment path and no running process. These adjacent
reads detect changes during the query; they do not guarantee future stability or make
manager mutation atomic. Durable owner/refusal records remain the failure authority
for retry. Deactivation does not clear either those records or the manager's failed
state, and no `reset-failed` command is issued.
