# AGENTS.md — shared agent skills

This directory is the one tracked home for the repository's agent skills. Every agent reads
the same files through a symlink from its own skill path:

| Agent | Path it reads | Symlink target |
| --- | --- | --- |
| Claude Code | `.claude/skills` | `../agents/skills` |
| Codex | `.agents/skills` (repository discovery) and `.codex/skills` | `../agents/skills` |
| OpenCode | `.claude/skills` discovery; generated `.opencode/commands` wrappers | Canonical `agents/skills` |

Edit skills here only. Never replace a symlink with a copy, and never add an agent-specific
skill directory elsewhere in the repository; divergent copies are the failure this layout
prevents.

## Write every skill for every agent

- One directory per skill, holding `SKILL.md` with YAML frontmatter that has only `name` and
  `description`. The `name` matches the directory name.
- Use the shell, `git` and `gh` only. Do not name a tool, slash command, memory store or
  message transport that exists in only one agent.
- Name the agent by its family (`claude`, `codex`, `deepseek`), with a public role suffix only
  when the maintainer assigns one (`codex-review`). Never use a session name, thread ID or model
  version.
- Tell the agent to read this file. Some agents load a skill without the surrounding
  `AGENTS.md`, so each `SKILL.md` names `agents/skills/AGENTS.md` in its opening lines.
- A skill is a list of work steps. Invoking the skill or the task is the authorization for
  its steps; a skill asks the maintainer only for missing information or for authority beyond
  that task (WDR 001, Decision 3).
- Decisions live in the records under `docs/architecture/adr/`, `docs/product/pdr/` and
  `docs/engineering/wdr/`. A skill cites the record ("PDR 002, Decision 2") and states the
  behavior as what Koinon currently does, rather than restating a decision as a general rule.
  A skill adds no approval step, stop condition or prohibition that no record states (WDR 001,
  Decisions 3 and 5).
- Add every new skill to `README.md` in this directory in the same change. Run
  `go run ./scripts/skill-commands` after changing skill metadata or adding, renaming or
  retiring a skill, and commit the generated OpenCode wrappers. The contributor check
  runs `go run ./scripts/skill-commands --check` to catch drift.

## Content that must stay out

This repository is public. Skills and anything they commit must never contain private
thread or session IDs, keys, credentials, inbox or peer message bodies, memory store content,
environment dumps, email addresses, or absolute paths under a home directory.
