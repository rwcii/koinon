# Native manager evidence

The portable supervisor is separate from native manager activation. DQ-11 still
requires real Linux and macOS evidence for owned activation, readiness, crash restart,
permanent refusal without restart, explicit retry, stop and removal preserving data.

The current native interface fixture is `scripts/probe-launchd.py`. It runs only with
`--run-isolated-job --output PATH` on macOS. The dedicated CI job runs it under the
current user's GUI domain. It uses one randomly named job, a temporary plist
outside the login agent directory, and a synthetic Python child. It never modifies a
production service or a system-domain job. Cleanup targets only that fixture label.
Missing manager capability is an unmet result and a failing job, not a passing skip.

The fixture collects command help, missing/loaded job observations, argument rendering,
duplicate bootstrap behavior, explicit restart and removal. Manager environment dumps
are not retained. XML output is reduced to job identity fields; text observation is
retained only for selected identity fields. This evidence is for choosing and validating
an observation mechanism; it is not a claim of Koinon lifecycle acceptance.

Loaded ownership must agree with the registered artifact, exact loaded executable
arguments, runner identity and memory handshake. Artifact bytes on disk plus label
existence cannot prove that the manager loaded those bytes. Unknown observations must
remain unknown. Linux has structured properties through systemd's documented D-Bus
interface, including FragmentPath, ExecStart and MainPID. The macOS mechanism still
uses the version-tested native interface described below; no label-only fallback
is accepted as ownership proof.

The fixture is intended for disposable CI machines. Cleanup runs on ordinary failures,
but cannot run after SIGKILL or a machine crash. In that case the temporary job may
remain loaded until the GUI domain ends. Its child exits after 90 seconds and
`KeepAlive` is false, so it does not become a respawning service. Local reproduction
requires retaining the exact fixture label and handling that residual-job possibility;
the fixture never sweeps other jobs.

The native macOS 26.6.2 probe found that `list -x` is unavailable and that converting
the supported listing through `plutil` fails. The observation candidate therefore
requires a strictly recognized `print` envelope and exact path, program, argument
boundaries and PID. Duplicate, incomplete or changed evidence returns unknown. The
fixture now checks the candidate against its actual loaded job, including spaces,
quotes, backslashes, Unicode and trailing whitespace in separate arguments. This is
a version-tested interface, not a promise of a stable Apple API. Linux observation
uses typed D-Bus properties instead. Both paths recheck observations; these checks
do not lock the service manager or eliminate later state changes.

Both native evidence workflows run for pull requests targeting `develop` and pushes
to `develop` or `main`, and expose `workflow_dispatch` for on-demand reproduction.
They have no path filters: dependencies can change native behavior without editing
the fixture or manager modules directly. Each job retains its isolated synthetic job,
read-only repository permissions, bounded runtime and evidence upload on failure.

These triggers make evidence reproducible after feature branches are deleted. They
do not configure branch protection or establish that native evidence is a required
merge gate. Promotion must inspect the applicable native results as well as the unit
matrix. Historical feature-branch evidence does not substitute for results on the
candidate being reviewed. Persistent login and complete installer acceptance remain
separate gates under #42.
