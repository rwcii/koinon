# Coordinated work — definition of done

- Propose to a synthetic role before native A becomes B: B lists it, gets claim conflict on
  start while A's lease is live, and cannot use A's generation or ownership replay. After
  release/expiry B starts with a new consumer/generation. Same-host, different native ID must
  not merge identities. Unheld, conflicting, subagent and foreign-repository cases refuse.
- Legacy participant claims continue their original generation across verified succession;
  legacy native claims keep their old owner. Migration changes neither deadlines nor cursors.
  Editing/clearing a proposal cannot remove an active claim's native-owner binding.
- Test every ownership operation and retry against a wrong native caller and a copied consumer
  label. Native identity and role come from actual registry/transport metadata, not arguments.
- Report snapshots evidence and scope, releases resource claims, freezes the report, and
  leaves the item reported. Owner self-verdict and a reset successor of the worker participant
  are refused even with another consumer label; a different coordinator succeeds.
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
- Build migrations from committed old-schema fixtures and retain old native/participant
  claims, scope history and keyed outcomes. Incompatible older writers refuse the new schema.
- MCP/CLI/API coverage uses synthetic sessions, temporary state, ephemeral ports and private
  provider stubs; never live agents, services or credentials. Update actual tool-purpose and
  family coverage guards when adding operations.

Gate for each chunk: check skill; latest develop; vet, full race suite with 30-minute timeout,
whitespace/privacy scan, four CGO-free builds for runtime changes, Linux/macOS Go and native
CI, and exact-head observer verdict. Every PR enables squash auto-merge. Build no product
chunk before maintainer approval of the reviewed plan at Gate B.
