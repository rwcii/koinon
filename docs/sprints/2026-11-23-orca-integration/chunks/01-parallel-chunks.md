# Chunk 01 — Parallel sprint chunks

Cites `../decision.md` (Decided in the spec and kept: `sprint`; acceptance 1) and
`../definition-of-done.md` (Sprint plans with parallel chunks).

## Outcome

A sprint plan says, for each chunk, what it depends on and what it alone changes, and lists
the chunks that can run at the same time. A contributor test refuses a parallel view that
breaks either rule.

## Work

1. `agents/skills/sprint/SKILL.md`: each chunk declares `Depends on` (chunk numbers or
   "none") and `Owns` (files, packages and interfaces that only it changes); `sprint.md` has a
   table of chunks with both columns and a parallel view of groups. A coordinator dispatches a
   chunk only when its dependencies have merged, and never two chunks with an owned item in
   common at the same time. Shared files that every chunk adds lines to, such as the
   changelog, are named as not owned. Peer review of the plan hash stays.
2. `internal/contributor/sprint_plan_test.go`: parse each `docs/sprints/*/sprint.md` that has
   a "Parallel view" heading; fail on a chunk whose dependency is in the same or a later
   group, on two chunks of one group with a common owned item, and on a chunk that is in no
   group. Two owned items overlap when they are the same path, when one is a directory that
   holds the other, or when they name the same record. Synthetic control plans for each fault
   and each kind of overlap (same file, directory and file under it, same record).
3. Regenerate the OpenCode wrappers if the description changes; update the hash pins that the
   contributor tests keep, if any.
4. Records and docs: WDR 002 (the sprint procedure source) records the format; the skills
   README row; CHANGELOG.

## Tests

The control plans fail for each fault and a good plan passes; this sprint's `sprint.md`
passes.
