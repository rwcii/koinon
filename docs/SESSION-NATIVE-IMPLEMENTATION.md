# Native session supervision implementation

This extends the approved [installation design](MEMORY-INSTALLATION-DESIGN.md)
without enabling a new backend yet. Memory activation is isolated in PR #56; this
branch is the next slice, for the bridge/notifier session pair. The installer remains
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
tests. It is not called by registration or installation. In particular, the current
session runner does not yet implement the required launchd refusal boundary; rendering
a plist is not permission or evidence to load it.

## Implementation sequence

1. Add pure canonical session command and LaunchAgent rendering in platform support,
   with synthetic tests for exact argument boundaries, restart predicate, umask,
   deterministic identity and malformed selections. Keep public activation disabled.
2. Add bounded private runner ownership and refusal records. Bind configuration and
   runner generation, PID/start marker and both directly owned child identities.
   Join bridge handshake and notifier readiness to those identities. A healthy bridge
   alone is insufficient. Reuse durable publication and process observation primitives;
   do not reuse repository-memory selection records for participant sessions.
3. Add generation-bound stop and explicit retry. Preserve both the triggering error
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
