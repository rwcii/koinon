# Portable session runner

`session_supervisor.py` and `session_supervisor_state.py` stage the portable ownership
boundary for one explicitly selected bridge/notifier pair. They are not yet invoked
by the public session commands or native artifacts. Existing session startup remains
unchanged until selection, activation and native lifecycle acceptance are integrated.

The caller supplies a private absolute state directory, installation and configuration
digests, and exact bridge/notifier argument arrays. It must derive these from validated
session selection, never from peer messages. The runner does not select a manager or
register a job. Native managers retain responsibility for restart cadence.

## Ownership and readiness

A bounded versioned owner record binds installation, configuration, runner generation,
PID/start marker, phase, pending spawn intent, and both directly owned child
PID/start/generation triples. Before each fork, the runner durably records which child
is about to be created. It clears that intent only after capturing the child identity
or confirming direct-child exit. A crash in that interval leaves explicit uncertainty.
The runner holds `supervisor.lock`; publication uses private durable JSON primitives.
A refusal or unconfirmed prior owner prevents replacement. Symlinks, unsafe files,
foreign installation records and malformed records are preserved and refused.

Readiness requires both private control responses to match the directly owned children,
including kernel PID and process-start observations. The notifier's readiness record
and reported bridge generation must match the selected pair. Delivery health remains
separate: readiness does not promise that every queued notification has been delivered.

## Stop, refusal and recovery

The stop record names the captured runner generation. A stale request is inert; an
invalid or foreign request refuses. Shutdown runs in reverse child order. Before a
child's control generation is known, only its unreaped direct `Popen` handle can be
terminated. Afterwards shutdown uses the versioned generation-bound control request;
a refusal or lost response never falls back to an unguarded stop or a PID signal.
Children are waited for without force-killing them. Unconfirmed cleanup preserves the
triggering error and records a separate shutdown failure.

Stop completion requires the same captured runner identity, no unresolved spawn
intent, and proof that both captured and final recorded children are dead. An absent
child record while spawn intent is unresolved is not proof that no child was created.
A crashed runner can be restarted when every recorded process is provably dead and
no unresolved spawn remains; ambiguous interruption requires recovery.

Permanent 70/78 failures produce a refusal before another attempt can run. Explicit
retry holds the supervisor lock and refuses while any recorded process is alive or
unknown, or a spawn remains unresolved. It durably records permission to retry that
exact prior runner generation, then removes only the refusal, retaining owner evidence
and all inbox/journal data. A permanent failure in the owner record still requires this
explicit retry when the separate refusal marker is missing; stale retry records cannot
authorize a replacement generation. It does not clean stale endpoints
or adopt another process. A bounded private diagnostic file is opened before children
start; failure to persist refusal records does not turn a permanent launchd failure
into a restart loop. The wrapper maps permanent failures to a non-restarting exit while
retaining their original classification in available records or diagnostics.

Process observations and record rechecks detect changes but are not an atomic lock on
all processes. Manager activation, saved native selection, public command integration,
real native session jobs, and removal remain subsequent acceptance gates under #42.

## Unresolved-spawn recovery

An unresolved spawn cannot be disproved from missing metadata. The staged
`Records.recover_spawn(generation, assert_no_unrecorded_child=True)` API therefore
requires an explicit operator assertion about the exact captured generation after the
operator has independently established that no unrecorded child remains. It refuses
while the runner or any recorded child is alive or unknown. It preserves complete
owner/refusal evidence in a bounded private recovery record with
`basis: operator_assertion`; it does not delete or rewrite the original records.

The assertion alone does not start or authorize a new attempt. A separate explicit
retry records permission for that generation and clears the refusal. Neither action
turns asserted absence into observed shutdown: `wait_stopped` still refuses completion
when the captured generation retains unresolved spawn evidence. Public command
integration must expose this distinction and exact-generation confirmation before
native activation is enabled. Deleting state files is not a supported recovery path.

Existing nonprivate legacy supervisor locks are refused without changing their modes.
Upgrade integration must report and resolve that condition explicitly; it cannot
silently chmod or adopt retained lock evidence.
