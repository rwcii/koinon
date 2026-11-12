# Remote control v2 decision

Source: Andrew, direct implementation request, 2026-11-12, and
`docs/REMOTE-CONTROL-V2-SPEC.md`. Implement the supplied scope. Remote browser
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
