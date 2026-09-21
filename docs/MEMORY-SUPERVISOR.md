# Repository memory supervisor (staged)

`memory_service.py` implements the portable lifecycle portion of DQ-11 slice 3.
It requires a validated saved memory-service selection. The installer copies it but
still does not create selections or activate memory services. Native memory manager activation and loaded-job ownership checks are available
for explicitly saved selections. Complete installation and native session supervision
remain pending. A rendered artifact or a successful wrapper test does not establish
persistent service support.

The commands are `run`, `ensure`, `status`, and `stop`, with explicit `--prefix` and
`--repo`. Optional state-root/backend arguments must match the saved selection.
`ensure` verifies the selected manager, registered artifact and loaded command before
activation, then requires matching manager PID, supervisor generation and child readiness.
An unavailable manager returns a foreground start command with `running: false`. `ensure --retry` explicitly clears this
installation's validated refusal only after the previous supervisor and child are
proven dead. It then retries the selected lifecycle.

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
launchd exit mapping. Native acceptance uses `scripts/test-native-memory.py --run-isolated-job --backend
systemd|launchd --output PATH`. It creates only temporary synthetic installations,
exercises restart, permanent failure, refusal-recording failure and recovery, then
checks that removal preserves the store and the other fixture's running service.

The Linux fixture deliberately uses runtime registration and verifies both loader
and enablement links are removed. It exercises the shared ownership checks, but does
not establish persistent registration or login persistence. Production activation
uses persistent registration; persistent installation remains an installer acceptance
gate. The macOS fixture uses a temporary plist outside LaunchAgents. Neither fixture
proves login startup. Cleanup refuses ambiguous ownership and retains evidence; killing
the driver can leave a synthetic job requiring verified cleanup. Prefer disposable CI.

Launchd selections must record the current user's explicit `gui/UID` domain. Old
records remain readable, but no domain is inferred or silently added. New systemd
selections can explicitly choose `template_version: 2`, which disables environment
substitution while preserving literal arguments. Absent versions retain template v1
bytes; existing artifacts are never silently rewritten.

Loaded identity checks require the exact program and argument list plus the registered
artifact. Systemd may expose one owned loader symlink with safe ancestors and a literal
target equal to that artifact; indirect aliases are refused. Rechecks detect changes
but do not lock the manager's global state. A failed or timed-out manager operation
returns an error, never inferred readiness. `stop` remains generation-bound and does
not unregister a job; deactivation callers must verify ownership and completed stop
before removing registration.
