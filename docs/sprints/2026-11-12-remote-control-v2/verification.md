# Remote control v2 verification

## Current acceptance run

Andrew approved retaining crash survival with the documented total-write-failure
limit, and approved an unsigned DCO follow-up and draft PR for CI. See
[decision.md](decision.md) for the questions, answers and scope. Current develop
was merged before preparing the follow-up. The combined focused v2/auth/QR race
run passed in 13.064s on that integrated source, and `go vet ./...` passed.
Exact-head full-suite, CI and review
results belong to the draft PR and private run logs; the pre-integration results
below do not imply those runs passed. Andrew has an iPhone for Safari acceptance;
Android and elapsed retention remain unmeasured.

## Pre-integration checkpoint after commit 7322934 — 2026-11-12

The entries below this section describe the committed implementation. The
follow-up currently consists of uncommitted expiry-retry changes, reviewed
independently by a same-family reviewer. Failed expiry writes now keep their
reason, report incomplete persistence, retry on bearer requests and host
diagnostics, and remain denied during clock rollback. Mixed retry failures
remain visible even if another pending write succeeds in the same diagnostics
response. See [review.md](review.md) for the findings and dispositions.

| Follow-up check | Result |
| --- | --- |
| Regression on original runtime | FAIL as expected: expiry persistence failure hidden (`false`) |
| Focused v2 race suite, `-count=1`, including three new regressions | PASS, 10.300s |
| Local/API lifetime and QR policy race suite, `-count=1` | PASS, 17.663s |
| `go vet ./...` | PASS |
| Native-tagged compile and vet | PASS; service-manager tests not executed |
| Four CGO-free builds | PASS: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 |
| Original supplied specification | Unchanged, blob `badcc108a863a75e46832f005e5342a5bac0bd78` |

Logs are private under `remote-control-v2-evidence/expiry-focused-race.log` and
`expiry-builds-vet.log` in the main checkout's ignored handoff directory.
The full repository race suite has not been rerun on this follow-up; the previous
full-suite failure below remains a historical result, not a follow-up pass.
The branch is two upstream commits behind `origin/develop`; final integration
and required exact-head CI remain open. No follow-up commit, push or PR was made.

No actual phones or disposable native runners are connected. The local Xcode
command-line installation has no `xctrace` device tool, `adb` is unavailable,
and OrbStack lists no Linux machines. Docker is available but its existing
application containers were left alone. These observations do not establish
that devices or external runners are unavailable to the maintainer. Access was
requested in the conversation. No phone, elapsed-retention, native service or CI
pass is claimed. See [acceptance.md](acceptance.md) for the remaining run sheet.

The pending retry fixes the recoverable-storage case. Process loss before any
successful durable write, followed by arbitrary host-clock rollback, remains
unresolved. A separate fallback journal only protects against database-specific
failures; total-storage failure still loses evidence. Invalidating sessions after
unclean shutdown would trade away crash survival. Those alternatives were raised
in the conversation; neither was adopted without an answer.

The supplied draft specification is preserved unchanged. Implementation began at
`dfcca45` and the branch fast-forwarded to `848572461ba78d21c891c0289d178b8b32f00315`
for the tailnet-only dashboard setting before integration. No shared daemon binary
or service, live Tailscale configuration, PR, merge or release was changed.

The final implementation fingerprint is
`26ecf4e47389b0d7427ef59b073751d5feaca9a6776cd823f265be23b130e68e`:
SHA256 of the 34 changed/untracked internal/ paths in sorted order, each followed
by NUL, file bytes and NUL. Documentation is outside this implementation fingerprint.
The final round uses `GOMAXPROCS=2`; earlier rounds used 4 on this shared host.
The full race command keeps the project's 30-minute per-package timeout.

## Final automated evidence

| Check | Result |
| --- | --- |
| Focused v2 acceptance with `-race -count=1` | PASS, 87.210s |
| Existing local/browser/API lifetime, restart and origin checks | PASS in the final compiled race-test binary before the last browser-only draft guard |
| Actual previous-reader database compatibility | PASS, 0.730s parent rerun on pristine baseline code (earlier 2.865s) |
| `go vet ./...` | PASS |
| `gofmt -l .` | PASS, no output |
| `git diff --check`, base comparison and private-content review | PASS |
| Four CGO-free builds: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 | PASS |
| Native-tagged compile and vet | PASS; no service-manager tests executed |
| Direct Node recovery, submit and device-control scripts | PASS |
| Optional Chrome viewport harness | FAIL: 360px Sessions load timed out; unchanged binary also timed out. No layout acceptance claim. |
| Full final `go test -race -timeout 30m ./...` | FAIL; exact package results below |

Focused acceptance covers DurableRemote, RemoteBrowserRecovery,
MobileBrowserRefreshAndWrites, RefreshKeepsFormsInUse, SubmitUsesTheFormAttributes,
NoControlHides, Attention, OperationReceipt and DeviceBrowser tests. It includes:

- Fixed deadline, expiry boundary, restart/store reopen, durable tombstones and
  simulated clock rollback, offline password/origin changes and disabled startup.
- Capacity without eviction, storage failure/corruption, failed logout and storage
  admission; old CSRF rejected and fresh tokens loaded without submitting forms.
- Concurrent duplicate operations, changed payload conflicts, lost-result lookup,
  reauthentication, expiry, retained revision conflict and failed receipt write
  rolling back its mutation. No automatic browser POST replay.
- Browser draft opt-in, explicit recovery, expiry and storage failures; room target
  isolation; pending IDs retained after client clock rollback; newer/changed text
  preserved. Memory-editor bodies and password/input fields are excluded.
- Device controls refresh CSRF after 403 using only a read, preserve entered labels
  and require another explicit click. Failed metadata refresh leaves text usable.
- Attention source counts/cursors and links, no agent inbox acknowledgement or
  decision resolution, unknown activity and invalid receipt-version handling.

Previous-reader evidence inserts versioned v2 session/receipt namespace rows,
closes the database and calls unchanged baseline openStore/loadSettings/catalog
validation. The schema 37 catalog opens and rows remain. This proves database
reader compatibility, not a native binary downgrade or service-manager upgrade.

Some interim builds overlapped source/template edits and failed with embedded-file
length mismatch or missing dashboardAccess during the base update. These invalid
snapshots produced no test evidence; checks were restarted on frozen source.

## Baseline reproduction and broad-run failures

The full race run started on unchanged runtime before implementation failed:
TestContextCyclePickupDialogsOnDashboard reproduced a race between fakeAdapter.Inspect
(context_cycle_test.go:59) and pickupDialog (context_cycle_pickup_wait_test.go:21).
Core then reached 30m in TestDeepSeekCycleFlow. TestProcessEnvironment and
TestGrokRefreshInterval/local_time also failed. CLI passed in 737.619s and launcher
passed in 237.468s in that run; those passes do not reproduce the historical CLI failure.
Other packages included cached results. No repairs were made to these failures.

A separate pristine dfcca45 archive ran a bounded five-minute race subset. It
failed the schema-six migration fixture (registration expired), CLI import timeout,
launcher nested scans, platform environment observation and setup hook/Grok tests.
The shorter budget and observed compiler/test contention are explicit limitations;
these are not a clean baseline or proof of each historical failure's cause.

Targeted pristine-baseline runs then reproduced:

- CLI startup deadlines in TestServeStatusAndShutdown, TestMessageCommands,
  TestParticipantCommands and TestServeAndClientsUseRecordedPort, plus maintainer
  secret daemon-unavailable failure: 107.797s.
- TestHomebrewBottles with Apple metadata archive entries and TestHandoffSeedBlock
  with an unavailable C.UTF-8 locale warning: 3.684s.

Earlier complete implementation runs repeated the context-cycle fixture race and
core 30m timeout, CLI startup, contributor, platform and setup failures. Launcher
scans failed in one run and passed in another. MCP reached its 30m package budget
in TestIdentitySources; the isolated unchanged identity test passed in 231.202s.
TestDashboardContextPolicy also failed a timing assertion (expected source/confirmed
0s, observed 1s), while its isolated baseline rerun passed in 10.732s. The MCP package
timeout and context-policy timing observation remain unclassified verification
gaps, not proven v2 regressions or silently repaired baseline problems.

## Remaining acceptance and limits

No actual iOS Safari or Android Chrome versions or elapsed one-hour, overnight or
48-hour tests were measured. Cellular/VPN/sleep transitions, close/reopen and
home-screen cookie retention, keyboard/zoom at 360/390px, and actual native
Linux/macOS reboot, abrupt-death, upgrade and downgrade acceptance remain open.
Synthetic clocks, headless desktop Chrome and cross-compilation do not establish
these results. Live Serve/native-service test opt-ins were disabled.

Storage that cannot write expiry evidence cannot preserve an in-memory denial
through process loss. If the host clock then rolls backward after storage is
repaired, that unrecorded expiry can appear future again. Successful tombstones
survive restart and rollback; the combined failed-write/process-loss/rollback case
remains a limitation rather than a durability claim.

At capacity, a new login returns 409 and directs the person to an already signed-in
browser or host Settings to forget a session. It does not expose the inventory on
the capacity error page. This differs from the draft's suggested capacity-page list.

The original unexplained short phone sign-out has no verified v2 incident cause.
The old restart-clears-memory path was identified and replaced; that is not proof
that it caused the historical report. New reason codes and observations support
future diagnosis after authorized deployment.

Local independent reviews and corrections are in [review.md](review.md). No PR or
publication CI was created. Cross-family commit-bound reviews and exact-head CI
remain outstanding, as do the actual-device/platform acceptance matrix. The implementation initially ended without a signed checkpoint because no signing
key is configured. Andrew subsequently instructed committing all changes after
that signing-key note; the local unsigned DCO checkpoint interpretation and policy
sources are recorded in review.md. Raw logs and a complete patch backup are
private under the main checkout's ignored handoff directory.


## Completed final full-run result

The final command exited 1 on the final implementation fingerprint above:
`GOMAXPROCS=2 go test -race -timeout 30m ./...`.

| Package | Result |
| --- | --- |
| cmd/koinon | FAIL, 1051.274s: startup deadlines, maintainer daemon unavailable, participant and recorded-port tests |
| internal/contributor | FAIL, 124.993s: Homebrew Apple metadata entries and handoff locale warning |
| internal/core | FAIL, 1801.325s: schema-six migration fixture registration expired; 30m package timeout during TestSchemaThirtyOneKeepsState |
| internal/launcher | FAIL, 545.109s: TestNestedScanBounds |
| internal/platform | FAIL, 11.747s: TestProcessEnvironment |
| internal/setup | FAIL, 197.581s: TestGrokRefreshInterval/local_time |
| internal/mcp | PASS, 425.586s |
| internal/importer | PASS, 78.990s |
| Other tested packages | PASS, including cached results where Go reused unchanged test inputs |

No data race was printed by this last run. The context-cycle fixture race was
reproduced on unchanged runtime and earlier implementation runs; it was not
repaired or claimed fixed. The final MCP pass supersedes earlier package-timeout
observations as the final-source result. The full suite is not clean, and the core
timeout means it did not finish all core tests. The focused final v2 acceptance
run completed successfully and covers the new behavior listed above.

No unrelated runtime repairs or test suppressions were introduced. Final formatting,
whitespace, original-spec hash, unstaged state and private-content checks were
rechecked after documentation completion. The private patch includes new files,
excludes the supplied spec, and validates with reverse `git apply --check`; a
separate exact spec copy is preserved privately.
