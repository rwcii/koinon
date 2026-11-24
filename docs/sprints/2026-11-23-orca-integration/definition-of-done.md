# Orca integration (#795) — definition of done

Each chunk merges complete through `ship` into `develop`, with the docs gate (WDR 004, Decision
7), the Linux and macOS CI, `peer-review` and `refusal-review` by the other agent family, and
the records and docs that its decision changes. Tests use synthetic peers, temporary state and
private tmux servers (WDR 006).

## Sprint plans with parallel chunks (chunk 01)

- The `sprint` skill tells the planner to give each chunk `Depends on` (chunk numbers, or
  "none") and `Owns` (the files, packages and interfaces that only that chunk changes), and to
  add a parallel view to `sprint.md`: groups of chunks whose dependencies are met by earlier
  groups and whose owned items do not overlap.
- The skill tells the coordinator to dispatch a chunk only when its dependencies have merged,
  and never two chunks with overlapping owned items at the same time.
- A contributor test reads every `sprint.md` with a parallel view in `docs/sprints/`, and
  fails when a group holds a chunk whose dependency is in the same or a later group, or two
  chunks whose `Owns` lists share an item. A control plan with each fault fails the test.
- This sprint's own plan uses the format and passes the test.

## Orca launch (chunk 02)

- Tests in `internal/launcher` with a fake tmux and synthetic environments:
  - `TMUX` unset: the launcher starts a tmux session named for the address with the role, and
    attaches it.
  - `TMUX` set by the host terminal: the launcher runs the agent in the current pane.
  - Orca-like extra terminal variables (any `ORCA_*`, `TERM_PROGRAM`, `COLORTERM`) do not
    change the address, the session name or the registration.
  - Two linked worktrees of one repository, roles `p1-driver` and `p2-driver`: distinct
    addresses and distinct tmux session names; the label comes from the Git common directory.
- docs/USAGE.md gets "Run Koinon members from Orca": the command form, the role rule (always
  `--role` with the pair), the address rule for worktrees, the observer's scratch copy, and
  that each computer runs its own coordinator. Spec section 7's sentence that the default
  tmux session is `<family>-<repo>` is corrected there: the session name includes the role.
- `pair` says that the observer makes its own detached worktree of the frozen checkpoint,
  outside the driver's worktree, and removes it after the review.
- Live check (gates the sprint's completion, not this chunk's merge): on a computer with Orca, one Claude and one Codex member launched through
  `orca terminal create ... --command "koinon <family> --directory DIR --role ROLE"` register,
  show their panes in Orca, receive a Koinon message and a wake, and `coordinate` reads their
  panes. The result goes into `verification.md`. The chunk can merge with its synthetic tests
  before that; the sprint is not complete until the live check passes (`sprint.md`).

## `coordinate` as the Captain (chunk 03)

- Dispatch: for a ready chunk (parallel view, dependencies merged), the coordinator creates
  or updates its one work item with the chunk's outcome and owned items, assigns it to the
  pair, and never creates a second work item for a chunk that has one that is not finished.
  At a driver swap, the new driver proposes its own family and role before it starts.
- Loop awareness: at each team check, a work item that is not finished, with two or more
  failed attempts (a
  `rejected` event, or a head of its pull request whose CI, preflight or review failed or
  asked for changes) since the last head on which every required check passed, or since its
  first start, is reported to the maintainer with what failed, as a board card. Starts and
  releases are not attempts: Koinon sets a checkpoint at each of them, and a pair hands over
  with both at one commit.
- Goal and drift: at each team check, the coordinator compares each active work item with the
  sprint's `decision.md` and `definition-of-done.md`, and reports work outside them before
  it grows.
- Disagreement record: a pair's disagreement that goes to the maintainer is written on the
  work item (the driver's `work_update` with lifecycle `blocked` and a blocker with the two
  positions and the card reference, sent again on each update until the verdict), and the
  coordinator tracks it there. A work report would release the claim and wait for a verdict
  (PDR 004, Decision 2), and Koinon keeps a blocker only while the item is `blocked`.
- Contributor tests check the skill text for each step and its source.
- `sprint` reuses the issue's item only for chunks that one pair builds in order; a
  dispatched chunk has its own item.
- PDR 002, Decision 5 records the additions.

## Merge gates (chunk 04)

- `.koinon/skills/preflight.md` with the Review run command, the publish command and the lens
  policy for security, code, standards and refusal; each lens runs in a fresh worker on the
  frozen head with no forge credential; the publish step posts one result comment and the
  `preflight` status on that head. PDR 008, Decision 5 changes for Koinon's own adoption.
- `ship`: after the reviews pass, the author puts a card on the board that names the pull
  request and its exact head as its Where field, exactly `OWNER/REPO#PR@HEAD`; after the
  approval, it posts `board-verdict` on that head with the card reference; a new head needs a
  new card. A test of the posting tool refuses a verdict whose head differs.
- `release`: stops when the promotion pull request and its evidence are ready, reports to
  the maintainer, and after the maintainer's merge pushes the tag and checks the publish.
  No skill text tells an agent to merge into `main`; a contributor test checks that.
- The gates are opt-in project policy: new optional facts "Merge approval" (ship) and
  "Promotion merge" (release), whose defaults keep today's steps; Koinon's facts opt in. A
  project without them is not changed.
- Rollout: the chunk's own pull request is the first use of `preflight` and `board-verdict`.
  The `develop` ruleset requires both, with a passing preflight, after the merge, with the
  maintainer's go, read back with the rulesets API.
- WDR 004 records the gates with the maintainer's words.

## Fleet Decisions (chunk 05)

- The fleet role: `koinon fleet enable` on a separate install or an existing node, an admin
  password set at that step (12 characters to 1024 bytes, the rule of the maintainer secret,
  `internal/core/maintainer_secret.go`), and two new Ed25519 client keys. The write key is
  stored encrypted with a key derived from the admin password (a memory-hard KDF with its
  parameters stored beside it); its clear key is never written. The read key is stored like the
  node's other private keys (mode 0600). `koinon fleet enroll` writes one enroll statement for
  each key, with scope `read` or `full`, for each node's `koinon maintainer key add`.
- Key scope: an added key has scope `read` or `full`; a key added before this change is `full`.
  A `read` key is refused for each write with `signature_refused`, reason `scope`.
- Reads: the read key signs every read, with no password. The page loads, refreshes after a
  stale revision, and filters with reads only.
- The top navigation shows **Fleet** when the role is on, with Fleet Decisions: the layout of
  the Decisions page, each row with its node, and a node filter (all, or one node). The page
  reads each paired node's board live with signed `board_list` and `decision_get`; a node that
  does not answer shows as unreachable. The node's own cards are read locally.
- A verdict on the Fleet page asks for the admin password, unlocks the key for that action
  only, sends a version 2 signed `decision_verdict` for that node with the card's revision and
  snapshot digest, verifies the signed answer (`signed.VerifyAnswer`), and writes the board's
  audit record. The node records `by USER (fleet-board)` and its own audit record.
- A node that does not answer: `computer_offline` before the send means nothing ran, and the
  page says the node is unreachable and the card still waits there; nothing is queued (the
  card waits on its node, as the approved design says). `outcome_unknown` after the send: the
  page shows the outcome as unknown; the board keeps the signed envelope until its answer is
  known and sends the same bytes again when the node answers, with no key unlock. Within 5
  minutes of signing, the node gives the first answer or runs the action once; from 5 minutes
  to 7 days, the first answer, or an expired refusal that means it never ran; after 7 days, the
  board reads the card's verdicts live with the read key and shows them.
- Tests: a wrong admin password unlocks nothing and sends nothing; an admin password outside
  the length rule is refused; the read path signs reads only, and a write with the read key is
  refused with reason `scope`; a key added before the change keeps scope `full`; a node that
  goes away before the answer shows `outcome_unknown`, and the resend after it reconnects shows
  the first answer within 7 days, an expired refusal as never ran, and after 7 days the live
  read; a repeated action gives the first answer; a verdict on a stale
  revision is refused and the page reads again; an unsigned, mis-signed or wrongly targeted
  envelope is refused by the node; a node secret alone cannot make a fleet action; a node that
  does not answer is unreachable; the write key file holds no clear key. Two live nodes on Linux and
  macOS: a verdict from the Fleet page reaches the card's node.
- PDR 011 records the fleet board decision, the read key, the outcome check, no verdict queue,
  the admin password rule and the node key check (spec section 3), with the maintainer's words.

## Fleet Sessions (chunk 06)

- New signed operations, version 2 only: `sessions` (the Sessions page's rows with their
  revisions), `session_retire`, `session_purge`, `session_unpurge`, `inbox_clear` and
  `claims_release`, each bound to the session's revision, once-only receipts, audited on the
  node with the key's user and client.
- Fleet Sessions: the layout of the Sessions page, each row with its node, a node filter, and
  the session actions; each action asks for the admin password and is signed for one node.
  The board writes its own audit record for each action. A node that does not answer is
  handled as in Fleet Decisions: unreachable before the send, unknown outcome after it, with
  the outcome read when the node answers again.
- Tests for each operation: success, stale revision, unknown session, a key without access,
  version 1 refused, the repeat gives the first answer, and the board's verification of the
  signed answer. PROTOCOL.md documents the operations.

## Records and docs (each chunk)

- The records that the chunk changes carry the decisions above with the maintainer's words.
- README, USAGE, PROTOCOL, INSTALL and the skills README describe the change in the same pull
  request (docs gate). CHANGELOG has an Unreleased entry for each visible change.
