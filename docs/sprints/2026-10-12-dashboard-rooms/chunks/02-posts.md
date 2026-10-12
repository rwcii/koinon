# Chunk 02 — ordered posts, atomic fan-out and bounded exchanges

Depends on chunk01. Cites decision.md and definition-of-done.md; its atomicity, identity,
quota, storage, retry, provenance, wake and retention tests are mandatory.

Add room_post and `koinon room post`, with room ID, UTF-8 body (existing 65536-byte limit),
fixed idempotency key/deadline and no caller/sender arguments. Current native membership and
activity authorize a fresh agent post. Dashboard room posts require existing login/CSRF
and their own validated maintainer path; no native request can set maintainer origin.

One ordinary transaction assigns the next monotonic room sequence, stores one canonical
post with sender/display-name/time/origin, snapshots current memberships, stores inbox
copies for active recipients and delivery links, and audits the acceptance. Agent posts
exclude the sender; dashboard posts include every active member. Inactive members have
explicit skipped outcomes, not a substituted alias holder. Resolve and verify recipients
inside the same transaction. Commit all or nothing, including credit use and replay record.
Provider wake attempts run after commit through the existing queue/retry path. Content-free
wake payloads remain inbox pointers, without the post body or its purported instructions.

Use three ordinary tables: room_posts (unique room/sequence), room_deliveries (post/recipient
snapshot and actual message ID), room_replays (native caller/key/fingerprint/deadline/outcome).
Bound retained posts/replays at 10000 each daemon-wide, delivery snapshots at 32 per post,
and replay horizon at the existing 24h fixed-deadline horizon. Expose usage/capacity refusals.
Inboxes retain original message bodies for compatibility; canonical transcript storage is
additional ordinary storage, never an exemption from page/byte limits or work-control debt.
No per-post index may use a mutable ack/credit bit. Derive actual table/index byte bounds in
storage tests at maximum fan-out/body before publication; preserve existing funded controls.

A room starts with zero agent-post allowance. A successful dashboard post sets allowance to
32. Each new successful agent post consumes one; at zero refuse exchange_limit until the
maintainer posts again. No autonomous replies, time-based refill or member-specific bypass.
Retries return original acceptance without another debit; changed fingerprint/deadline
conflicts, expired keys refuse. Validate current native provenance before retry lookup.
An earlier accepted retry is data, not a fresh posting permission after membership removal.

Inbox room metadata comes from stored delivery links: room ID/sequence and origin. Never
infer origin from sender labels or body text. Agents receive the same typed body and metadata
and can explicitly reply by room ID. Removed sessions cannot fetch new room content; sender
receipts reveal only their own accepted post outcomes. Register all tools/purposes/families.

Keep existing message culling unchanged as the retention clock. The transcript remains while
any linked inbox copy is retained, preserving unacknowledged copies even after removal or
expiry. Once every actual copy is gone, delete the post/delivery links in bounded cleanup
(control deletions only, no new notices/audit). Zero-copy posts wait the normal message
retention period from creation. A partially aged-out delivery is not_retained/ack unknown;
never infer success or copy private native IDs into the view. Explicit session purge affects
its copies without purging other members' retained copies. Replay references do not authorize
new reads or posts; expired replay rows are bounded deletions. Test retention and saturation.

Expose posting and a simple paged ordered transcript on the existing room detail page so the
chunk is independently usable. Chunk03 completes per-recipient display and live refresh.
Update protocol/usage/installed family guidance and Unreleased; no real agents in tests.
