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
