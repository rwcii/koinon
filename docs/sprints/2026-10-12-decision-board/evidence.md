# Decision board — integration evidence

This inventory records existing code and required verification. It is not a technical
chunk specification or product implementation. Source baseline: develop 496ec52,
read on 2026-10-12.

## Maintainer choices applied after the inventory

- PDR 007, Decision 1, records Markdown for card context, recommendation and comments,
  code in code blocks, and ASD-STE100 card prose
  ([#206 comment 6119203856](https://github.com/rwcii/koinon/issues/206#issuecomment-6119203856)).
  The static reference's plain-text renderer is a starting point, not the final text format.
- PDR 007, Decision 3, scopes printable ASCII to terminal-copy commands
  ([#306 comment 6119251724](https://github.com/rwcii/koinon/issues/306#issuecomment-6119251724)).
  Code displayed only for reading can contain non-ASCII text. Terminal-copy entries keep
  the original line-break, zero-width, bidirectional and look-alike exclusions.
- PDR 005, Decision 3, makes the authenticated dashboard verdict the maintainer's
  instruction and approval; no separate human-presence check is added. The existing
  dashboard-only verdict write path still distinguishes that origin from peer text.
  The source guide and MCP instructions now describe this origin after PR #311; the
  installed guide changes when that runtime is upgraded.

## Existing dashboard boundary

- `internal/core/dashboard.go` and `dashboard_auth.go` enforce loopback literal Host,
  matching Origin, dashboard login, and CSRF for mutations. The bearer secret does not
  authorize dashboard actions, and the dashboard cookie does not authorize `/v1/`.
- `internal/core/dashboard_actions.go` uses a typed action path and fixed notices. The
  board must use this boundary for human verdicts/dismissal; adding agent fields to a
  common update struct would not preserve that boundary.
- `internal/core/audit.go` and `storage.go` commit an accepted dashboard audit record in
  the same transaction as its change. If the record cannot fit, the change rolls back.
  Auth-check failures do not write an audit record. Targets and reasons contain fixed
  metadata, not the decision text, commands, credentials or private native identity.
- `internal/core/web/` supplies embedded templates/style/script under the existing CSP;
  pages do not need external fonts, scripts, a provider database or remote publication.
- Current views return fragments every five seconds while visible. Refresh pauses for an
  open disclosure or focused field and discards a response arriving after such a focus.
  Decision edits and optional verdict notes must preserve that behavior and detect stale
  revisions independently of refresh.

## Agent rows

- `internal/core/dashboard_data.go` projects sessions in keyset pages of 100, with active
  sessions first. `dashboard.go` attaches observation and work-claim data.
- `sessionClaims` groups live advisory claims by their stored consumer. The sessions
  page currently looks up native `FAMILY:ID` keys. A holder's default consumer can instead
  be `participant:ADDRESS`; the board's working projection must account for both, with
  participant work shown only on the current holder. This is a projection requirement,
  not permission to move or inherit a native claim.
- `internal/core/observations.go` supplies shared freshness/unknown reasons: registration
  active is not activity; stale or unsupported context is not a zero-token value. A
  daemon restart discards these memory-only observations until new evidence arrives.
- The board needs only public display names, safe work descriptions, context counts and
  decision refs. Existing session DTOs also contain native identities, repository paths
  and terminal/wake metadata; returning the whole DTO would violate board privacy.
  Work text remains author-controlled and must pass the board's content validation.

## Agent interfaces and caller identity

- `internal/mcp/tools.go` obtains identity from native metadata before tool dispatch and
  strictly decodes accepted arguments. Unknown fields are refused. New board create/edit
  arguments must omit verdict/dismissal/audit fields and row telemetry fields.
- `internal/core/server.go` strictly decodes JSON with a bounded request body. The daemon
  requires its secret and current caller; a model-supplied recipient is a resolution input,
  not the caller. The new bearer path must not route into the dashboard human operation.
- Existing native MCP families and DeepSeek's documented command path share daemon
  records. No second board store or provider-specific relay is needed for reads.
- Participant names can resolve to different native holders. A verdict delivery must
  specify its resolved recipient policy and test holder changes, expiry, retirement and
  retained records. A name lookup alone does not establish identity or wake availability.

## Durability, wake and capacity

- `internal/core/store.go` uses one SQLite database, transactional schema migration and
  an exact schema-catalog check. Current schema is 15. A future branch chooses its next
  schema version against the then-current develop, rather than assuming plans migrate in
  a fixed order. Extra indexes/triggers can invalidate control-allocation proofs.
- `internal/core/storage.go` guards every mutation, empty-WAL proof, ordinary/reserved
  page limits and existing durable work debt. A bounded row count is not an allocation
  proof or a promise that the physical page budget always has room.
- `internal/core/wake.go` serializes wake submission with acknowledgement, persists wake
  attempts, and can reconcile an uncertain provider result. Reading the existing inbox
  also moves its read marker; a board read must not suppress an unrelated inbox notice.
- `internal/core/wake_providers.go` accepts content-free notice strings and routes them
  through the actual exact-session adapter. Provider acceptance is not receiving-model
  proof. The board needs its own pending/read/handled semantics; it cannot obtain durable
  verdict delivery merely by appending a peer-authored summary to an ordinary inbox.
- Existing messages can become failed after bounded unanswered notices. A verdict delivery
  promise must remain queryable and pending until its explicit acknowledgement, including
  after daemon restart or archive. Wakes may repeat; handling must deduplicate the same ID.
- `docs/WORK-ITEMS-GO-STORAGE.md` documents the finite page debt and fixed-size flag-only
  controls. Any new funded promise or use of reserved capacity needs a checked derivation
  for its actual tables/indexes and must preserve all existing credits.

## UI reference boundary

The maintainer supplied a static board template. Its generic layout uses a maximum width
of 1180 pixels, system fonts and system light/dark colors, responsive cards, a horizontally
scrollable agent table, context meters, count indicators, optional verdict notes, disabled
pending buttons, inline save errors, separate command boxes and read-only reference lookup.
The reference shows context and recommendation line breaks. The new board renders those
fields and comments from Markdown, with code blocks and ASD-STE100 prose as stated above.
The existing item convention for manual work is an fyi title beginning `ACTION FOR YOU`;
the board sorts those items first.

The template's provider API, private live data, labels, repository policy and previous
missing-telemetry assumptions are not this deliverable. No private board was imported.

## Cases the definition of done must cover

- MCP, CLI and bearer attempts to supply every human-owned verdict/dismissal field.
- Dashboard auth/Origin/CSRF failures, stale displayed revision, concurrent author edits,
  and a verdict whose audit or recipient admission fails atomically.
- Forged telemetry, unknown context, participant versus native claims, and unsafe work text.
- Terminal-copy command line breaks, DEL, tabs, zero-width, bidi and look-alike characters;
  Markdown paragraphs, lists, line breaks and code blocks in context, recommendation and
  comments; non-ASCII code displayed only for reading; inert HTML/script; known credential
  signatures. Review author-written card prose against ASD-STE100; the requirement does
  not specify a daemon grammar checker.
- The typed dashboard verdict reaches the recipient as the maintainer's instruction and
  approval. A peer's body, quote or display label cannot create that origin. Existing native
  permissions apply without a second board approval for already approved work.
- Lost verdict-write/ack replies, restart before and after commit, repeated wakes,
  acknowledgement races, holder changes, expiry and purging of retained recipients.
- Complete multi-page waiting/archive reads and immutable snapshot/reference lookup.
- Ordinary-band saturation alongside funded work controls, no starvation or private IDs
  in status/audit/wakes, and visible refusal without claiming successful delivery.

No database migration, live service, native configuration or new board transport was changed
while collecting this evidence.
