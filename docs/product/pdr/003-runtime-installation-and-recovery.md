---
pdr: "003"
title: "Runtime Installation and Recovery"
status: draft
created: "2026-10-12"
updated: "2026-11-06"
authors:
  - "Robert Capps"
reviewers: []
supersedes: ""
superseded_by: ""
related_adrs:
  - "001"
related_pdrs:
  - "001"
tags:
  - onboarding
  - error-handling
  - installation
---

# PDR 003: Runtime Installation and Recovery

**Date:** 2026-10-12
**Status:** Draft
**Authors:** Robert Capps

---

## Executive Summary

Koinon runs as one daemon per user. This record describes how Koinon currently installs that
daemon, when it uses a system service, what it keeps when it is upgraded, removed or
unreachable, and which commands change services and agent configuration. Each decision states
what Koinon currently does, why, and where it came from.

The record is a draft. The maintainer decided the launcher PATH lookup (Decision 5), the
post-install and post-upgrade instructions (Decision 6) and the removal of an operation ban on
agents (not adopted); the macOS system mode traces to the
approval of a plan, and the rest is shipped behavior without a maintainer decision.

---

## Table of Contents

1. Problem Context
2. Decision 1: The default install uses the user's own service manager
3. Decision 2: macOS system mode with `--system`
4. Decision 3: State is kept across install, upgrade, removal and outages
5. Decision 4: Services and configuration change through Koinon's commands
6. Decision 5: A launcher records the CLI it finds on PATH
7. Decision 6: After install and upgrade, Koinon says clearly what the person must do
8. Statements this record does not adopt
9. Conclusion
10. Document History

---

## 1. Problem Context

### The Problem

A person installs Koinon on Linux or macOS and expects the daemon to keep running, to come back
after a restart and to keep messages, memory and work through upgrades. On macOS the launchd
agent runs in the desktop login's `gui/<uid>` domain, so it stops when that login ends and
cannot be managed over SSH (#283). Installation guidance written by agents also accumulated
statements about sudo, system services and state resets that read as bans on operations the
maintainer has asked for.

### Product Requirements

1. An install works without administrator rights on a machine with a user service manager, and
   says what to run when there is none.
2. A person can choose a daemon that survives logout on macOS.
3. Install, upgrade, removal and an outage do not lose state.
4. A refusal names its cause and the command that resolves it.

### Scope

Covers `koinon install`, `koinon upgrade`, `koinon uninstall`, `koinon setup`, `koinon recover`
and the macOS system mode. Registration after an outage is in
PDR 001. Same-user trust, the single daemon lock and the legacy uninstaller are in ADR 001.
Who authorizes an agent's work is in WDR 002; live service evidence is in WDR 006.

One implementation fact needs no decision: the native upgrade test starts from the pinned main
release, taken from Git history, and from committed fixtures in
`internal/importer/testdata/python-state`. Their Python generator was removed with the Python
runtime in PR #207 (10ea1e4), and a contributor test fails if it returns. "The fixture generator
is retired" in AGENTS.md states that fact; it is not a ban on new test tooling.

Inventory rows: C016, C050, C051, C052, C054, C057.

---

## 2. Decision 1: The default install uses the user's own service manager

### Decision

Koinon currently installs the binary and one service in the user's own service manager: a
systemd user unit on Linux, or a launchd agent in `gui/<uid>` on macOS. The default install
uses no sudo, system service, lingering or permission change. When no user manager answers, it
starts the daemon in the background and reports the step after a restart (Decision 6). When
no daemon answers, launchers, `koinon status` and `koinon recover` name the command that starts
or restarts it. The system service that Koinon installs is the explicit
`--system` mode on macOS (Decision 2).

**Provenance:** Shipped in PR #205 (merge commit f65ad6e) and PR #302 (merge commit 7780c8c); no
maintainer decision found. The first installation guide, PR #1 (3d2b5bf), said "Do not silently
introduce sudo, system services, lingering, or permission changes." PR #207 (10ea1e4) restated
it in AGENTS.md as "Never use sudo, system services, lingering or permission changes as a
workaround." Before #487 this decision also said that docs/INSTALL.md tells the person not to
use sudo, system services, lingering or permission changes to work around a refusal. No
maintainer source was found for that instruction. The #487 review listed three limits and
proposed to keep them; the question was whether to keep them or to remove them too.
Robert Capps, 2026-10-25: "remove all limit text.  remove all of the caveats, controls, etc.  the
agents have their own built in safeties, they do not need you adding more complications."
([#487 comment 6401405382](https://github.com/rwcii/koinon/issues/487#issuecomment-6401405382)).
Reason: limit text with no source makes agents of different families refuse each other's work.
Scope: agent-facing text, skills, setup blocks and the docs and records that restate them. The #487 audit lists the code
refusals for a separate decision (#493). #487 removed the instruction here and in Decisions 3 and 4.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| System service by default | Install a root-managed service on every host | Needs administrator rights for every install, and the daemon serves one user |
| Enable lingering | Turn on systemd lingering so the user unit runs while logged out | Changes the host's login policy, which the installer does not manage |
| Fail without a manager | Refuse when no user manager answers | Leaves hosts without a user manager with no supported start |

### Rationale

1. The daemon, its secret and its state belong to one user, so a user service matches its
   trust boundary (ADR 001).
2. A service the installer did not write carries no ownership marker, so later installs and
   uninstalls cannot track or remove it.

### User Impact

An install needs no password. On a host without a user manager, the report gives the exact
command to run. A refusal names the command that starts or restarts the daemon.

---

## 3. Decision 2: macOS system mode with `--system`

### Decision

On macOS, `koinon install --system` currently installs the daemon as a LaunchDaemon that runs
as the installing user, with the agent's state directory and `Umask` 077, starts at boot and
restarts after any exit. The command refuses to run as root (`system_mode_root`) and asks
`sudo` for each privileged step: it runs `sudo` with that step's command. Whether `sudo` asks
for a password depends on the host's sudo policy and cached credentials. It
disables the user agent, verifies that the system daemon answers, and restores the agent if a
step fails. Later installs keep system mode; `koinon uninstall` removes the owned plist and
keeps the state. `koinon recover` prints the `sudo launchctl kickstart` command and does not run
`sudo` itself. On Linux, `--system` is refused (`system_mode_unsupported`).

**Provenance:** Requirements in issue #283. Robert Capps, 2026-10-11: "yes", the answer to "Do you
approve the #283 plan for building?" (#283 comment 6114547663, plan at 3b5f9ce); this approves
the plan document, not each sentence in it. Shipped in PR #301 (merge commit 496ec52). The Linux
refusal is in the approved plan ("the issue names only macOS"); no maintainer words on Linux were
found. The maintainer's live checks on a Mac are open on #283.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Keep only the launchd agent | Document the logout limit | The daemon stops at logout and cannot be managed over SSH (#283) |
| Run the daemon as root | A LaunchDaemon without `UserName` | Breaks the same-user state and secret boundary (ADR 001) |
| Run the installer under `sudo` | `sudo koinon install --system` | Refused (`system_mode_root`): the daemon runs as the user, and the whole installer would run as root |
| Linux system unit | Add a system-wide systemd unit | Not requested; #283 names only macOS |

### Rationale

1. A LaunchDaemon survives logout and can be managed from SSH (#283; Background-session proof in
   #283 comment 6114915869).
2. Each privileged operation is a separate `sudo` command under the host's own sudo policy.
3. One daemon holds the state; the user agent stays disabled while system mode is installed.

### User Impact

A Mac user who wants the daemon to survive logout runs `koinon install --system` as themselves
and authenticates to `sudo` as the host's policy requires. Linux users keep the user unit and get a fixed error for
`--system`.

---

## 4. Decision 3: State is kept across install, upgrade, removal and outages

### Decision

Koinon currently keeps the state directory through every install, upgrade and uninstall.
`koinon uninstall` removes only the configuration, service file and binary that carry its
ownership, and reports the kept state path. The Python-era upgrade verifies the import before it
removes the previous services, and a failure before that point restores the previous runtime.
The daemon deletes messages and sessions by its fixed retention rules and by the maintainer's
purge mark, set in the dashboard or with `koinon sessions purge` (#388); `koinon maintenance
sweep` runs the deletion at once. `koinon recover` ends a storage block after the person corrected
its cause; an unreachable daemon is answered with the restart command, not with new state or a
new secret.

**Provenance:** Shipped in PR #205 (merge commit f65ad6e), PR #240 (merge commit b7beffc) and PR
#302 (merge commit 7780c8c); no maintainer decision found. Before #487 this decision also quoted
AGENTS.md: "Do not reset a cursor/checkpoint, alter a secret, purge a registry or delete
retained state." It came from PR #207 (10ea1e4), which merged and broadened the Python-era "Do
not reset a checkpoint silently" and "Never purge shared socket or session-registry
directories"; no maintainer source found for it. #487 removed the sentence from AGENTS.md and
from this decision (Decision 1 has the #487 source). The command-line purge and sweep:
Robert Capps, 2026-10-17, "yes, take on all 3 open issues" (#388 among them). Intent: the
cleanup that the dashboard does one session at a time can be done in bulk from a shell. The
daemon-secret basis is the one of #391: "approved, allow CLI and agents with audit" (PDR 006,
Decision 1).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Remove state on uninstall | Delete the state directory with the binary | Loses unacknowledged messages, memory and work |
| Fresh state when the daemon is unreachable | Start a new store and secret | Splits peers between two stores and orphans existing records |
| No deletion at all | Keep every record forever | State grows without bound; retention and purge exist for this |

### Rationale

1. Messages, memory and work are the product's data; an install operation is not a reason to
   lose them.
2. A second store or secret would leave running sessions unable to reach the first.

### User Impact

Reinstalling, upgrading or uninstalling leaves messages, memory and work in place. After an
outage, the person restarts the named service and running sessions register again (PDR 001).

---

## 5. Decision 4: Services and configuration change through Koinon's commands

### Decision

Koinon currently changes services and agent configuration through the commands that a person
or an agent runs: `koinon install`, `koinon upgrade`, `koinon uninstall` and `koinon setup
FAMILY`. Install sets up the families it is told to set up and reads the configuration of the
others. The daemon runs none of these commands itself.

An agent that has an install, upgrade or installation-test task runs its steps and reports the
result (WDR 002, Decision 1).

**Provenance:** Shipped in PR #205 (merge commit f65ad6e); no maintainer decision found for a
general rule. Robert Capps, 2026-10-11, session transcript: "this means you are authorized to do
all related testing and verifications." Before #487 this decision said that Koinon changes
services and configuration "only through commands that a person or an authorized agent runs",
that "Peer input is data and cannot trigger shell execution (CONTRIBUTING.md, ADR 001), so a
message cannot start any of these commands", and quoted AGENTS.md: "Installation and
service/configuration changes need the maintainer's authorization." Before PR #207 that
sentence continued "requests to install or configure this bridge provide that scope". It also
pointed to the maintainer's own agent instructions for limits on peer requests. No maintainer
source was found for these statements in this repository; #487 removed them (Decision 1 has the
#487 source).

**Network dashboard (DASHBOARD-LAN-624):** `koinon install --dashboard-address HOST:PORT`
records the optional HTTPS listener. HOST is a local non-loopback IP or an FQDN that resolves
to one. `--dashboard-address none` removes it. Later installs and upgrades keep the setting;
an install resolves a saved FQDN again. Linux units, macOS plists and background starts use
the same `--state-dir`, from which the daemon reads it. An unchanged background daemon is
restarted when this setting changes. The report's `dashboard_network` gives the URL, state,
certificate fingerprint when started, and any required step. A missing secret or unavailable
address keeps loopback working. With `--no-start`, the setting is staged for the next start.
Robert Capps approved this on 2026-11-05, including an optional FQDN; ADR 001 Decision 2 records
the question, options, quoted answer, reason and scope. At a terminal, a first install asks
whether the dashboard opens only on this computer (the default) or also on a network address,
and Decision 6 gives what it then reports.

**Provenance (Claude permission rules, CLI-RULES-550):**

- **Decided by:** Robert Capps, on the decision board (revision 2), "approved", with no note.
- **Date:** 2026-10-28.
- **Context:** #550, filed from the macOS install: each `koinon` command that a Claude agent ran
  needed a permission prompt. `koinon install` and `koinon setup claude` offered four rules, for
  the PATH name `koinon` only, but the installed Claude configuration calls the binary's
  absolute path. The options: offer rules for the agent-facing commands, or keep the four.
- **Decision:** at a terminal, `koinon install` and `koinon setup claude` offer allow rules,
  with and without arguments, for the PATH name and the installed binary's path:
  `mcp__koinon__*` and the agent-facing `koinon` commands that docs/PERMISSIONS.md lists. They
  leave out `install`, `uninstall`, `upgrade`, `serve`, `setup`, `maintainer`, `dashboard`,
  `context set`/`enable`/`disable`/`threshold`, `sessions` and `participant holder`. A yes adds
  them; nothing is added without it, and `koinon uninstall` leaves them.
- **Reason and scope:** a prompt for each agent command stops the work. The decision covers the
  offered Claude rules only.

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Fresh approval per step | Ask the maintainer before each install, restart or test step | Blocks work the maintainer already assigned |
| Self-installing runtime | The daemon installs or repairs services on its own | A message would change the host without anyone running a command |

### Rationale

1. Every service change comes from a command whose report a person can read.
2. Repeated questions stall the assigned work.

### User Impact

Nothing on the host changes until someone runs a Koinon command. An agent assigned an install or
upgrade task carries it out and reports the result.

---

## 6. Decision 5: A launcher records the CLI it finds on PATH

### Decision

Decided: when `launchers.json` has no CLI path for the family, `koinon <family>` finds the
family's CLI on PATH, records its absolute path in `launchers.json`, reports the path it chose
and starts the agent. Later launches use the recorded path; `--cli` still overrides it.

Implemented: `launchCLI` in `internal/launcher` searches `PATH` only when there is no `--cli`
and the family has no entry or an empty entry. It reads the file again under
`launchers.lock` and uses an entry recorded meanwhile instead of replacing it. When the CLI is
not on `PATH`, the launch stops and names `koinon setup <family>` or `--cli`, as before. An
invalid file or a relative or missing recorded path still refuses and is not changed.

**Provenance:** The recorded-path-only launcher shipped in PR #186 (merge commit e2bc2f3); no
reason recorded.
Robert Capps, 2026-10-12: "b, next item", choosing the PATH lookup that records and reports the
path over the recorded-path-only launcher ([#306 comment 6119328447](https://github.com/rwcii/koinon/issues/306#issuecomment-6119328447)).

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Recorded path only (previous behavior) | Stop and ask for `koinon setup` when no path is recorded | Adds a step before the first launch |
| Search PATH on every launch | Resolve the CLI anew each time | A change to PATH or an upgrade could start a different program than the one recorded |

### Rationale

1. The first launch works without an extra step.
2. The printed path shows which program ran, and later launches use that same path.

### User Impact

`koinon codex` works on a machine where only the Codex CLI is installed and prints the path it
recorded.

---

## 7. Decision 6: After install and upgrade, Koinon says clearly what the person must do

### Decision

Decided: the text that Homebrew shows after `brew install koinon` and `brew upgrade koinon`
clearly says what the person must do. When steps remain, it starts with an action line, then
gives each command on its own line, in order, and says what `koinon install` does. When no
step remains, it says that no action is required. A change to this text keeps it clear.

Decided: an install never appears to succeed when Koinon does not work. Without a user
service manager, the install starts the daemon itself and names the task that remains: start
it again after a restart.

Implemented: the `caveats` method of the formula that `scripts/homebrew-formula.sh` writes. The
action line reads "ACTION REQUIRED: to finish the Koinon installation, run these commands, in
order:". The commands come from `koinon install --plan`, or are the fixed steps when the plan
cannot check everything. A current installation shows "The Koinon installation is current. No
action is required." `TestHomebrewFormula` in `internal/contributor` keeps this text.

Implemented: when no user service manager answers, `startAgent` in `internal/install` starts
`koinon serve` in its own session, with its output in `daemon.log` in the state directory,
and waits until a daemon of its version answers. An older daemon exits for its new binary
first; the install sends it no signal, because a manager that the install cannot reach may run
it. The report says `"service": "background"`, and `restart_step`, also
printed on the terminal, says to run `koinon install` again after a restart or after `brew
upgrade koinon`. A daemon that does not start or answer fails the install with
`daemon_not_started`. Without a manager, `koinon install --plan` names `koinon install` when no
daemon of its version answers, and the uninstall stops a daemon that still answers after the
managed services are gone. `TestBackgroundInstall`, `TestBackgroundStart`, `TestPlanSteps` and
`TestUninstallStopsBackgroundDaemon` cover these outcomes.

Implemented: the network dashboard questions of a first `koinon install` at a terminal
(DASHBOARD-LAN-624, part 2, in ADR 001 Decision 2) end with what the person must know: the
URL and the certificate's SHA-256 fingerprint, with the instruction to compare it once in the
browser, or the code and step that remain. A network address without a maintainer secret gets
the offer to set or generate one; a generated secret is shown once on the terminal. After a
secret set there, the install restarts the daemon once and reports the status after that
restart, never the status from before it: a failed restart is `dashboard_restart_failed` with
its step, and the install fails with `daemon_restart_failed`. The tests in
`internal/install/dashboard_questions_test.go` cover these outcomes.

**Provenance:** #551 replaced the fixed steps with the plan's commands under the heading "The
steps that this installation needs:", without the sentence that said what `koinon install`
does. On a new Linux machine, v0.9.2 showed `koinon install` with no instruction to run it.
Robert Capps, 2026-11-05: "the brew installer no longer prompts the user to run 'koinon install'
a user would never know they need to run that command." and "post install/upgrade needs to
CLEARLY identify what the user needs to do.  this content keeps getting replaced without any
request to do so" (#618).
On a Linux container without a systemd user manager, `koinon install --port 47687` reported
`"ok": true` with `"service": "manual_required"`, and `koinon dashboard` then found no daemon.
Robert Capps, 2026-11-05: "this needs to be part of the installer, or raise a post install task
for the user.  it can't appear to succeed and then not work." (#619). Asked whether the install
should start the daemon in the background and report the step after a restart, in place of a
failed install that names a command to run in a session that stays open, the maintainer
answered "yes".

### Alternatives Considered

| Alternative | Description | Why Rejected |
| ----------- | ----------- | ------------ |
| Plan commands under a heading (previous behavior) | "The steps that this installation needs:" and the commands | It does not tell the person to run them; the maintainer found it unclear |
| Report `manual_required` with `"ok": true` (previous behavior) | The install succeeds and the report holds the start command | The install appears to succeed and Koinon does not work |
| Fail the install with the start command | The person starts `koinon serve` in a session that stays open | A step that the installer can take itself |

### Rationale

1. A person who has just run `brew install` does not know that Koinon needs more steps.
2. The action line comes first, so the person sees what to do before the upgrade notes.
3. The installer takes the step that it can take; the person gets only the step that it cannot.

### User Impact

After `brew install koinon` on a new machine, the person reads that they must run
`koinon install`, and what it does. On a host without a user service manager, `koinon install`
starts the daemon and says to run it again after a restart.

---

## Statements this record does not adopt

| Inventory row | Statement (short) | Where it appears | Why not adopted |
| ------------- | ----------------- | ---------------- | --------------- |
| C052 | Agents never run `--system` or `sudo` on the maintainer's machine | AGENTS.md (Installation, upgrade and security); docs/INSTALL.md (System mode on macOS); docs/sprints/2026-10-11-macos-system-service/decision.md (Sudo boundary) | No maintainer words found; #283 and its comments were checked. Its only source is a sentence in the plan approved at Gate B, and that approval covers the plan, not each sentence. It bans an operation the maintainer may assign; the host's sudo policy and the native permission settings already govern each privileged step. Robert Capps, 2026-10-12: "a, next item", removing the sentence ([#306 comment 6119281303](https://github.com/rwcii/koinon/issues/306#issuecomment-6119281303)); the step 2 wording pull request removes it from AGENTS.md and docs/INSTALL.md. |

---

## Conclusion

Koinon currently installs into the user's own service manager, offers a macOS LaunchDaemon
through an explicit `--system` install, keeps state through install operations and outages, and
changes the host through its own commands. A launcher records the CLI it finds on PATH, the
text after install and upgrade clearly says what the person must do, an install without a
service manager starts the daemon itself, and agents can
run `--system` or `sudo`, as the maintainer decided. The macOS system mode traces to
the approval of a plan; the rest is shipped behavior the maintainer has not yet decided.

---

## Document History

| Date       | Change           |
| ---------- | ---------------- |
| 2026-10-12 | Initial creation |
| 2026-10-12 | Recorded the maintainer's decisions on the launcher PATH lookup and the `--system`/`sudo` sentence (#306) |
| 2026-10-12 | Decision 5 implemented: the launcher records the CLI it finds on PATH (#306) |
| 2026-10-25 | Decisions 1, 3 and 4: removed the unsourced sudo workaround, state-reset and authorization text (#487) |
| 2026-11-05 | Decision 6: the maintainer's decisions on the post-install and post-upgrade instructions, implemented (#618, #619) |
| 2026-11-05 | Decision 4: the Claude permission rules decision (CLI-RULES-550), moved into this record (#623) |
| 2026-11-06 | Decisions 4 and 6: the network dashboard questions of `koinon install` (#624, part 2) |
