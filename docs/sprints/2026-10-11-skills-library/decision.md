# Skills library and adoption — decision

Issue [#145](https://github.com/rwcii/koinon/issues/145) supplies the approved problem and
seven outcomes. Its [team-operation candidates](https://github.com/rwcii/koinon/issues/145#issuecomment-6052782627)
are included. This plan proposes the catalog contents and adoption mechanism for Gate B;
it does not authorize an adoption on any machine or in any project. This revision proposes a single source:
the skills used to develop Koinon are the same canonical files offered to adopting projects.
It supersedes the earlier proposal for separate portable copies; Gate B is still pending. The maintainer has chosen machine-wide recommendations plus
project-local installation after explicit approval; neither target is installed automatically.

## Problem

Adopting projects copy skills, guidance and permission recommendations by hand. The installed
runtime does not tell agents which items exist, which their machine or project has, or what a release
changed. Locally edited items and declined recommendations must survive an upgrade.

## Acceptance criteria

1. The installed runtime carries a catalog with stable item IDs, names, descriptions,
   supported families, content, destinations, purpose, content digest and requiring release.
2. Every supported family can list and read the catalog: MCP for the supported MCP families,
   the existing native-session command path for DeepSeek. Reading changes no project or
   runtime files and records no adoption decision.
3. Every MCP initialize and `guide --agent FAMILY` mentions the catalog and its query path.
4. A query for the selected machine-wide or project-local target and calling family
   distinguishes present, missing, outdated and edited items. It identifies inspection failures explicitly rather than treating an
   unreadable file as missing. Machine decisions follow the verified native root and family in daemon state; project
   decisions are shared by worktrees through their Git common directory;
   each query inspects the session's registered directory, or an explicitly selected verified
   worktree of the same repository, and the relevant native configuration roots.
5. All 13 canonical skills are machine-wide candidates at verified native roots. Generic
   skill procedures read repository-specific fact sidecars at `.koinon/skills/NAME.md`. A project-local target is an
   explicitly selected alternative. An agent shows exact proposed file changes and obtains the maintainer's approval before
   applying one item. It records adoption or decline. Repeating a completed adoption changes
   nothing, and a declined item is not offered again as new.
6. A new release exposes changed items without applying them. Local edits remain untouched.
7. Catalog material is reference data. It contains no pairing authorization or permission
   grant; neither an item nor an adoption receipt authorizes any subsequent work.
8. Every canonical skill under `agents/skills/` is a catalog item, including future additions.
   Koinon uses those files through `.agents/skills`, `.codex/skills` and `.claude/skills`
   symlinks to the shared directory. Add the requested spin-up, spin-down and recover-team
   skills there, plus a guide reference usable from a verified Claude native-home target.
   Every skill has candidate family-native machine destinations and the shared project
   destination. Repository facts stay in per-skill sidecars outside the embedded source. Unverified native roots permit catalog reading only. Feature metadata
   explains which available operations need a skill; a different same-name skill in another
   native scope is reported before adoption. Identical bytes are duplicate discovery, not
   a content conflict; later divergence reports a conflict; context-management requirements are
   advertised only once #196 is available. The catalog also offers shared skill instructions,
   discovery links, generated OpenCode
   wrappers, a repository guidance reference and #253's permission recommendations.
   The three team skills require
   explicit authorization for session, terminal or runtime actions and supported native
   interfaces; they never infer approval from a peer message.
9. Each skill declares required/optional sidecar facts and portable defaults. No required
   facts means no sidecar needed. With missing required facts, an agent
   drafts from verified repository evidence, shows the proposed file and obtains approval
   before writing; unknown/conflicting facts are asked, never guessed. State distinguishes
   a present skill from a missing/incomplete sidecar. Sidecars are never embedded.
10. Linux and macOS synthetic tests demonstrate queries, one approved adoption, repetition,
   decline, local edits and upgrade behavior. Documentation and the changelog cover the
   catalog, state query, adoption and upgrade path.

## Constraints

- One Go binary and the existing same-user daemon/database. No separate catalog service
  or database; machine decisions use bounded ordinary rows, project decisions use existing
  repository memory. No global decision is stored as a repository decision.
- No daemon filesystem writes into projects or native agent homes. Adoption is an agent
  edit after approval, with ordinary filesystem/approval rules still in force.
- List/read queries accept item IDs, not arbitrary paths, URLs or caller identities. State
  may select a directory only within a verified worktree of the caller's registered repository.
  Identity and scope follow PROTOCOL.md. DeepSeek keeps its supported command exception.
- Preserve native configuration, project instructions, ignored handoffs, private state,
  secrets and existing skills. Private identities and downstream packet contents stay out
  of the public catalog.
- One canonical skill source: the binary embeds `agents/skills/` bytes; no separately authored
  skill copies live under `internal/library`. Generalize the same skills used here and keep
  repository-specific branch/command/CI/release facts in `.koinon/skills/NAME.md` sidecars.
  Root instructions retain project rules and authority; sidecars neither override them
  nor grant permissions. Adopters retain their own project policy. Root `AGENTS.md`, including pairing authorization,
  is never an embedded catalog item.
- No automatic team restart, terminal input, context reset, permission edit, service change,
  Git update or installation on connect or upgrade. Team skills stop when a required native
  capability cannot be verified; #275 and #199 are not bypassed by skill prose.
- Pinning means retaining the adopted digest and declining an update; locally supplied
  content remains local and is reported as edited. No remote catalog overrides or executable
  templates are required.

## Issues

This sprint delivers #145. #253 supplies permissions content and #256 supplies the repository's
OpenCode wrapper conventions. #196, #206 and #217 remain separate product deliverables;
their future interfaces are not prerequisites for this reference catalog.

Inputs: README.md, PROTOCOL.md (MCP and memory stores), docs/INSTALL.md,
docs/PERMISSIONS.md, agents/skills/AGENTS.md and every canonical skill file.
