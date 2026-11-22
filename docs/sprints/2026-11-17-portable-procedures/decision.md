# Portable procedures (#732): decision and acceptance

## Problem

An adopting project keeps its own copy of each procedure skill in its repositories and installs
them by their bare names on the host, where they hide other projects' skills of the same name.
It moves its procedures into Koinon and keeps only its `.koinon/skills/<name>.md` facts. The
Koinon catalog lacks seven of its procedures. A computer with no Koinon gets no skill, because
each installed `koinon-NAME` entry loads its catalog item through the daemon (PDR 008,
Decision 2).

## Source

- Robert Capps, 2026-11-17, in another project's orchestrator terminal, relayed by that session
  (claude-koinon did not see these words): "I built koinon to manage these functions for us,
  because you were struggling with the local skills. Can we create an installable script that
  deploys these skills at the dev system level if koinon isn't installed? this would eliminate
  the skills in repo altogether." Then: "I'm ok with moving the skills to koinon, assuming they
  do not contain any [of that project's] proprietary information." Intent: Koinon holds the
  procedures once for every project, and the projects keep only their facts.
- Robert Capps approved this design on the decision board on 2026-11-17 at 18:10 UTC: the seven
  generic skills and one install script for a computer with no Koinon. He saw the exact text of
  each skill before it came to Koinon.
- Copyright: Robert Capps, 2026-11-17. At that project's terminal, relayed: "Robert Capps and
  Stratovera, LLC". In claude-koinon's terminal he first chose "Koinon LICENSE" (Robert Capps
  only, no notice in the skills); asked which of the two answers is right, he chose "Change
  Koinon's LICENSE": LICENSE names "Robert Capps and Stratovera, LLC" and covers every skill,
  and the skills carry no notice of their own.
- The install script changes PDR 008 Decision 2; Robert Capps approved it on the decision
  board on 2026-11-17 at 21:58 UTC (chunk 02; PDR 008 Decision 2 holds the decision).

## Acceptance

1. The catalog holds seven more skills: `preflight`, `provenance`, `refusal-audit`,
   `standards-review`, `record`, `sprint-closeout` and `standards-audit`. Each one is the text
   that the maintainer saw, with the changes that he approved on 2026-11-17: the opening names
   its Koinon record and the authorization of WDR 002, Decision 1, and three sentences lose the
   limit phrases that #487 removed from the skills; and `refusal-audit` keeps no running log of
   decisions, as he directed.
   Each runs in every supported agent family with the adopting project's facts.
2. `koinon install` and the daemon write a `koinon-NAME` entry for each of them, as for the
   other catalog skills.
3. LICENSE names "Robert Capps and Stratovera, LLC"; the MIT text is unchanged.
4. PDR 008 records the decision with its source, scope and the copyright answer.
5. Chunk 02, after its verdict: on a computer with no Koinon, one script writes the skills of
   one pinned release, checked against that release's SHA256SUMS, under the `koinon-` names in
   the verified skill roots; a later `koinon install` replaces them with its entries.

## Scope

Koinon offers the seven skills; it adopts none of them for its own work now, and adds no fact
file or tool command for them. The steps of a skill that need Koinon tools (messages, work
claims, the board) need Koinon. The adopting project's own changes are its work.

## The release number for portable procedures (#732)

- **Decided by:** Robert Capps.
- **Date:** 2026-11-18, terminal.
- **Context:** the release from develop after v0.12.0 holds #735 (seven more catalog skills),
  #736 (the skills archive and `scripts/install-skills.sh` for a computer with no Koinon), the
  fixes #730 and #737, and CI-only changes; the store stays at schema 39. The agent proposed
  v0.13.0 for the two new features.
- **Decision:** v0.13.0: "release is go, 0.13.0".
- **Reason and scope:** the answer names no further reason. This covers this release only; it
  sets no rule for later version numbers.

## The release number for the skill entry check (#741)

- **Decided by:** Robert Capps.
- **Date:** 2026-11-18, terminal.
- **Context:** after v0.13.0, the Unify orchestrator found that no read-only check confirms the
  installed `koinon-NAME` skill entries (#741); the fix makes `koinon install --plan` check them
  (#742). The store stays at schema 39.
- **Decision:** v0.13.1: "when done, release 0.13.1 with the fix, then install it and have the
  unify coordinator verify the fix".
- **Reason and scope:** the answer names no further reason. This covers this release only; it
  sets no rule for later version numbers.
