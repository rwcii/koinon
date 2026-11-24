# Orca integration (#795) — decision

## Problem

The maintainer wants Orca and Koinon to run together (spec `Koinon v0.13.3 — Orca Integration
Spec`, given by the maintainer on 2026-11-23, raised for the Xavior orchestration platform).
Orca launches the members, gives each pair one isolated worktree and hosts diff review. Koinon
holds the record: work items, memory, decisions and sessions, across the maintainer's
computers. Today:

- `sprint` writes ordered chunks with no declared ownership, so a coordinator cannot tell
  which chunks can run at the same time.
- `coordinate` watches panes and checks the team, but does not dispatch work, find loops,
  compare progress with the sprint's goal or keep the pair's disagreements.
- Nothing documents a Koinon launcher as an Orca terminal command, or the address rules for
  more than one pair in one repository.
- `develop` requires CI, `decision-citations`, `peer-review` and `refusal-review`, but no
  preflight lenses and no board verdict, and `release` merges to `main` (WDR 004; WDR 002,
  Decision 6, deprecated by #487).
- Each computer has its own dashboard. The maintainer cannot see or answer the cards, or
  manage the sessions, of all computers in one window.

## Scope decisions

### Release number

- **Decided by:** Robert Capps. **Date:** 2026-11-23, terminal.
- **Context:** the spec names v0.13.3, which was released on 2026-11-20.
- **Decision:** "13.4 is the right place": these changes go into v0.13.4.

### Node keys in the mesh (spec section 3)

- **Decided by:** Robert Capps. **Date:** 2026-11-23, terminal.
- **Context:** the spec says that the nodes share secrets. They do not: each daemon keeps its
  own credentials and link key, and its peers keep only the pin of that key (PDR 011, Decision
  1); each answer to a version 2 envelope is signed by the computer that ran it (PDR 011,
  Decision 7); a maintainer client has its own key (PDR 011, Decision 6).
- **Decision:** "the spec agent didn't read that part of the repo, your recall is better than
  its." No new key design. Chunk 05 checks the spec's acceptance (one node's credentials do
  not let anyone act as another node or as the fleet board) with tests, and PDR 011 records it.

### The fleet board (spec section 2)

- **Decided by:** Robert Capps. **Date:** 2026-11-23, decision board, revision 4 approved
  after feedback on revisions 1 to 3.
- **Context:** the spec asked for a summary board that "holds a mirror" of each node's cards,
  with a separate admin password and signed verdicts. A card lives only on its own computer
  (PDR 011, cards for agents on another computer). The maintainer: "the summary board should
  reflect the meshed nodes decisions so that the maintainer can look at one window and provide
  answers to any open decision record on any node. this is a pass through, not a duplicate."
  Feedback on revision 1: "it CAN be a separate install, or IT CAN be configured on an
  existing node. it still joins a mesh through the same invite system." On revision 2: "On an
  existing node, that node's own cards are reflected on its own decision card and the fleet
  decision card. let's also add a fleet session management function that allows manipulation
  of the fleet nodes' sessions from the same overview dashboard." On revision 3: "for clarity
  of design, we should implement a fleet option in the top nav, and it should let you get to a
  session and decision page that largely mirrors the existing node specific pages, but also
  allowing to filter by connected node."
- **Decision:** revision 4 approved:
  1. The fleet board is a role of a Koinon instance: a separate install, or a role turned on
     for an existing node. It joins the mesh with the same invitation (`koinon link invite`,
     `koinon link join`).
  2. On an existing node, its own cards show on its own board and on the fleet board. Both
     read the one stored card; there is no copy.
  3. It has its own client signing key, separate from the node's link key, added once on each
     node with `koinon maintainer key add --statement FILE`.
  4. The top navigation gets a **Fleet** item with two pages, Fleet Decisions and Fleet
     Sessions. Each page follows the layout of the node's own page, with a filter by connected
     node (all, or one node), and shows each row's node.
  5. The pages read the cards and sessions of every node live at that node. A node that does
     not answer shows as unreachable, not as empty.
  6. Fleet Sessions has the node page's session actions (retire, purge, unpurge, inbox clear,
     claim release), as new signed operations.
  7. Each verdict and session action needs the admin password too. It unlocks the signing key,
     stored encrypted on disk, never in clear.
  8. Each action is a version 2 signed envelope for one node, bound to the card or session
     revision. The node checks the key, records it `by USER (fleet-board)`, audits it, and
     signs its answer. The board audits it too. A stale revision is refused, and the page reads
     again.
  9. Local pages keep their own verdicts and actions.
- **Reason and scope:** one window over all nodes, as a pass through. Actions wait while the
  board is offline; there is no fallback to agent approval. Rejected: the admin password as a
  second login with the key in clear (a stolen disk gives the key); a copy of every card
  (PDR 011).

### Fleet reads, lost answers and the admin password

- **Decided by:** Robert Capps. **Date:** 2026-11-23, decision board, approved at revision 4.
- **Context:** the approved fleet board design reads every node live, and keeps the signing key
  encrypted until the admin password unlocks it for one action; it does not say how reads
  authenticate while the key is locked. Each signed read needs an envelope signed by an added
  key (`internal/core/signed_actions.go`). `outcome` finds only the receipts of the key's own
  authorization (`signedOutcome`), so a read key cannot check a write key's action. A write runs
  once; for 7 days the same envelope gives its first answer again; a write that has not run must
  arrive within 5 minutes of its signing (USAGE, signed actions with a client key). Spec section
  2: "Pending decisions wait in their queue. There is no fallback to agent approval." The
  maintainer's decision for #695 (2026-11-21): no queue for signed sends. Added keys have no
  scope (`internal/core/maintainer_keys.go`). Codex's plan review of round 1 found the gaps in
  the outcome check, the queue and the password rule.
- **Decision:** approved:
  1. The fleet board has two keys. The read key signs reads only. The write key signs verdicts
     and session actions; the admin password unlocks it for each action.
  2. The read key is stored like the node's other private keys (mode 0600, not encrypted), so
     the pages read at login with no password.
  3. Added keys get a scope: the enroll statement names `read` or `full`. A `read` key is
     refused for each write (`signature_refused`, reason `scope`). Existing keys stay `full`.
     `koinon fleet enroll` writes one statement for each key.
  4. Outcome check after a lost answer, with no key unlock: the board keeps each signed write
     envelope until its answer is known, and sends the same signed bytes again when the node
     answers. Within 5 minutes of signing, the node gives the first answer, or runs the action
     once if it never arrived; this is the action that the maintainer signed. From 5 minutes to
     7 days, the node gives the first answer if it ran; an expired refusal means it never ran.
     After 7 days, the board reads the item live with the read key (the card's verdicts, the
     session's state) and shows what it finds.
  5. No verdict queue. "Pending decisions wait" means that the cards wait on their own nodes:
     an unreachable node shows as unreachable, and the maintainer answers the card when it is
     back, on the Fleet page or the node's own page.
  6. The admin password has the length rule of the maintainer secret: 12 characters to 1024
     bytes (`internal/core/maintainer_secret.go`).
- **Reason and scope:** the approved design needs live reads at login with a locked write key,
  and a check of a lost answer without the key. A stolen board disk then gives reads of all
  nodes, but no verdict and no session action. Cost: a key scope in the store and the key
  commands, in chunk 05. Rejected: one key kept unlocked in memory for the board login, with the
  admin password asked for each write as a check only (a running board holds the write key in
  clear); a queue of verdicts on the board, sent when the node is back (it differs from the #695
  decision, and a queued verdict can meet a changed card); no length rule for the admin password.

### Merge gates (spec section 8)

Recorded in [WDR 004, Decision 8](../../engineering/wdr/004-publication-and-verification.md),
which holds the rule: the maintainer's words of 2026-11-23 and the agents' design.

### Chunk 04: the end of the preflight fix loop

- **Decided by:** Robert Capps. **Date:** 2026-11-23, decision board: the recommendation below
  approved with no note. The board recorded that approval from the dashboard; the maintainer
  has not confirmed it since #800, a note recorded on the wrong card. This was its own card,
  separate from the card on the `preflight` status that WDR 004, Decision 8 records under
  "Changed on 2026-11-23".
- **Context:** five preflight runs rejected chunk 04 (heads 6389bd3, f4f580f, 5a3cbb1, da2bb66
  and 2d9c61c). Each fresh run found new points, mostly wording and attribution, not faults of
  the earlier fixes; a run costs about $2 and 30 minutes. The maintainer: "5th fix round? that
  sounds like you are in a fix loop". The options were to run again until all four lenses
  approve, to skip the preflight publish for this pull request, or to stop after the sixth run.
- **Decision:** stop after the sixth run. Fix only its findings of wrong script or test
  behavior, with no seventh run; publish the sixth run's result as it is, and list each open
  finding in the pull request. The Codex peer-review and refusal-review and the maintainer's
  card on the exact head decide the merge; accepted review findings get one fix commit and one
  bounded Codex recheck, not a new preflight.
- **Reason and scope:** the card's text, written by the author: the loop did not converge, and
  the reviews and the card are the gates that decide the merge. This applies to chunk 04's
  pull request, whose base has no required `preflight` status yet.
- **Superseded on 2026-11-24 (terminal, Robert Capps):** "preflight is required, passing is
  required." The words were said about the gate in general, in reply to the text "preflight
  shows its true result and is not required"; the author applies them to chunk 04's pull
  request too, which needs a passing preflight at its final head (WDR 004, Decision 8).

### The observer's scratch copy (spec sections 6 and 9.1)

- **Decided by:** Robert Capps. **Date:** 2026-11-23, terminal, "yes" to the recommendation.
- **Decision:** the observer makes its own detached `git worktree add` of the frozen checkpoint,
  outside the pair worktree, for example under its own `$TMPDIR`, and removes it after the
  review. Orca and the Koinon launcher need no change; `pair` and the Orca setup doc say so.

### Decided in the spec and kept

- Koinon work items are the source of truth for assignment, ownership, checkpoints and
  completion evidence (spec section 1). Orca run state and peer messages carry no approval.
- `sprint` declares each chunk's dependencies and file and interface ownership, and a parallel
  view (spec section 4). Peer review of the plan stays.
- `coordinate` adds dispatch, loop awareness, goal and drift checks and a disagreement record
  (spec section 5). The spec's words: "To fill the Captain role in the Orca Bot workflow,
  add: Dispatch: assign ready chunks to pairs by creating or updating their work items, per
  section 4. Loop awareness: detect repeated attempts on a work item without new checkpoints
  or passing checks; record what failed and escalate. Goal tracking and drift: at each team
  check, compare progress against the sprint's `decision.md` and `definition-of-done.md`, and
  surface deviation before scope expands. Disagreement record: record pair disagreements
  escalated to the maintainer on the work item, so the Captain can track them."
- `pair` is unchanged: one shared branch and worktree that the driver owns (spec section 6).
- Orca runs `koinon <family>` as the terminal command, always with `--role` that names the pair
  (spec section 7). Pane access does not cross the mesh: each computer runs its own
  coordinator.

### Not in this sprint

- A Koinon pane adapter for Orca's terminal CLI (spec section 7, later migration; section 9.4).
- Team topology (spec section 9.3): each engineer runs an own Orca and Koinon stack is the
  working assumption; nothing here depends on it.

## Acceptance

1. A sprint plan with independent chunks lists them in its parallel view, with their
   dependencies and their owned files; a chunk with an unmet dependency or an overlapping
   owned file is not in the same parallel group.
2. `koinon claude` and `koinon codex` started as Orca terminal commands, with Orca's terminal
   variables, register and keep their addresses; two pairs in two worktrees of one repository
   get distinct addresses through `--role`.
3. `coordinate` dispatches a ready chunk by creating or updating exactly one work item for it,
   reports a work item with repeated attempts and no new checkpoint, compares each team check
   with the sprint's decision and definition of done, and records an escalated disagreement on
   the work item. Corrections of 2026-11-24 (chunk 03): Koinon sets a checkpoint at each
   start and release, so a loop counts failed attempts, not checkpoints; Robert Capps approved
   this on the decision board, and PDR 002, Decision 5 holds the approved text. "Exactly one"
   means one item that is not withdrawn: a chunk whose only items were withdrawn before it
   merged gets a new item (the agents' design, PDR 002, Decision 5).
4. For a project whose facts opt in, as Koinon's do: a pull request into `develop` gets
   `preflight` and `board-verdict` on its exact head, and the `develop` ruleset requires them
   after the maintainer applies it; `ship` posts `board-verdict` only after it reads an
   approval of a card that names that head; `release` stops before the merge to `main`. A
   project without those facts keeps its current steps.
5. The Fleet pages show the open cards and the sessions of every connected node, with a node
   filter; an unreachable node shows as unreachable; a verdict or session action needs the
   admin password, is signed by the fleet board's key for one node and revision, and is
   recorded and audited on that node and on the board; a stale revision, an unsigned or
   mis-signed action is refused.
6. A node's own credentials, or the fleet board's login alone, cannot make a fleet board
   action.
