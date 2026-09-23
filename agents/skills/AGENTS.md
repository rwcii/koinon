# AGENTS.md — shared agent skills

This directory is the one tracked home for the repository's agent skills. Every agent reads
the same files through a symlink from its own skill path:

| Agent | Path it reads | Symlink target |
| --- | --- | --- |
| Claude Code | `.claude/skills` | `../agents/skills` |
| Codex | `.agents/skills` (repository discovery) and `.codex/skills` | `../agents/skills` |

Edit skills here only. Never replace a symlink with a copy, and never add an agent-specific
skill directory elsewhere in the repository; divergent copies are the failure this layout
prevents.

## Write every skill for every agent

- One directory per skill, holding `SKILL.md` with YAML frontmatter that has only `name` and
  `description`. The `name` matches the directory name.
- Use the shell, `git` and `gh` only. Do not name a tool, slash command, memory store or
  message transport that exists in only one agent.
- Name the agent by its family (`claude`, `codex`, `deepseek`), with a public role suffix only
  when the user assigns one (`codex-review`). Never use a session name, thread ID or model
  version.
- Tell the agent to read this file. Some agents load a skill without the surrounding
  `AGENTS.md`, so each `SKILL.md` names `agents/skills/AGENTS.md` in its opening lines.
- Keep repository rules in the repository's root `AGENTS.md`; a skill points to them rather
  than restating them.
- Add every new skill to `README.md` in this directory in the same change.

## Content that must stay out

This repository is public. Skills and anything they commit must never contain private
thread or session IDs, keys, credentials, inbox or peer message bodies, memory store content,
environment dumps, email addresses, or absolute paths under a home directory.
