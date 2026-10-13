# Decision board — framing review

## Frozen framing 680d1aff604a67177b2c7f79107f06ded8ab65ac

Claude checked the decision against #206 and its settled points. One gap was accepted:
manual-work priority had no stated marker in the existing item fields. The decision now
uses the supplied design's exact `ACTION FOR YOU` title prefix on an fyi item. Such items
sort before the rest, then by item order and reference. No new agent-row fields are added.
Claude accepted this correction on 2026-10-12; other framing content carries forward.

This is a framing review. The definition of done and technical chunks still need their
own frozen plan review and the maintainer's Gate B approval before implementation.

## Maintainer-choice reconciliation, 2026-10-12

The [fixed scope](https://github.com/rwcii/koinon/issues/206#issuecomment-6123696469)
applies PDR 007, Decisions 1 and 3, and PDR 005, Decision 3. Claude agreed to this bounded
scope before edits. The earlier plain-text framing is superseded by Markdown for card
context, recommendation and comments, code blocks, and ASD-STE100 card prose. Printable
ASCII remains required for terminal-copy commands, with the original rejected character
classes; read-only code blocks are outside that restriction. An authenticated dashboard
verdict is the maintainer's instruction and approval, without a new human-presence check.

The original human-only verdict, frozen text, durable delivery, archive and waiting-query
requirements carry forward. Technical mechanisms remain to be specified and reviewed;
this reconciliation adds no board runtime, schema or external publication.

## Gate A and plan, 2026-10-13

The maintainer approved `decision.md` with a correction to criterion 4 (a change to decided
text reopens the item for a new verdict; the record keeps the history) and the requirement
that agents query stored records, never the page
([#206 comment 6148016252](https://github.com/rwcii/koinon/issues/206#issuecomment-6148016252),
[6148194337](https://github.com/rwcii/koinon/issues/206#issuecomment-6148194337)). Criteria 4
and 10 and PDR 007 Decisions 2 and 4 record both. Claude and Codex agreed the plan criteria
before these edits. The definition of done, `sprint.md` and chunks 01–03 wait for Codex's frozen review.

## Plan review 1823064, 2026-10-13

Codex requested changes ([#206 comment 6149542089](https://github.com/rwcii/koinon/issues/206#issuecomment-6149542089)).
Claude accepted all four findings and the refusal note:

- F1: `decision_history` pages verdict history; lists page on the immutable item ID, with
  the stated walk result and the `mine` index.
- F2: a reopen does not cancel an unacknowledged delivery. Nothing deletes decision records
  automatically; a 64 MiB text bound and the item bound refuse new writes instead.
- F3: acknowledgement is an ordinary write, refused visibly when the ordinary band is full.
- F4: a wake whose range holds verdict messages names their verdict IDs.
- Refusal note: the HTML rule covers decision-status retrieval only.

Bounded review of f66be95: Codex accepted the F2–F4 and refusal-note fixes. One F1 case
remained: a maximum-size verdict serialized with JSON escaping passed the 64 KiB history
page. Claude accepted it: page limits count the serialized reply, the history page is
256 KiB, and a test reads the largest possible record in a page.

## Gate B request at 22aa63c, 2026-10-13

The maintainer did not give Gate B. The plan refused new items and verdicts at fixed
decision bounds; the maintainer requires a board that does not fail at a fixed limit, with
the lifecycle active, in progress, archive and deep archive, where nothing is deleted
([#206 comment 6150321526](https://github.com/rwcii/koinon/issues/206#issuecomment-6150321526),
states in [6150362521](https://github.com/rwcii/koinon/issues/206#issuecomment-6150362521)).
The plan now removes the item and text bounds, adds `decision_close`, the deep-archive log
file with crash-safe moves, and `decision_restore`. Criteria 2, 5 and 9 and PDR 007
Decision 1 record the lifecycle.
