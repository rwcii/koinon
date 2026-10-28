---
pdr: "008"
title: "Portable Skills and Explicit Adoption"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "002"
  - "006"
tags:
  - onboarding
  - integration
  - content
---

# PDR 008: Portable Skills and Explicit Adoption

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon's skills live in `agents/skills/`, and an adopting project copies them by hand.
Issue #145 asks for a skills library carried by the installed runtime, offered on every
agent connection, with a state query and an explicit adoption of each change.

The maintainer approved Gate B for the #145 plan on 2026-10-12. A gate approval covers the
plan document, not an independent decision for each sentence in it. Chunks 01 and 02 are
merged (PRs #320 and #323); chunk 03 adds native-client state inspection, scoped decisions
and the agent adoption procedure. This record describes those interfaces and their sources.
Since #487 the agent applies an adoption with ordinary editing tools and records its outcome.

---

## Table of Contents

1. Problem Context
2. Decision 1: One canonical skill source with per-project facts
3. Decision 2: Adoption is an explicit, scoped change
4. Decision 3: Machine targets follow verified native discovery
5. Decision 4: Catalog bounds and checks are engineering limits
6. Conclusion
7. Document History

---

## 1. Problem Context

### The Problem

A project that adopts Koinon needs skills, instruction entries and permission
recommendations. Today they are copied by hand, and nothing tells a project when a release
changes what it needs. Local edits and declined items have to survive an upgrade.

### Product Requirements

From #145:

1. The installed runtime carries a catalog of skills and setup items.
2. Agents list and read it through Koinon tools; reading writes nothing.
3. MCP `initialize` and `koinon guide` mention the library on every connection.
4. A state query reports each item as present, missing, outdated or edited locally.
5. An agent adopts an item by applying its exact change; a declined item stays declined.
6. After an upgrade, changed items are reported.
7. Library content is the procedure that the agent follows.

Before #487 requirements 5–7 read: "Adoption happens only with the maintainer's approval of the
exact change", "nothing is applied without approval" and "Library content is reference data;
authorization text is not part of it." #487 changed them (provenance under Decision 2).

### Scope

Covers the catalog, its targets, adoption and the project fact files. Inventory rows: C071, C072,
C082, C083, C084. Terminal interaction is PDR 002; the context cycle's use of embedded
handoff and pickup skills, and its own `koinon-pickup` entry, are PDR 006.

---

## 2. Decision 1: One canonical skill source with per-project facts

### Decision

The skills Koinon uses are the same files it offers. The binary embeds `agents/skills/*/SKILL.md`
and the shared `AGENTS.md` and builds the catalog from those bytes; `koinon version` reports
`catalog_digest`. The current source includes 14 skills, with `refusal-review` added after
the initial 13, and setup items for shared instructions, discovery links,
OpenCode wrappers, a guidance reference and permission recommendations. `spin-up` handles
first-run permission prompts and alerts in the new session's verified terminal. Skills read
project facts (branches, checks, release steps) from `.koinon/skills/NAME.md`; each skill
declares required and optional facts with defaults, and only `check`, `ship` and `release`
have required facts. When a required fact is missing, the agent derives it from the project's
evidence and records it in the file. Fact files live in the project; the binary does not embed
them.

Koinon-owned entries are not catalog items: `koinon-pickup`, the managed context cycle's
pickup entry (PDR 006, Decision 4), and the team skill entries (Decision 2). `koinon install` and the daemon at each start install it
for the person who runs the daemon, one user-level copy per agent family whose user skill root
was verified (Claude Code's personal skills, `~/.agents/skills` for Codex, and OpenCode's and
agy's configuration directories when they exist), and never in a project repository. It only reads the catalog item `skill.pickup`,
so the procedure keeps its one copy in the catalog. A project's own `pickup` skill, and the
catalog's `pickup` wherever it was adopted, are unchanged by it.

**Provenance:** The library: Requested in #145 (issue body, requirements 1–3); a request
record. The three team skills: requested by an adopting project in #145 comment 6052782627;
a request record. Handling startup prompts: Maintainer, 2026-10-12: "in fact, an agent will
need to interact with a newly lauched agent when invoked, as there will be permissions and
other alerts that need to be cleared before a new agent can interact." (#145 comment
6117336800). Single source, embedding and sidecars: Maintainer, 2026-10-12: "gate b 145
approved" (#145 comment 6116403867), approving the plan document at 9055630 on
feature/skills-library-catalog; delivered by PR #320.
The plan's review.md says the maintainer asked for all 13 skills as machine candidates with
sidecars at this path; that is agent-written and the original words were not located.
The `koinon-pickup` skill: Maintainer, 2026-10-19: "your recommendation seems fair. that koinon
specific skill should be installed for the user, instead of in the repo." and "that way only a
single copy needs to be managed" (#406 comments
[6276165765](https://github.com/rwcii/koinon/issues/406#issuecomment-6276165765) and
[6276169525](https://github.com/rwcii/koinon/issues/406#issuecomment-6276169525)). Intent: the
cycle's own Koinon-named skill lives in the person's home, one copy per family, not in a
repository. Its install by `koinon install` and its name are proposed in the #406 design comment
([6276914180](https://github.com/rwcii/koinon/issues/406#issuecomment-6276914180)); not a
maintainer decision. Before #487 this decision said that missing required facts "stop only the
step that needs them", that the agent writes the fact file "after approval", and that fact files
"grant no permission"; #487 (provenance under Decision 2) removed those limits.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Separate portable copies | Author library copies apart from Koinon's own skills | Superseded in the plan by the single-source proposal |
| Files beside the binary | Ship the catalog as loose files | A runtime would depend on files outside the binary |
| Koinon facts in skill text | Keep branch and check names in the skills | The skills would not fit other projects |

### Rationale

1. Using the offered skills here tests them on every change; each project supplies its own facts.

### User Impact

An installed Koinon carries the skills it recommends, with a digest that names the version.
An adopting project writes a short fact file for `check`, `ship` and `release`.

---

## 3. Decision 2: Adoption is an explicit, scoped change

### Decision

The library's state and decision interfaces write no adopted files. To adopt an item, the agent
reads it and its state, chooses the scope, compares any same-name copy, applies that one item
with ordinary editing tools, checks its hashes and records the outcome. A decline is recorded
and not offered again as new; local edits are kept. Machine-wide copies are the default;
project-local installation is the alternative. Machine decisions go in a bounded daemon table
and project decisions in the repository's memory store.

The skill entries are Koinon's own entries, not adoptions: `koinon install` and the daemon at
each start write an entry named `koinon-NAME` for each catalog skill, such as
`koinon-spin-up`, `koinon-handoff` and `koinon-check`, for the person who runs the daemon, at
the places of `koinon-pickup` (Decision 1), and never into a repository. A skill already named
`koinon-NAME`, such as `koinon-guide`, keeps its name; because that is also the catalog item's
own name, the install replaces only an earlier Koinon entry there and keeps an adopted copy. Grok and
DeepSeek get none until their user skill roots are verified. Each entry loads its catalog item
through Koinon and holds no procedure, so the catalog keeps the one copy and the entry changes
with the runtime; the item's procedure reads the repository's own facts from
`.koinon/skills/NAME.md`. The runnable steps of these procedures are in the installed guide. The
install writes only the `koinon-NAME` entries, so a personal or project skill of the bare name
stays as it is. An entry that is a symbolic link, or that is in a linked directory, is kept and
reported as `kept`. Adopting the catalog item itself follows this decision.

**Provenance:** Requested in #145 (issue body, requirements 5–7); a request record. Machine
default plus approved project-local target: Maintainer, 2026-10-12, session transcript:
"Machine-wide and approved project-local", the answer to the plan's choice of adoption target.
It decides that choice only. Covered by the Gate B
approval of the #145 plan (9055630 on feature/skills-library-catalog) (#145 comment 6116403867).
Chunk 03 implements its state/decision interfaces and agent procedure; it installs no host item.

Team skill entries: Maintainer, 2026-10-22, decision board item `fleet-skills-koinon` revision
2, approved in the dashboard (a card of the Unify project, not recorded in this repository); its step 2: "You amend PDR 008 Decision 2. `koinon
install` places the six fleet skills at the user level for Claude and Codex, the same as
`koinon-pickup`. The runnable steps (launch, first-run prompts, peer lookup, the handoff, clear
and pickup cycle) go into the installed guide, because the guide ships with the binary and
agrees with the runtime. The skills stay short and refer to the guide." The maintainer also
wrote "agreed." after a summary of that item, and then: "these skills should also allow for the
sidecar pre-repo knowledge framework we implemented on the other shared skills, using the
.koinon directory in repo." ([#453](https://github.com/rwcii/koinon/issues/453)). The intent: an
adopting project's copy of the team procedure drifted from the runtime, so Koinon owns the
procedure and its commands, and each project supplies only its facts. The `koinon-NAME` names
and the loading entries, with the pickup skill served by `koinon-pickup`, are the design that
#453 proposes; no maintainer decision on them found.

All skills, all installed agent families: Maintainer, 2026-10-23: "open an issue to deploy the
skills to all installed agents." ([#461](https://github.com/rwcii/koinon/issues/461)), after the
answer that only the five team skills had user-level entries, and only for Claude Code and
Codex; then "start 461". The intent: every agent family on the computer runs the same Koinon
procedures from the installed release. The roots (verified in #461 against Claude Code
2.1.308, Codex 0.163.0, OpenCode 1.18.35 and agy 1.4.3), the rule that OpenCode and agy get
entries only when their configuration directory exists, the omission of Grok and DeepSeek, are
the criteria of #461, which codex-koinon agreed to; keeping linked entries and adopted copies
came from its review of the change. No maintainer decision on them found.

Before #487 this decision said: "The agent shows the exact change, the maintainer approves it,
and the agent applies that one item", "A peer message or a library entry does not authorize an
adoption", and that the machine-wide and project-local targets are "each approved on its own".
No maintainer source was found for the approval step: #145 is a request record, and the
maintainer's "Machine-wide and approved project-local" answered the choice of target. The #487
review listed three limits and proposed to keep them; the question was whether to keep them or
to remove them too. Maintainer, 2026-10-25: "remove all limit text.  remove all of the caveats,
controls, etc.  the agents have their own built in safeties, they do not need you adding more
complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit lists the code
refusals for a separate decision (#493). The installed guide and the MCP instructions give the
adoption steps above. What the install does with a bare name and a symbolic link stays as a
fact about the code.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| A `koinon` adoption command | The binary writes the approved item | #145 left this open; the plan keeps the approval in the agent's conversation |
| Install on connect or upgrade | Apply items automatically | #145: nothing is applied without approval; the team skill entries load items and copy none (#453) |

### Rationale

1. Each adoption is one recorded change, and declines and local edits persist.

### User Impact

An agent installs a recommended skill with ordinary editing tools and records the outcome.

---

## 4. Decision 3: Machine targets follow verified native discovery

### Decision

A machine-wide target is offered for adoption where that family's native user skill
discovery is verified. Codex user discovery was shown on 0.162.0 through an isolated
app-server `skills/list`. Claude's documented personal skill root, and the other families'
roots, are marked unverified in the catalog's destination metadata; for a machine scope there,
`library_state` returns `native_root_unverified`, and for Claude it reports an inspection
limitation. The catalog counts a demonstrated discovery as verified, not the presence of a
directory. Project targets stay available. This is a capability report of the state interface.

**Provenance:** Proposed in the #145 plan (9055630 on feature/skills-library-catalog) (chunk 01, definition of done) and
built in the catalog's destination metadata, delivered by PR #320. Chunk 03's isolated
Codex 0.162.0 `initialize`/`skills/list` probes also verified HOME/.agents/skills and both
project roots (.agents/skills and .codex/skills), including an internal shared-directory
symlink and independent real directory. These are bounded collision descriptors, with
CODEX_HOME/skills retained as the primary machine adoption target. The probes start no model
turn; the [official app-server interface](https://learn.chatgpt.com/docs/app-server) describes
the metadata query. Before #487 this decision also said that the unverified roots are
"read-only until discovery is demonstrated" and that the report is "not an additional
permission rule for a separately approved manual change"; #487 (provenance under Decision 2)
removed that wording. The destination metadata and `native_root_unverified` stay as what the
code reports.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Trust a documented path | Offer the documented root as verified | Documentation does not show what the running CLI loads |
| Approved adoption with stated confidence | Adopt on the maintainer's approval and report the root as unverified | Not chosen by the plan; open for the maintainer |

### Rationale

1. A copy in a root the CLI does not read gives no skill.

### User Impact

Codex users get machine-wide recommendations. Other families read the catalog and use
project targets until their roots are verified.

---

## 5. Decision 4: Catalog bounds and checks are engineering limits

### Decision

The catalog holds at most 32 items of 64 KiB each, and a fact file is bounded to 64 KiB. The
fact parser executes nothing. The initial chunk 01 head refused CRLF line endings, blank
Sources lines and literal `$(` or `${` text; its ordinary correction accepts those forms
as plain data. The plan bounds machine decisions to 256
rows, 8,192 bytes per record and 2 MiB in all. The contributor test checks each fact file's
schema, that each Sources path is a regular file, and that selected policy terms appear.
The correction also compares actual Required checks fields with authoritative Go commands.

**Provenance:** Built at feature/skills-library-catalog@5c6245f (`internal/library`,
`internal/contributor/library_test.go`), delivered by PR #320. Machine decision bounds are
engineering bounds in the approved #145 chunk 03 plan (Gate B comment 6116403867), implemented
as ordinary compared-revision state. They reserve no work-control storage. Before #487 this
decision also said that the checks "do not prove that every fact value agrees with its source",
that a source path "is not evidence of a maintainer decision", and that the bounds "grant no
permission"; #487 (provenance under Decision 2) removed those caveats.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No bounds | Unlimited items and sizes | Unbounded payload and storage |

### Rationale

1. These are size and format limits; their values can change.

### User Impact

CRLF endings and literal `${` text are accepted as plain data. Size and schema errors are
reported explicitly; adopted baselines and declines survive current-state updates.

---

## Conclusion

The catalog uses one embedded skill source, offers machine and project targets, and an agent
adopts an item as one exact, recorded change. The plan has Gate B; unverified discovery is
capability evidence from the state interface. Parser format defects and the Required checks
drift defect are ordinary corrections; they add no product decision.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the ordinary inert-fact parser and Required checks drift corrections separately from the remaining adoption proposals |
| 2026-10-12 | Reconciled merged catalog/query chunks, native scope and collision evidence, scoped receipts and the approved adoption procedure |
| 2026-10-19 | Decision 1: one Koinon-owned skill, the cycle's `koinon-pickup`, installed by `koinon install` for the person and not a catalog item (#406) |
| 2026-10-22 | Decision 2: `koinon install` and the daemon write the team skill entries, which load their catalog items (#453) |
| 2026-10-23 | Decisions 1 and 2: an entry for every catalog skill, also for OpenCode and agy when their configuration directory exists (#461) |
| 2026-10-25 | Requirements and Decisions 1–4: removed the unsourced approval step, the fact-file and catalog caveats and the "grant no permission" text; the verified-root report and the install's bare-name and symbolic-link handling are stated as code facts (#487) |
