# Chunk 02: install with no Koinon

Cites `../decision.md` (Acceptance 5) and `../definition-of-done.md` (The install script).
Starts after chunk 01 merges. Robert Capps approved the change to PDR 008 Decision 2 on
2026-11-17; PDR 008 Decision 2 records it.

1. The release workflow: build `koinon-skills.tar.gz` from `agents/skills/*/SKILL.md` of the
   tag, and add its line to SHA256SUMS before the publish job.
2. `scripts/install-skills.sh VERSION`: download the asset and SHA256SUMS of that release,
   check the hash, and write each skill except `koinon-guide` as `koinon-NAME/SKILL.md`, with
   its `name:` line changed, into `~/.claude/skills` and `~/.agents/skills`, and into the
   OpenCode and agy skill folders only when their configuration folder exists (the roots of
   #461). Keep a symbolic link. Report created, replaced, unchanged or kept for each entry.
3. Tests from the definition of done, including `koinon install` after the script.
4. PDR 008 Decision 2 with the verdict's source; docs/INSTALL.md; CHANGELOG.
