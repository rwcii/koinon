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
PID/start marker, phase, and both directly owned child PID/start/generation triples.
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

Stop completion requires a terminal record for the captured runner, the same runner
identity, and proof that both captured and final recorded children are dead. A dead
runner left in a nonterminal phase is not proof that startup created no child. Such
ambiguous interruption remains a recovery condition rather than an invented clean stop.

Permanent 70/78 failures produce a refusal before another attempt can run. Explicit
retry holds the supervisor lock and refuses while any recorded process is alive or
unknown, or ownership never reached a terminal record. It removes only the refusal,
retaining owner evidence and all inbox/journal data. It does not clean stale endpoints
or adopt another process. A bounded private diagnostic file is opened before children
start; failure to persist refusal records does not turn a permanent launchd failure
into a restart loop. The wrapper maps permanent failures to a non-restarting exit while
retaining their original classification in available records or diagnostics.

Process observations and record rechecks detect changes but are not an atomic lock on
all processes. Manager activation, saved native selection, public command integration,
real native session jobs, and removal remain subsequent acceptance gates under #42.
