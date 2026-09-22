# Shared agent skills

The skills in this directory work in every agent that develops Koinon. Claude Code and Codex
read them through symlinks, so there is one copy of each skill. Rules for writing a skill are in
[AGENTS.md](AGENTS.md).

## Skills

| Skill | Use it to |
| --- | --- |
| [handoff](handoff/SKILL.md) | Commit a public snapshot of this agent's work to `handoff/<agent>/` at the end of a session. |
| [pickup](pickup/SKILL.md) | Resume from this agent's newest handoff and verify it against the live repository. |
| [sprint](sprint/SKILL.md) | Plan a deliverable of more than one pull request: decision, definition of done, ordered chunks, peer review of the plan. |
| [check](check/SKILL.md) | Run the checks that must pass before a push. |
| [ship](ship/SKILL.md) | Take one change from a work branch to a squash merge on `develop`. |
| [peer-review](peer-review/SKILL.md) | Review another agent's frozen commit and record the verdict bound to it. |
| [release](release/SKILL.md) | Promote `develop` to `main` once the release is shown to install and upgrade. |

A typical change runs `check`, then `ship`, with the other agent family running `peer-review`.
A larger deliverable starts with `sprint`. `release` promotes the result to `main`.

## Run a skill

| Agent | Command |
| --- | --- |
| Claude Code | `/<name>`, for example `/handoff` |
| Codex | `$<name>`, for example `$handoff`, or select it from `/skills` |
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
