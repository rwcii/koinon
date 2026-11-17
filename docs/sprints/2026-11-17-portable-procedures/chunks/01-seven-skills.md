# Chunk 01: seven skills

Cites `../decision.md` (Acceptance 1-4) and `../definition-of-done.md` (The seven skills).

1. Copy each draft into `agents/skills/NAME/SKILL.md` byte for byte, then make only the
   changes that the maintainer approved on 2026-11-17 (see `definition-of-done.md`). The drafts were supplied
   privately; these are the SHA-256 values of the text that the maintainer saw (provenance is
   the revision without its cost section, reported to him):

   | Skill | Draft SHA-256 |
   | --- | --- |
   | preflight | `5ca23e2f43701890985b9fb74b56f960bfe21db4069d7769cde6e739236263ce` |
   | provenance | `4cc79242536faa924879ed70c2d93d368978682713937ed6b0990d125430fbbe` |
   | refusal-audit | `1c2429800f12d3f580a14fd2367daa92cf91f661357b6618ba358c13b8246ee1` |
   | standards-review | `9e4739f08646eace81f792c8a0cfe77255553f8cf7e5941a3a7c455b858590fe` |
   | record | `85b8447ec1b1fc0222cdfd603ef7cd854a226daa466eb5aa6798e6d385a7fd11` |
   | sprint-closeout | `e225206a32cd6573d1b0b5083ae9c3b517963a7d3a01fa05fa7054c77d616001` |
   | standards-audit | `be4467826000349b81f2cd127fdf67b32c5cf3d407e152212602ce48314f09b3` |
 Change only its
   backticked placeholder `` `<Koinon record for ...>` `` to `[PDR 008](link), Decision 5`, the record of this
   decision. Show the maintainer every change before the pull request.
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
