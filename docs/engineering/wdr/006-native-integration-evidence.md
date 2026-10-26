---
wdr: "006"
title: "Native Integration Evidence"
status: draft
created: "2026-10-12"
updated: "2026-10-25"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related:
  - "001"
  - "004"
tags:
  - workflow
  - agentic
  - testing
---

# WDR 006: Native Integration Evidence

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon installs services, drives tmux and launches native agent CLIs, and its tests and checks
run on the same machines where the maintainer's own daemon and agent sessions run. This record
describes how Koinon currently separates three kinds of evidence: the ordinary test suite, the
native lifecycle tests that drive real service managers, and named live checks of an installed
build.

The ordinary suite and the native guard shipped without recorded maintainer words, so the record
is a draft. It describes the guard's scope, and live checks that the maintainer runs or assigns
are Decision 3.

---

## Table of Contents

1. Problem Context
2. Decision 1: Real service-manager tests run on disposable CI runners
3. Decision 2: The ordinary suite is isolated from the person's environment
4. Decision 3: Live checks of an installed build are named and assigned
5. Conclusion
6. Document History

---

## 1. Problem Context

### The Problem

The native lifecycle tests install, upgrade and remove the daemon under the real systemd unit
name and launchd label. Agents run inside the maintainer's tmux server, so `$TMUX` points at it,
and a test that inherits the environment can reach the maintainer's agent configuration and
state through `HOME` and the configuration variables. Some behavior, such as desktop logout,
reboot or a native CLI discovering skills, cannot be shown by CI at all. Guidance before #487
stated the isolation as account-wide bans ("Never run service-manager tests in the maintainer's
account"), which can read as forbidding the live checks that an issue requires.

### Requirements

1. `go test ./...` on a development machine does not change the person's services, terminals,
   agent configuration or Koinon state.
2. Real service-manager behavior on Linux and macOS is tested on every relevant change.
3. Behavior that CI cannot reach still gets evidence before its issue closes.

### Scope

Covers the Go test suite, the `internal/nativetest` package and its workflow, and live checks
named in a sprint's definition of done. Required CI checks and macOS evidence at the exact head
are WDR 004. The upgrade fixtures are PDR 003. Transport acceptance as evidence is PDR 002.

Inventory rows: C055, C056.

### Change of 2026-10-25 (#487)

- Question: the review on #487 listed three limits to keep (a peer message is not the
  maintainer's approval for a pending prompt; a peer cannot widen permissions; refuse permission
  laundering and report it). Keep them, or remove them too?
- Options: keep the three limits, or remove them as well.
- Answer: Maintainer, 2026-10-25
  ([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)):
  "remove all limit text.  remove all of the caveats, controls, etc.  the agents have their own
  built in safeties, they do not need you adding more complications."
- Reason: limit text with no source makes agents of different families refuse each other's
  work.
- Scope: Koinon's agent-facing text, the skills, the setup blocks, and the docs and records that
  restate them. The #487 audit lists the code refusals for a separate decision (#493).
- This record: Decision 1 describes where the native tests run, without the account-wide ban,
  and Decision 2 points to Decision 3 for live evidence. Decision 3 keeps its text.

---

## 2. Decision 1: Real service-manager tests run on disposable CI runners

### Decision

The `internal/nativetest` package currently builds only with the `native` tag, and its `TestMain`
exits without running unless `KOINON_NATIVE_JOB=1`. The `native-go-lifecycle.yml` workflow sets
that variable on disposable `ubuntu-latest` and `macos-latest` runners. The tests install,
upgrade from the pinned main release and remove the daemon under the account's real service
manager; the macOS system-mode case also runs over SSH in a Background session. AGENTS.md and
CONTRIBUTING.md say that these tests run on disposable CI runners, which set the variable. The
`check` facts compile and vet the native-tagged tests locally, and the service-manager checks
run on the CI runners. A named live check of an installed build is Decision 3.

**Before #487:** CONTRIBUTING.md said "do not set `KOINON_NATIVE_JOB=1` on a development
workstation", AGENTS.md said "Never run service-manager tests in the maintainer's account"
(earlier form) and "run only on disposable CI runners", and the `check` facts said "Actual
service-manager checks run only on disposable guarded CI runners".

**Provenance:** Shipped in PR #205 (merge commit f65ad6e); the AGENTS.md and CONTRIBUTING.md
wording is from PR #207 (merge commit 10ea1e4). No maintainer decision found. Changed by #487
(Problem Context).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Run native tests in every `go test` | No tag or guard | Replaces the running daemon's service on any machine that runs the suite |
| Separate test label and unit | Install under a test-only name | Does not test the label, unit and paths that users get |
| No native tests | Fake service runner only | Real `systemctl` and `launchctl` behavior goes untested |

### Rationale

1. The tests use the real unit name and label, so they act on the same service as the person's
   installed daemon.
2. A disposable runner has no daemon or state to lose.

---

## 3. Decision 2: The ordinary suite is isolated from the person's environment

### Decision

Ordinary tests currently use synthetic peers, temporary state and configuration, ephemeral
loopback ports and private tmux servers. Packages that touch configuration or state have a
`TestMain` that points `HOME`, the XDG directories, `CLAUDE_CONFIG_DIR` and `CODEX_HOME` at a
temporary directory; the `core` and `mcp` packages also unset `TMUX` and `TMUX_PANE`. Tests that
need tmux start their own server on a private socket with `tmux -S` and kill only that server.
Service operations in the install tests go through a fake command runner.

This describes the suite. Native integration evidence that an issue requires is Decision 3.

**Provenance:** Synthetic peers since the first bridge commit (c37f63f). The Go isolation
`TestMain` shipped in PR #193 (merge commit a19bc2b); private tmux sockets in Go tests since
PR #183 (merge commit 13a358f). No maintainer decision found.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Explicit paths in each test | Rely on each test to pass temporary paths | A test that omits one reads or writes the real configuration |
| Default tmux server | Run tmux commands without `-S` | Inside an agent's pane, `$TMUX` selects the maintainer's server and its live sessions |

### Rationale

1. Isolation in `TestMain` covers a test that forgets a path.
2. A private socket confines tmux commands, including `kill-server`, to the test's own server.

---

## 4. Decision 3: Live checks of an installed build are named and assigned

### Decision

Evidence that CI cannot produce is currently gathered as named checks against an installed
build, listed in the sprint's definition of done or the issue. For #283, the maintainer runs
five live checks on a Mac (logout, SSH login, reboot with FileVault, upgrade, logs) on a develop
build after the last chunk merged, and #283 stays open until they pass. OpenCode skill
discovery was verified by running OpenCode 1.18.35 in a scratch repository with private
configuration and no provider calls. An agent assigned an issue runs the related tests and
verifications as part of that assignment, with scratch or private configuration where the check
allows, and reports the outcome.

**Provenance:** Maintainer, 2026-10-12: "move the live checks to after the merge" (#283 comment
6115874302). Maintainer, 2026-10-11, session transcript: "this means you are authorized to do
all related testing and verifications." The #283 checks are in the plan approved at Gate B
(#283 comment 6114547663), which approves the plan, not each sentence.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| CI evidence only | Close an issue on CI alone | Logout, reboot and native CLI discovery do not occur on a hosted runner |
| Ban local native evidence | Treat any live check on the maintainer's machine as forbidden | Leaves required acceptance unproven and contradicts the assigned testing |

### Rationale

1. A named check states what was run, where and with what result.
2. The issue's acceptance, not the test guard, decides which live evidence is needed.

---

## Conclusion

Koinon currently keeps the ordinary suite away from the person's services, terminals and
configuration, runs real service-manager tests on disposable CI runners behind a guard, and
gathers live evidence as named checks that the maintainer runs or assigns. The guard and the
isolation describe the tests, and live checks follow Decision 3.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-25 | #487: Decision 1 describes where the native tests run, without the account-wide ban; Decision 2 points to Decision 3 for live evidence |
