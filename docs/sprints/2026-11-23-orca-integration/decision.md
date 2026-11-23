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

### Merge gates (spec section 8)

- **Decided by:** Robert Capps. **Date:** 2026-11-23, terminal.
- **Context:** the spec restores approval gates: a board verdict on the exact reviewed head
  before a merge to `develop`, and a human merge to `main`. The agents work under the
  maintainer's GitHub account, so GitHub cannot tell an agent from the maintainer. The options
  were skill rules only; a required status from a GitHub App that only the fleet board holds;
  and a separate GitHub identity for the agents.
- **Decision:**
  - The preflight lenses: "security, code, standards, refusal".
  - No separate identity: "they do not. that was a DISASTER when we tried to implement it on
    unify."
  - The threat model: "An agent bypassing a gate and merging is not a threat model we're
    trying to solve for at this point."
  - "I approve your non blocking controls proposed": a required `preflight` status (the four
    lenses), a required `board-verdict` status that the agent posts after it reads the
    maintainer's approval of a card that names the pull request and its exact head, and a
    `release` skill that stops when the promotion pull request and its evidence are ready; the
    maintainer merges to `main`, and the agent then tags and checks the publish steps.
- **Reason and scope:** the gates are process rules that GitHub shows as statuses; they do not
  stop an agent that goes around them. The ruleset change is a repository setting and waits
  for the maintainer's direct go when chunk 04 ships.

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
  (spec section 5).
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
   the work item.
4. A pull request into `develop` needs `preflight` and `board-verdict` on its exact head in
   addition to the current checks; `ship` posts `board-verdict` only after it reads an approval
   of a card that names that head; `release` stops before the merge to `main`.
5. The Fleet pages show the open cards and the sessions of every connected node, with a node
   filter; an unreachable node shows as unreachable; a verdict or session action needs the
   admin password, is signed by the fleet board's key for one node and revision, and is
   recorded and audited on that node and on the board; a stale revision, an unsigned or
   mis-signed action is refused.
6. A node's own credentials, or the fleet board's login alone, cannot make a fleet board
   action.
