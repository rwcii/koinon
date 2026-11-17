# Chunk 01: seven skills

Cites `../decision.md` (Acceptance 1-4) and `../definition-of-done.md` (The seven skills).

1. Copy each draft into `agents/skills/NAME/SKILL.md` byte for byte. Change only its
   backticked placeholder `` `<Koinon record for ...>` `` to `[PDR 008](link), Decision 5`, the record of this
   decision. Show the maintainer the seven changed lines before the pull request.
2. PDR 008: Decision 5, "Procedures that adopting projects bring to the catalog", with the
   source, decision, reason, scope and the copyright answer from `decision.md`; Decision 1's
   skill count (14 to 21) and its list of the skills with required facts; the Document History.
3. LICENSE: "Copyright (c) 2026 Robert Capps and Stratovera, LLC".
4. `agents/skills/README.md`: one row for each skill. Generate the OpenCode commands with the
   existing tooling.
5. Tests: `TestTeamSkills`, `TestOpenCodeCommandSkills`, the offered-only set in
   `TestRepositoryFactsMatchCanonicalSchemasAndPolicy`, the final-hash constants, and the name
   scan with its control.
6. CHANGELOG Unreleased.
