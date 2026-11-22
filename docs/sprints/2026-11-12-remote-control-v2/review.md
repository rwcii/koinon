# Local independent review

Current publication: [draft PR #673](https://github.com/rwcii/koinon/pull/673).
The implementation notes below are historical checkpoints. The continuation
decision and the cross-family findings below supersede their open questions.

## Original implementation review

Same-family subagents reviewed implementation independently of their owned code.
These are local findings and dispositions, not cross-family commit-bound statuses.
No active same-repository other-family reviewer was available in runtime discovery.
No PR was published; exact-head cross-family review and CI remain outstanding.

| Finding | Disposition |
| --- | --- |
| Room recovery read `room` while forms submit `room_id` | Corrected; retained synthetic isolation tests |
| Refresh 401 did not clear saved drafts on observed revocation | Corrected; retained browser test |
| Changed-content refusal could navigate away and clear draft via original accepted receipt | Accepted-notice allowlist preserves form; disputed operation keeps draft; retained stale_revision/decided tests |
| Client clock rollback discarded future pending IDs | Retain IDs and reconcile explicitly; retained test |
| Accepted original receipt could clear later edits | Clear only matching drafts written no later than operation creation |
| Device controls reused stale CSRF after restart indefinitely | Refresh device metadata after 403 without replacing label inputs or replaying POST; retained Node and Go regression passed |
| Attention read cursor bypassed storage admission | Uses standard ordinary write transaction; blocked-store regression added; separate narrow transaction/locking review passed |
| Draft saving matched private memory editor bodies | Restrict message bodies to send/room-post form actions and notes to decision forms; retained negative memory-editor test passes |
| Session writes bypassed storage admission | Use ordinary/control store transactions |
| Failed logout read reported success | Deny bearer immediately; return incomplete durable revocation |
| Copied cookie origin rejection tombstoned original valid login | Reject request only; revocation follows trusted config reconciliation |
| Offline origin change could later revive old-origin sessions | Trusted startup/enable reconciliation durably revokes other-origin sessions |
| Saved disabled state on startup did not revoke with empty runtime config | Startup reconciliation now durably revokes |
| SQLite attached WAL databases cannot guarantee receipt atomicity | Use same main database transaction and backward-compatible namespaced rows |
| Exact schema validation could prevent rollback | Keep schema 37; pristine previous-reader open/catalog/settings test passed |

Residual engineering limitation: if storage cannot record expiry evidence, the
in-memory denial cannot survive process loss. A subsequent server clock rollback
with repaired storage can make an unrecorded expiry appear future again. Successful
expiry tombstones survive restart and rollback. This combined storage-failure/time
case remains a limitation, rather than a claim of durable evidence that never wrote.

Plan review confirmed the supplied delivery order, scope and separate actual-device
acceptance. The local plan is not a published sprint or a merge-ready verdict.


The sprint skill says "Commit the plan under project policy"; repository ship facts
require "Cryptographically signed, human-authored DCO commits (git commit -S -s)".
No signing keys are available in this checkout's environment. The plan and change
therefore remain reviewable in the worktree, without an unsigned checkpoint or
invented human identity. No permission request or publication is implied.


Final integration review reproduced the device-control CSRF finding at source
fingerprint f5398e111ea9eb3406a57ec1c9650add620a092b35bb84f6489dc894e8659827.
The correction and retained device test produce fingerprint
fd23f332dcb969defa2185b0fad24e61a8b7a3aa9ea4c193ac49f27a0e5767f4. The parent
reviewed the narrow correction and its no-replay/label-preservation regression.
A subsequent parent correction put Attention cursor writes through storage admission; a separate narrow review passed. Final required checks were restarted after both corrections.

Final draft eligibility correction excludes memory editors despite their shared `body` field name. The matching negative test and all existing recovery cases pass directly in Node. Required checks were restarted with GOMAXPROCS=2 on this final source to reduce shared-host contention; earlier runs used 4.


Final implementation fingerprint after the draft guard:
26ecf4e47389b0d7427ef59b073751d5feaca9a6776cd823f265be23b130e68e.
Separate narrow privacy review passed against the actual templates: send/room
bodies and decision notes remain eligible, memory editors and password/input
fields are excluded. Both saving and recovery share the guard.


## Local checkpoint instruction — 2026-11-12

After the implementation report stated that no signing key was configured and the
changes remained uncommitted, Andrew instructed: "ok commit all changes so far. whats
next?" This is interpreted as authorization for an unsigned local DCO checkpoint
of the full current change, including the preserved draft specification. GitHub
identity was verified as Andrew Deugarte, andyde8, account 153491212. No signing
configuration or publication policy was changed, and no push/PR was requested.


## Commit 7322934 continuation review — 2026-11-12

A separate Codex reviewer read the frozen commit without editing it. It found
that failed expiry persistence entered the in-memory deny list, then the deny-list
fast path skipped every later durable retry. This could leave expiry unwritten
even after storage recovered. The failure also omitted the incomplete-revocation
flag and lost the expiry reason on the rejected response. Accepted.

The follow-up retains each denial reason, retries on a later bearer request, and
retries pending rows when host diagnostics are read. The diagnostics path matters
when the rejected browser has discarded its cookie. A failed write keeps access
denied and incomplete persistence visible. Successful persistence clears the
pending row. Diagnostics exclude pending denials from the active count.

The reviewer also identified a mixed-result reporting defect in the follow-up:
a later successful write could hide an earlier failed write in one diagnostics
response. Accepted and corrected by aggregating failures across that response.
Three regression tests cover request retry through rollback and store reopen,
host-only diagnostic retry, and mixed successful/failed diagnostic retries.
The first regression failed on the original code because incomplete persistence
was reported as false. This is local independent review; no cross-family status
or frozen follow-up commit approval is claimed.

This correction does not create evidence when all durable writes fail and the
process ends before a retry succeeds. The original draft already describes that
limit for failed revocation. A fallback journal and an unclean-start invalidation
policy were raised with Andrew. He subsequently chose crash survival with the
documented total-write-failure limit; see [decision.md](decision.md).


## Cross-family review of 6fbdea9

The independent Claude reviewer completed the frozen-commit peer and refusal
reviews. Its broad focused race suite passed in 347.087s; targeted Node recovery
and device tests ran. Both verdicts requested changes.

| Finding | Disposition |
| --- | --- |
| P1/R1: any backward host-clock step permanently expired a valid session | Accepted: retain the persisted observation high-water mark and original deadline; only deadline expiry creates an expiry tombstone. Add small-correction/restart regression. |
| P2/R2: a fast phone clock caused valid receipt-backed actions to fail | Accepted: use an advertised host-time anchor and monotonic browser elapsed time, separate from local draft timestamps; document original-at freshness and host acceptance retention. Add fast/slow/changed-clock and retained-ID regressions. |
| P3: host diagnostics missed global or unreadable-inventory revocation retries | Accepted: queue target/reason even before rows are readable; resolve known rows individually once storage recovers. |
| P4: unreadable-store logout omitted incomplete revocation | Accepted: queue the bearer hash/reason for host-only recovery; add store-close/reopen regression. |
| P5: forget-all rewrote a previously persisted end reason in memory | Accepted: preserve the row's original reason; add expiry-reason regression. |
| R3: lifetime decision lacked its complete context and direct quote | Accepted: verify the original conversation and record the question, alternatives, full name, answer and scope in PDR 010 §9; ADR and sprint cite it. |
| R4: historical checkpoints looked like current unanswered questions | Accepted: label history and point current readers to the continuation decision and draft PR. |
| Attention cursors have no row reclamation policy | Tracked in #672; existing physical storage admission still applies. No new cursor-retention promise is introduced in this correction. |
| Device script reads a diagnostics field absent from the response | Minor cleanup tracked in #672; no security or acceptance effect was established. |

Accepted corrections require follow-up review on their committed head. The prior
347.087s pass and native CI results remain bound to 6fbdea9; they do not approve
later source changes.
