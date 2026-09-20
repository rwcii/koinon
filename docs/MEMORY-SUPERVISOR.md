# Repository memory supervisor (staged)

`memory_service.py` implements the portable lifecycle portion of DQ-11 slice 3.
It requires a validated saved memory-service selection. The installer copies it but
still does not create selections or activate memory services. Native manager operations,
loaded-job ownership checks, Linux/macOS manager acceptance, and complete installation
remain pending. A rendered artifact or a successful wrapper test does not establish
persistent service support.

The commands are `run`, `ensure`, `status`, and `stop`, with explicit `--prefix` and
`--repo`. Optional state-root/backend arguments must match the saved selection.
`ensure` observes without creating state and returns a foreground start command when
needed; it does not activate a manager. `ensure --retry` explicitly clears this
installation's validated refusal only after the previous supervisor and child are
proven dead. It then returns the same staged start command.

The runner holds a permanent private supervisor lock, starts one child, and checks
its PID, process start marker, generation, schema and required capabilities against
the memory handshake and owner record. It never adopts an existing service. Its
private owner record binds the installation, configuration and runner generation.
Missing, malformed or unreadable evidence cannot establish readiness or shutdown.
Process observation distinguishes an unreaped zombie from a running process.

`stop` writes one private, generation-bound request under a separate writer lock.
The runner checks it during startup, operation and retry backoff. The request remains
as bounded evidence at one fixed path; a new request replaces it and a stale request
cannot stop a later generation. No PID read from a record is signalled. The runner
stops only its captured memory generation and reaps its own child. Before readiness,
it may terminate the process it directly started.

Foreground retry begins at 10 seconds, doubles to a 60-second cap, and resets after
60 healthy seconds. Managed execution returns retryable failures to the manager;
it does not run a second retry loop. Permanent failures retain a private refusal
record. For launchd, permanent 70/78 failures map to a zero wrapper exit while status
retains the original failure. If recording fails, the wrapper still exits zero and
observation remains unavailable. A preopened, private, bounded diagnostic file records
the original failure and recording failure when the filesystem permits it. A failure
of both diagnostic sinks still cannot turn a permanent launchd failure into a retry.

If child exit cannot be confirmed, supervision ends with `shutdown_unconfirmed` (78)
and preserves both `primary_code` and `shutdown_code`. This deliberately prevents a
replacement child while the old child may still hold the store, even when the original
failure was temporary. Memory drain is not escalated to SIGKILL. Explicit retry also
refuses while either old process is live or its state is unknown. Stores, sidecars,
work history, claims, cursors and existing external services are preserved.

Portable tests use temporary installed copies, synthetic repositories and directly
owned subprocesses. They cover real readiness/stop, refusal/retry, backoff, stale stop
requests, zombie observation, external-service preservation, private diagnostics and
launchd exit mapping. Native systemd/launchd acceptance is a separate remaining gate.
