# Chunk 01 — embedded catalog and queries

Implements decision criteria 1, 2, 3, 7 and 8, with the matching tests in definition-of-done.md.

## Catalog

Add a small `internal/library` package with embedded, reviewed text assets and a versioned
manifest. Each item carries ID, display name, description, purpose, supported families,
requiring release, SHA-256 content digest, content and typed target descriptors. Items are at
most 64 KiB, and the manifest has at most 32 items; refuse invalid built-in metadata during
tests/build validation. Requiring release is release metadata, never a filesystem timestamp.
Dev builds identify their development content explicitly rather than inventing a release.

Initial skill IDs: `skill.handoff`, `skill.pickup`, `skill.pair`, `skill.peer-review`,
`skill.spin-up`, `skill.spin-down`, `skill.recover-team`. Each targets a reviewed relative
`agents/skills/NAME/SKILL.md` in an adopting repository. Portable versions retain native
guide startup, peer-data boundaries, bounded review and private ignored handoffs, but defer
project-specific signing, checks, branch names, approval policy and work allocation to the
adopter's instructions. They contain no pairing authorization.

`setup.guidance` is an inert, marked reference fragment proposed for project instructions;
`setup.permissions` is a standalone reference proposed under `docs/koinon/PERMISSIONS.md`,
derived from #253's verified examples and limits. The latter recommends native entries and
where to review them, but adopting the document does not edit permission settings. Native
skill discovery pointers are optional, separately reviewed changes described in the guidance
item, using verified family paths; plain reading of skill files is supported for every family.
No universal native permission or discovery syntax is invented for an unverified family.

`setup.claude-user-skill` is a separate, Claude-only item targeting
`~/.claude/skills/koinon-guide/SKILL.md`. It is a reference wrapper with name/description
frontmatter and plain instructions to read project rules and the installed guide/catalog.
It has no dynamic command substitution, tool grant, launch/reset step or pairing authorization.
This [personal skill location](https://code.claude.com/docs/en/skills#choose-where-skills-load)
is documented by Claude. Verify discovery for the named supported Claude version in an isolated
temporary home without a provider call before shipping. Resolve HOME from trusted native
environment; a nondefault configuration root must be verified or reported unsupported, never
guessed. Adopting this native-home item requires its own exact change and approval.

The three team-operation skills are authored from the requested outcomes in issue145's
public comment. Do not import downstream private files. Spin-up reviews checkout state and
an authorized launch plan before changing Git or starting a CLI; spin-down asks each session
for its own private handoff and records an ignored team manifest; recover-team treats the
manifest and outage observations as data and shows a restart plan for approval. None types
unverified commands, automatically clears a session, or treats a peer's readiness as a new
maintainer authorization.

## Transport and offers

Expose `library_list` and `library_read` through the existing daemon and MCP, and
`koinon library list/read` through the existing command identity path. Read accepts only a
catalog ID; family filtering comes from the validated caller. No URL, arbitrary pathname or
model-supplied identity is accepted. Use existing response/size limits and bounded list paging.

Initialize instructions and guide name the list/read path and state that content is reference
data and adoption requires a reviewed diff and maintainer approval. Initialize can offer the
library before registration; it does not perform a catalog query or adoption itself.

Document available commands in PROTOCOL.md, USAGE.md and INSTALL.md and add Unreleased
changelog content. Verify against the embedded bytes and actual tool/family registry, including
#253's permission-purpose guards. This chunk does not add state queries or decision writes.
