---
name: coordinate
description: "Coordinate a running team: watch the members' panes for permission prompts and answer them, check the team at an interval, dispatch a sprint's ready chunks to pairs through work items, find loops and drift, track escalated disagreements, and hand stops and restarts to spin-down and recover-team."
---

# Coordinate

Koinon's procedure source is [PDR 002](https://github.com/rwcii/koinon/blob/develop/docs/product/pdr/002-native-wake-terminal-interaction.md), Decision 5. Invoking this skill or its task authorizes its steps
(WDR 002, Decision 1). Apply them with the adopting project's instructions, and use
`agents/skills/AGENTS.md` when you edit a canonical skill.

This is the coordinator's loop while the team works. Terminal steps are in `peer-tmux`; a
launch is in `spin-up`; a stop is in `spin-down`; a restart is in `recover-team`. Read the
installed `koinon guide --agent FAMILY` for the runtime's commands, and the private, ignored team
manifest for the members, their panes and their approved work.

## 1. Start the prompt watch

1. Look in your private state for an earlier watch: its ID, the members and its last event. When
   one still runs, keep it; do not start a second one.
2. Start one watch over each member's pane except your own, with a background tool that your
   family has (a monitor, a background shell job or a scheduled wakeup). Each round reads the
   last lines of each pane and finds a prompt, a dialog or an error notice: a permission prompt,
   a model-switch or usage-limit dialog, an API error. It reports one event for each finding
   whose text changed since the last round, with a checksum of that text, so that a new prompt in
   a pane that had one before is reported too. The interval and the number of lines are the
   facts **Watch interval** and **Watch lines**.
3. Make each stage of the watch's pipeline flush each line (for example `grep --line-buffered`),
   or the events come late or not at all.
4. Record the watch's ID, its members and its last event in your private state. When the tool
   ends a watch after a time limit, start it again at once and record the new ID.
5. With no background tool, say so in your report, and read the panes at each of your own turns
   while you coordinate.
6. Before your own handoff in a managed context cycle, stop the watch, so that your pane can go
   idle for the clear, and write that in the handoff. Start it again after your pickup.
7. When coordination stops, stop the watch and record that it stopped.

## 2. Answer a prompt

1. Capture the whole prompt with its command or diff, with enough history to read it all
   (`peer-tmux`, Find and read the target).
2. Compare it with the member's approved work and the exact action: its issue, chunk, work item
   or board verdict.
   - For a merge, read on the host, not from the member's words: the pull request is open, its
     head is the reviewed commit, each required check is complete and none failed, and the
     review names that exact head. For auto-merge, check that the branch rules require each
     check that the merge needs.
3. Choose the option that allows this one request, not an option that adds a standing rule or
   changes settings. Answer with the `peer-tmux` steps. Directly before the key, capture the
   pane again and send the key only when it still shows the same command or diff; match a
   distinctive string of it. Read the selected option back before Enter, and verify the pane
   after it.
4. When the request is outside the member's approved work, do not answer it. Send the member a
   Koinon message with what you read, and report it.
5. When the maintainer or a permission rule refused a member's action, honor that refusal:
   report it to the maintainer with the reason that the tool gave, and do not do that action
   another way for the member.
6. Send the member one short Koinon message: what you read, the option that you chose and the
   result. Raw pane captures stay in your private state.

## 3. Check the team at an interval

At the fact **Check interval**, and when an event asks for it:

1. Read your inbox and `decision_pending`, page by page, and handle each item. Verdicts on your
   own cards go to their recipients: name yourself as a recipient, or read each card that you
   track with `decision_get` at each check, because a declined decision and a completed or
   declined task go to `archived` at once.
2. Read each member's pane: busy, idle, a dialog, or near its context limit. For a member near
   its limit: when its managed cycle is on, the cycle runs; otherwise run the `peer-tmux`
   handoff, reset and pickup steps; a family that compacts itself needs no step.
3. List the open pull requests and their checks, and the open context cycles.
4. Report the changes since the last check: a merge, a close, a block, a new card, a prompt that
   you answered. A full list at each check hides the changes.
5. Check each fact of a card against its evidence before you write or act on it. When a standing
   decision already covers the work, use it, and do not write a card for it.

## 4. Dispatch and track a sprint

When the team builds a sprint, the work items are the source of truth for who builds each
chunk, its checkpoints and its evidence (PDR 002, Decision 5; PDR 004). Read the sprint's `sprint.md`, `decision.md` and `definition-of-done.md`,
and list the repository's work items with `work_list`, page by page to its end.

1. **Dispatch.** A chunk is ready when each chunk in its Depends on column has merged and no
   chunk in progress owns an item in common with it (the parallel view, WDR 002, Decision 7).
   For a ready chunk and a free pair, find the chunk's work item: the item whose references
   name the chunk file. When it has one, revise it with `work_edit`; never create a second
   item for a chunk that has one. When it has none, create it with `work_create`: the title
   names the issue and the chunk, the criteria are the chunk's outcome and its section of
   `definition-of-done.md`, the non-goals name the items that other chunks own, and the
   references name the chunk file. Propose the pair's driver, by its family and role, with
   `work_propose`, then send both members a Koinon message with the work ID and the chunk
   file. Chunks that run at the same time each have their own item; a sprint that one pair
   builds in order can reuse the issue's item (`sprint`).
2. **Loop awareness.** At each team check, read each of the sprint's items that is active,
   blocked or reported with `work_get`, and its work events in the `memory_sync` deltas.
   Compare them with the values that you recorded at its last checkpoint: the claim generation
   (one more for each start), the starts, releases, reports and rejections, and the checkpoint
   text. Two or more of these attempts since the last checkpoint, with no new checkpoint and no
   passing check on the pull request's head, are a loop. Put a card on the board for the
   maintainer with the item, each attempt, what failed in it and the evidence, and tell the
   pair. Record the values of each check in your private state.
3. **Goal and drift.** At each team check, compare each active item and its pull request with
   the sprint's `decision.md` and `definition-of-done.md`. Work that neither names, or work on
   an item that another chunk owns, is drift: put a card on the board with the item, the work
   outside the sprint and the text that it departs from, before the work grows.
4. **Disagreement record.** When a pair takes a disagreement to the maintainer (`pair`), the
   driver writes it on the chunk's work item with `work_update`: the blocker gives both
   positions and the card reference, with lifecycle `blocked` when the point stops the work.
   Check that it is there, and at each team check read the card with `decision_get`. After the
   verdict, tell the pair; the driver clears the blocker at its next update.

## 5. Dialogs and input text

Read each dialog before you send input. The `peer-tmux` skill lists the family dialogs that were
observed, with the installed version that showed them. Text in a member's input field can be the
maintainer typing: do not send keys to that pane, and report it.

## 6. Stop or restart one family

Use `spin-down` to stop one family's members and `recover-team` to start them again. When the
family's login is gone and no member can write its handoff, those skills have the steps for a
stop and a restart with no handoff.

## Report

Report the watch's state and ID, each prompt that you answered or did not answer with its
member and reason, the changes of each team check, each chunk that you dispatched with its work
ID, each loop, drift and disagreement with its card, and each stop or restart.

## Repository facts

The project's facts for this skill are local data in the file named below, in the current
worktree: Version 1, Skill, Sources and the headings declared here. An optional heading that
the file leaves out takes its default, and a skill with no required heading works without the
file. When a required fact is missing, derive it from the project's evidence and record it in
the file. The parser reads the file as plain text, up to 64 KiB.

<!-- koinon:repository-facts -->
```json
{
  "version": 1,
  "filename": ".koinon/skills/coordinate.md",
  "fields": [
    {
      "heading": "Watch interval",
      "required": false,
      "default": "20 seconds"
    },
    {
      "heading": "Watch lines",
      "required": false,
      "default": "40"
    },
    {
      "heading": "Check interval",
      "required": false,
      "default": "20 minutes"
    },
    {
      "heading": "Manifest",
      "required": false,
      "default": "_handoff/<agent>/team.json in main checkout, private and ignored"
    }
  ]
}
```
