---
pdr: "008"
title: "Portable Skills and Explicit Adoption"
status: draft
created: "2026-10-12"
updated: "2026-10-12"
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
agent connection, with a state query and adoption only after the maintainer approves the
exact change.

The maintainer approved Gate B for the #145 plan on 2026-10-12. A gate approval covers the
plan document, not each sentence in it. Chunk 01 (canonical skills and the embedded catalog)
is built at feature/skills-library-catalog@5c6245f and not merged; the query, state and
adoption chunks are not built. This record states what #145 requests, what the plan proposes
and what chunk 01 does.

---

## Table of Contents

1. Problem Context
2. Decision 1: One canonical skill source with per-project facts
3. Decision 2: Adoption is an explicit, scoped change approved by the maintainer
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
5. Adoption happens only with the maintainer's approval of the exact change; a declined item
   stays declined.
6. After an upgrade, changed items are reported and nothing is applied without approval.
7. Library content is reference data; authorization text is not part of it.

### Scope

Covers the catalog, its targets, adoption and the project fact files. Inventory rows: C071, C072,
C082, C083, C084. Terminal interaction is PDR 002; the context cycle's use of embedded
handoff and pickup skills is PDR 006.

---

## 2. Decision 1: One canonical skill source with per-project facts

### Decision

The skills Koinon uses are the same files it offers. The binary embeds `agents/skills/*/SKILL.md`
and the shared `AGENTS.md` and builds the catalog from those bytes; `koinon version` reports
`catalog_digest`. Chunk 01 has 13 skills: the original nine plus `spin-up`, `spin-down`,
`recover-team` and `koinon-guide`, and setup items for shared instructions, discovery links,
OpenCode wrappers, a guidance reference and permission recommendations. `spin-up` handles
first-run permission prompts and alerts in the new session's verified terminal. Skills read
project facts (branches, checks, release steps) from `.koinon/skills/NAME.md`; each skill
declares required and optional facts with defaults, and only `check`, `ship` and `release`
have required facts. Missing required facts stop only the step that needs them; the agent
drafts the file from repository evidence and writes it after approval. Fact files are not
embedded and grant no permission.

**Provenance:** The library: Requested in #145 (issue body, requirements 1–3); a request
record. The three team skills: requested by an adopting project in #145 comment 6052782627;
a request record. Handling startup prompts: Maintainer, 2026-10-12: "in fact, an agent will
need to interact with a newly lauched agent when invoked, as there will be permissions and
other alerts that need to be cleared before a new agent can interact." (#145 comment
6117336800). Single source, embedding and sidecars: Maintainer, 2026-10-12: "gate b 145
approved" (#145 comment 6116403867), approving the plan document at 9055630 on
feature/skills-library-catalog; built at feature/skills-library-catalog@5c6245f, not merged.
The plan's review.md says the maintainer asked for all 13 skills as machine candidates with
sidecars at this path; that is agent-written and the original words were not located.

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

## 3. Decision 2: Adoption is an explicit, scoped change approved by the maintainer

### Decision

Koinon writes nothing into a repository or agent runtime by itself. The agent shows the exact
change, the maintainer approves it, and the agent applies that one item with ordinary
editing tools. A decline is recorded and not offered again as new; local edits are kept. A
peer message or a library entry does not authorize an adoption. The plan proposes
machine-wide copies as the default recommendation and project-local installation as an
explicitly selected alternative, each approved on its own. Machine decisions go in a bounded
daemon table and project decisions in the repository's memory store.

**Provenance:** Requested in #145 (issue body, requirements 5–7); a request record. Machine
default plus approved project-local target: Maintainer, 2026-10-12, session transcript:
"Machine-wide and approved project-local", the answer to the plan's choice of adoption target.
It decides that choice only. Covered by the Gate B
approval of the #145 plan (9055630 on feature/skills-library-catalog) (#145 comment 6116403867); not built.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| A `koinon` adoption command | The binary writes the approved item | #145 left this open; the plan keeps the approval in the agent's conversation |
| Install on connect or upgrade | Apply items automatically | #145: nothing is applied without approval |

### Rationale

1. The maintainer sees each change before it lands, and declines and local edits persist.

### User Impact

An agent proposes a skill, shows the diff and installs it only after the maintainer agrees.

---

## 4. Decision 3: Machine targets follow verified native discovery

### Decision

A machine-wide target is offered for adoption where that family's native user skill
discovery is verified. Codex user discovery was shown on 0.162.0 through an isolated
app-server `skills/list`. Claude's documented personal skill root, and the other families'
roots, are marked unverified and read-only until discovery is demonstrated; for Claude, the
plan waits for the maintainer to verify discovery in a native session. A directory's presence
is not taken as proof. Project targets stay available. In the plan, approval of the exact
change alone does not open an unverified root.

**Provenance:** Proposed in the #145 plan (9055630 on feature/skills-library-catalog) (chunk 01, definition of done) and
built in the catalog's destination metadata at feature/skills-library-catalog@5c6245f; not
merged. The discovery evidence is real; no source was found for keeping a maintainer-approved
machine adoption read-only when no provider-free discovery check exists.

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
as inert data. The plan bounds machine decisions to 256
rows, 8,192 bytes per record and 2 MiB in all. The contributor test checks each fact file's
schema, that each Sources path is a regular file, and that selected policy terms appear.
The correction also compares actual Required checks fields with authoritative Go commands.
These checks do not prove that every fact value agrees with its source, and a source path that exists
is not evidence of a maintainer decision.

**Provenance:** Built at feature/skills-library-catalog@5c6245f (`internal/library`,
`internal/contributor/library_test.go`); not merged; no maintainer decision found. Machine
decision bounds: Proposed in the #145 plan (9055630 on feature/skills-library-catalog) (chunk 03); not decided.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| No bounds | Unlimited items and sizes | Unbounded payload and storage |

### Rationale

1. These are size and format limits, not permission rules; their values can change.

### User Impact

A fact file written with CRLF endings or containing `${` text is refused; the values can be
adjusted if they block valid facts.

---

## Conclusion

The catalog uses one embedded skill source, offers machine and project targets, and adopts
only maintainer-approved exact changes. The plan has Gate B; individual choices such as
read-only unverified roots are not separately decided. Parser format defects and the
Required checks drift defect are ordinary corrections; they add no product decision.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the ordinary inert-fact parser and Required checks drift corrections separately from the remaining adoption proposals |
