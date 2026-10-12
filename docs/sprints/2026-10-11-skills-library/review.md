# Skills library and adoption — review

The observer reviews the frozen plan before Gate B. Record one material finding batch and
each disposition here. Changed criteria, chunks or done-criteria receive bounded follow-up.

| Finding at 7d3e560 (Claude) | Disposition |
| --- | --- |
| Medium: agent-home state is required but no initial item targets that home. | Fixed: add separately approved `setup.claude-user-skill` at the documented personal skill path and test that real item under temporary HOME. Native discovery must be verified without a provider call; nondefault unverified roots report a limitation. |
| Medium: the MCP process cannot infer the worktree the model is editing. | Fixed: default to the registered directory and accept an explicit directory only within a listed worktree of the same registered Git common directory. Test linked and foreign worktrees. |
| Minor: split the three team skills into another chunk. | Dropped: the two chunks are independently complete; the definition of done already requires focused review and scenario tests of the team skill content. No implementation or native action occurs during planning. |

Bounded follow-up (Claude): PASS at `597960e7bdde78a2c2fa1e626a51b46da2dd503b`.
Both accepted findings are resolved in the specification and required test coverage. This
verdict reviews the plan; implementation and native-discovery evidence are still to be built.
No findings remained in that revision. Its approval does not cover the changed canonical-source
proposal below; the earlier Gate B request is superseded.

## Canonical-source revision

The revised proposal uses the actual shared skills for Koinon and adopters, generalizes them
in place, extracts project policy into root instructions, preserves the three symlinks, and
embeds the canonical bytes. New skills enter the same enumeration. Native-CI classification
changes because those bytes become installed runtime content. There are now three complete
chunks: canonical source/catalog, query/offer, state/adoption.

Review of this changed scope is pending. Carry the earlier resolved native-home and verified
worktree-selection findings forward, and review the canonical-source, policy-preservation,
link/collection handling and new chunk boundaries before presenting Gate B again.

Changed-scope review (Claude, `743003e9ff1da26c2bd50cf6c3a63e0b978d6311`):

| Finding | Disposition |
| --- | --- |
| Medium: an adopter's real skill directory is not a custom symlink; its handling was unspecified. | Fixed: `directory_conflict` is a distinct inspection result. Stop/report link adoption, never traverse, move, merge or delete the directory. Test real directories and preserved local skills. |
| Minor: choose an exact filename for the permissions embedding file. | Dropped: its canonical source directory is already fixed (`docs/PERMISSIONS.md`); the Go filename is a routine implementation choice and changes no boundary. |
| Minor: the plan PR must wait for the service PR. | Dropped as a new change: the root one-open-PR rule already applies. No plan publication races that target; Gate B uses the reviewed branch artifact. |

Bounded verification (Claude): PASS at `07bbf67e678dc05456c46399970d31dfc1e85511`; unchanged scope review carries
forward. No product work is authorized before direct maintainer Gate B approval.


## Machine-wide and explicitly approved project-local revision

The maintainer selected both targets. This proposal keeps the canonical-source and
preservation decisions, makes machine-wide native copies the default recommendation,
retains explicit project-local approval, and separates machine decisions in bounded
ordinary daemon rows from project decisions in existing repository memory. Codex user
skill discovery was demonstrated through isolated app-server skills-list at 0.162.0;
unverified family roots remain read-only. Requirement metadata names available features
only. Changed-scope review is pending; this proposal is not Gate B approval.


Changed-scope review (Claude, `3279482522f214b2479e39371da1b489ec6dfb51`):

| Finding | Disposition |
| --- | --- |
| MEDIUM: machine-wide copies can shadow same-name project workflows; a stale global skill can hide changed canonical source. | Accepted and fixed: generic check/ship/release/sprint are project-only; operation skills are machine candidates. Inspect both applicable scopes, report name_conflict separately from file state, preserve both and stop adoption pending separately reviewed conflict resolution. Tests cover local edits and stale global copies. Claude precedence is documented; Codex native skills-list lists both and is not invocation proof. |
| Evidence limitation: no confirmed provider-free Claude user-skill discovery command | Accepted: its documented candidate target stays read-only unless maintainer native-session discovery is verified at the final head. Do not invent a CLI or claim directory presence as proof. |

Bounded fix verification (Claude): PASS at
`43192873803b24b824d97dbf52de62d9f70e5a48`; unchanged canonical-source/storage/scope review
carries forward. No material finding remains. In this repository the canonical operation
skills already exist, so machine adoption reports a same-name conflict; project discovery
continues to use the shared source. Machine copies serve projects without that collision.
No product or adoption authorization is inferred from either review.


## Generic skills and repository sidecars revision

The maintainer asked to include all 13 skills as machine candidates and use per-repository
fact sidecars, confirming `.koinon/skills/NAME.md` as their location. This planning-only
revision supersedes the earlier exclusion of generic workflow skills and the earlier Gate B
proposal. Every canonical skill declares/reads its fields and drafts missing facts for
separate approval. Root authority remains; facts never grant permissions or remove steps.
This repository's facts move outside embedding, with contributor source-drift tests.
Identical native/project skill bytes are duplicate discovery; different contents retain
name_conflict. Sidecar presence/schema is separate from canonical skill content state.
Changed-scope review is pending; earlier storage/native-proof fixes carry forward. No Gate B
implementation or actual sidecar/skill/native-configuration changes are inferred here.


Sidecar-scope review (Claude, `abfce58573089fd773df1f110648de31163bdd51`):

| Finding | Disposition |
| --- | --- |
| MEDIUM: required sidecars duplicate portable conventions and block skills unnecessarily | Accepted and fixed: declare required/optional facts plus defaults per skill. Check/ship/release need facts without portable answers; other skills need no sidecar unless project overrides exist. Native proof, handoff/root convention, guide interface and authorization gates remain canonical. Only missing required facts stop dependent steps. State/tests distinguish no-sidecar-needed and missing-required. |
| MINOR: define required-skill behavior outside a repository | Dropped: chunk03 already reports unavailable project context, never a guessed missing path; required repository-specific steps cannot proceed. |

Bounded verification pending; unchanged sidecar/native/store/conflict review carries forward.
