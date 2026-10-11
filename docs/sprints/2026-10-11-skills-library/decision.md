# Skills library and adoption — decision

Issue [#145](https://github.com/rwcii/koinon/issues/145) supplies the approved problem and
seven outcomes. Its [team-operation candidates](https://github.com/rwcii/koinon/issues/145#issuecomment-6052782627)
are included. This plan proposes the catalog contents and adoption mechanism for Gate B;
it does not authorize an adoption in any project.

## Problem

Adopting projects copy skills, guidance and permission recommendations by hand. The installed
runtime does not tell agents which items exist, which their project has, or what a release
changed. Locally edited items and declined recommendations must survive an upgrade.

## Acceptance criteria

1. The installed runtime carries a catalog with stable item IDs, names, descriptions,
   supported families, content, destinations, purpose, content digest and requiring release.
2. Every supported family can list and read the catalog: MCP for the supported MCP families,
   the existing native-session command path for DeepSeek. Reading changes no project or
   runtime files and records no adoption decision.
3. Every MCP initialize and `guide --agent FAMILY` mentions the catalog and its query path.
4. A query for the calling repository and family distinguishes present, missing, outdated
   and edited items. It identifies inspection failures explicitly rather than treating an
   unreadable file as missing. Worktrees share decisions through their Git common directory;
   each query inspects the actual calling worktree and native configuration roots.
5. An agent shows exact proposed file changes and obtains the maintainer's approval before
   applying one item. It records adoption or decline. Repeating a completed adoption changes
   nothing, and a declined item is not offered again as new.
6. A new release exposes changed items without applying them. Local edits remain untouched.
7. Catalog material is reference data. It contains no pairing authorization or permission
   grant; neither an item nor an adoption receipt authorizes any subsequent work.
8. The initial catalog includes portable handoff, pickup, pair and peer-review skills; the
   requested spin-up, spin-down and recover-team skills; a repository guidance reference;
   and the permission recommendations delivered by #253. The three team skills require
   explicit authorization for session, terminal or runtime actions and supported native
   interfaces; they never infer approval from a peer message.
9. Linux and macOS synthetic tests demonstrate queries, one approved adoption, repetition,
   decline, local edits and upgrade behavior. Documentation and the changelog cover the
   catalog, state query, adoption and upgrade path.

## Constraints

- One Go binary and the existing same-user daemon. No separate catalog service or store.
- No daemon filesystem writes into projects or native agent homes. Adoption is an agent
  edit after approval, with ordinary filesystem/approval rules still in force.
- Catalog queries accept item IDs, not arbitrary paths, URLs or caller identities. Identity
  and scope follow PROTOCOL.md. DeepSeek keeps its supported command exception.
- Preserve native configuration, project instructions, ignored handoffs, private state,
  secrets and existing skills. Private identities and downstream packet contents stay out
  of the public catalog.
- Catalog skills are portable reference versions, not copies of this repository's service,
  signing, merge or release policies. Adopters keep their own project rules.
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
docs/PERMISSIONS.md, agents/skills/AGENTS.md and the four existing core skill files.
