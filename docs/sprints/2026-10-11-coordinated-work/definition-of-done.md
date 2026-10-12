# Coordinated work — definition of done

- Propose to a synthetic role before native A becomes B: B lists it, gets claim conflict on
  start while A's lease is live, and cannot use A's generation or ownership replay. After
  release/expiry B starts with a new consumer/generation. Same-host, different native ID must
  not merge identities. Unheld, conflicting and foreign-repository cases refuse. An identified
  sub-agent without a participant cannot hold a role; parent-attributed calls use the parent's
  registered eligibility (PDR 001, Decision 2).
- Existing/default participant claims continue their original generation across verified
  succession (PDR 001, Decision 6); legacy native claims keep their old owner. Migration
  changes neither deadlines nor cursors.
  Editing/clearing a proposal cannot remove an active claim's native-owner binding.
- Test every ownership operation and retry against a wrong native caller and a copied consumer
  label. Native identity and role come from actual registry/transport metadata, not arguments.
- Report snapshots evidence and scope, releases resource claims, freezes the report, and
  leaves the item reported. The exact reporting consumer is refused before verdict replay
  and commit; a different consumer succeeds (PDR 004, Decision 3). Cover a different consumer
  in the same native session and a later session of the same worker participant. Neither native
  actor nor participant equality adds a verdict refusal; normal caller/consumer validation applies.
- Accept is finished/completed for queries and retention. Reject requires the exact current
  report and a new scope, returns open, retains both scopes/report/verdict, and permits a
  fresh start. Re-report/re-verdict stale IDs/revisions are refused; fixed retries return the
  original outcome. A report does not authorize continuing work or executing its references.
- Saturate ordinary pages, entry/event/replay/scope slots and logical-byte budgets. Report
  refuses before acknowledging an unfunded verdict promise, leaving the old claim and its
  funded release/finish intact. Every acknowledged report can still accept or reject from
  its verdict reserve; release/finish/expiry preserve their existing credits.
  Verify page/byte bounds with maximum-size synthetic rows on Linux/macOS. Extra schema
  objects or an index on a credit flag are refused. No control relies on ordinary space.
- Count verdict reservations on reported items; no gap, double-charge or leaked credit at
  restart/rollback/retry/accept/reject. At the scope-history limit, a fresh native claim can
  still start and withdraw; an unfundable report refuses without stranding the claim.
- Native provider stubs demonstrate content-free coalesced notices to assignee and creator,
  no unrelated/foreign delivery, no guessed holder, transport uncertainty, a failed task,
  read/ack separation, loss, reconnect, and holder changes. Work catch-up remains authoritative.
- Freeze and page a memory snapshot and process subsequent deltas before acknowledgment.
  Test more than 100 relevant items, pagination races and an already-advanced participant
  cursor after a reset: current assignment/report queries still reveal outstanding work.
- Ordinary notice-enqueue refusal cannot roll back a funded work control. Reconciliation
  discovers its durable event later and retries, with one pending notice per audience.
- Fill all 256 audience rows and introduce another relevant audience: no extra row/notice
  and no eviction, visible usage/limit/saturation, and complete paged current queries for
  the omitted audience. Once an eligible row is reclaimed, reconciliation sends its head
  once. This ordinary notification limit never consumes a funded control's reserve.
- Build migrations from committed old-schema fixtures and retain old native/participant
  claims, scope history and keyed outcomes. Incompatible older writers refuse the new schema.
- Ordinary MCP/CLI/API tests use synthetic sessions, temporary state, ephemeral ports and
  private provider stubs (WDR 006, Decision 2). Update actual tool-purpose and
  family coverage guards when adding operations.

Each chunk uses the check/ship skills and WDR 004 for publication and checks, WDR 003 for
the exact-head observer verdict, and WDR 006 for native evidence. Every PR enables squash
auto-merge. Gate B for the proposed product mechanisms remains pending (WDR 002, Decision 5);
the maintainer's decided verdict rule is not a separate approval gate.
