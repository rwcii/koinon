# Shared agent skills

The skills in this directory work in every agent that develops Koinon. Claude Code and Codex
read them through symlinks, so there is one copy of each skill. Rules for writing a skill are in
[AGENTS.md](AGENTS.md).

## Skills

| Skill | Use it to |
| --- | --- |
| [handoff](handoff/SKILL.md) | Commit a public snapshot of this agent's work to `handoff/<agent>/` at the end of a session. |
| [pickup](pickup/SKILL.md) | Resume from this agent's newest handoff and verify it against the live repository. |

## Run a skill

| Agent | Command |
| --- | --- |
| Claude Code | `/handoff`, `/pickup` |
| Codex | `$handoff`, `$pickup`, or select it from `/skills` |
| Any other agent | Tell the agent: "Read `agents/skills/<name>/SKILL.md` and follow it." |

## Handoffs

Each agent writes only `handoff/<agent>/`, and `pickup` reads only the caller's own directory
unless the user names another agent. Handoffs are committed on the current work branch and reach
`develop` with its pull request, so they are public: see the content rules in
[AGENTS.md](AGENTS.md). Private notes stay in the ignored `_handoff/` directory.

## Layout

```text
agents/skills/          the tracked skills
.claude/skills  -> ../agents/skills
.agents/skills  -> ../agents/skills   (Codex repository discovery)
.codex/skills   -> ../agents/skills
handoff/<agent>/        committed handoffs, one directory per agent
```

To add a skill, create `agents/skills/<name>/SKILL.md` and add a row to the table above.
