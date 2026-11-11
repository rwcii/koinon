# Remote dashboard verification

This record is an acceptance checklist, not a claim that every row passed. The
[design input](REMOTE-DASHBOARD-SPEC.md) requires real proxy and phone evidence in
addition to synthetic tests. Keep account details, machine names, login output,
cookies, messages, and configuration captures outside Git.

## Initial environment evidence — 2026-11-11

The inspected macOS host has Tailscale 1.102.3 at
`/Applications/Tailscale.app/Contents/MacOS/Tailscale`, absent from `PATH`.
`status --json` reported `BackendState=Stopped` and `Self.Online=false`, with no
certificate domains. `serve status --json` was `{}`. No live Serve configuration
was changed and no shared service was restarted for that inspection. Actual Serve
forwarding was unavailable while Tailscale was stopped.

The [v1.102.3 proxy source](https://github.com/tailscale/tailscale/blob/v1.102.3/ipn/ipnlocal/serve.go)
preserves incoming Host for an HTTP backend (`r.Out.Host = r.In.Host`). Its proxy
leaves Origin unchanged and has no response rewriting of Location or Set-Cookie.
The [CLI source](https://github.com/tailscale/tailscale/blob/v1.102.3/cmd/tailscale/cli/serve_v2.go)
and [official CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve)
support persistent `--bg` mappings and targeted removal. Removal without an
explicit path can remove all mounts on the port; use `--set-path=/`. Enabling
Serve can clear Funnel for the selected host/port, so Koinon refuses that conflict.
These are source findings, not observed forwarding or cellular evidence.

## Real Serve probe, before acceptance

The opt-in integration harness starts a disposable synthetic dashboard-only backend,
checks that port 8443 is unused, observes real backend Host/Origin, verifies a Secure
cookie round trip and CSRF logout, then removes its exact mapping. After coordinating
the port and connecting Tailscale, run:

```sh
KOINON_REAL_SERVE=1 go test ./internal/core -run '^TestRealServeForwarding$' -count=1 -v -timeout 2m
```

A skipped test is not proxy evidence. Same-host tailnet routing can require a second
host on some platforms; a connection failure must be recorded, not bypassed with
insecure TLS. For a manual cross-host probe, use a disposable synthetic daemon state
and a synthetic dashboard-only backend.
Do not use production messages or sessions. Before a live configuration change,
inspect current users and active work through Koinon, read the existing Serve
configuration, and coordinate the temporary port. The following command examples
use the installed macOS CLI; on Linux use `tailscale`.

```sh
/Applications/Tailscale.app/Contents/MacOS/Tailscale version
/Applications/Tailscale.app/Contents/MacOS/Tailscale status --json
/Applications/Tailscale.app/Contents/MacOS/Tailscale serve status --json
```

The account owner must first connect Tailscale and complete any HTTPS certificate
consent from the [Serve setup instructions](https://tailscale.com/docs/features/tailscale-serve).
Choose an unused HTTPS port such as 8443 after inspecting background, foreground,
TCP, Web, and Funnel configuration. Start the synthetic loopback handler, note its
actual port as `BACKEND_PORT`, and add only the temporary root mapping:

```sh
/Applications/Tailscale.app/Contents/MacOS/Tailscale serve --bg --https=8443 --set-path=/ http://127.0.0.1:BACKEND_PORT
```

Through `https://MACHINE.TAILNET.ts.net:8443`, capture these results privately:

| Probe | Required evidence |
| --- | --- |
| GET `/` | Backend receives configured external Host; response redirects to `/dashboard/` with a relative Location. |
| Login POST | Backend receives the browser's external HTTPS Origin; correct secret gives the expected redirect and Secure, HttpOnly, Strict cookie. |
| Authenticated GET and write | Browser returns the Secure cookie over HTTPS; read and session-bound CSRF write succeed. |
| Foreign/missing/null Origin | Writes fail; identity and forwarding headers do not authorize them. |
| Forged forwarding headers | They do not change accepted Host or expected Origin. |
| Wrong backend Host | Handler rejects it. |
| GET/POST `/v1/` | No agent routes are exposed, even with an API bearer. |
| Local/remote cookie crossover | Neither session authenticates on the other surface, even if the ID is copied into the other cookie name. |

Compare the mapping to the recorded synthetic target before removing it. Remove
only that mapping, and verify the original configuration remains:

```sh
/Applications/Tailscale.app/Contents/MacOS/Tailscale serve --bg --https=8443 --set-path=/ off
/Applications/Tailscale.app/Contents/MacOS/Tailscale serve status --json
```

Do not use a broad `serve reset` or remove a mapping changed by another owner.
Record the host OS version, Tailscale version, backend Host and Origin behavior,
redirect/cookie outcomes, and cleanup outcome. Repeat on a Linux host. A
synthetic adapter or local browser is not evidence of this proxy path.

## Acceptance matrix

| Area | Required test | Evidence state at initial inspection |
| --- | --- | --- |
| Request boundary | Real Serve login/write, Host/Origin, redirects, Secure cookies, CSRF, forged headers, cookie crossover, `/v1/` rejection | Pending: Tailscale stopped. |
| macOS lifecycle | Enable twice; daemon restart; host restart; upgrade; disable twice; re-enable; stable port/URL and owned cleanup | Pending live validation. |
| Linux lifecycle | Same lifecycle matrix with installed Tailscale and native service manager | Pending: Linux host unavailable in this session. |
| Existing Serve use | Preserve another mount/port; refuse a conflicting root, TCP, foreground, or Funnel service | Synthetic tests required; live preservation check pending. |
| Dependency failures | Missing CLI, disconnected host, certificate permission, denied operator/policy, daemon unavailable, occupied port | Synthetic tests required; account/policy paths need live checks. |
| State continuity | Local and remote see the same delivery and verdict; concurrent decision revision yields conflict | Automated integration and live check required. |
| Cellular | Phone Wi-Fi off: sign in, read agents, send a message, submit a verdict; verify host receipt and verdict | Pending: physical phone action required. |
| iOS Safari | Core workflows; record iOS and Safari versions | Pending: real device unavailable. |
| Android Chrome | Core workflows; record Android and Chrome versions | Pending: real device unavailable. |
| Layout | 360/390 CSS px, portrait/landscape, zoom, long identifiers, keyboard, 44px targets and 16px fields | Automated/synthetic checks are distinct from physical-device checks. |
| Reconnection | Wi-Fi to cellular; background/resume; lost POST response; preserved forms; stale label; no replay | Synthetic behavior tests required; real transitions pending. |
| Authentication lifecycle | Wrong/right secret, expiry, secret change, logout, daemon restart, remote disable | Automated tests required; live browser check pending. |

## Required repository checks

Record exact content/commit and results in the implementation report; this checklist
does not assert that the commands ran:

```sh
go vet ./...
gofmt -l .
go test -race -timeout 30m ./...
git diff --check
go vet -tags native ./internal/nativetest/
go test -tags native -run '^$' ./internal/nativetest/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/koinon-remote-linux-amd64 ./cmd/koinon
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/koinon-remote-linux-arm64 ./cmd/koinon
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o /tmp/koinon-remote-darwin-amd64 ./cmd/koinon
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /tmp/koinon-remote-darwin-arm64 ./cmd/koinon
```

Review private-content matches, the Unreleased changelog, and record/index
consistency. WDR 004 requires Linux/macOS CI and independent review on the frozen
commit; WDR 006 reserves native service-manager execution for disposable runners.
Missing platform or device evidence stays explicit and is not replaced by a
passing static build.

## Implementation evidence — 2026-11-11

The implementation source was frozen as Git tree
`77411a529c2c38030b85800ad42cb578d221c5d6`, based on develop commit
`3b69b92d64a040fb4303ccc011211e3e5c38fb9a`. These are source identifiers, not
claims of a signed commit or live deployment. This evidence section was added
after the run; it changes no executable source.

Local environment: macOS 14.8.3, arm64, Go 1.27.3; installed Tailscale 1.102.3.
A final read-only inspection still reported Tailscale Stopped, host offline,
no certificate domains, and empty Serve configuration. No live mapping was
changed, no shared service was restarted, and this build was not installed.

| Check | Observed result |
| --- | --- |
| Isolated handler and authentication | Synthetic Go tests passed for exact Host, Origin, forged forwarding headers, remote/local cookie crossover, origin-bound sessions, CSRF, login/logout/expiry/secret change, and absent `/v1/` routes. |
| Shared actions and attribution | Integration tests passed for remote message receipt, verdict attribution, local view continuity, conflicting local verdict and secret-change invalidation. |
| Persistent lifecycle and CLI | Synthetic adapter and real ephemeral loopback tests passed for repeat enable/disable, saved port/URL, restart, interrupted setup/cleanup, conflicts, owned removal, unavailable daemon/status, unknown secret observation, uninstall recovery and renamed-identity recovery. |
| Listener ownership | Repeated race tests passed, including ten lifecycle/port/conflict repetitions during correction and five independent final remote-test repetitions. Shutdown removes forwarding before releasing the backend socket; explicit re-enable transfers its reserved socket directly to the server. |
| Mobile behavior | Node DOM simulations passed for refresh, held drafts, stale timing, foreground/online events, selected submitters, duplicate taps, uncertain responses and no write replay. These are synthetic script checks. |
| Viewport browser probe | Unavailable: installed Chrome 157.0.8103.50 accepted CDP commands but timed out loading the first synthetic 360px page. No viewport, keyboard, zoom or physical-device pass is claimed. |
| Static builds | All four `CGO_ENABLED=0` builds passed: linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. |
| Static and repository checks | `go vet ./...`, empty `gofmt -l .`, whitespace checks, added-content privacy review, JavaScript syntax, native-tagged test compilation and native-tagged vet passed. Native service-manager tests were not run on this shared host. |
| Independent reviews | The other agent family recorded separate peer-review PASS and refusal-review PASS verdicts on the full source tree above. It reproduced focused race tests, five final remote repetitions, package vet and whitespace checks. Commit-bound GitHub gates remain pending. |

The final full race run used:

```sh
COPYFILE_DISABLE=1 LC_ALL=C go test -json -race -timeout 30m ./...
```

It completed with **exit 1**. Core passed in 824.275 seconds, the CLI in
141.628 seconds, MCP in 325.408 seconds, and the new adapter passed. All packages
affected by this feature passed. The failures were `internal/platform` and
`internal/setup`, with the two underlying cases below. JSON test events counted
1,765 passing, nine skipped, and three failing test/subtest outcomes; the Grok
parent and its subtest account for two of the three failures. Skipped real-proxy
and viewport probes are not acceptance evidence.

### Baseline failures and investigation

The same cases were reproduced in an unchanged, clean checkout of
`3b69b92d64a040fb4303ccc011211e3e5c38fb9a`; none was fixed in this feature.

| Case | Reproduction and evidence |
| --- | --- |
| `TestProcessEnvironment` | `go test -count=1 -run '^TestProcessEnvironment$' ./internal/platform` returns an empty synthetic marker with no error. A temporary owned-child probe showed `kern.procargs2` omitted the marker from a `/bin/sleep` buffer entirely, while an owned Go child exposed it and parsed correctly. The OS omission's cause remains unknown; this is not attributed to SIP without evidence. |
| `TestGrokRefreshInterval/local_time` | `go test -count=1 -run '^TestGrokRefreshInterval/local_time$' ./internal/setup` fails restoration. BurntSushi TOML 1.5.0 round-trips the synthetic local time `07:32:00` as `14:32:00` under America/Denver, so the semantic safety check rejects it. The same targeted test passes with `TZ=UTC`. |
| `TestHomebrewBottles` | Initial uncorrected runs included macOS AppleDouble provenance entries. The baseline reproduces it; `COPYFILE_DISABLE=1` makes the targeted test pass and was used for the final full run. |
| `TestHandoffSeedBlock` | Initial runs printed an unavailable `C.UTF-8` locale warning. The baseline reproduces it; `LC_ALL=C` makes the targeted test pass and was used for the final full run. |
| `TestResumesNeedsTheSameStateVersion` | Earlier full and baseline runs intermittently reported the synthetic child killed near the test's 300ms probe deadline. Five isolated baseline repetitions passed, and the final full CLI package passed. The kill's cause remains unproved. |

An introduced partial-startup cleanup panic and a listener Close-before-Serve
race were found during implementation and fixed before the final source freeze.
The existing unsafe-startup test and repeated remote lifecycle tests passed after
the corrections; the final full core package passed.

### Remaining acceptance and publication evidence

Connect Tailscale and enable its HTTPS prerequisites, coordinate the temporary
port, then run the opt-in real Serve command above. Repeat the proxy/lifecycle
matrix on macOS and Linux. On a phone with Wi-Fi off, sign in, read agents, send
a message and give a decision verdict; verify receipt and the recorded verdict
on the host. Complete the Safari/Chrome, layout, keyboard, zoom and reconnection
rows with actual device and browser versions. Verify real retained-name behavior
and the documented name-change recovery when the platform permits it.

Local Git author and signing identity were unavailable. Changes remain staged on
the dedicated feature branch, with no commit, PR, merge or release created. The
project requires signed DCO commits and commit-bound reviews/statuses; the local
tree reviews above do not claim those gates passed. After signing is available,
freeze the commit, have the independent reviewer bind its verdicts to that exact
head, and run the required Linux/macOS and native lifecycle CI.


## Dashboard setup UI follow-up — 2026-11-11

The earlier implementation evidence above describes its frozen source and the
conditions of that run. It is historical evidence, not the status of a later
onboarding UI change. In particular, its staged/no-commit description is historical;
the remote-dashboard implementation was subsequently committed locally.
The UI follow-up is based on committed remote-dashboard
checkpoint `3b6611ebd80d66dcacf5c7f87f55fc1daa402955`. Its final source, repository
checks, and independent verdicts must be recorded against the final follow-up
content before claiming those checks passed.

The follow-up adds **Go remote** and **Settings → Remote access** using the
existing dashboard interface. Its automated acceptance covers authenticated
status and URL-only QR reads, explicit setup/enable/disable, native approval-link
validation, unchanged Serve ownership, and a remote disable that can close its
own connection without waiting on its own request lock. Read, foreground, and
reconnect behavior must retain the no-write-replay rule. Synthetic adapter and
loopback tests do not establish account approval, real Serve forwarding,
cellular access, or physical-device layout acceptance.

For a manual UI check, open the updated dashboard and:

1. Select **Go remote** and confirm it opens **Settings → Remote access** in the
   existing navigation and visual style.
2. Select **Check my setup**. If the section shows an approval link, complete the
   Tailscale account step in its tab, return, and select **Check again**. Confirm
   that returning does not enable access until **Enable remote access** is pressed.
3. Inspect the enabled URL and scan the QR on a phone connected to the same
   Tailscale network. Sign in with the existing secret. Turn Wi-Fi off, send a
   message and verdict, and verify both results on the host.
4. Interrupt a status read and confirm that unavailable or stale data is labeled.
   Reconnect and confirm no submitted action is replayed.
5. Open **Turn off remote access**, read the explanation, then press the button.
   If the remote connection closes, inspect the local dashboard or run
   `koinon remote status` to verify shutdown and owned-mapping cleanup.

Record actual platform/browser versions and results separately. This subsection
adds a checklist; it reports no new test pass, service restart, or live settings
change.


## One-time browser QR amendment — 2026-11-11

Andrew reported: “Ok, I put in secret. It worked on my phone.” This is reported
phone password sign-in success. The report does not identify a browser version,
phone OS, cellular network, or host-side message/verdict evidence. It does not
complete the cellular and device matrix. Andrew then explicitly chose one-time
browser QR sign-in and ordinary-URL password fallback (PDR 010, Decision 4).
The earlier URL-only QR evidence remains evidence of that earlier implementation,
not verification of this authentication change.

The updated flow needs separate automated and real-browser checks:

| Area | Required check |
| --- | --- |
| Ticket creation | Requires a signed-in session, expected Origin, and session-bound CSRF; creation returns an in-memory QR credential without changing Serve configuration. |
| Scope and lifetime | One successful redemption, two-minute expiry, exact configured remote-origin binding; replay, wrong origin, expiry, disable, secret change, and restart reject outstanding tickets. |
| Landing GET | Opening or previewing `/dashboard/pair` does not consume a ticket. The ticket is a fragment, never a query or log field. |
| Phone redemption | Script clears the fragment before one POST; the resulting remote cookie has the existing Secure, HttpOnly, Strict and path attributes. Invalid tickets cannot start a local or API session. |
| Interrupted redemption | Lost response is not retried automatically; create a new QR or use password sign-in. |
| Ordinary access | Signed-out visits to the copied or bookmarked dashboard URL require the existing password. The CLI QR remains an ordinary URL shortcut. |
| Remote header | Go remote remains on the local surface at narrow widths, and is absent from the actual remote surface at phone and desktop widths. |

For the device check, connect Tailscale on the phone, create a fresh **sign-in QR**
from the signed-in computer, scan it, and confirm that it signs in without password
entry. Test a second scan and an expired code, then verify ordinary URL access in
a signed-out browser still asks for the Koinon password. Record actual device,
browser and network details. Run the cellular read/message/verdict test separately
with Wi-Fi off and verify results on the host.

This subsection records the reported password result and the amended checklist.
It claims no automated, cellular, ticket-redemption or physical-layout test pass.


### Reported short phone session and lifetime refinement

Andrew reported a phone sign-out after about five minutes away and asked for a
longer session. The prior implementation's configured idle limit was 24 hours,
with seven days absolute, so five minutes was not its configured expiry. The
cause remains unknown; this record does not treat the longer limit as a verified
fix for that report.

The follow-up extends only remote browser idle time to seven days and retains
the seven-day absolute maximum. Local browser and maintainer API-token idle time
stays 24 hours. The QR's two-minute expiry limits an unredeemed ticket, not the
session it opens. Verify these cases separately, including revocation on logout,
disable, password change and restart. Browser-cookie loss can require a new
sign-in regardless of the server lifetime. No new test result is claimed here.
