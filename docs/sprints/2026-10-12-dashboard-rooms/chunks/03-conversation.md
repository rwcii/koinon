# Chunk 03 — conversation, delivery outcomes and final proof

Depends on chunk02; cite decision.md and definition-of-done.md for the remaining view,
refresh, family, audit, retention and end-to-end checks. No new room authority or scheduling.

Complete the dashboard room page with one monotonic-sequence transcript: sender, authenticated
origin, time, inert body and the frozen recipient snapshot's delivery and acknowledgement.
Join existing live message outcomes by non-reused AUTOINCREMENT IDs. For deleted inbox rows,
show not_retained and unknown acknowledgement instead of a fabricated result. Display skipped
inactive recipients and remaining agent-exchange allowance. Adding/removing a member never
rewrites the recipient snapshot of an earlier post. Read-only views acknowledge no agents.

Use bounded keyset paging by room sequence (at most 100 posts/one MiB body data per page),
with explicit more/next cursor and a retention floor/gap marker. Refresh by the existing
partial-view mechanism without duplicating/reordering old posts or losing paged history.
Escape sender/name/body as the existing dashboard does; render no active peer HTML/scripts.
Keep authenticated reads and existing CSRF-protected mutations distinct. Room list/detail
links and membership controls use stable IDs, never ambiguous names or private native IDs.

Synthetic end-to-end tests cover dashboard human post, each family replying, cross-room
isolation, concurrent roster/post changes, expiration, exact-native re-registration versus
replacement, missed wakes, read/ack separation, allowance exhaustion/refill, restart and
retention gaps. Verify every accepted membership/post has its same-transaction audit record,
without message bodies/credentials in the audit. Existing Go and guarded native CI must pass
at the reviewed final head. Update docs, tool-purpose/family guards and Unreleased, and close
#217 only after the merged full deliverable is verified. #206 remains a separate issue.
