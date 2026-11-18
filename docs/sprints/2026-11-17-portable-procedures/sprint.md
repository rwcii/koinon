# Portable procedures — chunks

Each chunk cites `decision.md` and `definition-of-done.md`, and merges complete through ship
into develop. Claude drives, Codex observes. One open pull request at a time. Issue #732.
Review in `review.md`.

| Chunk | Complete outcome | Definition of done | Dependencies |
| --- | --- | --- | --- |
| [01 Seven skills](chunks/01-seven-skills.md) | The catalog and the installed entries have the seven skills; LICENSE and PDR 008 record the decision | The seven skills | None |
| [02 Install with no Koinon](chunks/02-standalone-install.md) | One script installs the skills of a pinned release on a computer with no Koinon | The install script | 01; the PDR 008 Decision 2 change was approved on 2026-11-17 |

#732 closes when chunk 02 merges, the change is released through the normal release process,
and the delivery check of `definition-of-done.md` passes against that release. This plan
authorizes no merge to main, tag or publication; the release follows its own approval.
