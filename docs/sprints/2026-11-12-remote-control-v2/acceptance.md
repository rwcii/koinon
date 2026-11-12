# Remote control v2 acceptance run sheet

All rows below are **unmeasured**. Use the final tested source and a new private
state directory on a disposable preview host. Do not reuse production state,
secrets, daemon services or Serve mappings. Before starting, record the exact
commit or source fingerprint, host OS, browser version, preview URL origin and
server-reported session creation/deadline. Keep credentials and phone captures
in private evidence storage.

## Phones

Run each sequence separately in real iOS Safari and real Android Chrome. Record
both OS and browser versions. Use synthetic peers, rooms and decisions; observe
message/verdict results on the receiving host as well as the phone.

| Sequence | Action and expected evidence |
| --- | --- |
| Initial sign-in | Sign in with password and separately with one-use QR; record the fixed 48-hour deadline, without saving bearer cookies in reports. |
| Reconnect | Switch Wi-Fi/cellular, disconnect/reconnect VPN, lock/sleep, and return. Reads recover, stale content is marked, and no POST replays. |
| Drafts | Enter unsent message/decision text; test opt-in off and on, explicit recovery, changed targets, newer edits, tab close/reopen and home-screen launch separately. Private memory/password fields never become drafts. |
| Receipts | Submit synthetic message, room post and verdict while losing the response. Explicitly look up/retry the same operation; host records one mutation. Changed content and stale revisions remain distinguishable. |
| Process restart | Restart only the preview daemon, then abruptly terminate it in a separate trial. Sign-in survives within its original deadline; reads recover fresh CSRF; unsent forms remain; no write replays. |
| Elapsed retention | Record observations after one real hour, overnight, immediately before and at/after the original 48-hour deadline. Access survives before expiry and is rejected at expiry. Record wall elapsed time; do not substitute fake clocks. |
| Storage recovery | On disposable state only, inject an expiry write failure; incomplete persistence is visible. Repair storage and read host diagnostics. A later daemon restart and clock rollback cannot revive the successfully persisted denial. |
| Layout | At 360/390px, open the software keyboard, zoom text and read long proposals. Record overflow, focus, target visibility and action-result feedback. |

Start the elapsed retention observation only after a reachable isolated preview
is available. A locally built binary or desktop browser result does not start
that measurement.

## Native lifecycle and CI

Use disposable Linux/systemd and macOS/launchd accounts. The existing native
suite changes account services; run it only on those runners:

```sh
KOINON_NATIVE_JOB=1 go test -tags native -count=1 -v -timeout 25m ./internal/nativetest/
```

The repository workflow `.github/workflows/native-go-lifecycle.yml` runs both
platforms. `.github/workflows/tests.yml` supplies the full Go matrix. Required
results must identify the final head after integration with current `develop`.
Track previously reproduced full-suite failures separately; retain raw failures
and distinguish new failures from those reproduced on unchanged source.

In addition to the existing native installation/upgrade/removal checks, record
remote-session and receipt behavior across graceful restart, abrupt death,
reboot, upgrade from the pinned release and downgrade. Verify fixed deadlines
and committed revocations on disk, then check them in real browser requests.
Compile/vet and cross-platform builds do not establish these lifecycle results.

Publication remains pending actual acceptance, project reviews and CI. No
service-manager, elapsed-device or CI result is implied by this run sheet.
