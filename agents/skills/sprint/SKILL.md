---
name: sprint
description: Plan a deliverable of more than one pull request before building it. Write the decision (problem, acceptance criteria, constraints), fix the definition of done, split the work into ordered chunks that each merge complete, have the other agent family attack the plan, and get the user's approval at each gate. Use when asked to plan a sprint, scope a feature or kick off a build. A single-issue fix does not need a sprint.
---

# Sprint

Read `agents/skills/AGENTS.md` and the root `AGENTS.md` first.

A sprint settles what to build, what "done" means and the order of work before any code is
written. It writes plans, not production code. Existing design documents under `docs/` and
open issues are inputs: a sprint cites them and does not decide them again.

## Scope first

- Plan only what the goal needs to reach `main`, correctly and completely. Do not add
  fail-safes, options or phases that the goal does not require.
- A deliverable that fits one pull request gets no sprint; open an issue and work it.
- When scoping needs an answer that the documents and the request do not give, ask the user
  one question at a time, with your recommended answer. Do not build on a guess.

## Layout

Take the date from the shell (`date -u +%Y-%m-%d`). The sprint home is
`docs/sprints/<date>-<slug>/`:

| File | Contents |
| --- | --- |
| `decision.md` | Problem, acceptance criteria and constraints, in three separate sections. No design. |
| `definition-of-done.md` | Tests required, integration points, edge cases, and the commands that prove done. |
| `sprint.md` | The chunks: outcome, the criteria each satisfies, its done-criteria, its dependencies. |
| `chunks/NN-<slug>.md` | The technical specification for one chunk only. |
| `review.md` | Each review finding and its disposition: fixed (what changed) or rejected (why). |

A small sprint is one file, `docs/sprints/<date>-<slug>.md`, with one section for each file
above; each phase below then writes its section instead of a file. Use the directory only when
the chunks need separate specifications.

## Phase 0: frame

1. Read the design documents under `docs/` and the root `AGENTS.md` sections that the work
   touches.
2. List the related open issues. When the sprint delivers more than one issue, create a
   GitHub milestone named `<date>-<slug>` and attach them, so `pickup` can find the sprint's
   open work. Both agents use one GitHub account, so mark each issue with the label
   `agent:<agent-id>` of the agent that takes it; two agents then do not take the same issue.
3. State the boundary of the deliverable to the user in one or two sentences and get
   agreement.

## Phase 1: decision → gate A

Write `decision.md`:

- **Problem**: the current state, the gap, who needs it and why now. No solution.
- **Acceptance criteria**: outcomes a person who did not build it can check.
- **Constraints**: non-goals; the security boundary and other rules in the root `AGENTS.md`
  that apply; Linux and macOS support; dependencies and order.
- **Issues**: which issues the sprint delivers, and which it leaves to a later sprint, with
  the reason.

**Gate A**: the user approves `decision.md` before any specification is written. An approval
the user already gave for this content counts; do not ask again.

## Phase 2: definition of done

Write `definition-of-done.md` before the specification:

- The unit and integration tests the work needs, with synthetic peers only.
- The integration points exercised for real, including the native service managers on
  Linux and macOS where the work touches them.
- The edge cases that must have tests.
- The gate: the `check` skill, plus the CI workflows under `.github/workflows/` that cover the
  change.

## Phase 3: chunks

Split the acceptance criteria into chunks, and record which chunks each one depends on. Each chunk is one pull request that builds,
passes its part of the definition of done and merges on its own. A new session must be able to
build it after reading only `decision.md`, `definition-of-done.md` and its chunk file. Write
`sprint.md` and one file per chunk. Both cite the decision and the definition of done; they do
not repeat them.

## Phase 4: review → gate B

Commit the plan on a work branch. Ask the other agent family to review that commit: it reads
only the plan files and the documents they cite, not the planning conversation. When no other
agent is available, record the review as pending and tell the user. The reviewer reports only
material findings that it has reproduced or can show in the text, and checks that:

- the chunks together satisfy every acceptance criterion;
- each criterion can be checked;
- no chunk conflicts with a constraint or a cited document;
- each chunk is self-contained and in the right order;
- the definition of done would catch a wrong build;
- no assumption is left unverified.

Record every finding in `review.md` with its disposition. A finding that changes the chunks, the
criteria or the definition of done gets a second review of the changed plan.

**Gate B**: the user approves the reviewed plan before any chunk is built. As at gate A, an
approval already given counts.

## Phase 5: build → gate C for each chunk

Build each chunk with the `ship` skill. Do not start a chunk until the chunks it depends on
have merged; chunks with no dependency between them may be built at the same time. Once a chunk's branch is pushed and CI is running, put further changes on
a new branch. File each new deferral as an issue on the milestone when it is decided. When the
build shows that the plan is wrong, correct the plan file first and gate it again.
