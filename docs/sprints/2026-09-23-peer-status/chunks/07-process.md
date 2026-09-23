# Sprint 2026-09-23 · Chunk 07 — Process

> References: `decision.md`, `sprint.md`, chunk 06, `docs/WORK-ITEMS-POLICY.md`. Not restated
> here.

## Scope

Criterion 10: the `ship` and `sprint` skills in `agents/skills/`.

## Approach

- `ship`, step 1: when the change delivers an issue, start one memory work item for it with the
  installed `memory.py work` commands, keyed by the agent's native session identity (chunk
  06), link the issue in its title, and reuse it through build, review and merge. Finish it
  after the merge. A read-only reviewer does not claim work.
- `sprint`, phase 5: one work item per chunk, started by the agent that builds it.
- Follow `agents/skills/AGENTS.md`: shell, `git` and `gh` only; no single-agent tool names.

## Done-criteria (this chunk's slice)

- Criterion 10 holds. The suite is exempt for this agent-only change (#105).
