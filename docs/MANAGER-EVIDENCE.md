# Native manager evidence

The portable supervisor is separate from native manager activation. DQ-11 still
requires real Linux and macOS evidence for owned activation, readiness, crash restart,
permanent refusal without restart, explicit retry, stop and removal preserving data.

The current native interface fixture is `scripts/probe-launchd.py`. It runs only with
`--run-isolated-job --output PATH` on macOS. The dedicated branch CI job runs it under
the current user's GUI domain. It uses one randomly named job, a temporary plist
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
requires verification against the native interface; no text parser or label-only
fallback has been accepted as ownership proof.
