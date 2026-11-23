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
- Live check: on a computer with Orca, one Claude and one Codex member launched through
  `orca terminal create ... --command "koinon <family> --directory DIR --role ROLE"` register,
  show their panes in Orca, receive a Koinon message and a wake, and `coordinate` reads their
  panes. The result goes into `verification.md`. If no computer has Orca when the chunk is
  ready, the check goes into a tracked issue and the chunk merges with the synthetic tests.

## `coordinate` as the Captain (chunk 03)

- Dispatch: for a ready chunk (parallel view, dependencies merged), the coordinator creates
  or updates its one work item with the chunk's outcome and owned items, assigns it to the
  pair, and never creates a second work item for a chunk that has one.
- Loop awareness: at each team check, a work item whose events show two or more attempts (a
  start, a release or a report) since its last checkpoint, with no new checkpoint and no
  passing check, is reported to the maintainer with what failed, as a board card.
- Goal and drift: at each team check, the coordinator compares each active work item with the
  sprint's `decision.md` and `definition-of-done.md`, and reports work outside them before
  it grows.
- Disagreement record: a pair's disagreement that goes to the maintainer is written on the
  work item (a work report with the two positions), and the coordinator tracks it there.
- Contributor tests check the skill text for each step and its source.
- PDR 002, Decision 5 records the additions.

## Merge gates (chunk 04)

- `.koinon/skills/preflight.md` with the Review run command, the publish command and the lens
  policy for security, code, standards and refusal; each lens runs in a fresh worker on the
  frozen head with no forge credential; the publish step posts one result comment and the
  `preflight` status on that head. PDR 008, Decision 5 changes for Koinon's own adoption.
- `ship`: after the reviews pass, the author puts a card on the board that names the pull
  request and its exact head; after the approval, it posts `board-verdict` on that head with
  the card reference; a new head needs a new card. A test of the posting tool refuses a
  verdict whose head differs.
- `release`: stops when the promotion pull request and its evidence are ready, reports to
  the maintainer, and after the maintainer's merge pushes the tag and checks the publish.
  No skill text tells an agent to merge into `main`; a contributor test checks that.
- The `develop` ruleset requires `preflight` and `board-verdict`, applied after the
  maintainer's go in this chunk's pull request; `scripts/setup-repo.sh` declares it.
- WDR 004 records the gates with the maintainer's words; WDR 002, Decision 6 and the
  `sprint-closeout` policy check name the change.

## Fleet Decisions (chunk 05)

- The fleet role: `koinon fleet enable` on a separate install or an existing node, an admin
  password set at that step (at least 12 characters), and a new Ed25519 client key stored
  encrypted with a key derived from the admin password (a memory-hard KDF with its parameters
  stored beside it); the clear key is never written. `koinon fleet enroll` writes the enroll
  statement for each node's `koinon maintainer key add`.
- The top navigation shows **Fleet** when the role is on, with Fleet Decisions: the layout of
  the Decisions page, each row with its node, and a node filter (all, or one node). The page
  reads each paired node's board live with signed `board_list` and `decision_get`; a node that
  does not answer shows as unreachable. The node's own cards are read locally.
- A verdict on the Fleet page asks for the admin password, unlocks the key for that action
  only, sends a version 2 signed `decision_verdict` for that node with the card's revision and
  snapshot digest, verifies the signed answer (`signed.VerifyAnswer`), and writes the board's
  audit record. The node records `by USER (fleet-board)` and its own audit record.
- Tests: a wrong admin password unlocks nothing and sends nothing; a verdict on a stale
  revision is refused and the page reads again; an unsigned, mis-signed or wrongly targeted
  envelope is refused by the node; a node secret alone cannot make a fleet action; a node that
  does not answer is unreachable; the key file holds no clear key. Two live nodes on Linux and
  macOS: a verdict from the Fleet page reaches the card's node.
- PDR 011 records the fleet board decision and the node key check (spec section 3).

## Fleet Sessions (chunk 06)

- New signed operations, version 2 only: `sessions` (the Sessions page's rows with their
  revisions), `session_retire`, `session_purge`, `session_unpurge`, `inbox_clear` and
  `claims_release`, each bound to the session's revision, once-only receipts, audited on the
  node with the key's user and client.
- Fleet Sessions: the layout of the Sessions page, each row with its node, a node filter, and
  the session actions; each action asks for the admin password and is signed for one node.
- Tests for each operation: success, stale revision, unknown session, a key without access,
  version 1 refused, the repeat gives the first answer, and the board's verification of the
  signed answer. PROTOCOL.md documents the operations.

## Records and docs (each chunk)

- The records that the chunk changes carry the decisions above with the maintainer's words.
- README, USAGE, PROTOCOL, INSTALL and the skills README describe the change in the same pull
  request (docs gate). CHANGELOG has an Unreleased entry for each visible change.
