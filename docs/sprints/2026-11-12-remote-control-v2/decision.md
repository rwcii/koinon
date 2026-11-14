# Remote control v2 decision

Source: Andrew Deugarte, direct implementation request, 2026-11-12, and
`docs/REMOTE-CONTROL-V2-SPEC.md`. PDR 010 §9 records the lifetime question,
alternatives and his direct answer: “The confirmed session policy is a fixed
48 hours from sign-in, with no automatic extension.” Implement that supplied scope. Remote browser
sign-in lasts exactly 48 hours from authentication without extension, including
routine restart and upgrade. Local browser and API-token policy is unchanged.

Success is the spec acceptance matrix AUTH-1–5, PHONE-1–2, FORM-1, ACTION-1,
UI-1 and LIFE-1, with automated, simulated, reported and actual device evidence
identified separately. The initial authorization excluded shared daemon or live Tailscale changes,
PR publication, merge and release; the continuation below updates publication scope. The original draft remains preserved.


## Acceptance continuation — 2026-11-12

**Decided by:** Andrew Deugarte. **Date:** 2026-11-12.

**Context:** A rejected expiry write could remain unwritten after storage repair.
The retry correction preserves the expiry reason and retries from a rejected
browser request or host diagnostics. If every durable write fails and the process
ends before a retry succeeds, a later host-clock rollback can still make the last
stored session appear unexpired. The question was whether to retain crash
survival with this documented limit, or sign out all remote browsers after every
unclean restart. The recommendation was to retain crash survival.

**Decision:** Andrew answered: "Yes — keep crash survival (recommended)".
Keep restart continuity and the retry correction. Document the combined
storage-failure/process-loss/clock-rollback limit; do not add crash-triggered
invalidation or claim evidence survived a write that failed.

**Reason and scope:** Routine crash recovery should retain remote sign-in within
its original fixed 48-hour deadline. This decision covers failed expiry evidence;
it does not change committed revocation, fixed deadlines, local/API policy or
storage privacy. Revisit if a trusted clock source or a requirement to fail closed
after every crash is introduced.

**Context:** Linux/macOS CI and native service tests require a published branch.
The choice was to keep the change local or publish a draft PR, with signed
commits required or an explicit unsigned DCO follow-up exception.

**Decision:** Andrew answered: "Yes — draft PR; unsigned DCO follow-up allowed".
Commit the tested follow-up with DCO sign-off and publish the branch and draft PR
for CI and project review. The exception covers this continuation's integration
and follow-up commits. It does not change repository-wide signing policy.

**Reason and scope:** Run CI and independent review while actual-device acceptance
continues. Do not merge, release, close issues, change the shared daemon or change
live Tailscale configuration under this publication authorization.

**Device evidence:** Andrew answered "I have an iPhone only" to the real-device
availability question. Plan actual iOS Safari testing; Android Chrome remains
unverified until a real device is available. This availability report is not a
waiver of Android acceptance.


**Preview scope — 2026-11-12:** Andrew Deugarte answered “Yes — add the temporary
private preview route” to adding private Tailscale HTTPS port 9443 for the isolated
iPhone test. The alternative was to keep the preview local. The question stated
that preview state/password are separate, the existing 443 route remains unchanged,
and only the preview route will be removed after testing. This authorizes that
temporary route and preview process lifecycle only; it does not authorize replacing
the shared daemon or altering other Serve mappings. Before/after mapping evidence
is kept privately. Revisit when preview testing ends and the route is removed.

## REL-SCOPE-673: remote control v2 in v0.11.0 — 2026-11-13

- **Decided by:** Robert Capps.
- **Date:** 2026-11-13, revision 2, approved. Andrew Deugarte (@andyde8) proposed the
  design and the policy choices above.
- **Context:** whether release v0.11.0 (REL-NEXT-VERSION) contains this pull request. The
  first revision recommended a release without it, because its device and retention
  acceptance was open, its policy choices were recorded as Andrew's, and the maintainers'
  team had not reviewed it. Robert answered that revision: "this is the next release. Why
  defer it?", and in the terminal: "673 comes with .11". The options were to release without
  it, or to include it with his decision on its policy choices, a cross-family review and one
  real-device check.
- **Decision:** Robert approved the inclusion. He takes as his own decisions the fixed 48-hour
  remote sign-in that survives a restart, and crash survival with its combined
  storage-failure, process-loss and clock-rollback limit (both above). Before the merge,
  codex-koinon reviews the head that is merged with develop, and Robert signs in on an iPhone,
  restarts the daemon and checks the sign-in after one hour. The Android Chrome, overnight and
  48-hour checks stay open in #672 after the release.
- **Reason and scope:** v0.11.0 ships remote control v2 with the maintainer's decision, a
  review by a second agent family and one real-device check. This covers this release; the
  checks that stay open are not waived for later releases.
- **Review when:** an Android, overnight or 48-hour check in #672 fails.

## Safari on a laptop for the one-hour check of v0.11.0 — 2026-11-14

- **Decided by:** Robert Capps, directly in the terminal.
- **Date:** 2026-11-14.
- **Context:** REL-SCOPE-673 asks for an iPhone sign-in, a daemon restart and a check after one
  hour (board card PHONE-673-TEST). On a test daemon of the merged #673, the iPhone stayed
  signed in across the restart, and then the iPhone and a desktop browser both lost the
  sign-in while the daemon kept both sign-ins valid. The main lead, not proven: the installed
  Koinon on HTTPS port 443 and the test daemon on 8444 used one cookie name, and browsers do
  not separate cookies by port; #681 names the cookie after the port. Robert asked: "does the
  test need to be on a phone? can it be on Safari on a laptop?" The options were the
  one-hour check on the iPhone again, or in Safari on a laptop. A laptop does not test
  behavior that only iOS has, such as tabs that iOS closes in the background.
- **Decision:** "laptop Safari is enough for the release." The one-hour check of v0.11.0 runs
  in Safari on a laptop, on a test daemon of #681.
- **Reason and scope:** the iPhone part that ran (sign-in, restart, reload) passed, and the
  failure also hit a desktop browser. This covers v0.11.0 only. The result is recorded as a
  laptop Safari result, not an iPhone pass. The Android Chrome, overnight and 48-hour checks
  stay open in #672.
- **Review when:** a remote sign-in on an iPhone ends before its 48 hours.
