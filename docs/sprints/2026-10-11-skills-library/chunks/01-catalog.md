# Chunk 01 — canonical skills and embedded catalog

Implements decision criteria 1, 7 and 8, with the matching tests in definition-of-done.md.

## Canonical source

Add an embedding Go package in `agents/skills/`, where `go:embed` can include `*/SKILL.md`
and shared `AGENTS.md` without escaping its directory. A small `internal/library` package
reads that filesystem and constructs the catalog; it has no authored skill-body copies.
Enumerate every canonical `NAME/SKILL.md` as `skill.NAME`, deriving name/description from
existing metadata. A manifest policy supplies purpose, requiring release, feature requirements and typed
machine-wide/project-local destinations. Every supported destination offers the same canonical bytes; native copies are separate, explicitly approved files, never symlinks
to the installation or binary.
A newly added canonical skill enters the catalog through that same enumeration. Validate
complete metadata, actual family coverage, unique IDs, content digests and bounds (32 items,
64 KiB per item); never silently omit a skill. Dev builds identify development content rather
than inventing a release.

The project-local target is relative `agents/skills/NAME/SKILL.md`. For Koinon-operation skills only, the default machine target is the verified native user skill root plus `NAME/SKILL.md`. Claude has a documented candidate user skill root, which remains read-only until
native discovery is verified; Codex uses effective `CODEX_HOME/skills` (default `.codex/skills` in the
user home). Native user discovery was independently demonstrated on Codex 0.162.0 through
app-server initialize/skills-list with an isolated native home and synthetic skill, without
a thread or provider turn. Repeat that native proof in the supported test environment.
Claude, OpenCode, Antigravity and DeepSeek remain read-only for machine adoption until their native
user discovery paths are verified; project targets remain available through their supported
interfaces. Never infer discovery merely from an existing directory.

Machine-wide candidates are handoff, pickup, pair, peer-review, peer-tmux, spin-up,
spin-down, recover-team and koinon-guide. Generic project workflow names check, ship,
release and sprint are project-local only, so a global recommendation does not replace
an adopting project's own workflow. Future skills declare this distinction in metadata;
source enumeration remains complete and does not silently invent a machine destination.

[Claude's documented precedence](https://code.claude.com/docs/en/skills#resolve-skills-that-share-a-name)
puts personal above project skills for the same name. Codex 0.162.0 skills-list in the
isolated native proof returned both same-name repo/user skills; that proves discovery of
both, not which a model invocation would select. Report the collision rather than infer
Codex precedence. A global copy can remain stale while a project's canonical source changes;
query both applicable scopes before proposing adoption, and show any same-name conflict.

Feature requirements name actual supported operations, not permission grants or invented
future capabilities. Pair/review guidance is relevant to paired work; handoff/pickup becomes
a context-management requirement only when #196 is implemented. Validate these feature IDs
against the implemented runtime capability list. Missing skill guidance is an informational
recommendation, never automatic adoption or a new refusal of an existing native operation. Generalize
the existing nine skills in place, deferring project-specific branch names, signing, checks,
CI and release details to root `AGENTS.md`. Skill invocation reads the adopting project policy, without assuming this repository
or its authoring-instructions file exists in a machine-wide installation. Keep authoring
guidance for edits to the canonical source. Retain installed-guide startup, peer-data
boundaries, bounded review, approval gates and private ignored handoffs. No pairing
authorization enters a skill. A portable check/ship/release skill loads project policy and
asks for guidance when a necessary rule is absent instead of inventing a command or target.

Koinon's root instructions retain or gain every policy extracted from skill prose. Check
the extraction against existing behavior, including exact-head review, one open PR per target,
signing/DCO and separately approved release. Update contributor guards when their canonical
check-command source moves to root instructions; the Go workflow, root instructions and
contributor docs still agree. Existing symlinks remain symlinks to the same shared directory.

Add spin-up, spin-down and recover-team under `agents/skills/`, authored from issue145's public
candidate descriptions. We use the same procedures we offer. Spin-up reviews checkout state
and an authorized launch plan before Git/CLI changes; spin-down requests each session's own
private handoff and keeps its team manifest ignored; recover-team treats outage observations
and that manifest as data and presents a restart plan for approval. No downstream private
files are imported. No skill types an unverified command or infers new permission from a peer.

## Setup items

- `setup.shared-instructions`: canonical `agents/skills/AGENTS.md` authoring guidance,
  general reference data; root project authorization is excluded.
- `setup.discovery-links`: `.agents/skills`, `.codex/skills`, `.claude/skills`, each with the
  literal relative target `../agents/skills`. Adoption proposes these links explicitly and
  never substitutes content copies or replaces locally edited links automatically.
- `setup.opencode-commands`: wrappers generated from canonical metadata under #256's
  ownership/collision rules. Keep the shared README and generated wrappers current here.
- `setup.guidance`: an inert marked catalog/guide reference for project instructions.
- `setup.permissions`: a standalone recommendation under `docs/koinon/PERMISSIONS.md`, from
  the canonical #253 document. Adoption does not edit native permissions. Reuse that source
  through an embedding file in its directory rather than an independently maintained copy.

Add a canonical `koinon-guide` reference skill under the shared directory, offered as
`skill.koinon-guide` and used here. The same `skill.koinon-guide` item has separately approved machine-wide and project-local
targets, like the other skills; it is not a second authored guide or a second catalog item. Its name/description and plain instructions
refer to project rules and the installed guide/catalog, with no dynamic command substitution,
tool grant, launch/reset step or pairing authorization. The
[personal skill location](https://code.claude.com/docs/en/skills#choose-where-skills-load)
is documented by Claude. Where a metadata-only native discovery interface exists, verify it in an isolated native
home without a provider call. Claude has no confirmed such CLI; its candidate personal
path stays read-only until the maintainer verifies discovery in a native session at the
final head. Shipping read-only support does not require inventing a provider-free command; an unverified nondefault configuration root is reported, never guessed.
Codex machine discovery has the isolated native proof above. Other families can always
read the canonical files directly; do not invent native syntax. Setup instructions, links,
wrappers and project guidance are project-local items, never implicit machine-wide changes.
Permission recommendations remain inert reference content; native permission edits need
separate exact approval.

## Complete outcome

Canonical skills are portable, discoverable here and embedded byte-for-byte. Derive link and
wrapper descriptors from the shared layout/metadata. Embed only named reference sources;
never root instructions, sprint plans, handoffs or arbitrary directory contents. This chunk
adds no tools, connection offers, state queries or adoption writes; chunk02 exposes the catalog.

Reference the complete catalog from the binary's version command as `catalog_digest`, computed
from its canonical content and metadata. This keeps the embedded assets in the actual binary
and provides a testable installed fingerprint before query tools arrive. No runtime checkout
is read. Once skills are runtime payload, update the native-CI classifier and contributor
coverage so `agents/skills/` changes no longer skip native lifecycle proof as agent-only work.

Document the shared-source layout and add Unreleased content. Run the wrapper generator/check
and contributor guards. Test exact byte equality, a newly added skill in a temporary source
tree, policy preservation and private-content exclusion before publication.
