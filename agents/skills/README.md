# Shared agent skills

The skills in this directory work in every agent that develops Koinon. Claude Code, Codex
and OpenCode discover the canonical skills through symlinks. OpenCode also has generated
slash-command wrappers that load these same skills. Rules for writing a skill are in
[AGENTS.md](AGENTS.md).

## Skills

| Skill | Use it to |
| --- | --- |
| [handoff](handoff/SKILL.md) | Write a snapshot of this agent's work to the ignored `_handoff/<agent>/` at the end of a session. |
| [pickup](pickup/SKILL.md) | Resume from this agent's newest handoff and verify it against the live repository. |
| [sprint](sprint/SKILL.md) | Plan a deliverable of more than one pull request: decision, definition of done, ordered chunks, peer review of the plan. |
| [check](check/SKILL.md) | Run the checks that must pass before a push. |
| [pair](pair/SKILL.md) | Share one checkout, trade the driver role at committed checkpoints, and finish within fixed criteria and bounded review. |
| [ship](ship/SKILL.md) | Take one change through the project's branch, review and merge policy. |
| [peer-tmux](peer-tmux/SKILL.md) | Inspect a peer terminal or run a handoff, context reset and pickup cycle through tmux. |
| [peer-review](peer-review/SKILL.md) | Review another agent's frozen commit and record the verdict bound to it. |
| [refusal-review](refusal-review/SKILL.md) | Check another agent's frozen commit for new refusals, gates and restrictions without a source, and for maintainer decisions recorded without their context. |
| [release](release/SKILL.md) | Promote and publish a release after project-specific checks. |
| [spin-up](spin-up/SKILL.md) | Prepare and carry out a team launch, including first-run prompts. |
| [spin-down](spin-down/SKILL.md) | Collect each session's handoff and complete the team stop plan. |
| [recover-team](recover-team/SKILL.md) | Reconcile team state and carry out a restart plan. |
| [koinon-guide](koinon-guide/SKILL.md) | Read installed runtime guidance and catalog recommendations. |

A typical change runs `check`, then `ship`, with the other agent family running `peer-review` and `refusal-review`.
A larger deliverable starts with `sprint`. `release` promotes the result to `main`.

## Run a skill

| Agent | Command |
| --- | --- |
| Claude Code | `/<name>`, for example `/handoff` |
| Codex | `$<name>`, for example `$handoff`, or select it from `/skills` |
| OpenCode | `/<name>`, for example `/handoff`; the command loads the canonical skill |
| Any other agent | Tell the agent: "Read `agents/skills/<name>/SKILL.md` and follow it." |

## OpenCode commands

From the repository root, generate the wrappers after adding, renaming, retiring or changing
skill metadata:

```sh
go run ./scripts/skill-commands
go run ./scripts/skill-commands --check
```

The command needs only Go and the checkout. It reads each skill's `name` and `description`
from single-line YAML strings (plain, JSON-style double quoted, or YAML single quoted).
Quote descriptions containing YAML punctuation, such as `description: "Reason: detail"`.
Names match the directory and `[a-z0-9-]+`; native OpenCode command names and aliases are
reserved. The wrappers contain the description and a skill-loading template with one literal
`$ARGUMENTS`. They do not copy skill instructions.

Generated files have an ownership comment. Generation repairs or removes only those files,
and narrowly adopts the nine original wrappers when their template is unchanged. A custom
command that collides with a skill name, modified legacy template or managed symlink causes
an error before any changes. Custom commands and native OpenCode package files are preserved.
The existing nine wrappers are already tracked on `develop`, so merging this change replaces
them normally.

Check mode compares bytes and the executable bit without writing; it accepts checkout
permissions from the user's umask. Go contributor tests run it on Linux and
macOS, so missing, changed or obsolete wrappers fail CI. Native discovery was verified with
OpenCode 1.18.35 in a scratch repository with private configuration and no provider calls.
See OpenCode's [commands](https://opencode.ai/docs/commands/) and
[TUI command names](https://opencode.ai/docs/tui/).

## Handoffs

Each agent writes its own `_handoff/<agent>/` in the main checkout, and `pickup` reads the
caller's own directory, or another agent's when the maintainer names it. `_handoff/` is ignored
by Git: handoffs are session state, not part of the product. They go in the main checkout
because a linked worktree is removed when its work merges.

## Layout

```text
agents/skills/          the tracked skills
.claude/skills  -> ../agents/skills
.agents/skills  -> ../agents/skills   (Codex repository discovery)
.codex/skills   -> ../agents/skills
.opencode/commands/    generated slash commands that load the skills
_handoff/<agent>/       ignored handoffs, one directory per agent (main checkout only)
```

To add a skill, create `agents/skills/<name>/SKILL.md`, add a row to the table above, and run
`go run ./scripts/skill-commands`. Commit the generated wrappers with the skill change.

## Embedded library and project facts

The Go package here embeds `*/SKILL.md` and the shared `AGENTS.md`; the binary builds its
catalog directly from those bytes. `koinon version` reports `catalog_digest` without
reading a checkout. Setup link/command descriptors derive from the shared layout/metadata;
the permissions recommendation embeds `docs/PERMISSIONS.md` from its own source directory.
The catalog holds the skills and the shared authoring file; root instructions, sprint plans,
repository fact values and handoffs stay in the checkout. The library tools read the catalog and
record adoption state.

All skills are portable machine-wide and project-local candidates. Read the adopting project's
policy and the schema at the end of each skill. Required facts live in that worktree's
`.koinon/skills/NAME.md`; optional facts use declared defaults. Koinon's own fact files cite
public policy/CI sources. A missing required fact is derived from the project's evidence and
recorded in the file. Skill adoption and native configuration changes are their own steps.

Codex user discovery was verified in an isolated native home on 0.162.0. The catalog reports
Claude's documented personal location and the other families' user roots as unverified, for
reading, until native discovery is shown. Each destination uses the same canonical bytes; a
user copy and a project copy of one name with different content are reported as a conflict.
